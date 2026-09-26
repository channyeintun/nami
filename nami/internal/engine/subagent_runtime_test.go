package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/channyeintun/nami/internal/swarm"
)

// writeSwarmProject creates a repository root holding a swarm spec and returns
// the root, which is what a child agent receives as its working directory.
func writeSwarmProject(t *testing.T, spec swarm.Spec) string {
	t.Helper()
	root := t.TempDir()
	// The project root is located by walking up to a .git directory.
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	specPath := filepath.Join(root, swarm.ProjectSpecRelativePath)
	if err := os.MkdirAll(filepath.Dir(specPath), 0o755); err != nil {
		t.Fatalf("mkdir spec dir: %v", err)
	}
	data, err := json.Marshal(spec)
	if err != nil {
		t.Fatalf("marshal spec: %v", err)
	}
	if err := os.WriteFile(specPath, data, 0o644); err != nil {
		t.Fatalf("write spec: %v", err)
	}
	return root
}

func TestWithRolePromptSections(t *testing.T) {
	root := writeSwarmProject(t, swarm.Spec{Roles: []swarm.RoleSpec{
		{Name: "coder", Purpose: "writes code", Handoff: swarm.HandoffSpec{Required: true, Targets: []string{"reviewer"}}},
		{Name: "reviewer", Purpose: "reviews code"},
	}})
	const base = "base prompt"

	tests := []struct {
		name         string
		role         string
		wantErr      bool
		wantContains string
	}{
		// A plain child agent carries no role. The spec has nothing to say
		// about it, so it must still start rather than fail on the lookup.
		{name: "no role", role: ""},
		{name: "blank role", role: "   "},
		{name: "role without handoff policy", role: "reviewer"},
		{name: "role with handoff policy", role: "coder", wantContains: `Swarm handoff policy for role "coder"`},
		{name: "undefined role", role: "ghost", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := withRolePromptSections(base, root, tt.role)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("withRolePromptSections(%q) succeeded, want an error", tt.role)
				}
				return
			}
			if err != nil {
				t.Fatalf("withRolePromptSections(%q): %v", tt.role, err)
			}
			if !strings.HasPrefix(got, base) {
				t.Fatalf("prompt = %q, want it to start with the base prompt", got)
			}
			if tt.wantContains == "" && got != base {
				t.Fatalf("prompt = %q, want the base prompt unchanged", got)
			}
			if tt.wantContains != "" && !strings.Contains(got, tt.wantContains) {
				t.Fatalf("prompt = %q, want it to contain %q", got, tt.wantContains)
			}
		})
	}
}

// Without a spec in the project, any role simply adds nothing.
func TestWithRolePromptSectionsWithoutSwarmSpec(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	for _, role := range []string{"", "coder"} {
		got, err := withRolePromptSections("base", root, role)
		if err != nil {
			t.Fatalf("withRolePromptSections(%q): %v", role, err)
		}
		if got != "base" {
			t.Fatalf("prompt = %q, want the base prompt unchanged", got)
		}
	}
}
