package skills

import (
	"slices"
	"strings"
	"testing"
)

func TestParseFrontmatterExtractsKeysAndBody(t *testing.T) {
	fm, body := ParseFrontmatter(`---
name: my-skill
description: Does a thing
---
# Heading

Body text.
`)

	if fm["name"] != "my-skill" {
		t.Errorf("name = %q", fm["name"])
	}
	if fm["description"] != "Does a thing" {
		t.Errorf("description = %q", fm["description"])
	}
	if !strings.HasPrefix(body, "# Heading") {
		t.Errorf("body = %q, want the frontmatter stripped", body)
	}
}

func TestParseFrontmatterKeepsColonsInValues(t *testing.T) {
	// Descriptions routinely contain colons and URLs; only the first splits.
	fm, _ := ParseFrontmatter(`---
description: Use when: the user asks about https://example.com/x
---
body
`)
	want := "Use when: the user asks about https://example.com/x"
	if fm["description"] != want {
		t.Fatalf("description = %q, want %q", fm["description"], want)
	}
}

func TestParseFrontmatterReturnsContentUnchangedWithoutDelimiters(t *testing.T) {
	content := "# Just markdown\n\nNo frontmatter here.\n"
	fm, body := ParseFrontmatter(content)
	if len(fm) != 0 {
		t.Errorf("fm = %v, want empty", fm)
	}
	if body != content {
		t.Errorf("body = %q, want the input unchanged", body)
	}
}

func TestParseFrontmatterHandlesUnterminatedBlock(t *testing.T) {
	// A missing closing delimiter must not swallow the document.
	content := "---\nname: broken\n"
	fm, body := ParseFrontmatter(content)
	if len(fm) != 0 {
		t.Errorf("fm = %v, want empty for an unterminated block", fm)
	}
	if body != content {
		t.Errorf("body = %q, want the input unchanged", body)
	}
}

func TestParseFrontmatterSkipsBlankAndKeylessLines(t *testing.T) {
	fm, _ := ParseFrontmatter(`---
name: my-skill

a line with no colon
---
body
`)
	if len(fm) != 1 || fm["name"] != "my-skill" {
		t.Fatalf("fm = %v, want only the valid pair", fm)
	}
}

func TestParseFrontmatterNeverReturnsNilMap(t *testing.T) {
	// Callers index the map directly, so it must always be usable.
	for _, content := range []string{"", "no frontmatter", "---\nunterminated"} {
		if fm, _ := ParseFrontmatter(content); fm == nil {
			t.Errorf("ParseFrontmatter(%q) returned a nil map", content)
		}
	}
}

func TestSplitCSVTrimsAndDropsBlanks(t *testing.T) {
	got := splitCSV(" a , b ,, c ,  ")
	want := []string{"a", "b", "c"}
	if !slices.Equal(got, want) {
		t.Fatalf("splitCSV = %#v, want %#v", got, want)
	}
}

func TestSplitCSVOnEmptyInput(t *testing.T) {
	for _, input := range []string{"", "   ", ",,,"} {
		if got := splitCSV(input); len(got) != 0 {
			t.Errorf("splitCSV(%q) = %#v, want empty", input, got)
		}
	}
}

// Editors on Windows save a byte order mark and CRLF line endings; neither
// may hide the frontmatter or leak into the body.
func TestParseFrontmatterHandlesByteOrderMarkAndCRLF(t *testing.T) {
	content := "\ufeff---\r\nname: review\r\ndescription: Review a change\r\n---\r\nBody line\r\n"
	fm, body := ParseFrontmatter(content)
	if fm["name"] != "review" || fm["description"] != "Review a change" {
		t.Fatalf("frontmatter = %#v", fm)
	}
	if body != "Body line\r\n" {
		t.Fatalf("body = %q, want the text after the closing line", body)
	}
}

func TestParseFrontmatterUnquotesScalars(t *testing.T) {
	fm, _ := ParseFrontmatter("---\ndescription: \"Formats code: Go and Rust\"\nargument-hint: 'a path'\nname: \"half\n---\nbody")
	if got := fm["description"]; got != "Formats code: Go and Rust" {
		t.Errorf("description = %q", got)
	}
	if got := fm["argument-hint"]; got != "a path" {
		t.Errorf("argument-hint = %q", got)
	}
	if got := fm["name"]; got != "\"half" {
		t.Errorf("an unmatched quote was removed: %q", got)
	}
}

func TestSplitCSVAcceptsYAMLFlowLists(t *testing.T) {
	got := splitCSV(`[Read, "Write", 'Bash']`)
	want := []string{"Read", "Write", "Bash"}
	if !slices.Equal(got, want) {
		t.Fatalf("splitCSV = %q, want %q", got, want)
	}
}
