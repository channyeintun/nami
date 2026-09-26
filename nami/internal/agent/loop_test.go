package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/ipc"
)

func TestHandleToolCallsTurnAnswersEveryToolCall(t *testing.T) {
	// Providers reject a transcript whose assistant tool_use has no matching
	// tool_result, so every call must be answered even when the batch fails
	// or pauses part-way through.
	calls := []api.ToolCall{
		{ID: "call-1", Name: "save_implementation_plan", Input: "{}"},
		{ID: "call-2", Name: "apply_patch", Input: "{}"},
	}
	tests := []struct {
		name        string
		results     []api.ToolResult
		batchErr    error
		wantErr     bool
		wantOutputs map[string]string
	}{
		{
			name:     "cancelled batch",
			batchErr: context.Canceled,
			wantErr:  true,
		},
		{
			name:     "plan review pause",
			results:  []api.ToolResult{{ToolCallID: "call-1", Output: "plan saved"}},
			batchErr: &PauseForPlanReviewError{},
			wantOutputs: map[string]string{
				"call-1": "plan saved",
			},
		},
		{
			name: "complete batch",
			results: []api.ToolResult{
				{ToolCallID: "call-1", Output: "plan saved"},
				{ToolCallID: "call-2", Output: "patched"},
			},
			wantOutputs: map[string]string{
				"call-1": "plan saved",
				"call-2": "patched",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := &QueryState{Messages: []api.Message{{Role: api.RoleAssistant, ToolCalls: calls}}}
			deps := QueryDeps{
				ExecuteToolBatch: func(context.Context, []api.ToolCall) ([]api.ToolResult, error) {
					return tt.results, tt.batchErr
				},
			}
			yield := func(ipc.StreamEvent, error) bool { return true }

			err := handleToolCallsTurn(context.Background(), state, deps, yield, modelTurn{toolCalls: calls})
			if tt.wantErr != (err != nil) {
				t.Fatalf("handleToolCallsTurn error = %v, want error %v", err, tt.wantErr)
			}
			if tt.wantErr && !errors.Is(err, tt.batchErr) {
				t.Fatalf("expected the batch error to propagate, got %v", err)
			}

			answered := make(map[string]api.ToolResult)
			for _, message := range state.Messages[1:] {
				if message.Role != api.RoleTool || message.ToolResult == nil {
					t.Fatalf("expected only tool result messages after the tool calls, got %+v", message)
				}
				if _, dup := answered[message.ToolResult.ToolCallID]; dup {
					t.Fatalf("tool call %q answered twice", message.ToolResult.ToolCallID)
				}
				answered[message.ToolResult.ToolCallID] = *message.ToolResult
			}
			for _, call := range calls {
				result, ok := answered[call.ID]
				if !ok {
					t.Fatalf("tool call %q has no tool result", call.ID)
				}
				want, real := tt.wantOutputs[call.ID]
				if real && (result.Output != want || result.IsError) {
					t.Fatalf("tool call %q: got %+v, want the batch result %q", call.ID, result, want)
				}
				if !real && (!result.IsError || result.Output == "") {
					t.Fatalf("tool call %q: expected an explanatory error result, got %+v", call.ID, result)
				}
			}
		})
	}
}
