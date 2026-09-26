package agent

import (
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/channyeintun/nami/internal/api"
)

func TestParseMemoryIndexEntriesKeepsNotesInsideTheMemoryDir(t *testing.T) {
	root := t.TempDir()
	memoryDir := filepath.Join(root, "memory")
	outsideDir := filepath.Join(root, "outside")
	for _, dir := range []string{memoryDir, outsideDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("create %s: %v", dir, err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	symlink := func(target, link string) {
		t.Helper()
		if err := os.Symlink(target, link); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	write(filepath.Join(memoryDir, "project-style.md"), "Use tabs.")
	write(filepath.Join(outsideDir, "credentials.md"), "token=secret")
	symlink(outsideDir, filepath.Join(memoryDir, "linked-dir"))
	symlink(filepath.Join(outsideDir, "credentials.md"), filepath.Join(memoryDir, "linked-note.md"))
	symlink(filepath.Join(memoryDir, "project-style.md"), filepath.Join(memoryDir, "alias.md"))
	// The whole memory directory may itself be reached through a symlink.
	symlink(memoryDir, filepath.Join(root, "memory-link"))

	tests := []struct {
		name      string
		indexDir  string
		filename  string
		wantValid bool
	}{
		{name: "plain note", indexDir: memoryDir, filename: "project-style.md", wantValid: true},
		{name: "symlink to a note inside", indexDir: memoryDir, filename: "alias.md", wantValid: true},
		{name: "memory dir reached through a symlink", indexDir: filepath.Join(root, "memory-link"), filename: "project-style.md", wantValid: true},
		{name: "parent traversal", indexDir: memoryDir, filename: "../outside/credentials.md"},
		{name: "symlinked directory leading outside", indexDir: memoryDir, filename: "linked-dir/credentials.md"},
		{name: "symlinked note leading outside", indexDir: memoryDir, filename: "linked-note.md"},
		{name: "the memory dir itself", indexDir: memoryDir, filename: "."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			index := MemoryFile{
				Path:    filepath.Join(tt.indexDir, "MEMORY.md"),
				Type:    memoryTypeProjectIndex,
				Content: "- [" + tt.filename + "] Style notes (project)",
			}
			entries := ParseMemoryIndexEntries(index)
			if len(entries) != 1 {
				t.Fatalf("expected one entry, got %+v", entries)
			}
			entry := entries[0]
			if tt.wantValid {
				if entry.Issue != "" || entry.NotePath == "" {
					t.Fatalf("expected a usable note path, got path %q issue %q", entry.NotePath, entry.Issue)
				}
				if excerpt := loadMemoryNoteExcerpt(entry.NotePath); excerpt != "Use tabs." {
					t.Fatalf("expected the note content, got %q", excerpt)
				}
				return
			}
			if entry.Issue == "" {
				t.Fatalf("expected the entry to be rejected, got note path %q", entry.NotePath)
			}
		})
	}
}

func TestStableSystemPromptDoesNotDriftWithTheClock(t *testing.T) {
	// The stable prompt is the cached prefix shared by every query and by
	// compaction. Any text that changes with the wall clock makes each new
	// query miss the provider's prompt cache for the whole conversation.
	synctest.Test(t, func(t *testing.T) {
		capabilities := api.ModelCapabilities{SupportsToolUse: true, SupportsCaching: true}
		first := ComposeStableSystemPrompt("base prompt", SystemContext{}, capabilities)
		time.Sleep(90 * time.Second)
		second := ComposeStableSystemPrompt("base prompt", SystemContext{}, capabilities)
		if first != second {
			t.Fatalf("stable system prompt changed as time passed:\nfirst:\n%s\n\nsecond:\n%s", first, second)
		}
	})
}
