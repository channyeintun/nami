package tools

import (
	"strings"
	"testing"
)

func TestLineEndingTextMapsBackToOriginal(t *testing.T) {
	contents := []string{"", "a", "a\n", "a\r\n", "a\r", "\r\r\n", "\r\n\r\n", "x\ry\r\nz\n", "tail\r\n\r"}
	for _, content := range contents {
		text := newLineEndingText(content)
		if want := strings.ReplaceAll(content, "\r\n", "\n"); text.normalized != want {
			t.Fatalf("normalized(%q) = %q, want %q", content, text.normalized, want)
		}
		if got := text.apply(nil); got != content {
			t.Fatalf("apply(nil) on %q = %q, want the original", content, got)
		}
		// Every normalized byte maps to exactly the original bytes it came from.
		for i := range len(text.normalized) {
			source := content[text.origin[i]:text.origin[i+1]]
			if strings.ReplaceAll(source, "\r\n", "\n") != text.normalized[i:i+1] {
				t.Fatalf("%q: normalized byte %d maps to %q", content, i, source)
			}
		}
	}
}

// Edits match against LF-normalized text, because that is how read_file shows
// files, but only the edited range may change in the file: every other line
// keeps exactly the line ending it had.
func TestLineEditsPreserveUntouchedLineEndings(t *testing.T) {
	cases := []struct {
		name      string
		content   string
		oldString string
		newString string
		want      string
	}{
		{"lf file", "a\nb\nc\n", "b", "B", "a\nB\nc\n"},
		{"crlf file", "a\r\nb\r\nc\r\n", "a\nb", "A\nB", "A\r\nB\r\nc\r\n"},
		{"crlf file, inserted lines", "a\r\nb\r\n", "a\n", "a\nx\ny\n", "a\r\nx\r\ny\r\nb\r\n"},
		{"mostly lf with one crlf line", "a\nb\r\nc\nd\n", "c", "C", "a\nb\r\nC\nd\n"},
		{"mostly crlf with one lf line", "a\r\nb\nc\r\nd\r\n", "c", "C", "a\r\nb\nC\r\nd\r\n"},
		{"edit spanning mixed endings", "a\nb\r\nc\n", "a\nb\nc", "x\ny", "x\ny\n"},
		{"lone carriage return elsewhere", "bar = \"\r\"\nfoo\n", "foo", "FOO", "bar = \"\r\"\nFOO\n"},
		{"trailing newline kept", "a\r\nb\r\n", "b\n", "B", "a\r\nB\r\n"},
		{"no trailing newline", "a\nb", "b", "B", "a\nB"},
		{"deleting everything leaves an empty file", "a\r\nb\r\n", "a\nb\n", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := inWorkspace(t)
			path := writeWorkspaceFile(t, workspace, "file.txt", tc.content)

			output, err := runValidatedTool(t, NewFileEditTool(), map[string]any{
				"filePath":  path,
				"oldString": tc.oldString,
				"newString": tc.newString,
			})
			if err != nil {
				t.Fatalf("replace_string_in_file: %v", err)
			}
			if output.IsError {
				t.Fatalf("output = %s", output.Output)
			}
			if got := readWorkspaceFile(t, path); got != tc.want {
				t.Fatalf("content = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestLineEditsReplaceAllPreservesLineEndings(t *testing.T) {
	workspace := inWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "file.txt", "x\r\ny\nx\r\nz\n")

	output, err := runValidatedTool(t, NewFileEditTool(), map[string]any{
		"filePath":   path,
		"oldString":  "x",
		"newString":  "w",
		"replaceAll": true,
	})
	if err != nil || output.IsError {
		t.Fatalf("replace_string_in_file: err=%v output=%q", err, output.Output)
	}
	if got, want := readWorkspaceFile(t, path), "w\r\ny\nw\r\nz\n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}

// Lines outside the hunks keep their endings; line breaks inside replaced
// blocks take the file's predominant ending, LF here (three LF, two CRLF).
func TestApplyPatchPreservesUntouchedLineEndings(t *testing.T) {
	workspace := inWorkspace(t)
	path := writeWorkspaceFile(t, workspace, "mixed.txt", "one\ntwo\r\nthree\nfour\nfive\r\n")

	output := runApplyPatch(t, strings.Join([]string{
		"*** Begin Patch",
		"*** Update File: mixed.txt",
		"@@",
		"-one",
		"+ONE",
		"@@",
		" four",
		"-five",
		"+FIVE",
		"+six",
		"*** End Patch",
	}, "\n"))
	if output.IsError {
		t.Fatalf("output = %s", output.Output)
	}
	if got, want := readWorkspaceFile(t, path), "ONE\ntwo\r\nthree\nfour\nFIVE\nsix\r\n"; got != want {
		t.Fatalf("content = %q, want %q", got, want)
	}
}
