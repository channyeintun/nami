package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/permissions"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

func TestApplyPatchPermissionTargetsUsesInputAlias(t *testing.T) {
	patch := "*** Begin Patch\n*** Update File: sample.txt\n@@\n-old\n+new\n*** End Patch"
	call := toolpkg.PendingCall{
		Tool: toolpkg.NewApplyPatchTool(),
		Input: toolpkg.ToolInput{
			Params: map[string]any{"input": patch},
		},
	}

	targets, summary := applyPatchPermissionTargets(call)
	if len(targets) != 1 {
		t.Fatalf("expected 1 target, got %d", len(targets))
	}
	if targets[0] != "sample.txt" {
		t.Fatalf("unexpected target: %q", targets[0])
	}
	if summary != "sample.txt" {
		t.Fatalf("unexpected summary: %q", summary)
	}
}

// fakeTool is a registry tool whose behaviour a test controls.
type fakeTool struct {
	name       string
	permission toolpkg.PermissionLevel
	output     string
	calls      atomic.Int64
}

func (f *fakeTool) Name() string                        { return f.name }
func (f *fakeTool) Description() string                 { return "test tool " + f.name }
func (f *fakeTool) InputSchema() any                    { return map[string]any{"type": "object"} }
func (f *fakeTool) Permission() toolpkg.PermissionLevel { return f.permission }
func (f *fakeTool) Concurrency(toolpkg.ToolInput) toolpkg.ConcurrencyDecision {
	return toolpkg.ConcurrencySerial
}

func (f *fakeTool) Execute(context.Context, toolpkg.ToolInput) (toolpkg.ToolOutput, error) {
	f.calls.Add(1)
	return toolpkg.ToolOutput{Output: f.output}, nil
}

func newFakeRegistry(tools ...*fakeTool) *toolpkg.Registry {
	registry := toolpkg.NewRegistry()
	for _, tool := range tools {
		registry.Register(tool)
	}
	return registry
}

// A final plan saved in the same batch as a write pauses the turn for review.
// The calls cut off by the pause still need results: the assistant message
// keeps every tool call, and a call with no result makes the provider reject
// the first request after the review.
func TestExecuteToolCallsAnswersEveryCallWhenPausingForPlanReview(t *testing.T) {
	savePlan := &fakeTool{name: "save_implementation_plan", permission: toolpkg.PermissionReadOnly, output: "plan saved"}
	write := &fakeTool{name: "fake_write", permission: toolpkg.PermissionWrite, output: "written"}
	read := &fakeTool{name: "fake_read", permission: toolpkg.PermissionReadOnly, output: "read"}
	registry := newFakeRegistry(savePlan, write, read)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	bridge := ipc.NewBridge(strings.NewReader(""), io.Discard)
	router := ipc.NewMessageRouter(ctx, bridge)
	permissionCtx := newPermissionContext(string(permissions.ModeBypassPermissions), false)

	calls := []api.ToolCall{
		{ID: "call-plan", Name: "save_implementation_plan", Input: "{}"},
		{ID: "call-write", Name: "fake_write", Input: "{}"},
		{ID: "call-read", Name: "fake_read", Input: "{}"},
	}
	results, err := executeToolCalls(ctx, bridge, router, registry, permissionCtx, costpkg.NewTracker(), nil, nil, nil, "session", t.TempDir(), 0, nil, nil, calls)
	if _, ok := errors.AsType[*agent.PauseForPlanReviewError](err); !ok {
		t.Fatalf("err = %v, want a plan review pause", err)
	}

	if savePlan.calls.Load() != 1 {
		t.Fatalf("save_implementation_plan ran %d times, want 1", savePlan.calls.Load())
	}
	if write.calls.Load() != 0 || read.calls.Load() != 0 {
		t.Fatalf("calls after the pause ran: write=%d read=%d", write.calls.Load(), read.calls.Load())
	}
	if len(results) != len(calls) {
		t.Fatalf("got %d results for %d calls: %+v", len(results), len(calls), results)
	}
	for index, call := range calls {
		if results[index].ToolCallID != call.ID {
			t.Fatalf("results[%d].ToolCallID = %q, want %q", index, results[index].ToolCallID, call.ID)
		}
	}
	if results[0].IsError || results[0].Output != "plan saved" {
		t.Fatalf("plan result = %+v, want the tool output", results[0])
	}
	for _, skipped := range results[1:] {
		if !skipped.IsError || skipped.Output != planReviewSkippedMessage {
			t.Fatalf("skipped result = %+v, want the plan review skip error", skipped)
		}
	}
}

