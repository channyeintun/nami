package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// runJournaled runs body with a fresh journal in dir, seeded from resumeFrom.
func runJournaled(t *testing.T, dir string, name string, resumeFrom string, body string, runner *fakeRunner) (Result, string) {
	t.Helper()
	path := filepath.Join(dir, name+".ndjson")
	resumePath := ""
	if resumeFrom != "" {
		resumePath = filepath.Join(dir, resumeFrom+".ndjson")
	}
	journal, err := OpenJournal(path, resumePath)
	if err != nil {
		t.Fatalf("OpenJournal() error = %v", err)
	}
	result, err := runBody(t, body, runner, func(opts *Options) { opts.Journal = journal })
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return result, path
}

func statuses(result Result) []AgentStatus {
	out := make([]AgentStatus, 0, len(result.Agents))
	for _, agent := range result.Agents {
		out = append(out, agent.Status)
	}
	return out
}

func TestResumeReplaysAnUnchangedRunWithoutRunningAnything(t *testing.T) {
	dir := t.TempDir()
	body := `
const found = await agent('find', { schema: { type: 'object', properties: { items: { type: 'array' } } } })
return await parallel(found.items.map(item => () => agent('check ' + item)))
`
	structured := func(_ context.Context, call AgentCall) (AgentResult, error) {
		if call.Prompt == "find" {
			return AgentResult{Structured: json.RawMessage(`{"items":["a","b"]}`)}, nil
		}
		return AgentResult{Text: "ok " + call.Prompt}, nil
	}
	first, _ := runJournaled(t, dir, "first", "", body, &fakeRunner{respond: structured})

	replayer := &fakeRunner{respond: func(context.Context, AgentCall) (AgentResult, error) {
		return AgentResult{}, errors.New("a replayed run must not call the runner")
	}}
	second, _ := runJournaled(t, dir, "second", "first", body, replayer)

	if string(second.Value) != string(first.Value) {
		t.Fatalf("replayed value = %s, want %s", second.Value, first.Value)
	}
	if len(replayer.calls) != 0 {
		t.Fatalf("runner called for %q", replayer.prompts())
	}
	if !slices.Equal(statuses(second), []AgentStatus{AgentCached, AgentCached, AgentCached}) {
		t.Fatalf("statuses = %v", statuses(second))
	}
}

// A resumed run's journal must stand on its own, so a run resumed from it
// replays everything too.
func TestAResumedRunCanItselfBeResumed(t *testing.T) {
	dir := t.TempDir()
	body := "return [await agent('a'), await agent('b')]"
	runJournaled(t, dir, "one", "", body, &fakeRunner{})
	runJournaled(t, dir, "two", "one", body, &fakeRunner{})
	replayer := &fakeRunner{}
	runJournaled(t, dir, "three", "two", body, replayer)
	if len(replayer.calls) != 0 {
		t.Fatalf("third run called the runner for %q", replayer.prompts())
	}
}

// An agent with side effects can change what a later agent sees without
// changing its prompt. Once a re-run agent's result reaches the script, every
// call after it runs live, so a stale "run the tests" result is never replayed
// after the fix it was checking has changed.
func TestResumeRerunsEverythingAfterAnEditedCall(t *testing.T) {
	dir := t.TempDir()
	runJournaled(t, dir, "first", "", "await agent('fix X')\nreturn await agent('run the tests')", &fakeRunner{})

	runner := &fakeRunner{}
	second, _ := runJournaled(t, dir, "second", "first", "await agent('fix Y')\nreturn await agent('run the tests')", runner)
	if !slices.Equal(runner.prompts(), []string{"fix Y", "run the tests"}) {
		t.Fatalf("live calls = %q, want both", runner.prompts())
	}
	if !slices.Equal(statuses(second), []AgentStatus{AgentSucceeded, AgentSucceeded}) {
		t.Fatalf("statuses = %v", statuses(second))
	}
}

