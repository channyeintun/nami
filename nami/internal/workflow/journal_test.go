package workflow

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// runWithJournal runs spec against a journal at path, resuming from
// resumeFrom, and returns the nodes that actually executed.
func runWithJournal(t *testing.T, spec Spec, path string, resumeFrom string) []string {
	t.Helper()
	journal, err := OpenJournal(path, resumeFrom)
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	var mu sync.Mutex
	var executed []string
	if _, err := mustResolve(t, spec).Run(t.Context(), Options{
		Journal: journal,
		Run: func(ctx context.Context, req NodeRequest) (NodeResult, error) {
			mu.Lock()
			executed = append(executed, req.ID)
			mu.Unlock()
			return echoRunner(ctx, req)
		},
	}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return executed
}

func appendToFile(t *testing.T, path string, text string) {
	t.Helper()
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	if _, err := file.WriteString(text); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close %s: %v", path, err)
	}
}

// Run ids restart with each process, so a resumed run can be given the id,
// and so the journal path, of the run it resumes. Opening that journal used to
// truncate it, and a run that died before replaying everything took the
// records it had not replayed yet with it.
func TestOpeningAnExistingJournalKeepsItsRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wf_1.ndjson")
	spec := Spec{MaxParallel: 1, Nodes: []NodeSpec{node("a"), node("b", "a"), node("c", "b")}}
	runWithJournal(t, spec, path, "")

	// The resumed run opens its journal on the same path, then dies before
	// replaying anything.
	died, err := OpenJournal(path, path)
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	if err := died.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if executed := runWithJournal(t, spec, filepath.Join(t.TempDir(), "wf_2.ndjson"), path); len(executed) != 0 {
		t.Fatalf("resume executed %v; the journal lost records it had not replayed", executed)
	}
}

// A killed run leaves a half-written last line, and the next run on that path
// appends after it. The fragment must not hide what was written after it.
func TestJournalRecordsAfterATornLineStillReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "wf_1.ndjson")
	runWithJournal(t, Spec{Nodes: []NodeSpec{node("a")}}, path, "")
	appendToFile(t, path, `{"key":"v1:half-writt`)
	runWithJournal(t, Spec{Nodes: []NodeSpec{node("b")}}, path, "")

	both := Spec{Nodes: []NodeSpec{node("a"), node("b")}}
	if executed := runWithJournal(t, both, filepath.Join(t.TempDir(), "next.ndjson"), path); len(executed) != 0 {
		t.Fatalf("resume executed %v, want a and b replayed around the torn line", executed)
	}
}

// Journals hold the agents' output, so they are private to the user.
func TestJournalIsPrivateToTheUser(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "workflows")
	path := filepath.Join(dir, "wf_1.ndjson")
	journal, err := OpenJournal(path, "")
	if err != nil {
		t.Fatalf("OpenJournal: %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	for target, want := range map[string]os.FileMode{path: 0o600, dir: 0o700} {
		info, err := os.Stat(target)
		if err != nil {
			t.Fatalf("Stat: %v", err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s mode = %v, want %v", target, got, want)
		}
	}
}

// A record longer than load reads was written anyway, and from then on every
// resume from that journal failed. Such a record is now left out, so only its
// node re-runs; one just inside the limit still replays.
func TestJournalRecordsOnlyWhatItCanReadBack(t *testing.T) {
	encodedLength := func(output string) int {
		encoded, err := json.Marshal(journalRecord{Key: "k", NodeID: "n", Output: output})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		return len(encoded)
	}
	// The longest output whose record still fits on a line load will read. An
	// empty output is omitted from the record, so measure the overhead with one
	// character.
	longest := maxJournalLineBytes - 1 - (encodedLength("x") - 1)

	tests := []struct {
		name       string
		output     string
		wantReplay bool
	}{
		{name: "at the limit", output: strings.Repeat("x", longest), wantReplay: true},
		{name: "over the limit", output: strings.Repeat("x", longest+1), wantReplay: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			first := filepath.Join(dir, "first.ndjson")
			journal, err := OpenJournal(first, "")
			if err != nil {
				t.Fatalf("OpenJournal: %v", err)
			}
			journal.Record("small", "small", NodeResult{Output: "ok"})
			journal.Record("k", "n", NodeResult{Output: tc.output})
			if err := journal.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}

			resumed, err := OpenJournal(filepath.Join(dir, "second.ndjson"), first)
			if err != nil {
				t.Fatalf("resuming from the journal failed: %v", err)
			}
			defer resumed.Close()
			if _, ok := resumed.Replay("small"); !ok {
				t.Fatal("the small record did not replay")
			}
			if _, ok := resumed.Replay("k"); ok != tc.wantReplay {
				t.Fatalf("large record replayed = %v, want %v", ok, tc.wantReplay)
			}
		})
	}
}
