package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/hooks"
	"github.com/channyeintun/nami/internal/swarm"
	toolpkg "github.com/channyeintun/nami/internal/tools"
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

// A subagent_stop hook may hold a child open until its own condition holds,
// but not against the user: a cancelled stop must go through, or the child of
// a hook that keeps blocking can never be stopped.
func TestEvaluateChildStopHooksNeverBlocksACancel(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook in this test is a POSIX shell script")
	}
	hooksDir := t.TempDir()
	script := "#!/bin/sh\ncat >/dev/null\necho '{\"action\":\"deny\",\"message\":\"tests must pass first\"}'\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "subagent_stop"), []byte(script), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	runner := hooks.NewRunner(hooksDir)

	tests := []struct {
		stopReason   string
		wantContinue bool
	}{
		{stopReason: "end_turn", wantContinue: true},
		{stopReason: "cancelled", wantContinue: false},
	}
	for _, tt := range tests {
		t.Run(tt.stopReason, func(t *testing.T) {
			decision, err := evaluateChildStopHooks(
				t.Context(), runner, "child-session", "invocation", toolpkg.AgentRunRequest{}, exploreSubagentType,
				agent.StopRequest{StopReason: tt.stopReason}, &childLifecycleTracker{}, "", "", nil, nil, nil, "", time.Now(),
			)
			if err != nil {
				t.Fatalf("evaluateChildStopHooks: %v", err)
			}
			if decision.Continue != tt.wantContinue {
				t.Fatalf("stop %q: Continue = %v, want %v", tt.stopReason, decision.Continue, tt.wantContinue)
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