// Failures are never journaled, so resuming retries them, and siblings that
// were already in flight keep their cached results.
func TestResumeRetriesFailuresAndKeepsTheirSiblings(t *testing.T) {
	dir := t.TempDir()
	body := "return await parallel(['a', 'b', 'c'].map(x => () => agent('task ' + x)))"
	flaky := &fakeRunner{respond: func(_ context.Context, call AgentCall) (AgentResult, error) {
		if call.Prompt == "task b" {
			return AgentResult{}, errors.New("rate limited")
		}
		return AgentResult{Text: call.Prompt}, nil
	}}
	first, _ := runJournaled(t, dir, "first", "", body, flaky)
	if string(first.Value) != `["task a",null,"task c"]` {
		t.Fatalf("first value = %s", first.Value)
	}

	runner := &fakeRunner{}
	second, _ := runJournaled(t, dir, "second", "first", body, runner)
	if !slices.Equal(runner.prompts(), []string{"task b"}) {
		t.Fatalf("live calls = %q, want only the failed one", runner.prompts())
	}
	if string(second.Value) != `["task a","done: task b","task c"]` {
		t.Fatalf("second value = %s", second.Value)
	}
}

// Parallel work finishes in whatever order it finishes, and a replay settles
// in call order instead. Keys must not depend on either.
func TestResumeKeysDoNotDependOnCompletionOrder(t *testing.T) {
	dir := t.TempDir()
	body := `
return await pipeline(['slow', 'fast'],
  item => agent('one ' + item),
  (previous, item) => agent('two ' + item + ' after ' + previous))
`
	slowFirst := &fakeRunner{respond: func(_ context.Context, call AgentCall) (AgentResult, error) {
		if strings.HasPrefix(call.Prompt, "one slow") {
			time.Sleep(50 * time.Millisecond)
		}
		return AgentResult{Text: call.Prompt}, nil
	}}
	first, _ := runJournaled(t, dir, "first", "", body, slowFirst)

	replayer := &fakeRunner{}
	second, _ := runJournaled(t, dir, "second", "first", body, replayer)
	if len(replayer.calls) != 0 {
		t.Fatalf("runner called for %q", replayer.prompts())
	}
	if string(second.Value) != string(first.Value) {
		t.Fatalf("value = %s, want %s", second.Value, first.Value)
	}
}

// Identical calls are told apart by how many came before, so a loop of the
// same prompt replays one result per iteration.
func TestResumeReplaysRepeatedIdenticalCallsOncePerOccurrence(t *testing.T) {
	dir := t.TempDir()
	body := "const out = []\nfor (let i = 0; i < 3; i++) { out.push(await agent('find bugs')) }\nreturn out"
	counter := 0
	numbered := &fakeRunner{respond: func(context.Context, AgentCall) (AgentResult, error) {
		counter++
		return AgentResult{Text: strings.Repeat("x", counter)}, nil
	}}
	first, _ := runJournaled(t, dir, "first", "", body, numbered)

	replayer := &fakeRunner{}
	second, _ := runJournaled(t, dir, "second", "first", body, replayer)
	if len(replayer.calls) != 0 || string(second.Value) != string(first.Value) {
		t.Fatalf("value = %s (live %q), want %s replayed", second.Value, replayer.prompts(), first.Value)
	}
}

func TestResumeDoesNotReplayAcrossAChangedOption(t *testing.T) {
	dir := t.TempDir()
	runJournaled(t, dir, "first", "", "return await agent('x', { model: 'a' })", &fakeRunner{})
	runner := &fakeRunner{}
	runJournaled(t, dir, "second", "first", "return await agent('x', { model: 'b' })", runner)
	if len(runner.calls) != 1 {
		t.Fatalf("live calls = %d, want the changed call to run", len(runner.calls))
	}
}

