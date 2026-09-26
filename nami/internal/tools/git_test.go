package tools

import (
	"os"
	"os/exec"
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
		args, err := buildGitArgs(operation, params, gitRepo{root: "/repo"})
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
	args, err := buildGitArgs("log", params, gitRepo{root: "/repo"})
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
	args, err := buildGitArgs("blame", params, gitRepo{root: "/repo"})
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
		got, err := gitPathspec(tc.pathspec, gitRepo{root: repoRoot})
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

// A relative path is relative to the working directory, which may sit below
// the repository root; the command itself runs from the root. Resolving such
// paths against the root made log and diff silently report nothing and blame
// fail with "no such path".
func TestGitPathspecIsRelativeToTheWorkingDirectory(t *testing.T) {
	repo := gitRepo{root: filepath.FromSlash("/repo"), prefix: "nami/internal/"}
	cases := []struct {
		pathspec string
		want     string
		wantErr  bool
	}{
		{pathspec: "tools/git.go", want: "nami/internal/tools/git.go"},
		{pathspec: ".", want: "nami/internal"},
		{pathspec: "../go.mod", want: "nami/go.mod"},
		{pathspec: "../../README.md", want: "README.md"},
		{pathspec: filepath.FromSlash("/repo/docs/guide.md"), want: "docs/guide.md"},
		{pathspec: "../../../outside", wantErr: true},
	}
	for _, tc := range cases {
		got, err := gitPathspec(tc.pathspec, repo)
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

// commitTestFiles creates a repository in a new temporary directory holding
// files, committed with message, and returns the repository root.
func commitTestFiles(t *testing.T, message string, files map[string]string) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	for name, content := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{
		{"init", "--quiet"},
		{"add", "."},
		{"-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--quiet", "-m", message},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, output)
		}
	}
	return root
}

func TestGitToolResolvesPathsFromASubdirectory(t *testing.T) {
	root := commitTestFiles(t, "add main", map[string]string{"pkg/main.go": "package main\n"})
	subdir := filepath.Join(root, "pkg")

	tool := NewGitTool()
	logged, err := tool.Execute(t.Context(), ToolInput{Params: map[string]any{
		"operation": "log", "oneline": true, "file_path": "main.go", "cwd": subdir,
	}})
	if err != nil || !strings.Contains(logged.Output, "add main") {
		t.Fatalf("log from the subdirectory = %+v, %v; want the commit that added main.go", logged, err)
	}
	blamed, err := tool.Execute(t.Context(), ToolInput{Params: map[string]any{
		"operation": "blame", "file_path": "main.go", "cwd": subdir,
	}})
	if err != nil || blamed.IsError || !strings.Contains(blamed.Output, "package main") {
		t.Fatalf("blame from the subdirectory = %+v, %v; want the annotated file", blamed, err)
	}
}

// git can print far more than any result can use: log --stat over a long
// history, or a vendored dependency's diff. Buffering all of it within the
// timeout could take gigabytes, so the output is capped like bash's.
func TestGitToolCapsItsOutput(t *testing.T) {
	large := strings.Repeat("a line of generated text\n", 200_000) // about 5MB
	root := commitTestFiles(t, "add a large file", map[string]string{"large.txt": large})

	output, err := NewGitTool().Execute(t.Context(), ToolInput{Params: map[string]any{
		"operation": "show", "cwd": root,
	}})
	if err != nil {
		t.Fatalf("show: %v", err)
	}
	if len(output.Output) > maxForegroundOutputBytes+1024 {
		t.Fatalf("show returned %d bytes, want at most about %d", len(output.Output), maxForegroundOutputBytes)
	}
	if !strings.Contains(output.Output, "Output truncated") {
		t.Fatalf("output does not say it was truncated; ends with %q", lastBytes(output.Output, 120))
	}
}

// Outside a repository the model needs git's reason, not just its exit code.
func TestGitToolReportsWhyItFoundNoRepository(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	_, err := NewGitTool().Execute(t.Context(), ToolInput{Params: map[string]any{
		"operation": "status", "cwd": t.TempDir(),
	}})
	if err == nil || !strings.Contains(err.Error(), "not a git repository") {
		t.Fatalf("error = %v, want git's not-a-repository message", err)
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
