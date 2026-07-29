package engine

import (
	"slices"
	"strings"
	"testing"
)

func TestSplitMarkdownSectionsKeepsOnlyLevelTwoHeadings(t *testing.T) {
	sections := splitMarkdownSections(`# Title ignored

Preamble before any section is dropped.

## Current State
working on auth

## Learnings
- one
- two
`)

	if got := sections["Current State"]; got != "working on auth" {
		t.Errorf("Current State = %q", got)
	}
	if got := sections["Learnings"]; !strings.Contains(got, "- one") {
		t.Errorf("Learnings = %q", got)
	}
	// A level-one heading is not a section, and text before the first "## " has
	// nowhere to go.
	if _, present := sections["Title ignored"]; present {
		t.Errorf("level-one heading became a section: %v", sections)
	}
}

func TestParseBulletListNormalizesToBullets(t *testing.T) {
	got := parseBulletList("- already a bullet\nnot a bullet\n\n   \n- spaced   ")
	want := []string{"- already a bullet", "- not a bullet", "- spaced"}
	if !slices.Equal(got, want) {
		t.Fatalf("parseBulletList = %#v, want %#v", got, want)
	}
}

func TestMergeBulletListsPrefersPrimaryAndDeduplicates(t *testing.T) {
	got := mergeBulletLists(
		[]string{"- current one", "- shared"},
		[]string{"- shared", "- previous one"},
		10,
	)
	want := []string{"- current one", "- shared", "- previous one"}
	if !slices.Equal(got, want) {
		t.Fatalf("mergeBulletLists = %#v, want %#v", got, want)
	}
}

func TestMergeBulletListsDeduplicatesAcrossBulletPrefixes(t *testing.T) {
	// One side may arrive without the "- " prefix; both must count as the same
	// item rather than appearing twice.
	got := mergeBulletLists([]string{"shared"}, []string{"- shared"}, 10)
	if len(got) != 1 {
		t.Fatalf("mergeBulletLists = %#v, want a single entry", got)
	}
}

func TestMergeBulletListsHonoursLimit(t *testing.T) {
	got := mergeBulletLists([]string{"- a", "- b", "- c"}, []string{"- d"}, 2)
	if len(got) != 2 {
		t.Fatalf("mergeBulletLists = %#v, want 2 items", got)
	}
	// The limit must cut the fallback first, never reorder the primary.
	if got[0] != "- a" || got[1] != "- b" {
		t.Fatalf("mergeBulletLists = %#v, want the primary entries", got)
	}
}

func TestParseSessionMemoryMarkdownReadsLegacyHeadings(t *testing.T) {
	// Older snapshots used different headings; both spellings must parse so a
	// resumed session does not lose its memory.
	doc := parseSessionMemoryMarkdown(`## Current Objective
ship the auth fix

## Important Files
- internal/auth/token.go

## Next Steps
- write tests
`)

	if doc.SessionTitle != "ship the auth fix" {
		t.Errorf("SessionTitle = %q, want the legacy Current Objective", doc.SessionTitle)
	}
	if !slices.Contains(doc.FilesAndFunctions, "- internal/auth/token.go") {
		t.Errorf("FilesAndFunctions = %v", doc.FilesAndFunctions)
	}
	if !slices.Contains(doc.KeyResults, "- write tests") {
		t.Errorf("KeyResults = %v", doc.KeyResults)
	}
}

func TestParseSessionMemoryMarkdownPrefersCurrentHeadings(t *testing.T) {
	doc := parseSessionMemoryMarkdown(`## Session Title
current title

## Current Objective
legacy title
`)
	if doc.SessionTitle != "current title" {
		t.Fatalf("SessionTitle = %q, want the current heading to win", doc.SessionTitle)
	}
}

func TestParseSessionMemoryMarkdownHandlesEmptyContent(t *testing.T) {
	if doc := parseSessionMemoryMarkdown("   \n  "); doc.SessionTitle != "" || len(doc.Learnings) != 0 {
		t.Fatalf("empty content produced %+v", doc)
	}
}

func TestMergeSessionMemoryDocumentsPrefersCurrentScalars(t *testing.T) {
	previous := sessionMemoryDocument{SessionTitle: "old", CurrentState: "old state"}
	current := sessionMemoryDocument{SessionTitle: "new"}

	merged := mergeSessionMemoryDocuments(previous, current)
	if merged.SessionTitle != "new" {
		t.Errorf("SessionTitle = %q, want the current value", merged.SessionTitle)
	}
	// A field the new snapshot omitted falls back rather than being cleared.
	if merged.CurrentState != "old state" {
		t.Errorf("CurrentState = %q, want the retained previous value", merged.CurrentState)
	}
}

func TestFirstNonEmptySnippetSkipsBlanks(t *testing.T) {
	if got := firstNonEmptySnippet("", "   ", "real", "later"); got != "real" {
		t.Fatalf("firstNonEmptySnippet = %q", got)
	}
	if got := firstNonEmptySnippet("", "  "); got != "" {
		t.Fatalf("firstNonEmptySnippet = %q, want empty", got)
	}
}
