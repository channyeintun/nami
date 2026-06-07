package engine

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	artifactspkg "github.com/channyeintun/nami/internal/artifacts"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/hooks"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/permissions"
	"github.com/channyeintun/nami/internal/timing"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

func executeToolCalls(
	ctx context.Context,
	bridge ipc.EventSink,
	router *ipc.MessageRouter,
	registry *toolpkg.Registry,
	permissionCtx *permissions.Context,
	tracker *costpkg.Tracker,
	planner *agent.Planner,
	artifactManager *artifactspkg.Manager,
	hookRunner *hooks.Runner,
	sessionID string,
	maxOutputTokens int,
	turnMetrics *timing.CheckpointRecorder,
	turnStats *turnExecutionStats,
	calls []api.ToolCall,
) ([]api.ToolResult, error) {
	execState := newToolExecutionState(calls, maxOutputTokens, turnStats)
	if err := prepareToolCalls(ctx, bridge, router, registry, permissionCtx, planner, hookRunner, sessionID, calls, execState); err != nil {
		return nil, err
	}
	if err := executeApprovedToolBatches(ctx, bridge, tracker, artifactManager, hookRunner, sessionID, turnMetrics, turnStats, calls, execState); err != nil {
		return nil, err
	}
	if execState.pauseForPlanReview {
		return compactToolResults(execState.results), &agent.PauseForPlanReviewError{}
	}
	return execState.results, nil
}

type toolExecutionState struct {
	results            []api.ToolResult
	approved           []toolpkg.PendingCall
	approvalFeedback   map[int]string
	budget             toolpkg.ResultBudget
	aggregateBudget    *toolpkg.AggregateResultBudget
	pauseForPlanReview bool
	planSavedThisTurn  bool
}

func newToolExecutionState(calls []api.ToolCall, maxOutputTokens int, turnStats *turnExecutionStats) *toolExecutionState {
	budget := toolpkg.DefaultResultBudgetForModel(filepath.Join(os.TempDir(), "nami-session"), maxOutputTokens)
	aggregateBudget := toolpkg.NewAggregateResultBudget(budget)
	if turnStats != nil {
		turnStats.AggregateBudgetChars = aggregateBudget.MaxChars()
	}
	return &toolExecutionState{
		results:          make([]api.ToolResult, len(calls)),
		approved:         make([]toolpkg.PendingCall, 0, len(calls)),
		approvalFeedback: make(map[int]string, len(calls)),
		budget:           budget,
		aggregateBudget:  aggregateBudget,
	}
}

func prepareToolCalls(
	ctx context.Context,
	bridge ipc.EventSink,
	router *ipc.MessageRouter,
	registry *toolpkg.Registry,
	permissionCtx *permissions.Context,
	planner *agent.Planner,
	hookRunner *hooks.Runner,
	sessionID string,
	calls []api.ToolCall,
	state *toolExecutionState,
) error {
	for index, call := range calls {
		shouldContinue, err := prepareToolCall(ctx, bridge, router, registry, permissionCtx, planner, hookRunner, sessionID, calls, index, call, state)
		if err != nil || !shouldContinue {
			return err
		}
	}
	return nil
}

func prepareToolCall(
	ctx context.Context,
	bridge ipc.EventSink,
	router *ipc.MessageRouter,
	registry *toolpkg.Registry,
	permissionCtx *permissions.Context,
	planner *agent.Planner,
	hookRunner *hooks.Runner,
	sessionID string,
	calls []api.ToolCall,
	index int,
	call api.ToolCall,
	state *toolExecutionState,
) (bool, error) {
	call, tool, input, pendingCall, err := resolvePendingToolCall(registry, index, call)
	if err != nil {
		return true, recordToolPreparationError(bridge, state.results, index, calls[index], err)
	}
	if state.planSavedThisTurn && pendingCall.Tool.Permission() == toolpkg.PermissionWrite {
		state.pauseForPlanReview = true
		return false, nil
	}
	allowed, err := validatePlannedTool(ctx, planner, call, pendingCall, state.results, bridge)
	if err != nil {
		if _, ok := errors.AsType[*agent.PlanReviewRequiredError](err); ok {
			state.pauseForPlanReview = true
			return false, nil
		}
		return true, nil
	}
	if !allowed {
		return true, nil
	}
	authorization, err := authorizeToolCall(ctx, bridge, router, permissionCtx, call.ID, pendingCall)
	if err != nil {
		return false, err
	}
	if !authorization.Allowed {
		state.results[index] = api.ToolResult{ToolCallID: call.ID, Output: authorization.DenyReason, IsError: true}
		return true, emitToolError(bridge, call, authorization.DenyReason, toolpkg.ToolOutput{}, nil)
	}
	if authorization.Feedback != "" {
		state.approvalFeedback[index] = authorization.Feedback
	}
	hookDenied, err := runPreToolUseHooks(ctx, hookRunner, sessionID, call, index, state.results, state.approvalFeedback[index], bridge)
	if err != nil {
		return false, err
	}
	if hookDenied {
		return true, nil
	}
	if err := emitToolStart(bridge, call); err != nil {
		return false, err
	}
	state.approved = append(state.approved, toolpkg.PendingCall{Index: index, Tool: tool, Input: input})
	if pendingCall.Tool.Name() == "save_implementation_plan" {
		state.planSavedThisTurn = true
	}
	return true, nil
}

