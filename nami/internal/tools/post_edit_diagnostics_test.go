package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSummarizeDiagnosticsOutputKeepsWholeCharacters(t *testing.T) {
	// One long line of three-byte characters overruns the character budget.
	summary := summarizeDiagnosticsOutput([]byte("error: " + strings.Repeat("好", 1000)))
	if !utf8.ValidString(summary) {
		t.Fatalf("summary ends with a split character: %q", summary[len(summary)-6:])
	}
	if len(summary) > maxDiagnosticChars || !strings.HasSuffix(summary, "...") {
		t.Fatalf("summary is %d bytes ending %q, want at most %d ending in ...", len(summary), summary[len(summary)-6:], maxDiagnosticChars)
	}
}

// Diagnostics run after every Go edit. `go build ./...` writes an executable
// into the module root when the pattern matches a single main package, so the
// check must discard its output instead of leaving binaries in the workspace.
func TestGoDiagnosticsLeaveNoBuildOutput(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not available")
	}
	module := t.TempDir()
	writeModuleFile := func(name, content string) string {
		path := filepath.Join(module, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		return path
	}
	writeModuleFile("go.mod", "module example.com/probe\n\ngo 1.21\n")
	mainFile := writeModuleFile("main.go", "package main\n\nfunc main() {}\n")

	cases := []struct {
		name   string
		source string
		want   string
	}{
		{"clean main package", "package main\n\nfunc main() {}\n", ": clean"},
		{"compile error", "package main\n\nfunc main() { return 1 }\n", "too many return values"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			writeModuleFile("main.go", tc.source)
			diagnostics := runPostEditDiagnostics(context.Background(), []string{mainFile})
			if !strings.Contains(diagnostics, tc.want) {
				t.Fatalf("diagnostics = %q, want it to contain %q", diagnostics, tc.want)
			}

			entries, err := os.ReadDir(module)
			if err != nil {
				t.Fatalf("read module dir: %v", err)
			}
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			if want := []string{"go.mod", "main.go"}; !slices.Equal(names, want) {
				t.Fatalf("module contains %v after diagnostics, want only %v", names, want)
			}
		})
	}
}
