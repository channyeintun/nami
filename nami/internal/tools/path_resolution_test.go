package tools

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
}

// A relative path must stay inside the working directory even when it names a
// file that does not exist yet under a symlinked directory.
func TestResolveToolPathContainsNewFilesUnderSymlinks(t *testing.T) {
	workspace := inWorkspace(t)
	outside := t.TempDir()
	if err := os.MkdirAll(filepath.Join(workspace, "real"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	symlinkOrSkip(t, outside, filepath.Join(workspace, "escape"))
	symlinkOrSkip(t, filepath.Join(workspace, "real"), filepath.Join(workspace, "inside"))

	cases := []struct {
		name    string
		path    string
		wantErr bool
	}{
		{"new file in workspace", "new.txt", false},
		{"new file in new directory", "fresh/dir/new.txt", false},
		{"new file under symlink to a workspace dir", "inside/new.txt", false},
		{"new file under symlink out of the workspace", "escape/new.txt", true},
		{"new nested file under symlink out of the workspace", "escape/a/b/new.txt", true},
		{"dot-dot escape", "../new.txt", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resolved, err := resolveToolPath(tc.path)
			if (err != nil) != tc.wantErr {
				t.Fatalf("resolveToolPath(%q) = %q, %v; wantErr %v", tc.path, resolved, err, tc.wantErr)
			}
		})
	}
}

// Started from a symlinked directory, os.Getwd reports the link path while
// resolved files report the real one; containment has to compare like with
// like, or every existing relative path looks like an escape.
func TestResolveToolPathAcceptsFilesWhenWorkingDirIsASymlink(t *testing.T) {
	workspace := inWorkspace(t)
	writeWorkspaceFile(t, workspace, "existing.txt", "content\n")
	link := filepath.Join(t.TempDir(), "linked-workspace")
	symlinkOrSkip(t, workspace, link)
	if err := os.Chdir(link); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	// A shell that cd'd through the link exports the link path as PWD, and
	// os.Getwd trusts PWD when it names the current directory.
	t.Setenv("PWD", link)

	for _, path := range []string{"existing.txt", "new.txt"} {
		resolved, err := resolveToolPath(path)
		if err != nil {
			t.Fatalf("resolveToolPath(%q): %v", path, err)
		}
		if want := filepath.Join(link, path); resolved != want {
			t.Fatalf("resolveToolPath(%q) = %q, want %q", path, resolved, want)
		}
	}
}

// Creating a file must never write through an existing path: not a file that
// appeared after validation, and not a dangling symlink whose target lies
// outside the workspace.
func TestFileCreationDoesNotFollowDanglingSymlinks(t *testing.T) {
	cases := []struct {
		name   string
		tool   Tool
		params func(link string) map[string]any
	}{
		{"create_file", NewCreateFileTool(), func(link string) map[string]any {
			return map[string]any{"file_path": link, "content": "planted\n"}
		}},
		{"apply_patch add", NewApplyPatchTool(), func(link string) map[string]any {
			return map[string]any{"input": "*** Begin Patch\n*** Add File: " + link + "\n+planted\n*** End Patch"}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			workspace := inWorkspace(t)
			target := filepath.Join(t.TempDir(), "planted.txt")
			link := filepath.Join(workspace, "notes.txt")
			symlinkOrSkip(t, target, link)

			output, err := runValidatedTool(t, tc.tool, tc.params(link))
			if err == nil && !output.IsError {
				t.Fatalf("%s wrote through a dangling symlink: %q", tc.name, output.Output)
			}
			if _, statErr := os.Lstat(target); !os.IsNotExist(statErr) {
				t.Fatalf("symlink target was created (stat err = %v)", statErr)
			}
			if err != nil && !strings.Contains(err.Error(), "exists") {
				t.Fatalf("err = %v, want an already-exists failure", err)
			}
		})
	}
}

func TestCreateFileRefusesFileCreatedAfterValidation(t *testing.T) {
	workspace := inWorkspace(t)
	path := filepath.Join(workspace, "race.txt")
	tool := NewCreateFileTool()
	input := ToolInput{Name: tool.Name(), Params: map[string]any{"file_path": path, "content": "from create_file\n"}}
	if err := ValidateToolCall(tool, input); err != nil {
		t.Fatalf("validate: %v", err)
	}
	// Someone else creates the file while the call waits for approval.
	writeWorkspaceFile(t, workspace, "race.txt", "someone else's work\n")

	if _, err := runValidatedTool(t, tool, input.Params); err == nil {
		t.Fatal("create_file succeeded over a file that already exists")
	}
	if got := readWorkspaceFile(t, path); got != "someone else's work\n" {
		t.Fatalf("existing file was overwritten with %q", got)
	}
}
