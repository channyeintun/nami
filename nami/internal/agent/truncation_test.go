package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"
)

// multibyteText returns n two-byte runes after one ASCII byte, so every byte
// limit that is an even number lands inside a rune.
func multibyteText(n int) string {
	return "a" + strings.Repeat("é", n)
}

func writeTruncationFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "input.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write input: %v", err)
	}
	return path
}

func TestPromptTruncationKeepsRunesWhole(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		truncate func(t *testing.T, input string) string
	}{
		{
			name:  "instruction file",
			input: multibyteText(maxMemoryFileBytes),
			truncate: func(t *testing.T, input string) string {
				content, err := readMemoryFile(writeTruncationFile(t, input))
				if err != nil {
					t.Fatalf("readMemoryFile: %v", err)
				}
				return content
			},
		},
		{
			name:  "memory index",
			input: multibyteText(maxMemoryIndexBytes),
			truncate: func(t *testing.T, input string) string {
				content, err := readMemoryIndex(writeTruncationFile(t, input))
				if err != nil {
					t.Fatalf("readMemoryIndex: %v", err)
				}
				return content
			},
		},
		{
			name:  "memory note excerpt",
			input: multibyteText(maxMemoryNoteBytes),
			truncate: func(t *testing.T, input string) string {
				return loadMemoryNoteExcerpt(writeTruncationFile(t, input))
			},
		},
		{
			name:  "live snippet without line breaks",
			input: multibyteText(retrievalMaxSnippetBytes),
			truncate: func(t *testing.T, input string) string {
				return readFileSnippet(writeTruncationFile(t, input))
			},
		},
		{
			name:  "attempt log error signature",
			input: multibyteText(200),
			truncate: func(_ *testing.T, input string) string {
				return errorSignatureFromOutput(input)
			},
		},
		{
			name:  "directory listing",
			input: "",
			truncate: func(t *testing.T, _ string) string {
				dir := t.TempDir()
				for _, prefix := range "abcdefghijklmnopqrst" {
					name := string(prefix) + strings.Repeat("é", 100)
					if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
						t.Fatalf("create %s: %v", name, err)
					}
				}
				return listDirectory(dir)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.truncate(t, tt.input)
			if tt.input != "" && len(got) >= len(tt.input) {
				t.Fatalf("expected the input to be truncated, got %d of %d bytes", len(got), len(tt.input))
			}
			if !utf8.ValidString(got) {
				t.Fatalf("truncated text is not valid UTF-8; tail: %q", got[max(0, len(got)-40):])
			}
		})
	}
}
