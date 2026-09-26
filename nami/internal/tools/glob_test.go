package tools

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func runGlob(t *testing.T, params map[string]any) ToolOutput {
	t.Helper()
	output, err := NewGlobTool().Execute(context.Background(), ToolInput{Params: params})
	if err != nil {
		t.Fatalf("execute file_search: %v", err)
	}
	return output
}

// A walk that finishes without hitting the result limit must still report
// what it found; an empty output would read as "the tool returned nothing".
func TestGlobReturnsMatchesFromCompletedWalk(t *testing.T) {
	workspace := inWorkspace(t)
	goFile := writeWorkspaceFile(t, workspace, "pkg/a.go", "package pkg\n")
	writeWorkspaceFile(t, workspace, "pkg/b.txt", "text\n")

	cases := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"relative pattern", map[string]any{"query": "**/*.go"}, goFile},
		{"path scoped pattern", map[string]any{"query": "*.go", "path": filepath.Join(workspace, "pkg")}, goFile},
		{"absolute pattern", map[string]any{"query": filepath.Join(workspace, "**", "*.go")}, goFile},
		{"no match", map[string]any{"query": "**/*.rs"}, "No files found"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			output := runGlob(t, tc.params)
			if output.Output != tc.want {
				t.Fatalf("output = %q, want %q", output.Output, tc.want)
			}
		})
	}
}

func TestGlobReportsTruncation(t *testing.T) {
	workspace := inWorkspace(t)
	for _, name := range []string{"a.go", "b.go", "c.go"} {
		writeWorkspaceFile(t, workspace, name, "package x\n")
	}

	output := runGlob(t, map[string]any{"query": "*.go", "maxResults": 2})
	if !output.Truncated {
		t.Fatalf("Truncated = false, want true for 3 matches with maxResults=2")
	}
	lines := strings.Split(output.Output, "\n")
	if len(lines) != 3 || !strings.HasPrefix(lines[2], "(Results are truncated.") {
		t.Fatalf("output = %q, want two paths and a truncation note", output.Output)
	}
}

func TestGlobStopsOnCancelledContext(t *testing.T) {
	workspace := inWorkspace(t)
	writeWorkspaceFile(t, workspace, "a.go", "package x\n")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := NewGlobTool().Execute(ctx, ToolInput{Params: map[string]any{"query": "*.go"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}