// Relabelling a call for a nicer display must keep its cached result.
func TestResumeIgnoresLabelsAndPhases(t *testing.T) {
	dir := t.TempDir()
	runJournaled(t, dir, "first", "", "return await agent('x', { label: 'old', phase: 'A' })", &fakeRunner{})
	runner := &fakeRunner{}
	runJournaled(t, dir, "second", "first", "return await agent('x', { label: 'new', phase: 'B' })", runner)
	if len(runner.calls) != 0 {
		t.Fatalf("live calls = %q, want the relabelled call replayed", runner.prompts())
	}
}

func TestOpeningAnExistingJournalKeepsItsRecords(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.ndjson")
	journal, err := OpenJournal(path, "")
	if err != nil {
		t.Fatalf("OpenJournal() error = %v", err)
	}
	if err := journal.Record(journalRecord{Key: "v2:a:0", Text: "first"}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	_ = journal.Close()

	again, err := OpenJournal(path, path)
	if err != nil {
		t.Fatalf("OpenJournal() error = %v", err)
	}
	defer again.Close()
	if record, ok := again.Replay("v2:a:0"); !ok || record.Text != "first" {
		t.Fatalf("Replay() = %+v, %v; want the earlier record", record, ok)
	}
}

func TestJournalRecordsAfterATornLineStillReplay(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.ndjson")
	if err := os.WriteFile(path, []byte(`{"key":"v2:a:0","text":"kept"}`+"\n"+`{"key":"v2:b:0","te`), 0o600); err != nil {
		t.Fatal(err)
	}
	journal, err := OpenJournal(path, "")
	if err != nil {
		t.Fatalf("OpenJournal() error = %v", err)
	}
	if err := journal.Record(journalRecord{Key: "v2:c:0", Text: "after"}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	_ = journal.Close()

	reread, err := OpenJournal(filepath.Join(t.TempDir(), "next.ndjson"), path)
	if err != nil {
		t.Fatalf("OpenJournal() error = %v", err)
	}
	defer reread.Close()
	for _, key := range []string{"v2:a:0", "v2:c:0"} {
		if _, ok := reread.Replay(key); !ok {
			t.Errorf("record %s did not survive the torn line", key)
		}
	}
}

// A line longer than the loader reads would fail every later resume, so it is
// refused, and the refusal is reported rather than swallowed.
func TestJournalRefusesARecordItCouldNotReadBack(t *testing.T) {
	journal, err := OpenJournal(filepath.Join(t.TempDir(), "run.ndjson"), "")
	if err != nil {
		t.Fatalf("OpenJournal() error = %v", err)
	}
	defer journal.Close()
	err = journal.Record(journalRecord{Key: "v2:big:0", Label: "big", Text: strings.Repeat("x", maxJournalLineBytes)})
	if err == nil {
		t.Fatal("Record() accepted a record too large to read back")
	}
}

func TestJournalIsPrivateToTheUser(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "run.ndjson")
	journal, err := OpenJournal(path, "")
	if err != nil {
		t.Fatalf("OpenJournal() error = %v", err)
	}
	_ = journal.Close()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("journal mode = %o, want 600", mode)
	}
}

func TestJournalIgnoresRecordsFromTheOldKeyScheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.ndjson")
	if err := os.WriteFile(path, []byte(`{"key":"v1:abc","node_id":"a","output":"old"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	journal, err := OpenJournal(filepath.Join(t.TempDir(), "new.ndjson"), path)
	if err != nil {
		t.Fatalf("OpenJournal() error = %v", err)
	}
	defer journal.Close()
	if len(journal.cached) != 0 {
		t.Fatalf("cached = %v, want v1 records skipped", journal.cached)
	}
}

func TestNilJournalIsUsable(t *testing.T) {
	var journal *Journal
	if _, ok := journal.Replay("v2:x:0"); ok {
		t.Fatal("nil journal replayed a record")
	}
	if err := journal.Record(journalRecord{Key: "v2:x:0"}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := journal.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}
