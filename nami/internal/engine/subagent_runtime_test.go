package engine

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	artifactspkg "github.com/channyeintun/nami/internal/artifacts"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/hooks"
	"github.com/channyeintun/nami/internal/permissions"
	"github.com/channyeintun/nami/internal/session"
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

// The subagent runner reads the session's working directory from goroutines
// the main loop does not wait for: a workflow node, or an agent_team launch
// the tool executor abandoned when its turn was cancelled. The main loop moves
// the directory on /resume, so the two must not race. Run with -race.
func TestSubagentWorkingDirectoryIsReadSafelyWhileTheSessionMoves(t *testing.T) {
	state := &engineLoopState{cwd: "/before"}
	read := make(chan string, 1)
	go func() { read <- currentSubagentCWD(state, "/fallback") }()
	state.setCWD("/after")
	if got := <-read; got != "/before" && got != "/after" {
		t.Fatalf("currentSubagentCWD = %q, want one of the session's directories", got)
	}
}

// A child that stops at a limit has not finished; its parent must not take
// the last reply for the result without being told.
func TestChildSummaryNotesAStopAtALimit(t *testing.T) {
	messages := []api.Message{
		{Role: api.RoleUser, Content: "survey the parser"},
		{Role: api.RoleAssistant, Content: "Looked at lexer.go so far."},
	}
	if got := childSummary(messages, "end_turn"); got != "Looked at lexer.go so far." {
		t.Errorf("finished child summary = %q", got)
	}
	for _, reason := range []string{agent.StopReasonMaxTurns, agent.ContinuationStopBudgetExhausted, agent.ContinuationStopDiminishingReturns} {
		got := childSummary(messages, reason)
		if !strings.HasPrefix(got, "Looked at lexer.go so far.") || !strings.Contains(got, "stopped at a limit ("+reason+")") {
			t.Errorf("summary after %s = %q, want the reply and a note about the limit", reason, got)
		}
	}
}

// A workflow step's prompt is built by a program and holds the data the step
// works on. Archiving it behind a brief, as a long prompt from the parent
// model is, would hand the child a summary of its input instead of the input.
func TestChildTaskMessageDeliversAWorkflowPromptWhole(t *testing.T) {
	longPrompt := strings.Repeat("Check finding 17 in /repo/internal/parser.go. ", 60)
	if utf8.RuneCountInString(longPrompt) <= delegationPromptArchiveLimit {
		t.Fatalf("the prompt has %d characters, too few to be archived", utf8.RuneCountInString(longPrompt))
	}

	workflowDir := filepath.Join(t.TempDir(), "workflow-child")
	got := childTaskMessage(nil, workflowDir, toolpkg.AgentRunRequest{Description: "verify", Prompt: longPrompt, ForWorkflow: true}, exploreSubagentType)
	if got != strings.TrimSpace(longPrompt) {
		t.Fatalf("the workflow prompt reached the child as %d characters of %q..., want it whole", utf8.RuneCountInString(got), got[:80])
	}
	if _, err := os.Stat(filepath.Join(workflowDir, delegationPromptArchiveName)); !os.IsNotExist(err) {
		t.Fatalf("the workflow prompt was archived (stat error %v)", err)
	}

	delegatedDir := filepath.Join(t.TempDir(), "delegated-child")
	brief := childTaskMessage(nil, delegatedDir, toolpkg.AgentRunRequest{Description: "verify", Prompt: longPrompt}, exploreSubagentType)
	if !strings.HasPrefix(brief, "Delegated task brief:") {
		t.Fatalf("a long delegated prompt was not briefed: %q", brief)
	}
	if _, err := os.Stat(filepath.Join(delegatedDir, delegationPromptArchiveName)); err != nil {
		t.Fatalf("a long delegated prompt was not archived: %v", err)
	}
}

// findingsSchemaJSON is the kind of schema a workflow step returns its result
// in. An empty findings list is a valid answer.
const findingsSchemaJSON = `{"type": "object", "properties": {"findings": {"type": "array", "items": {"type": "string"}}}, "required": ["findings"]}`

// structured_output is in no allowlist: an Explore child, or one whose swarm
// role allows nothing, must still be able to deliver its result.
func TestExecuteToolCallsForSubagentAdmitsStructuredOutputPastEveryAllowlist(t *testing.T) {
	tool, err := toolpkg.NewStructuredOutputTool(json.RawMessage(findingsSchemaJSON), nil)
	if err != nil {
		t.Fatalf("NewStructuredOutputTool: %v", err)
	}
	registry := toolpkg.NewEmptyRegistry()
	registry.Register(tool)
	allowsNothing := &subagentRolePolicy{role: swarm.ResolvedRole{Name: "reader"}, allowedToolNames: map[string]struct{}{}}

	results, err := executeToolCallsForSubagent(
		t.Context(), exploreSubagentType, allowsNothing, registry, permissions.NewContext(), nil, nil,
		"child-session", t.TempDir(), nil, 0,
		[]api.ToolCall{{ID: "call-result", Name: toolpkg.StructuredOutputToolName, Input: `{"findings": []}`}},
	)
	if err != nil {
		t.Fatalf("executeToolCallsForSubagent: %v", err)
	}
	if len(results) != 1 || results[0].IsError {
		t.Fatalf("results = %+v, want the call to succeed", results)
	}
	if value, ok := tool.Value(); !ok || string(value) != `{"findings":[]}` {
		t.Fatalf("recorded value = %s (recorded %v), want the empty findings list", value, ok)
	}
}

