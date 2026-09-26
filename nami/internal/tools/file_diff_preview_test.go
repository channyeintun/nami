package tools

import (
	"fmt"
	"strings"
	"testing"
)

func numberedLines(prefix string, count int) string {
	lines := make([]string, 0, count)
	for index := range count {
		lines = append(lines, fmt.Sprintf("%s%d", prefix, index))
	}
	return strings.Join(lines, "\n") + "\n"
}

func TestBuildFileDiffPreview(t *testing.T) {
	cases := []struct {
		name           string
		oldContent     string
		newContent     string
		wantInsertions int
		wantDeletions  int
		wantLines      int
		wantEllipsis   bool
	}{
		{"no change", "a\n", "a\n", 0, 0, 0, false},
		{"one line changed", "a\n", "b\n", 1, 1, 2, false},
		{"change after unchanged prefix", "keep\na\n", "keep\nb\n", 1, 1, 3, false},
		{"twelve changes fit without a prefix", numberedLines("old", 6), numberedLines("new", 6), 6, 6, 12, false},
		// "@@" takes one of the twelve preview lines, so one change is left
		// out and the preview has to say so.
		{"twelve changes after a prefix", "keep\n" + numberedLines("old", 6), "keep\n" + numberedLines("new", 6), 6, 6, 12, true},
		{"more changes than fit", numberedLines("old", 7), numberedLines("new", 7), 7, 7, 12, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			preview, insertions, deletions := buildFileDiffPreview(tc.oldContent, tc.newContent)
			if insertions != tc.wantInsertions || deletions != tc.wantDeletions {
				t.Fatalf("insertions, deletions = %d, %d; want %d, %d", insertions, deletions, tc.wantInsertions, tc.wantDeletions)
			}
			lines := splitDiffLines(preview)
			if len(lines) != tc.wantLines {
				t.Fatalf("preview has %d lines, want %d:\n%s", len(lines), tc.wantLines, preview)
			}
			if hasEllipsis := len(lines) > 0 && lines[len(lines)-1] == "..."; hasEllipsis != tc.wantEllipsis {
				t.Fatalf("preview ends with ellipsis = %v, want %v:\n%s", hasEllipsis, tc.wantEllipsis, preview)
			}
		})
	}
}
