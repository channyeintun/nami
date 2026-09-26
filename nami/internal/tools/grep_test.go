package tools

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

type searchRunner func(ctx context.Context, searchPath, pattern, outputMode string, params map[string]any) (string, string, error)

// installFakeRipgrep puts an rg on PATH that prints the given stdout and
// stderr and exits with the given status, so each exit-status branch of
// grep_search can be exercised without an unreadable directory.
func installFakeRipgrep(t *testing.T, stdout, stderr string, exitCode int) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("fake rg is a shell script")
	}
	dir := t.TempDir()
	script := "#!/bin/sh\nprintf '%s' \"$FAKE_RG_STDOUT\"\nprintf '%s' \"$FAKE_RG_STDERR\" >&2\nexit \"$FAKE_RG_EXIT\"\n"
	if err := os.WriteFile(filepath.Join(dir, "rg"), []byte(script), 0o755); err != nil {
		t.Fatalf("write fake rg: %v", err)
	}
	t.Setenv("PATH", dir)
	t.Setenv("FAKE_RG_STDOUT", stdout)
	t.Setenv("FAKE_RG_STDERR", stderr)
	t.Setenv("FAKE_RG_EXIT", strconv.Itoa(exitCode))
}

// rg and grep exit 1 when nothing matched and 2 when any path failed — even
// after printing matches from the rest of the tree. An unreadable directory
// (a root-owned docker volume, say) must not throw away every other match.
func TestGrepHandlesSearchExitStatus(t *testing.T) {
	workspace := t.TempDir()
	match := filepath.Join(workspace, "a.txt") + ":1:needle"
	denied := "rg: " + filepath.Join(workspace, "locked") + ": Permission denied (os error 13)"

	cases := []struct {
		name         string
		stdout       string
		stderr       string
		exitCode     int
		wantErr      string
		wantContains []string
	}{
		{"matches", match + "\n", "", 0, "", []string{match}},
		{"no matches", "", "", 1, "", []string{"No matches found"}},
		{"matches with unreadable paths", match + "\n", denied + "\n", 2, "", []string{match, "Some paths could not be searched", denied}},
		{"failure without matches", "", "rg: regex parse error\n", 2, "regex parse error", nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			installFakeRipgrep(t, tc.stdout, tc.stderr, tc.exitCode)
			output, err := NewGrepTool().Execute(context.Background(), ToolInput{Params: map[string]any{
				"query":       "needle",
				"path":        workspace,
				"output_mode": "content",
			}})
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want an error containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("grep_search: %v", err)
			}
			for _, want := range tc.wantContains {
				if !strings.Contains(output.Output, want) {
					t.Fatalf("output = %q, want it to contain %q", output.Output, want)
				}
			}
		})
	}
}

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
				output, _, err := run(context.Background(), workspace, regexp.QuoteMeta(query), "content", map[string]any{})
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