// A schema child that tries to finish without calling structured_output is
// sent back to call it, but only twice, and never against a cancel.
func TestStructuredOutputNudgerIsBounded(t *testing.T) {
	newTool := func() *toolpkg.StructuredOutputTool {
		tool, err := toolpkg.NewStructuredOutputTool(json.RawMessage(findingsSchemaJSON), nil)
		if err != nil {
			t.Fatalf("NewStructuredOutputTool: %v", err)
		}
		return tool
	}

	nudger := &structuredOutputNudger{tool: newTool()}
	for attempt := 1; attempt <= maxStructuredOutputNudges; attempt++ {
		decision, nudge := nudger.beforeStop("end_turn")
		if !nudge || !decision.Continue || !strings.Contains(decision.FollowUpMessage, "structured_output") {
			t.Fatalf("stop %d: nudge = %v, decision = %+v; want a nudge to call structured_output", attempt, nudge, decision)
		}
	}
	if _, nudge := nudger.beforeStop("end_turn"); nudge {
		t.Fatal("the child was nudged past the limit")
	}

	if _, nudge := (&structuredOutputNudger{tool: newTool()}).beforeStop("cancelled"); nudge {
		t.Fatal("a cancelled child was held open")
	}

	recorded := newTool()
	if _, err := recorded.Execute(t.Context(), toolpkg.ToolInput{Params: map[string]any{"findings": []any{}}}); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if _, nudge := (&structuredOutputNudger{tool: recorded}).beforeStop("end_turn"); nudge {
		t.Fatal("a child that delivered its result was nudged")
	}

	if _, nudge := (&structuredOutputNudger{}).beforeStop("end_turn"); nudge {
		t.Fatal("a child without an output schema was nudged")
	}
}

// A schema child's valid structured_output call is its answer. The child ends
// there, with no further model turn, completed rather than cancelled, and
// with the object as its result. The child is nudged once first, for trying
// to answer in text.
func TestSchemaChildEndsWithItsStructuredOutput(t *testing.T) {
	useActiveSession(t, "session-a")
	model := &childModel{turns: []scriptedTurn{
		{text: "<final_answer>No findings.</final_answer>"},
		{toolCalls: []api.ToolCall{{ID: "call-result", Name: toolpkg.StructuredOutputToolName, Input: `{"findings": []}`}}},
	}}
	deps := newTestSubagentDeps(t, model, session.NewStore(t.TempDir()), costpkg.NewTracker())
	scope := subagentScope{ownerSessionID: "session-a", cwd: t.TempDir(), permissionCtx: permissions.NewContext()}

	result, err := deps.scopedRunner(scope)(t.Context(), toolpkg.AgentRunRequest{
		Description:  "find bugs",
		Prompt:       "List the bugs in the parser.",
		SubagentType: exploreSubagentType,
		OutputSchema: json.RawMessage(findingsSchemaJSON),
		ForWorkflow:  true,
	})
	if err != nil {
		t.Fatalf("run child: %v", err)
	}
	if result.Status != "completed" {
		t.Fatalf("status = %q (error %q), want completed", result.Status, result.Error)
	}
	if string(result.Structured) != `{"findings":[]}` {
		t.Fatalf("structured result = %s, want the empty findings list", result.Structured)
	}
	requests := model.modelRequests()
	if len(requests) != 2 {
		t.Fatalf("the child made %d model calls, want 2: one nudged, one ending in structured_output", len(requests))
	}
	first := requests[0]
	if !slices.ContainsFunc(first.Tools, func(def api.ToolDefinition) bool { return def.Name == toolpkg.StructuredOutputToolName }) {
		t.Fatalf("the child was not offered structured_output: %v", toolDefinitionNames(first.Tools))
	}
	for _, want := range []string{"calling the structured_output tool exactly once", "one step of an automated workflow"} {
		if !strings.Contains(first.SystemPrompt, want) {
			t.Errorf("system prompt does not contain %q", want)
		}
	}
}

