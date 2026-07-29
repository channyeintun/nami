package agent

import (
	"strings"
	"testing"
)

func runFilter(t *testing.T, chunks []string) (string, []GoalProgressUpdate) {
	t.Helper()
	filter := newProgressDirectiveFilter()
	var visible strings.Builder
	var updates []GoalProgressUpdate
	for _, chunk := range chunks {
		out, parsed := filter.Process(chunk)
		visible.WriteString(out)
		updates = append(updates, parsed...)
	}
	out, parsed := filter.Flush()
	visible.WriteString(out)
	updates = append(updates, parsed...)
	return visible.String(), updates
}

func TestProgressDirectiveFilterPassesPlainText(t *testing.T) {
	text := "Hello world.\nSecond line without newline"
	visible, updates := runFilter(t, []string{text})
	if visible != text {
		t.Fatalf("visible = %q, want %q", visible, text)
	}
	if len(updates) != 0 {
		t.Fatalf("updates = %v, want none", updates)
	}
}

func TestProgressDirectiveFilterStripsDirectiveLine(t *testing.T) {
	visible, updates := runFilter(t, []string{
		"Working on it.\n::progress{goal=\"fix auth\" percent=25 label=\"reading module\"}\nNext step.\n",
	})
	if visible != "Working on it.\nNext step.\n" {
		t.Fatalf("visible = %q", visible)
	}
	if len(updates) != 1 {
		t.Fatalf("updates = %v, want 1", updates)
	}
	update := updates[0]
	if update.Goal != "fix auth" || update.Percent != 25 || update.Label != "reading module" {
		t.Fatalf("update = %+v", update)
	}
}

func TestProgressDirectiveFilterHandlesSplitDeltas(t *testing.T) {
	visible, updates := runFilter(t, []string{
		"Text before\n::prog", "ress{per", "cent=40 label=\"wir", "ing retry\"}\nafter\n",
	})
	if visible != "Text before\nafter\n" {
		t.Fatalf("visible = %q", visible)
	}
	if len(updates) != 1 || updates[0].Percent != 40 || updates[0].Label != "wiring retry" {
		t.Fatalf("updates = %+v", updates)
	}
}

func TestProgressDirectiveFilterMidLineIsNotDirective(t *testing.T) {
	text := "see ::progress{percent=10} inline stays\n"
	visible, updates := runFilter(t, []string{text})
	if visible != text {
		t.Fatalf("visible = %q, want %q", visible, text)
	}
	if len(updates) != 0 {
		t.Fatalf("updates = %v, want none", updates)
	}
}

func TestProgressDirectiveFilterFlushParsesTrailingDirective(t *testing.T) {
	visible, updates := runFilter(t, []string{"Done.\n::progress{percent=90}"})
	if visible != "Done.\n" {
		t.Fatalf("visible = %q", visible)
	}
	if len(updates) != 1 || updates[0].Percent != 90 {
		t.Fatalf("updates = %+v", updates)
	}
}

func TestProgressDirectiveFilterFlushReleasesPartialPrefix(t *testing.T) {
	visible, updates := runFilter(t, []string{"::prog"})
	if visible != "::prog" {
		t.Fatalf("visible = %q", visible)
	}
	if len(updates) != 0 {
		t.Fatalf("updates = %v, want none", updates)
	}
}

func TestProgressDirectiveFilterAllowsIndentedDirective(t *testing.T) {
	visible, updates := runFilter(t, []string{"  ::progress{percent=55}\n"})
	if visible != "" {
		t.Fatalf("visible = %q, want empty", visible)
	}
	if len(updates) != 1 || updates[0].Percent != 55 {
		t.Fatalf("updates = %+v", updates)
	}
}

func TestParseProgressDirectiveRejectsGarbage(t *testing.T) {
	for _, line := range []string{
		"::progress{}",
		"::progress{nonsense}",
		"::progress{percent=abc}",
		"::progress percent=10",
		"progress{percent=10}",
	} {
		if _, ok := parseProgressDirective(line); ok {
			t.Fatalf("parseProgressDirective(%q) unexpectedly succeeded", line)
		}
	}
}

func TestGoalProgressStateApplyClampsMonotonically(t *testing.T) {
	state := &GoalProgressState{}

	if !state.Apply(GoalProgressUpdate{Goal: "fix auth", Percent: 30, Label: "reading"}) {
		t.Fatal("first apply reported no change")
	}
	if state.Goal != "fix auth" || state.Percent != 30 {
		t.Fatalf("after first apply = %+v", state)
	}

	// Lower percent must not move the bar backward, but a new label still emits.
	if !state.Apply(GoalProgressUpdate{Percent: 10, Label: "editing"}) {
		t.Fatal("label-only apply reported no change")
	}
	if state.Percent != 30 || state.Label != "editing" {
		t.Fatalf("after backward apply = %+v", state)
	}

	// A directive carrying no percent at all leaves the bar where it is.
	if state.Apply(GoalProgressUpdate{Label: "editing"}) {
		t.Fatal("percent-less apply reported change")
	}

	// Identical state emits nothing.
	if state.Apply(GoalProgressUpdate{Percent: 30, Label: "editing"}) {
		t.Fatal("no-op apply reported change")
	}

	// Percent caps below completion until the turn actually ends.
	if !state.Apply(GoalProgressUpdate{Percent: 100}) {
		t.Fatal("capped apply reported no change")
	}
	if state.Percent != maxInTurnPercent {
		t.Fatalf("after capped apply = %+v", state)
	}
}