// The read state has to remember a file as read_file saw it. Stat'ing it again
// after the result was sent recorded a change made in between as already seen,
// and the next read got the "unchanged" stub for content the model never saw.
func TestReadStateRemembersTheFileAsItWasRead(t *testing.T) {
	previous := toolpkg.GetGlobalFileReadState()
	t.Cleanup(func() { toolpkg.SetGlobalFileReadState(previous) })
	state := toolpkg.NewFileReadState()
	toolpkg.SetGlobalFileReadState(state)

	path := filepath.Join(t.TempDir(), "notes.txt")
	if err := os.WriteFile(path, []byte("first version\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	output, err := toolpkg.NewFileReadTool().Execute(t.Context(), toolpkg.ToolInput{Params: map[string]any{"filePath": path}})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}

	// Something else rewrites the file before the engine records the read.
	if err := os.WriteFile(path, []byte("second, longer version\n"), 0o600); err != nil {
		t.Fatalf("rewrite file: %v", err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatalf("chtimes: %v", err)
	}
	rememberInlineReadResult(state, output, false)

	current, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if state.SeenUnchanged(path, max(1, output.ReadOffset), output.ReadLimit, current) {
		t.Fatal("the read state records the rewritten file as already read")
	}
}

// failingTool fails the way a command does, with its whole output as the error.
type failingTool struct {
	name   string
	output string
}

func (f failingTool) Name() string        { return f.name }
func (f failingTool) Description() string { return "fails with a lot of output" }
func (f failingTool) InputSchema() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (f failingTool) Permission() toolpkg.PermissionLevel { return toolpkg.PermissionReadOnly }
func (f failingTool) Concurrency(toolpkg.ToolInput) toolpkg.ConcurrencyDecision {
	return toolpkg.ConcurrencySerial
}
func (f failingTool) Execute(context.Context, toolpkg.ToolInput) (toolpkg.ToolOutput, error) {
	return toolpkg.ToolOutput{Output: f.output, IsError: true}, nil
}

// hugeFailureOutput is far past any tool result budget, as a failing build's
// log can be.
var hugeFailureOutput = strings.Repeat("error: something went wrong\n", 40_000)

// requireBudgetedFailure checks that a failed call's result stayed a failure
// but was cut to fit the tool result budget.
func requireBudgetedFailure(t *testing.T, result api.ToolResult) {
	t.Helper()
	if !result.IsError {
		t.Fatal("the failed call's result is no longer marked as an error")
	}
	if len(result.Output) >= len(hugeFailureOutput)/10 {
		t.Fatalf("the failed call's result kept %d of %d characters inline", len(result.Output), len(hugeFailureOutput))
	}
	if !strings.Contains(result.Output, "Output truncated") {
		t.Fatalf("the cut result does not say it was truncated: %q", result.Output[len(result.Output)-200:])
	}
}

// A failed call's output counts against the context like any other. Sent
// whole, one failing build's log could fill the model's context.
func TestFailedToolOutputIsBudgeted(t *testing.T) {
	client := &scriptedClient{
		caps: api.ModelCapabilities{SupportsToolUse: true, MaxContextWindow: 200_000, MaxOutputTokens: 8_000},
		turns: []scriptedTurn{
			{text: "Building.", toolCalls: []api.ToolCall{{ID: "call-fail", Name: "fail", Input: "{}"}}},
			{text: "The build failed."},
		},
	}
	h := newTurnHarness(t, client, failingTool{name: "fail", output: hugeFailureOutput})

	if err := handleUserInputMessage(t.Context(), ipc.UserInputPayload{Text: "build it"}, h.deps, h.state); err != nil {
		t.Fatalf("handleUserInputMessage: %v", err)
	}
	for _, message := range h.state.messages {
		if message.ToolResult != nil && message.ToolResult.ToolCallID == "call-fail" {
			requireBudgetedFailure(t, *message.ToolResult)
			return
		}
	}
	t.Fatal("the conversation has no result for the failed call")
}

func TestFailedChildToolOutputIsBudgeted(t *testing.T) {
	registry := toolpkg.NewEmptyRegistry()
	registry.Register(failingTool{name: "bash", output: hugeFailureOutput})

	results, err := executeToolCallsForSubagent(t.Context(), generalPurposeSubagentType, nil, registry, newPermissionContext("bypassPermissions", false), nil, nil, "child-session", t.TempDir(), costpkg.NewTracker(), 8_000,
		[]api.ToolCall{{ID: "call-fail", Name: "bash", Input: "{}"}})
	if err != nil {
		t.Fatalf("executeToolCallsForSubagent: %v", err)
	}
	requireBudgetedFailure(t, results[0])
}

// sizedTool returns as many characters as its "size" parameter asks for.
type sizedTool struct{}

func (sizedTool) Name() string        { return "bash" }
func (sizedTool) Description() string { return "prints size characters" }
func (sizedTool) InputSchema() any {
	return map[string]any{"type": "object", "properties": map[string]any{"size": map[string]any{"type": "integer"}}}
}
func (sizedTool) Permission() toolpkg.PermissionLevel { return toolpkg.PermissionReadOnly }
func (sizedTool) Concurrency(toolpkg.ToolInput) toolpkg.ConcurrencyDecision {
	return toolpkg.ConcurrencySerial
}
func (sizedTool) Execute(_ context.Context, input toolpkg.ToolInput) (toolpkg.ToolOutput, error) {
	size, _ := input.Params["size"].(float64)
	return toolpkg.ToolOutput{Output: strings.Repeat("x", int(size))}, nil
}

// Once a batch's results have used up the aggregate inline budget, later
// results may keep nothing inline. A spent budget used to leave a preview
// length of zero, which the preview read as "no limit", so every result after
// that went inline whole.
func TestResultsAfterTheAggregateBudgetIsSpentStayOutOfContext(t *testing.T) {
	registry := toolpkg.NewEmptyRegistry()
	registry.Register(sizedTool{})
	budget := toolpkg.DefaultResultBudgetForModel("", 8_000)
	sizes := []int{
		budget.MaxChars - 500, // fits
		budget.AggregateMaxChars - (budget.MaxChars - 500) - 400, // fits, leaving 400
		1_000,   // spills, and its preview and note use up the rest
		500_000, // must stay out of context
	}
	calls := make([]api.ToolCall, 0, len(sizes))
	for index, size := range sizes {
		calls = append(calls, api.ToolCall{ID: fmt.Sprintf("call-%d", index), Name: "bash", Input: fmt.Sprintf(`{"size": %d}`, size)})
	}

	results, err := executeToolCallsForSubagent(t.Context(), generalPurposeSubagentType, nil, registry, newPermissionContext("bypassPermissions", false), nil, nil, "child-session", t.TempDir(), costpkg.NewTracker(), 8_000, calls)
	if err != nil {
		t.Fatalf("executeToolCallsForSubagent: %v", err)
	}
	if last := results[len(results)-1]; len(last.Output) > budget.PreviewLen {
		t.Fatalf("a result after the budget was spent kept %d of %d characters inline", len(last.Output), sizes[len(sizes)-1])
	}
}

// A child agent is a conversation of its own. A file its parent has read must
// still reach the child in full, not as an "unchanged since last read" stub
// pointing at a result only the parent holds, and the child's own reads are
// remembered apart from the parent's.
func TestChildAgentReadsAreTrackedApartFromTheParents(t *testing.T) {
	previous := toolpkg.GetGlobalFileReadState()
	t.Cleanup(func() { toolpkg.SetGlobalFileReadState(previous) })
	parentState := toolpkg.NewFileReadState()
	toolpkg.SetGlobalFileReadState(parentState)

	dir := t.TempDir()
	shared := filepath.Join(dir, "shared.txt")
	childOnly := filepath.Join(dir, "child-only.txt")
	for path, content := range map[string]string{shared: "read by both\n", childOnly: "read by the child\n"} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}
	parentRead, err := toolpkg.NewFileReadTool().Execute(t.Context(), toolpkg.ToolInput{Params: map[string]any{"filePath": shared}})
	if err != nil {
		t.Fatalf("parent read_file: %v", err)
	}
	rememberInlineReadResult(parentState, parentRead, false)

	registry := toolpkg.NewEmptyRegistry()
	registry.Register(toolpkg.NewFileReadTool())
	childCtx := toolpkg.WithFileReadState(t.Context(), toolpkg.NewFileReadState())
	readInChild := func(path string) string {
		t.Helper()
		results, err := executeToolCallsForSubagent(childCtx, generalPurposeSubagentType, nil, registry, newPermissionContext("bypassPermissions", false), nil, nil, "child-session", t.TempDir(), costpkg.NewTracker(), 8_000,
			[]api.ToolCall{{ID: "call-read", Name: "read_file", Input: fmt.Sprintf(`{"filePath": %q}`, path)}})
		if err != nil {
			t.Fatalf("executeToolCallsForSubagent: %v", err)
		}
		return results[0].Output
	}

	if got := readInChild(shared); !strings.Contains(got, "read by both") {
		t.Fatalf("the child got %q for a file only its parent had read", got)
	}
	if got := readInChild(childOnly); !strings.Contains(got, "read by the child") {
		t.Fatalf("first child read = %q", got)
	}
	if got := readInChild(childOnly); !strings.Contains(got, "File unchanged since last read") {
		t.Fatalf("a repeated child read = %q, want the unchanged stub", got)
	}
	parentReadOfChildFile, err := toolpkg.NewFileReadTool().Execute(t.Context(), toolpkg.ToolInput{Params: map[string]any{"filePath": childOnly}})
	if err != nil {
		t.Fatalf("parent read_file: %v", err)
	}
	if !strings.Contains(parentReadOfChildFile.Output, "read by the child") {
		t.Fatalf("the parent got %q for a file only its child had read", parentReadOfChildFile.Output)
	}
}
