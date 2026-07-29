package patch

import (
	"errors"
	"strings"
	"testing"
)

func hunkFrom(lines ...string) Hunk {
	hunk := Hunk{}
	for _, line := range lines {
		kind, value := classifyHunkLine(line)
		hunk.Lines = append(hunk.Lines, Line{Kind: kind, Value: value})
	}
	return hunk
}

func failureKind(t *testing.T, err error) FailureKind {
	t.Helper()
	failure, ok := errors.AsType[*Failure](err)
	if !ok {
		t.Fatalf("error = %T (%v), want *Failure", err, err)
	}
	return failure.Kind
}

func TestApplyReplacesMatchedBlock(t *testing.T) {
	got, err := Apply("alpha\nbeta\ngamma\n", "a.txt", []Hunk{
		hunkFrom(" alpha", "-beta", "+beta updated", " gamma"),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if want := "alpha\nbeta updated\ngamma\n"; got != want {
		t.Fatalf("Apply = %q, want %q", got, want)
	}
}

func TestApplyAppliesMultipleHunksInOneFile(t *testing.T) {
	content := "one\ntwo\nthree\nfour\nfive\n"
	got, err := Apply(content, "a.txt", []Hunk{
		hunkFrom("-one", "+ONE"),
		hunkFrom("-five", "+FIVE"),
	})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if want := "ONE\ntwo\nthree\nfour\nFIVE\n"; got != want {
		t.Fatalf("Apply = %q, want %q", got, want)
	}
}

// A hunk describes whole lines. Matching a fragment inside a longer line would
// splice the replacement into the middle of unrelated text.
func TestApplyRequiresLineAlignedMatches(t *testing.T) {
	_, err := Apply("prefix-target-suffix\n", "a.txt", []Hunk{hunkFrom("-target", "+replaced")})
	if err == nil {
		t.Fatal("Apply matched a fragment inside a line, want no-match failure")
	}
	if kind := failureKind(t, err); kind != FailureNoMatch {
		t.Fatalf("failure kind = %q, want %q", kind, FailureNoMatch)
	}
}

func TestApplyMatchesLastLineWithoutTrailingNewline(t *testing.T) {
	got, err := Apply("alpha\nomega", "a.txt", []Hunk{hunkFrom("-omega", "+updated")})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if want := "alpha\nupdated"; got != want {
		t.Fatalf("Apply = %q, want %q", got, want)
	}
}

func TestApplyRejectsAmbiguousHunks(t *testing.T) {
	_, err := Apply("dup\nother\ndup\n", "a.txt", []Hunk{hunkFrom("-dup", "+changed")})
	if kind := failureKind(t, err); kind != FailureMultipleMatch {
		t.Fatalf("failure kind = %q, want %q", kind, FailureMultipleMatch)
	}
}

func TestApplyRejectsOverlappingHunks(t *testing.T) {
	content := "a\nb\nc\n"
	_, err := Apply(content, "a.txt", []Hunk{
		hunkFrom(" a", "-b", "+B"),
		hunkFrom("-b", "+bee", " c"),
	})
	if kind := failureKind(t, err); kind != FailureOverlap {
		t.Fatalf("failure kind = %q, want %q", kind, FailureOverlap)
	}
}

func TestApplyRejectsEmptyHunkList(t *testing.T) {
	_, err := Apply("a\n", "a.txt", nil)
	if kind := failureKind(t, err); kind != FailureInvalidFormat {
		t.Fatalf("failure kind = %q, want %q", kind, FailureInvalidFormat)
	}
}

func TestApplyRejectsNoOpPatch(t *testing.T) {
	// Removing and re-adding the same line leaves the file untouched.
	_, err := Apply("a\nb\n", "a.txt", []Hunk{hunkFrom("-b", "+b")})
	if kind := failureKind(t, err); kind != FailureNoOp {
		t.Fatalf("failure kind = %q, want %q", kind, FailureNoOp)
	}
}

func TestLocateHunkRejectsContextOnlyHunk(t *testing.T) {
	_, err := LocateHunk("a\n", "a.txt", hunkFrom(" a"))
	if kind := failureKind(t, err); kind != FailureInvalidFormat {
		t.Fatalf("failure kind = %q, want %q", kind, FailureInvalidFormat)
	}
}

func TestLocateHunkRejectsUnknownLineKind(t *testing.T) {
	hunk := Hunk{Lines: []Line{{Kind: '?', Value: "x"}}}
	_, err := LocateHunk("x\n", "a.txt", hunk)
	if kind := failureKind(t, err); kind != FailureInvalidFormat {
		t.Fatalf("failure kind = %q, want %q", kind, FailureInvalidFormat)
	}
}

func TestLocateHunkReportsReplacementRange(t *testing.T) {
	content := "alpha\nbeta\ngamma\n"
	replacement, err := LocateHunk(content, "a.txt", hunkFrom("-beta", "+beta!"))
	if err != nil {
		t.Fatalf("LocateHunk: %v", err)
	}
	if content[replacement.Start:replacement.End] != replacement.OldBlock {
		t.Fatalf("range %d:%d does not cover OldBlock %q", replacement.Start, replacement.End, replacement.OldBlock)
	}
	if replacement.NewBlock != "beta!" {
		t.Fatalf("NewBlock = %q, want %q", replacement.NewBlock, "beta!")
	}
}

func TestFindLineAlignedMatch(t *testing.T) {
	cases := []struct {
		name    string
		content string
		needle  string
		index   int
		count   int
	}{
		{"empty needle", "abc", "", -1, 0},
		{"start of file", "abc\ndef\n", "abc", 0, 1},
		{"middle line", "abc\ndef\n", "def", 4, 1},
		{"multi line block", "a\nb\nc\n", "a\nb", 0, 1},
		{"fragment inside line", "xabcx\n", "abc", -1, 0},
		{"suffix of line", "xabc\n", "abc", -1, 0},
		{"prefix of line", "abcx\n", "abc", -1, 0},
		{"repeated", "dup\ndup\n", "dup", 0, 2},
		{"final line no newline", "a\nb", "b", 2, 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			index, count := findLineAlignedMatch(tc.content, tc.needle)
			if index != tc.index || count != tc.count {
				t.Fatalf("findLineAlignedMatch(%q, %q) = %d, %d; want %d, %d", tc.content, tc.needle, index, count, tc.index, tc.count)
			}
		})
	}
}

func TestParseThenApplyRoundTrip(t *testing.T) {
	document, err := Parse(strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: main.go",
		"@@",
		" package main",
		"-const version = \"1\"",
		"+const version = \"2\"",
		"*** End Patch",
	}, "\n"))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	content := "package main\nconst version = \"1\"\n"
	got, err := Apply(content, "main.go", document.Operations[0].Hunks)
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if want := "package main\nconst version = \"2\"\n"; got != want {
		t.Fatalf("Apply = %q, want %q", got, want)
	}
}
