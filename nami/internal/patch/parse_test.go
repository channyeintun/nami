package patch

import (
	"errors"
	"strings"
	"testing"
)

func patchText(lines ...string) string {
	return strings.Join(lines, "\n")
}

func TestParseAddFileSection(t *testing.T) {
	document, err := Parse(patchText(
		"*** Begin Patch",
		"*** Add File: pkg/new.go",
		"+package pkg",
		"+",
		"+func New() {}",
		"*** End Patch",
	))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(document.Operations) != 1 {
		t.Fatalf("Operations = %d, want 1", len(document.Operations))
	}
	operation := document.Operations[0]
	if operation.Action != ActionAdd {
		t.Errorf("Action = %q, want %q", operation.Action, ActionAdd)
	}
	if operation.Path != "pkg/new.go" {
		t.Errorf("Path = %q, want %q", operation.Path, "pkg/new.go")
	}
	want := []string{"package pkg", "", "func New() {}"}
	if len(operation.Lines) != len(want) {
		t.Fatalf("Lines = %#v, want %#v", operation.Lines, want)
	}
	for i := range want {
		if operation.Lines[i] != want[i] {
			t.Fatalf("Lines = %#v, want %#v", operation.Lines, want)
		}
	}
}

func TestParseUpdateSplitsHunksOnMarkers(t *testing.T) {
	document, err := Parse(patchText(
		"*** Begin Patch",
		"*** Update File: main.go",
		"@@",
		" first",
		"-old",
		"+new",
		"@@",
		" second",
		"+added",
		"*** End Patch",
	))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	operation := document.Operations[0]
	if len(operation.Hunks) != 2 {
		t.Fatalf("Hunks = %d, want 2", len(operation.Hunks))
	}
	if got := operation.Hunks[0].Lines[1]; got.Kind != '-' || got.Value != "old" {
		t.Errorf("first hunk removal = %+v, want {'-', \"old\"}", got)
	}
	if got := operation.Hunks[1].Lines[1]; got.Kind != '+' || got.Value != "added" {
		t.Errorf("second hunk addition = %+v, want {'+', \"added\"}", got)
	}
}

func TestParseDropsContextOnlyHunks(t *testing.T) {
	document, err := Parse(patchText(
		"*** Begin Patch",
		"*** Update File: main.go",
		"@@",
		" only context",
		"@@",
		" keep",
		"+added",
		"*** End Patch",
	))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := len(document.Operations[0].Hunks); got != 1 {
		t.Fatalf("Hunks = %d, want 1 (context-only hunk dropped)", got)
	}
}

func TestParseMultipleOperations(t *testing.T) {
	document, err := Parse(patchText(
		"*** Begin Patch",
		"*** Delete File: gone.txt",
		"*** Add File: fresh.txt",
		"+hello",
		"*** End Patch",
	))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(document.Operations) != 2 {
		t.Fatalf("Operations = %d, want 2", len(document.Operations))
	}
	if document.Operations[0].Action != ActionDelete || document.Operations[1].Action != ActionAdd {
		t.Fatalf("actions = %q, %q", document.Operations[0].Action, document.Operations[1].Action)
	}
}

func TestParseAcceptsCRLF(t *testing.T) {
	document, err := Parse("*** Begin Patch\r\n*** Add File: a.txt\r\n+one\r\n*** End Patch\r\n")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := document.Operations[0].Lines[0]; got != "one" {
		t.Fatalf("line = %q, want %q", got, "one")
	}
}

func TestParseRejectsMalformedPatches(t *testing.T) {
	cases := map[string]string{
		"missing begin marker": patchText("*** Add File: a.txt", "+x", "*** End Patch"),
		"missing end marker":   patchText("*** Begin Patch", "*** Add File: a.txt", "+x"),
		"content after end":    patchText("*** Begin Patch", "*** Add File: a.txt", "+x", "*** End Patch", "trailing"),
		"line outside section": patchText("*** Begin Patch", "stray line", "*** End Patch"),
		"missing path":         patchText("*** Begin Patch", "*** Add File:", "+x", "*** End Patch"),
		"add with plain line":  patchText("*** Begin Patch", "*** Add File: a.txt", "raw", "*** End Patch"),
		"delete with body":     patchText("*** Begin Patch", "*** Delete File: a.txt", "+x", "*** End Patch"),
		"update without body":  patchText("*** Begin Patch", "*** Update File: a.txt", "*** End Patch"),
		"update without change": patchText("*** Begin Patch", "*** Update File: a.txt", "@@",
			" context only", "*** End Patch"),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(text); err == nil {
				t.Fatalf("Parse(%q) succeeded, want failure", text)
			} else if failure, ok := errors.AsType[*Failure](err); !ok {
				t.Fatalf("Parse error = %T, want *Failure", err)
			} else if failure.Kind == "" || failure.Hint == "" {
				t.Fatalf("failure missing kind or hint: %+v", failure)
			}
		})
	}
}

func TestParseAllowsBlankLinesBetweenSections(t *testing.T) {
	if _, err := Parse(patchText("*** Begin Patch", "", "*** Add File: a.txt", "+x", "*** End Patch")); err != nil {
		t.Fatalf("Parse: %v", err)
	}
}

func TestTargetsListsEveryPath(t *testing.T) {
	targets, err := Targets(patchText(
		"*** Begin Patch",
		"*** Update File: a.txt",
		"@@",
		"-x",
		"+y",
		"*** Delete File: b.txt",
		"*** End Patch",
	))
	if err != nil {
		t.Fatalf("Targets: %v", err)
	}
	if len(targets) != 2 || targets[0] != "a.txt" || targets[1] != "b.txt" {
		t.Fatalf("Targets = %#v, want [a.txt b.txt]", targets)
	}
}

func TestTargetsPropagatesParseFailure(t *testing.T) {
	if _, err := Targets("not a patch"); err == nil {
		t.Fatal("Targets succeeded on malformed input, want failure")
	}
}

func TestFailureErrorIncludesHint(t *testing.T) {
	failure := newFailure(FailureNoMatch, "a.txt", "did not match", "reread the file")
	if got, want := failure.Error(), "did not match Recovery hint: reread the file"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	bare := newFailure(FailureNoMatch, "a.txt", "did not match", "")
	if got, want := bare.Error(), "did not match"; got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
	var nilFailure *Failure
	if got := nilFailure.Error(); got != "" {
		t.Fatalf("nil Failure Error() = %q, want empty", got)
	}
}

func TestClassifyHunkLine(t *testing.T) {
	cases := []struct {
		line  string
		kind  byte
		value string
	}{
		{"", ' ', ""},
		{" context", ' ', "context"},
		{"-removed", '-', "removed"},
		{"+added", '+', "added"},
		{"unmarked", ' ', "unmarked"},
	}
	for _, tc := range cases {
		kind, value := classifyHunkLine(tc.line)
		if kind != tc.kind || value != tc.value {
			t.Errorf("classifyHunkLine(%q) = %q, %q; want %q, %q", tc.line, kind, value, tc.kind, tc.value)
		}
	}
}
