package tools

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Each diagnostics run is a full `go build` or `tsc`. A grouped edit runs them
// once for everything it changed, and reports what they found.
func TestMultiReplaceRunsDiagnosticsOnceAndReportsThem(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	workspace := inWorkspace(t)
	writeWorkspaceFile(t, workspace, "go.mod", "module example.com/probe\n\ngo 1.21\n")
	path := writeWorkspaceFile(t, workspace, "main.go", "package main\n\nfunc one() int { return 1 }\n\nfunc two() int { return 2 }\n\nfunc main() {}\n")

	// A fake go on PATH counts its runs; the real toolchain stays out of it.
	fakeDir := t.TempDir()
	countFile := filepath.Join(fakeDir, "runs")
	script := "#!/bin/sh\necho run >> '" + countFile + "'\necho 'main.go:3:1: fake diagnostic'\nexit 1\n"
	if err := os.WriteFile(filepath.Join(fakeDir, "go"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake go: %v", err)
	}
	t.Setenv("PATH", fakeDir)

	output, err := runValidatedTool(t, NewMultiReplaceStringInFileTool(), map[string]any{
		"explanation": "rename",
		"replacements": []any{
			map[string]any{"filePath": path, "oldString": "return 1", "newString": "return 10"},
			map[string]any{"filePath": path, "oldString": "return 2", "newString": "return 20"},
		},
	})
	if err != nil || output.IsError {
		t.Fatalf("multi_replace_string_in_file: err=%v output=%q", err, output.Output)
	}

	runs, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("read run count: %v", err)
	}
	if count := strings.Count(string(runs), "run"); count != 1 {
		t.Fatalf("diagnostics ran %d times for one module, want 1", count)
	}
	if !strings.Contains(output.Diagnostics, "fake diagnostic") {
		t.Fatalf("Diagnostics = %q, want the build output", output.Diagnostics)
	}
}
