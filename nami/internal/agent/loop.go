package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/ipc"
)

type modelTurn struct {
	assistantText      string
	assistantReasoning string
	toolCalls          []api.ToolCall
	stopReason         string
	outputTokens       int
}

// PauseForPlanReviewError tells the query loop to stop after recording current
// tool results so the engine can surface the plan review gate immediately.
type PauseForPlanReviewError struct{}

func (e *PauseForPlanReviewError) Error() string {
	return "pause for implementation plan review"
}

func runIteration(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	yield func(ipc.StreamEvent, error) bool,
) error {
	state.TurnCount++
	state.TurnContext = LoadTurnContext()
	state.blockedStop = StopDecision{}
	runtime := &iterationRuntime{}
	if err := runIterationStages(ctx, state, deps, runtime, yield); err != nil {
		return err
	}

	turn, err := invokeModelWithRecovery(ctx, state, deps, yield)
	if err != nil {
		return err
	}

	assistantMessage := appendAssistantTurnMessage(state, turn)
	recordTurnOutput(state, turn)

	if shouldRetryWithoutToolUse(state, runtime.currentUserPrompt, turn) {
		return retryWithoutToolUse(state, yield)
	}

	if len(turn.toolCalls) > 0 {
		return handleToolCallsTurn(ctx, state, deps, yield, turn)
	}

	if err := finalizeAssistantTurn(ctx, state, deps, yield, assistantMessage, turn.stopReason); err != nil {
		return err
	}

	return nil
}

func appendAssistantTurnMessage(state *QueryState, turn modelTurn) api.Message {
	assistantMessage := api.Message{
		Role:             api.RoleAssistant,
		Content:          strings.TrimSpace(turn.assistantText),
		ReasoningContent: strings.TrimSpace(turn.assistantReasoning),
		ToolCalls:        turn.toolCalls,
	}
	if assistantMessage.Content != "" || len(assistantMessage.ToolCalls) > 0 {
		state.Messages = append(state.Messages, assistantMessage)
	}
	return assistantMessage
}

func recordTurnOutput(state *QueryState, turn modelTurn) {
	isToolTurn := len(turn.toolCalls) > 0
	// A tool turn ends the run of continuations even when the provider
	// reported no output for it.
	if isToolTurn || turn.outputTokens > 0 {
		state.Continuation.Record(turn.outputTokens, isToolTurn)
	}
	if turn.stopReason == "max_tokens" {
		postTurnPressure := EvaluateContextPressure(state.Messages, state.ContextWindow, state.MaxTokens, state.Continuation, ContextPressureSignals{
			SessionMemory:    state.SessionMemory,
			RetrievalTouched: state.RetrievalTouched,
			AttemptEntries:   state.AttemptEntries,
		})
		state.MaxTokens = nextOutputBudget(state.MaxTokens, state.MaxOutputCeiling, postTurnPressure)
	}
}

func retryWithoutToolUse(state *QueryState, yield func(ipc.StreamEvent, error) bool) error {
	if err := yieldEvent(yield, ipc.EventError, ipc.ErrorPayload{
		Message:     "Model asked a routine clarification for a concrete implementation task; retrying with a stronger directive.",
		Recoverable: true,
	}); err != nil {
		return err
	}
	state.NoToolRetryUsed = true
	state.Messages = append(state.Messages, api.Message{
		Role:    api.RoleUser,
		Content: strings.TrimSpace(`Continue working on the user's implementation request. The request is concrete enough to act on now. Do not ask routine clarifying questions, and do not use web search for basic syntax, examples, or small scaffold tasks that you can complete from standard coding knowledge. Make the simplest safe assumption, inspect local files if needed, and perform the relevant file changes directly. Only ask a clarifying question if a missing detail makes a concrete file change impossible or unsafe.`),
	})
	return nil
}

