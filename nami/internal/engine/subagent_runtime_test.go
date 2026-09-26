package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	artifactspkg "github.com/channyeintun/nami/internal/artifacts"
	"github.com/channyeintun/nami/internal/hooks"
	"github.com/channyeintun/nami/internal/permissions"
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

// An oversized result is cut to a preview whether or not the full output
// could be saved; a failed save must not put the whole output inline.
func TestExecuteToolCallsForSubagentTruncatesWhenTheSpillFails(t *testing.T) {
	storeRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(storeRoot, []byte("x"), 0o644); err != nil {
		t.Fatalf("write store root: %v", err)
	}
	brokenArtifacts := artifactspkg.NewManager(artifactspkg.NewLocalStore(storeRoot))
	huge := strings.Repeat("x", 1_000_000)
	registry := newFakeRegistry(&fakeTool{name: "read_file", permission: toolpkg.PermissionReadOnly, output: huge})

	results, err := executeToolCallsForSubagent(
		t.Context(), exploreSubagentType, nil, registry, permissions.NewContext(), nil, brokenArtifacts,
		"child-session", t.TempDir(), nil, 0,
		[]api.ToolCall{{ID: "call-read", Name: "read_file", Input: "{}"}},
	)
	if err != nil {
		t.Fatalf("executeToolCallsForSubagent: %v", err)
	}
	if len(results) != 1 || results[0].IsError {
		t.Fatalf("results = %+v, want one successful result", results)
	}
	output := results[0].Output
	if len(output) >= len(huge) {
		t.Fatalf("output kept %d of %d chars inline, want a preview", len(output), len(huge))
	}
	if !strings.Contains(output, "could not be saved") {
		t.Fatalf("output does not say why the full result is missing: %q", output[len(output)-200:])
	}
}

// A pre_tool_use hook that denies a call must deny it whether the parent
// makes the call or delegates it to a child agent.
func TestExecuteToolCallsForSubagentHonoursPreToolUseHooks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the hook in this test is a POSIX shell script")
	}
	hooksDir := t.TempDir()
	script := "#!/bin/sh\ncat >/dev/null\necho '{\"action\":\"deny\",\"message\":\"reads are audited\"}'\n"
	if err := os.WriteFile(filepath.Join(hooksDir, "pre_tool_use"), []byte(script), 0o755); err != nil {
		t.Fatalf("write hook: %v", err)
	}
	read := &fakeTool{name: "read_file", permission: toolpkg.PermissionReadOnly, output: "secret"}

	results, err := executeToolCallsForSubagent(
		t.Context(), exploreSubagentType, nil, newFakeRegistry(read), permissions.NewContext(), hooks.NewRunner(hooksDir), nil,
		"child-session", t.TempDir(), nil, 0,
		[]api.ToolCall{{ID: "call-read", Name: "read_file", Input: "{}"}},
	)
	if err != nil {
		t.Fatalf("executeToolCallsForSubagent: %v", err)
	}
	if read.calls.Load() != 0 {
		t.Fatal("the child ran a call its pre_tool_use hook denied")
	}
	if len(results) != 1 || !results[0].IsError || results[0].Output != "reads are audited" {
		t.Fatalf("results = %+v, want the hook's denial", results)
	}
}

func TestNormalizeDelegatedPromptLineKeepsCharactersWhole(t *testing.T) {
	line := normalizeDelegatedPromptLine("- " + strings.Repeat("漢", 300))
	if !utf8.ValidString(line) {
		t.Fatalf("brief line is not valid UTF-8: %q", line)
	}
	if got := utf8.RuneCountInString(line); got != 220 {
		t.Fatalf("brief line has %d characters, want 220", got)
	}
}

func TestNormalizeSubagentFinalAnswer(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    string
	}{
		{name: "tagged answer", content: "notes\n<final_answer>\nScope: x\n</final_answer>\n", want: "Scope: x"},
		{name: "tags in any case", content: "<FINAL_ANSWER>done</Final_Answer>", want: "done"},
		{name: "last closing tag ends the answer", content: "<final_answer>a</final_answer> b </final_answer>", want: "a</final_answer> b"},
		{name: "no tags", content: "  plain answer  ", want: "plain answer"},
		{name: "empty tags keep the whole text", content: "<final_answer> </final_answer>", want: "<final_answer> </final_answer>"},
		{name: "closing tag before opening tag", content: "</final_answer> x <final_answer>", want: "</final_answer> x <final_answer>"},
		// Characters whose lowercase form has a different byte length used
		// to shift the cut: "İ" shrinks, the Kelvin sign shrinks, "Ⱥ" grows
		// - far enough to slice past the end of the string and panic.
		{name: "shrinking characters before the tags", content: strings.Repeat("İ", 5) + " <final_answer>x</final_answer>", want: "x"},
		{name: "kelvin sign before the tags", content: "KK <final_answer>result</final_answer>", want: "result"},
		{name: "growing characters before the tags", content: strings.Repeat("Ⱥ", 20) + " <final_answer>x</final_answer>", want: "x"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeSubagentFinalAnswer(tt.content); got != tt.want {
				t.Fatalf("normalizeSubagentFinalAnswer(%q) = %q, want %q", tt.content, got, tt.want)
			}
		})
	}
}

// requirePrivate fails unless path grants nothing to group or others.
func requirePrivate(t *testing.T, path string) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	if perm := info.Mode().Perm(); perm&0o077 != 0 {
		t.Fatalf("%s has mode %v, want no group or other access", path, perm)
	}
}

// The archived prompt is the parent's full instructions to the child, which
// can quote anything from the conversation.
func TestArchiveDelegatedPromptIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	sessionDir := filepath.Join(t.TempDir(), "child-session")
	path, err := archiveDelegatedPrompt(sessionDir, "long task", strings.Repeat("do the thing. ", delegationPromptArchiveLimit))
	if err != nil {
		t.Fatalf("archiveDelegatedPrompt: %v", err)
	}
	if path == "" {
		t.Fatal("a prompt over the limit was not archived")
	}
	requirePrivate(t, sessionDir)
	requirePrivate(t, path)
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
