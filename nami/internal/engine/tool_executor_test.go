package engine

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"

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
