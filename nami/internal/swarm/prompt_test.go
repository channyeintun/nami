package swarm

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestFile(t *testing.T, path string, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func testProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	return root
}

func TestLoadRolePromptOverlayLoadsConstitutionAndRole(t *testing.T) {
	root := testProject(t)
	writeTestFile(t, filepath.Join(root, ".nami", "swarm", "constitution.md"), "shared rules")
	writeTestFile(t, filepath.Join(root, ".nami", "swarm", "roles", "coder.md"), "coder rules")

	overlay, err := LoadRolePromptOverlay(root, "Coder")
	if err != nil {
		t.Fatalf("LoadRolePromptOverlay: %v", err)
	}
	for _, want := range []string{"shared rules", "coder rules"} {
		if !strings.Contains(overlay.Content, want) {
			t.Fatalf("overlay missing %q:\n%s", want, overlay.Content)
		}
	}
}

// The role comes from the model's agent call, and without a project spec
// nothing else validates it. A role name must never be able to walk out of the
// roles directory and pull an arbitrary markdown file into a system prompt.
func TestLoadRolePromptOverlayRejectsRoleNamesThatEscapeTheRolesDir(t *testing.T) {
	root := testProject(t)
	writeTestFile(t, filepath.Join(root, "secret.md"), "SECRET CONTENT")

	for _, role := range []string{"../../../secret", "roles/../../../../secret", "..\\..\\..\\secret"} {
		overlay, err := LoadRolePromptOverlay(root, role)
		if err == nil && strings.Contains(overlay.Content, "SECRET CONTENT") {
			t.Fatalf("role %q read a file outside the roles directory", role)
		}
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("role %q: unexpected error %v", role, err)
		}
	}
}

func TestLoadRolePromptOverlayKeepsConstitutionForUnlistedRoleNames(t *testing.T) {
	root := testProject(t)
	writeTestFile(t, filepath.Join(root, ".nami", "swarm", "constitution.md"), "shared rules")

	overlay, err := LoadRolePromptOverlay(root, "Code Reviewer")
	if err != nil {
		t.Fatalf("LoadRolePromptOverlay: %v", err)
	}
	if !strings.Contains(overlay.Content, "shared rules") {
		t.Fatalf("overlay lost the shared constitution:\n%s", overlay.Content)
	}
}
