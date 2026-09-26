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

// An absolute pattern is walked from its last directory before any pattern
// syntax, and brace alternatives are pattern syntax too.
func TestGlobAbsolutePatternWithBraces(t *testing.T) {
	workspace := inWorkspace(t)
	source := writeWorkspaceFile(t, workspace, "src/a.go", "package src\n")
	test := writeWorkspaceFile(t, workspace, "test/b.go", "package test\n")
	writeWorkspaceFile(t, workspace, "other/c.go", "package other\n")

	output := runGlob(t, map[string]any{"query": filepath.Join(workspace, "{src,test}", "*.go")})
	if want := source + "\n" + test; output.Output != want {
		t.Fatalf("output = %q, want %q", output.Output, want)
	}
}

func TestSplitAbsoluteGlobPattern(t *testing.T) {
	if filepath.Separator != '/' {
		t.Skip("patterns are written with forward slashes")
	}
	cases := []struct {
		pattern     string
		wantDir     string
		wantPattern string
	}{
		{"/ws/src/*.go", "/ws/src", "*.go"},
		{"/ws/**/*.go", "/ws", "**/*.go"},
		{"/ws/{src,test}/*.go", "/ws", "{src,test}/*.go"},
		{"/ws/src/main.go", "/ws/src", "main.go"},
		{"/*.go", "/", "*.go"},
	}
	for _, tc := range cases {
		dir, pattern := splitAbsoluteGlobPattern(tc.pattern)
		if dir != tc.wantDir || pattern != tc.wantPattern {
			t.Errorf("splitAbsoluteGlobPattern(%q) = %q, %q; want %q, %q", tc.pattern, dir, pattern, tc.wantDir, tc.wantPattern)
		}
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
