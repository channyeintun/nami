package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

type searchRunner func(ctx context.Context, searchPath, pattern, outputMode string, params map[string]any) (string, error)

// A search for text that starts with "-" (a CLI flag, an HTML comment end)
// must reach the search tool as a pattern, never as an option.
func TestSearchRunnersTreatLeadingDashAsPattern(t *testing.T) {
	workspace := t.TempDir()
	file := filepath.Join(workspace, "flags.txt")
	if err := os.WriteFile(file, []byte("run --verbose\nkeep -q here\nplain\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	runners := map[string]searchRunner{"rg": runRipgrep, "grep": runGrepFallback}
	for name, run := range runners {
		t.Run(name, func(t *testing.T) {
			if _, err := exec.LookPath(name); err != nil {
				t.Skipf("%s not installed", name)
			}
			cases := map[string]string{"--verbose": "run --verbose", "-q": "keep -q here"}
			for query, wantLine := range cases {
				output, err := run(context.Background(), workspace, regexp.QuoteMeta(query), "content", map[string]any{})
				if err != nil {
					t.Fatalf("search %q: %v", query, err)
				}
				lines := splitOutputLines(output)
				if len(lines) != 1 || !strings.HasSuffix(lines[0], wantLine) || !strings.HasPrefix(lines[0], file) {
					t.Fatalf("search %q = %q, want the single line %q from %s", query, output, wantLine, file)
				}
			}
		})
	}
}
