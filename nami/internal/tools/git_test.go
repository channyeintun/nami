package tools

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestBuildGitArgsRejectsOptionShapedRevision(t *testing.T) {
	// git reads a leading "-" as an option, and diff, log, and show accept
	// --output=<file>. Passing it through would let the read-only tool
	// truncate or overwrite any file the process can write.
	for _, operation := range []string{"diff", "log", "show", "blame"} {
		params := map[string]any{"revision": "--output=/tmp/clobbered", "file_path": "README.md"}
		args, err := buildGitArgs(operation, params, "/repo")
		if err == nil {
			t.Errorf("%s: buildGitArgs = %q, want an error for an option-shaped revision", operation, args)
			continue
		}
		if !strings.Contains(err.Error(), "must not start with '-'") {
			t.Errorf("%s: error = %v, want the leading-dash rejection", operation, err)
		}
	}
}

func TestBuildGitArgsKeepsRevisionBeforePathspecs(t *testing.T) {
	params := map[string]any{"revision": " HEAD~2..HEAD ", "pathspecs": []any{"internal/tools"}}
	args, err := buildGitArgs("log", params, "/repo")
	if err != nil {
		t.Fatalf("buildGitArgs returned error: %v", err)
	}
	want := []string{"log", "HEAD~2..HEAD", "--", "internal/tools"}
	if !slices.Equal(args, want) {
		t.Fatalf("buildGitArgs = %q, want %q", args, want)
	}
}

func TestBuildGitArgsBlameOrdersRangeRevisionAndFile(t *testing.T) {
	params := map[string]any{"file_path": "main.go", "line_start": 3, "line_end": 9, "revision": "v1.0"}
	args, err := buildGitArgs("blame", params, "/repo")
	if err != nil {
		t.Fatalf("buildGitArgs returned error: %v", err)
	}
	want := []string{"blame", "-L", "3,9", "v1.0", "--", "main.go"}
	if !slices.Equal(args, want) {
		t.Fatalf("buildGitArgs = %q, want %q", args, want)
	}
}

func TestGitPathspecStaysInsideRepository(t *testing.T) {
	repoRoot := filepath.FromSlash("/repo")
	cases := []struct {
		pathspec string
		want     string
		wantErr  bool
	}{
		{pathspec: "internal/tools", want: "internal/tools"},
		{pathspec: ".", want: "."},
		{pathspec: "..hidden/file", want: "..hidden/file"},
		{pathspec: filepath.FromSlash("/repo/cmd/main.go"), want: "cmd/main.go"},
		{pathspec: filepath.FromSlash("/repo"), want: "."},
		{pathspec: "../outside", wantErr: true},
		{pathspec: "a/../../outside", wantErr: true},
		{pathspec: filepath.FromSlash("/elsewhere/file"), wantErr: true},
		{pathspec: "   ", wantErr: true},
	}
	for _, tc := range cases {
		got, err := gitPathspec(tc.pathspec, repoRoot)
		if tc.wantErr {
			if err == nil {
				t.Errorf("gitPathspec(%q) = %q, want an error", tc.pathspec, got)
			}
			continue
		}
		if err != nil || got != tc.want {
			t.Errorf("gitPathspec(%q) = %q, %v; want %q", tc.pathspec, got, err, tc.want)
		}
	}
}

func TestTimeoutFromParamsUsesFallback(t *testing.T) {
	const fallback = 7 * time.Second
	if got := timeoutFromParams(map[string]any{}, fallback); got != fallback {
		t.Errorf("missing timeout_ms = %v, want %v", got, fallback)
	}
	if got := timeoutFromParams(map[string]any{"timeout_ms": float64(-5)}, fallback); got != fallback {
		t.Errorf("negative timeout_ms = %v, want %v", got, fallback)
	}
	if got := timeoutFromParams(map[string]any{"timeout_ms": float64(1500)}, fallback); got != 1500*time.Millisecond {
		t.Errorf("timeout_ms 1500 = %v, want 1.5s", got)
	}
}
