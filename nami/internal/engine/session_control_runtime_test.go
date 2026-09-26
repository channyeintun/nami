package engine

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	toolpkg "github.com/channyeintun/nami/internal/tools"
)

// newTestRepo creates a repository with a "release" branch one commit behind
// HEAD, isolated from the user's git configuration.
func newTestRepo(t *testing.T) (dir string, git func(args ...string) string) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	dir = t.TempDir()
	git = func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_CONFIG_GLOBAL="+os.DevNull,
			"GIT_CONFIG_NOSYSTEM=1",
			"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@example.com",
		)
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
		}
		return strings.TrimSpace(string(output))
	}
	git("init", "-q")
	git("commit", "-q", "--allow-empty", "-m", "one")
	git("branch", "release")
	git("commit", "-q", "--allow-empty", "-m", "two")
	return dir, git
}

// The branch comes from the model. Git parses "-B<name>" as an option even
// after the path, which would reset an existing branch to HEAD.
func TestPrepareWorktreeRefusesAnOptionShapedBranch(t *testing.T) {
	repo, git := newTestRepo(t)
	before := git("rev-parse", "release")

	runtime := &sessionControlRuntime{}
	for _, branch := range []string{"-Brelease", "--force"} {
		for _, createBranch := range []bool{false, true} {
			_, _, err := runtime.prepareWorktree(t.Context(), repo, toolpkg.WorktreeControlRequest{
				Path:         filepath.Join(t.TempDir(), "worktree"),
				Branch:       branch,
				CreateBranch: createBranch,
			})
			if err == nil {
				t.Fatalf("branch %q (createBranch=%v) was accepted", branch, createBranch)
			}
		}
	}
	if after := git("rev-parse", "release"); after != before {
		t.Fatalf("branch release moved from %s to %s", before, after)
	}
}

// The session metadata records the branch through currentGitBranch, which
// runs one git command instead of the full turn-context scan.
func TestCurrentGitBranchReadsTheCheckedOutBranch(t *testing.T) {
	repo, git := newTestRepo(t)
	git("checkout", "-q", "release")
	t.Chdir(repo)
	if got := currentGitBranch(); got != "release" {
		t.Fatalf("currentGitBranch = %q, want release", got)
	}

	t.Chdir(t.TempDir())
	if got := currentGitBranch(); got != "" {
		t.Fatalf("outside a repository currentGitBranch = %q, want empty", got)
	}
}

func TestPrepareWorktreeStillAddsAnOrdinaryBranch(t *testing.T) {
	repo, _ := newTestRepo(t)
	target := filepath.Join(t.TempDir(), "worktree")

	runtime := &sessionControlRuntime{}
	path, created, err := runtime.prepareWorktree(t.Context(), repo, toolpkg.WorktreeControlRequest{Path: target, Branch: "release"})
	if err != nil {
		t.Fatalf("prepareWorktree: %v", err)
	}
	if !created || path != target {
		t.Fatalf("prepareWorktree = %q, created %v; want %q created", path, created, target)
	}
}