// A schema child that never calls structured_output has no result to give,
// even when it answered in text; reporting that text as a success would hand
// a program prose where it expects an object. The failure still carries the
// run's transcript and spend, so a workflow can count them against its budget.
func TestSchemaChildThatNeverCallsStructuredOutputFails(t *testing.T) {
	useActiveSession(t, "session-a")
	model := &childModel{turns: []scriptedTurn{
		{text: "<final_answer>No findings.</final_answer>"},
		{text: "<final_answer>Still none.</final_answer>"},
		{text: "<final_answer>None at all.</final_answer>"},
	}}
	deps := newTestSubagentDeps(t, model, session.NewStore(t.TempDir()), costpkg.NewTracker())
	scope := subagentScope{ownerSessionID: "session-a", cwd: t.TempDir(), permissionCtx: permissions.NewContext()}

	result, err := deps.scopedRunner(scope)(t.Context(), toolpkg.AgentRunRequest{
		Description:  "find bugs",
		Prompt:       "List the bugs in the parser.",
		OutputSchema: json.RawMessage(findingsSchemaJSON),
	})
	if err != nil {
		t.Fatalf("run child: %v", err)
	}
	if result.Status != "failed" || result.Error != "agent finished without calling structured_output" {
		t.Fatalf("result = status %q error %q, want the missing structured_output reported as a failure", result.Status, result.Error)
	}
	if result.Structured != nil {
		t.Fatalf("a failed child returned structured output %s", result.Structured)
	}
	if result.TranscriptPath == "" || result.OutputTokens == 0 {
		t.Fatalf("the failure lost its transcript (%q) or spend (%d output tokens)", result.TranscriptPath, result.OutputTokens)
	}
	if got := len(model.modelRequests()); got != 1+maxStructuredOutputNudges {
		t.Fatalf("the child made %d model calls, want %d: the first answer and one per nudge", got, 1+maxStructuredOutputNudges)
	}
}

// At a turn or continuation limit the query cannot go on whatever BeforeStop
// decides. A nudge there would only keep the stop hooks from seeing the
// child's real stop.
func TestStructuredOutputNudgerLeavesALimitStopToTheStopHooks(t *testing.T) {
	tool, err := toolpkg.NewStructuredOutputTool(json.RawMessage(findingsSchemaJSON), nil)
	if err != nil {
		t.Fatalf("NewStructuredOutputTool: %v", err)
	}
	for _, reason := range []string{agent.StopReasonMaxTurns, agent.ContinuationStopBudgetExhausted, agent.ContinuationStopDiminishingReturns} {
		if _, nudge := (&structuredOutputNudger{tool: tool}).beforeStop(reason); nudge {
			t.Fatalf("a child stopped at %s was nudged", reason)
		}
	}
}

// Tokens a child spent before it failed were spent all the same. They are
// charged to the session that owns the child, and they come back with the
// error so a workflow can count them against its budget.
func TestAChildThatFailsPartwayIsChargedAndReportsItsSpend(t *testing.T) {
	store := session.NewStore(t.TempDir())
	if err := persistSessionState(store, sessionStateParams{SessionID: "session-a", CreatedAt: time.Now(), Mode: agent.ModeFast, Tracker: costpkg.NewTracker()}); err != nil {
		t.Fatalf("persist session-a: %v", err)
	}
	// The owner is not the session shown, so its charge lands in its saved
	// total, where the test can read it.
	useActiveSession(t, "session-b")
	model := &childModel{turns: []scriptedTurn{
		// An invalid result: the child would retry, but the model fails next.
		{toolCalls: []api.ToolCall{{ID: "call-result", Name: toolpkg.StructuredOutputToolName, Input: `{"wrong": 1}`}}},
	}}
	deps := newTestSubagentDeps(t, model, store, costpkg.NewTracker())
	scope := subagentScope{ownerSessionID: "session-a", cwd: t.TempDir(), permissionCtx: permissions.NewContext()}

	result, err := deps.scopedRunner(scope)(t.Context(), toolpkg.AgentRunRequest{
		Description:  "find bugs",
		Prompt:       "List the bugs in the parser.",
		SubagentType: exploreSubagentType,
		OutputSchema: json.RawMessage(findingsSchemaJSON),
		ForWorkflow:  true,
	})
	if err == nil {
		t.Fatalf("the child succeeded: %+v", result)
	}
	if result.OutputTokens != 100 || result.TotalCostUSD <= 0 {
		t.Fatalf("result = %d output tokens, $%v; want the first turn's spend", result.OutputTokens, result.TotalCostUSD)
	}
	owner, loadErr := store.LoadMetadata("session-a")
	if loadErr != nil {
		t.Fatalf("LoadMetadata(session-a): %v", loadErr)
	}
	if owner.TotalCostUSD != result.TotalCostUSD {
		t.Fatalf("session-a's saved cost = %v, want the failed child's %v", owner.TotalCostUSD, result.TotalCostUSD)
	}
}
