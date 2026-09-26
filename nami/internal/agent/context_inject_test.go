package agent

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadTurnContextLeavesTheGitIndexAlone(t *testing.T) {
	// The turn context runs git status in the background on every iteration.
	// A plain status refreshes stale stat data by taking .git/index.lock and
	// rewriting the index, which makes the user's own concurrent git add or
	// commit fail on the held lock.
	if _, err := exec.LookPath("git"); err != nil {
		t.Skipf("git unavailable: %v", err)
	}
	repo := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-c", "user.name=nami", "-c", "user.email=nami@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		cmd.Dir = repo
		cmd.Env = append(os.Environ(), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL="+os.DevNull)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	source := filepath.Join(repo, "main.go")
	if err := os.WriteFile(source, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	runGit("add", "main.go")
	runGit("commit", "-q", "-m", "init")

	// Same content, new mod time: the index's cached stat data is now stale.
	later := time.Now().Add(time.Hour)
	if err := os.Chtimes(source, later, later); err != nil {
		t.Fatalf("touch main.go: %v", err)
	}
	indexPath := filepath.Join(repo, ".git", "index")
	before, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}

	t.Chdir(repo)
	LoadTurnContext()

	after, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("read index: %v", err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("loading the turn context rewrote the git index")
	}
}