func handleToolCallsTurn(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	yield func(ipc.StreamEvent, error) bool,
	turn modelTurn,
) error {
	results, err := deps.ExecuteToolBatch(ctx, turn.toolCalls)
	pauseForPlanReview, ok := errors.AsType[*PauseForPlanReviewError](err)
	if err != nil && !ok {
		appendToolResultMessages(state, turn.toolCalls, results, fmt.Sprintf("Tool call was interrupted before its result was recorded: %v", err))
		return err
	}
	appendToolResultMessages(state, turn.toolCalls, results, "Tool call was not run: execution paused for implementation plan review.")
	collectTouchedFiles(state, turn.toolCalls, results)
	invalidateGraphFiles(state, turn.toolCalls, results)
	repeated, err := recordFailedAttempts(deps.AttemptLog, turn.toolCalls, results)
	if err != nil {
		if telemetryErr := emitNoticeTelemetry(deps.EmitTelemetry, fmt.Sprintf("session attempt log update unavailable: %v", err)); telemetryErr != nil {
			return telemetryErr
		}
	}
	if repeated > 0 {
		if err := emitAttemptRepeatedTelemetry(deps.EmitTelemetry, repeated); err != nil {
			return err
		}
		if nudge := buildEditRetryNudge(turn.toolCalls, results); nudge != "" {
			state.Messages = append(state.Messages, api.Message{
				Role:    api.RoleUser,
				Content: nudge,
			})
		}
	}
	if pauseForPlanReview != nil {
		state.StopRequested = true
		if err := yieldEvent(yield, ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: "plan_review_required"}); err != nil {
			return err
		}
	}
	return nil
}

// appendToolResultMessages records the batch results, then answers every call
// the batch left without a result with an error explaining why. Providers
// reject any later request whose tool_use has no matching tool_result, so a
// cancelled or paused batch would otherwise break the rest of the session.
// The placeholders only go into the transcript; they are not failed attempts.
func appendToolResultMessages(state *QueryState, calls []api.ToolCall, results []api.ToolResult, missingReason string) {
	answered := make(map[string]struct{}, len(results))
	for _, result := range results {
		appendToolResultMessage(state, result)
		answered[result.ToolCallID] = struct{}{}
	}
	for _, call := range calls {
		if _, ok := answered[call.ID]; ok {
			continue
		}
		appendToolResultMessage(state, api.ToolResult{ToolCallID: call.ID, Output: missingReason, IsError: true})
	}
}

func appendToolResultMessage(state *QueryState, result api.ToolResult) {
	state.Messages = append(state.Messages, api.Message{
		Role:       api.RoleTool,
		Content:    result.Output,
		ToolResult: &result,
	})
}

func finalizeAssistantTurn(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	yield func(ipc.StreamEvent, error) bool,
	assistantMessage api.Message,
	stopReason string,
) error {
	if stopReason == "max_tokens" {
		return nil
	}

	normalizedStopReason := normalizeStopReason(stopReason)
	if deps.BeforeStop != nil {
		decision, err := deps.BeforeStop(ctx, StopRequest{
			Messages:         append([]api.Message(nil), state.Messages...),
			AssistantMessage: assistantMessage,
			StopReason:       normalizedStopReason,
			TurnCount:        state.TurnCount,
		})
		if err != nil {
			return err
		}
		if decision.Continue {
			state.blockedStop = decision
			state.Messages = append(state.Messages, api.Message{
				Role:    api.RoleUser,
				Content: stopBlockedFollowUp(decision),
			})
			return nil
		}
	}

	state.StopRequested = true
	if err := yieldEvent(yield, ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: normalizedStopReason}); err != nil {
		return err
	}
	return nil
}

func handlePendingStopRequest(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	yield func(ipc.StreamEvent, error) bool,
) (bool, error) {
	if deps.StopController == nil {
		return false, nil
	}
	reason, ok := deps.StopController.Consume()
	if !ok {
		return false, nil
	}

	assistantMessage := latestAssistantMessage(state.Messages)
	stopReason := normalizeStopReason(reason)
	if deps.BeforeStop != nil {
		decision, err := deps.BeforeStop(ctx, StopRequest{
			Messages:         append([]api.Message(nil), state.Messages...),
			AssistantMessage: assistantMessage,
			StopReason:       stopReason,
			TurnCount:        state.TurnCount,
		})
		if err != nil {
			return true, err
		}
		if decision.Continue {
			state.blockedStop = decision
			state.Messages = append(state.Messages, api.Message{
				Role:    api.RoleUser,
				Content: stopBlockedFollowUp(decision),
			})
			return true, nil
		}
	}

	state.StopRequested = true
	if err := yieldEvent(yield, ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: stopReason}); err != nil {
		return true, err
	}
	return true, nil
}

func stopBlockedFollowUp(decision StopDecision) string {
	followUp := strings.TrimSpace(decision.FollowUpMessage)
	if followUp != "" {
		return followUp
	}
	if strings.TrimSpace(decision.Reason) == "" {
		return "A local stop hook blocked completion. Continue working until the stop condition is satisfied."
	}
	return fmt.Sprintf("A local stop hook blocked completion: %s\n\nContinue working until the stop condition is satisfied.", strings.TrimSpace(decision.Reason))
}