func resolvePendingToolCall(registry *toolpkg.Registry, index int, call api.ToolCall) (api.ToolCall, toolpkg.Tool, toolpkg.ToolInput, toolpkg.PendingCall, error) {
	normalizedCall, err := normalizeToolCall(call)
	if err != nil {
		return api.ToolCall{}, nil, toolpkg.ToolInput{}, toolpkg.PendingCall{}, err
	}
	tool, err := registry.Get(normalizedCall.Name)
	if err != nil {
		return normalizedCall, nil, toolpkg.ToolInput{}, toolpkg.PendingCall{}, err
	}
	input, err := decodeToolInput(normalizedCall)
	if err != nil {
		return normalizedCall, nil, toolpkg.ToolInput{}, toolpkg.PendingCall{}, err
	}
	if err := toolpkg.ValidateToolCall(tool, input); err != nil {
		return normalizedCall, nil, toolpkg.ToolInput{}, toolpkg.PendingCall{}, err
	}
	pendingCall := toolpkg.PendingCall{Index: index, Tool: tool, Input: input}
	return normalizedCall, tool, input, pendingCall, nil
}

func validatePlannedTool(
	ctx context.Context,
	planner *agent.Planner,
	call api.ToolCall,
	pendingCall toolpkg.PendingCall,
	results []api.ToolResult,
	bridge ipc.EventSink,
) (bool, error) {
	if err := planner.ValidateTool(ctx, pendingCall.Tool.Name(), pendingCall.Tool.Permission()); err != nil {
		if _, ok := errors.AsType[*agent.PlanReviewRequiredError](err); ok {
			return false, err
		}
		results[pendingCall.Index] = api.ToolResult{ToolCallID: call.ID, Output: err.Error(), IsError: true}
		if emitErr := emitToolError(bridge, call, err.Error(), toolpkg.ToolOutput{}, err); emitErr != nil {
			return false, emitErr
		}
		return false, nil
	}
	return true, nil
}

func runPreToolUseHooks(
	ctx context.Context,
	hookRunner *hooks.Runner,
	sessionID string,
	call api.ToolCall,
	index int,
	results []api.ToolResult,
	feedback string,
	bridge ipc.EventSink,
) (bool, error) {
	if hookRunner == nil {
		return false, nil
	}
	responses, _ := hookRunner.Run(ctx, hooks.Payload{
		Type:      hooks.HookPreToolUse,
		SessionID: sessionID,
		ToolName:  call.Name,
		ToolInput: call.Input,
	})
	for _, resp := range responses {
		if resp.Action != "deny" {
			continue
		}
		reason := resp.Message
		if reason == "" {
			reason = "blocked by pre_tool_use hook"
		}
		reason = appendPermissionFeedback(reason, feedback)
		results[index] = api.ToolResult{ToolCallID: call.ID, Output: reason, IsError: true}
		return true, emitToolError(bridge, call, reason, toolpkg.ToolOutput{}, nil)
	}
	return false, nil
}

func emitToolStart(bridge ipc.EventSink, call api.ToolCall) error {
	return bridge.Emit(ipc.EventToolStart, ipc.ToolStartPayload{ToolID: call.ID, Name: call.Name, Input: call.Input})
}

func recordToolPreparationError(bridge ipc.EventSink, results []api.ToolResult, index int, call api.ToolCall, err error) error {
	results[index] = api.ToolResult{ToolCallID: call.ID, Output: err.Error(), IsError: true}
	return emitToolError(bridge, call, err.Error(), toolpkg.ToolOutput{}, err)
}
