package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/channyeintun/chan/internal/api"
	"github.com/channyeintun/chan/internal/compact"
	"github.com/channyeintun/chan/internal/ipc"
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
	if turn.outputTokens > 0 {
		state.Continuation.Record(turn.outputTokens, len(turn.toolCalls) > 0)
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
	if !yield(newEvent(ipc.EventError, ipc.ErrorPayload{
		Message:     "Model asked a routine clarification for a concrete implementation task; retrying with a stronger directive.",
		Recoverable: true,
	}), nil) {
		return context.Canceled
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
	var pauseForPlanReview *PauseForPlanReviewError
	if err != nil && !errors.As(err, &pauseForPlanReview) {
		return err
	}
	for _, result := range results {
		resultCopy := result
		state.Messages = append(state.Messages, api.Message{
			Role:       api.RoleTool,
			Content:    result.Output,
			ToolResult: &resultCopy,
		})
	}
	collectTouchedFiles(state, turn.toolCalls, results)
	invalidateGraphFiles(state, turn.toolCalls, results)
	repeated := recordFailedAttempts(deps.AttemptLog, turn.toolCalls, results)
	if repeated > 0 {
		_ = emitAttemptRepeatedTelemetry(deps.EmitTelemetry, repeated)
		if nudge := buildEditRetryNudge(turn.toolCalls, results); nudge != "" {
			state.Messages = append(state.Messages, api.Message{
				Role:    api.RoleUser,
				Content: nudge,
			})
		}
	}
	if pauseForPlanReview != nil {
		state.StopRequested = true
		if !yield(newEvent(ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: "plan_review_required"}), nil) {
			return context.Canceled
		}
	}
	return nil
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
			state.Messages = append(state.Messages, api.Message{
				Role:    api.RoleUser,
				Content: stopBlockedFollowUp(decision),
			})
			return nil
		}
	}

	state.StopRequested = true
	if !yield(newEvent(ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: normalizedStopReason}), nil) {
		return context.Canceled
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
			state.Messages = append(state.Messages, api.Message{
				Role:    api.RoleUser,
				Content: stopBlockedFollowUp(decision),
			})
			return true, nil
		}
	}

	state.StopRequested = true
	if !yield(newEvent(ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: stopReason}), nil) {
		return true, context.Canceled
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

func recallMemoryIndexes(
	ctx context.Context,
	recall func(context.Context, []MemoryFile, string) ([]MemoryRecallResult, error),
	files []MemoryFile,
	currentUserPrompt string,
	pressure ContextPressureDecision,
) ([]MemoryRecallResult, error) {
	if recall == nil || strings.TrimSpace(currentUserPrompt) == "" || pressure.SkipMemoryRecall {
		return nil, nil
	}

	hasMemoryIndexes := false
	for _, file := range files {
		if file.Type == memoryTypeProjectIndex || file.Type == memoryTypeUserIndex {
			hasMemoryIndexes = true
			break
		}
	}
	if !hasMemoryIndexes {
		return nil, nil
	}

	results, err := recall(ctx, files, currentUserPrompt)
	if err != nil {
		return nil, fmt.Errorf("memory recall unavailable: %w", err)
	}
	if len(results) == 0 {
		return nil, nil
	}
	return results, nil
}

func emitNoticeTelemetry(emit func(ipc.StreamEvent) error, message string) error {
	if emit == nil || strings.TrimSpace(message) == "" {
		return nil
	}
	return emit(newEvent(ipc.EventNotice, ipc.NoticePayload{Message: message}))
}

func emitMemoryRecallTelemetry(
	emit func(ipc.StreamEvent) error,
	files []MemoryFile,
	recalls []MemoryRecallResult,
) error {
	if emit == nil || len(recalls) == 0 {
		return nil
	}

	entries := SummarizeMemoryRecalls(files, recalls)
	if len(entries) == 0 {
		return nil
	}

	source := strings.TrimSpace(recalls[0].Source)
	for _, recall := range recalls[1:] {
		if strings.TrimSpace(recall.Source) != source {
			source = "mixed"
			break
		}
	}

	payload := ipc.MemoryRecalledPayload{
		Count:   len(entries),
		Source:  source,
		Entries: make([]ipc.MemoryRecallEntryPayload, 0, len(entries)),
	}
	for _, entry := range entries {
		payload.Entries = append(payload.Entries, ipc.MemoryRecallEntryPayload{
			Title:     entry.Title,
			NoteType:  entry.NoteType,
			Source:    entry.Source,
			IndexPath: entry.IndexPath,
			NotePath:  entry.NotePath,
			Line:      entry.Line,
		})
	}

	return emit(newEvent(ipc.EventMemoryRecalled, payload))
}

func invokeModelWithRecovery(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	yield func(ipc.StreamEvent, error) bool,
) (modelTurn, error) {
	toolUseRetryUsed := false
	for attempt := 0; attempt < 3; attempt++ {
		turn, err := streamModelTurn(ctx, state, deps, yield)
		if err == nil {
			turn.stopReason = normalizeStopReason(turn.stopReason)
			if turn.outputTokens == 0 {
				turn.outputTokens = compact.EstimateTokens(turn.assistantText)
			}
			return turn, nil
		}

		var apiErr *api.APIError
		if errors.As(err, &apiErr) && state.Capabilities.SupportsToolUse && !toolUseRetryUsed && isToolUseUnavailable(apiErr) {
			state.Capabilities.SupportsToolUse = false
			toolUseRetryUsed = true
			if !yield(newEvent(ipc.EventError, ipc.ErrorPayload{
				Message:     "Current model endpoint does not support tool use; retrying without tools.",
				Recoverable: true,
			}), nil) {
				return modelTurn{}, context.Canceled
			}
			continue
		}

		if errors.As(err, &apiErr) && apiErr.Type == api.ErrOverloaded {
			if !yield(newEvent(ipc.EventError, ipc.ErrorPayload{
				Message:     fmt.Sprintf("Model error (attempt %d/3): %s — retrying...", attempt+1, apiErr.Message),
				Recoverable: true,
			}), nil) {
				return modelTurn{}, context.Canceled
			}
			continue
		}

		if !errors.As(err, &apiErr) || apiErr.Type != api.ErrPromptTooLong || deps.CompactMessages == nil {
			return modelTurn{}, err
		}

		before := compact.EstimateConversationTokens(state.Messages)
		if !yield(newEvent(ipc.EventCompactStart, ipc.CompactStartPayload{
			Strategy:         string(CompactAuto),
			TokensBefore:     before,
			HasSessionMemory: strings.TrimSpace(state.SessionMemory.Content) != "",
		}), nil) {
			return modelTurn{}, context.Canceled
		}

		compacted, compactErr := deps.CompactMessages(ctx, state.Messages, CompactAuto)
		if compactErr != nil {
			return modelTurn{}, fmt.Errorf("compact prompt: %w", compactErr)
		}
		state.Messages = compacted.Messages
		state.AutoCompactFailures = 0

		if !yield(newEvent(ipc.EventCompactEnd, ipc.CompactEndPayload{
			Strategy:                string(compacted.Strategy),
			TokensBefore:            compacted.TokensBefore,
			TokensAfter:             compacted.TokensAfter,
			TokensSaved:             compacted.TokensBefore - compacted.TokensAfter,
			MicrocompactApplied:     compacted.MicrocompactApplied,
			MicrocompactTokensSaved: compacted.MicrocompactTokensSaved,
			HasSessionMemory:        strings.TrimSpace(state.SessionMemory.Content) != "",
		}), nil) {
			return modelTurn{}, context.Canceled
		}
	}

	return modelTurn{}, fmt.Errorf("model invocation failed after compaction retry")
}

func isToolUseUnavailable(err *api.APIError) bool {
	message := strings.ToLower(strings.TrimSpace(err.Message))
	if message == "" {
		return false
	}

	return strings.Contains(message, "no endpoints found that support tool use") ||
		strings.Contains(message, "does not support tool use") ||
		strings.Contains(message, "tool use is not supported") ||
		strings.Contains(message, "tool calls are not supported")
}

func streamModelTurn(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	yield func(ipc.StreamEvent, error) bool,
) (modelTurn, error) {
	stream, err := deps.CallModel(ctx, buildModelRequest(state))
	if err != nil {
		return modelTurn{}, err
	}

	turn := modelTurn{}
	for event, streamErr := range stream {
		if streamErr != nil {
			return modelTurn{}, streamErr
		}

		switch event.Type {
		case api.ModelEventToken:
			turn.assistantText += event.Text
			if !yield(newEvent(ipc.EventTokenDelta, ipc.TokenDeltaPayload{Text: event.Text}), nil) {
				return modelTurn{}, context.Canceled
			}
		case api.ModelEventThinking:
			turn.assistantReasoning += event.Text
			if !yield(newEvent(ipc.EventThinkingDelta, ipc.TokenDeltaPayload{Text: event.Text}), nil) {
				return modelTurn{}, context.Canceled
			}
		case api.ModelEventToolCall:
			if event.ToolCall != nil {
				turn.toolCalls = append(turn.toolCalls, *event.ToolCall)
			}
		case api.ModelEventUsage:
			if event.Usage != nil {
				turn.outputTokens = event.Usage.OutputTokens
			}
		case api.ModelEventStop:
			turn.stopReason = event.StopReason
		}
	}

	return turn, nil
}

func buildModelRequest(state *QueryState) api.ModelRequest {
	request := api.ModelRequest{
		Messages:     state.Messages,
		SystemPrompt: state.SystemPrompt,
		MaxTokens:    state.MaxTokens,
	}
	if state.Capabilities.SupportsToolUse {
		request.Tools = state.Tools
	}
	if effort := effectiveReasoningEffort(state.ModelID, state.ReasoningEffort, latestUserPrompt(state.Messages)); effort != "" {
		request.ReasoningEffort = effort
		return request
	}
	if budget := thinkingBudgetForPrompt(latestUserPrompt(state.Messages), state.Capabilities, state.MaxTokens); budget > 0 {
		request.ThinkingBudget = budget
	}
	return request
}

func effectiveReasoningEffort(modelID, configured, prompt string) string {
	if !api.SupportsOpenAIReasoningEffort(modelID) {
		return ""
	}

	baseline := api.ClampReasoningEffort(modelID, configured)
	if baseline == "" {
		baseline = api.DefaultReasoningEffort(modelID)
	}
	if requestsExtendedThinking(prompt) {
		return api.MaxReasoningEffort(modelID, baseline, api.ReasoningEffortXHigh)
	}
	return baseline
}

func capabilitySystemPrompt(capabilities api.ModelCapabilities) string {
	if capabilities.SupportsToolUse {
		return ""
	}
	return "No native tool use for current model. Text-only responses. Do not emit tool calls or pretend tools executed."
}

func warnUnsupportedThinking(
	userPrompt string,
	capabilities api.ModelCapabilities,
	yield func(ipc.StreamEvent, error) bool,
) error {
	if !requestsExtendedThinking(userPrompt) || capabilities.SupportsExtendedThinking {
		return nil
	}
	if !yield(newEvent(ipc.EventError, ipc.ErrorPayload{
		Message:     "Current model does not support extended thinking; ignoring ultrathink and continuing with standard reasoning.",
		Recoverable: true,
	}), nil) {
		return context.Canceled
	}
	return nil
}

var clarificationResponseTerms = []string{
	"need more information",
	"need a bit more information",
	"could you tell me",
	"can you tell me",
	"i need to know",
	"what is the purpose",
	"what content should it include",
	"what are you looking for",
	"what would you like",
	"do you have any design requirements",
	"too vague",
	"to create the best possible",
	"tell me what",
}

func shouldRetryWithoutToolUse(state *QueryState, userPrompt string, turn modelTurn) bool {
	if state == nil || state.NoToolRetryUsed || !state.Capabilities.SupportsToolUse {
		return false
	}
	if len(state.Tools) == 0 || len(turn.toolCalls) > 0 {
		return false
	}
	if normalizeStopReason(turn.stopReason) != "end_turn" {
		return false
	}
	if looksLikeQuestion(userPrompt) || !containsAny(normalizeIntentText(userPrompt), implementationIntentTerms) {
		return false
	}
	response := normalizeIntentText(turn.assistantText)
	if response == "" {
		return false
	}
	return looksLikeQuestion(turn.assistantText) || containsAny(response, clarificationResponseTerms)
}

func requestsExtendedThinking(prompt string) bool {
	prompt = strings.ToLower(prompt)
	return strings.Contains(prompt, "ultrathink")
}

func thinkingBudgetForPrompt(prompt string, capabilities api.ModelCapabilities, maxTokens int) int {
	if !capabilities.SupportsExtendedThinking || !requestsExtendedThinking(prompt) || maxTokens <= 1 {
		return 0
	}

	budget := maxTokens / 2
	if budget < 1024 && maxTokens > 1024 {
		budget = 1024
	}
	if budget > 8192 {
		budget = 8192
	}
	if budget >= maxTokens {
		budget = maxTokens - 1
	}
	if budget < 0 {
		return 0
	}
	return budget
}

func runProactiveCompaction(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	yield func(ipc.StreamEvent, error) bool,
) error {
	if deps.CompactMessages == nil || state.ContextWindow <= 0 {
		return nil
	}
	if state.AutoCompactFailures >= compact.MaxConsecutiveFailures {
		return nil
	}

	pressure := EvaluateContextPressure(state.Messages, state.ContextWindow, state.MaxTokens, state.Continuation, ContextPressureSignals{
		SessionMemory:    state.SessionMemory,
		RetrievalTouched: state.RetrievalTouched,
		AttemptEntries:   state.AttemptEntries,
	})
	hasSessionMemory := state.SessionMemory.HasContent()
	hasFreshSessionMemory := state.SessionMemory.IsFresh(time.Now())
	if !pressure.ShouldCompact && !(hasFreshSessionMemory && pressure.WarningThreshold > 0 && pressure.ConversationTokens >= pressure.WarningThreshold) {
		return nil
	}
	tokensBefore := pressure.ConversationTokens
	if tokensBefore <= 0 {
		return nil
	}

	if !yield(newEvent(ipc.EventCompactStart, ipc.CompactStartPayload{
		Strategy:         string(CompactAuto),
		TokensBefore:     tokensBefore,
		HasSessionMemory: hasSessionMemory,
	}), nil) {
		return context.Canceled
	}

	compacted, err := deps.CompactMessages(ctx, state.Messages, CompactAuto)
	if err != nil {
		state.AutoCompactFailures++
		if !yield(newEvent(ipc.EventError, ipc.ErrorPayload{
			Message:     fmt.Sprintf("auto compact failed: %v", err),
			Recoverable: true,
		}), nil) {
			return context.Canceled
		}
		return nil
	}

	state.AutoCompactFailures = 0
	state.Messages = compacted.Messages

	if !yield(newEvent(ipc.EventCompactEnd, ipc.CompactEndPayload{
		Strategy:                string(compacted.Strategy),
		TokensBefore:            compacted.TokensBefore,
		TokensAfter:             compacted.TokensAfter,
		TokensSaved:             compacted.TokensBefore - compacted.TokensAfter,
		MicrocompactApplied:     compacted.MicrocompactApplied,
		MicrocompactTokensSaved: compacted.MicrocompactTokensSaved,
		HasSessionMemory:        hasSessionMemory,
	}), nil) {
		return context.Canceled
	}

	return nil
}

func normalizeStopReason(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "end_turn"
	}
	return reason
}

func latestUserPrompt(messages []api.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == api.RoleUser {
			return messages[i].Content
		}
	}
	return ""
}

func latestAssistantMessage(messages []api.Message) api.Message {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == api.RoleAssistant {
			return messages[i]
		}
	}
	return api.Message{Role: api.RoleAssistant}
}

func newEvent(eventType ipc.EventType, payload any) ipc.StreamEvent {
	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err == nil {
			raw = data
		}
	}
	return ipc.StreamEvent{
		Type:    eventType,
		Payload: raw,
	}
}

// retrievalMeta holds metadata about a completed live retrieval pass.
type retrievalMeta struct {
	SnippetCount  int
	TokensUsed    int
	AnchorCount   int
	EdgesExpanded int
	Skipped       bool
}

const retrievalTouchedLimit = 64

// runLiveRetrieval builds a live retrieval section for the current turn.
func runLiveRetrieval(state *QueryState, currentUserPrompt string, pressure ContextPressureDecision) (string, retrievalMeta) {
	if pressure.SkipLiveRetrieval {
		return "", retrievalMeta{Skipped: true}
	}

	// Gather tool output from the most recent tool turn for anchor extraction.
	recentToolOutput := latestToolOutput(state.Messages)

	anchors := ExtractAnchors(currentUserPrompt, state.TurnContext.GitStatus, recentToolOutput, state.Graph)
	candidates, edgesExpanded := ScoreCandidates(anchors, state.TurnContext.CurrentDir, state.TurnContext.GitStatus, state.RetrievalTouched, state.Graph)
	snippets := ReadLiveSnippets(candidates, pressure.RetrievalBudgetTokens)

	section := FormatLiveRetrievalSection(snippets)
	tokensUsed := 0
	for _, s := range snippets {
		tokensUsed += len(s.Content) / 4
	}
	return section, retrievalMeta{
		SnippetCount:  len(snippets),
		TokensUsed:    tokensUsed,
		AnchorCount:   len(anchors),
		EdgesExpanded: edgesExpanded,
		Skipped:       false,
	}
}

// emitRetrievalTelemetry emits the EventRetrievalUsed event when retrieval ran.
func emitRetrievalTelemetry(emit func(ipc.StreamEvent) error, meta retrievalMeta) error {
	if emit == nil {
		return nil
	}
	return emit(newEvent(ipc.EventRetrievalUsed, ipc.RetrievalUsedPayload{
		SnippetCount:  meta.SnippetCount,
		TokensUsed:    meta.TokensUsed,
		AnchorCount:   meta.AnchorCount,
		EdgesExpanded: meta.EdgesExpanded,
		Skipped:       meta.Skipped,
	}))
}

// emitAttemptLogTelemetry emits the EventAttemptLogSurfaced event when attempt
// log entries exist for the current session.
func emitAttemptLogTelemetry(emit func(ipc.StreamEvent) error, entries []AttemptEntry, section string) error {
	if emit == nil {
		return nil
	}
	return emit(newEvent(ipc.EventAttemptLogSurfaced, ipc.AttemptLogSurfacedPayload{
		EntryCount: len(entries),
		TokensUsed: len(section) / 4,
		Injected:   section != "",
	}))
}

// emitAttemptRepeatedTelemetry emits the EventAttemptRepeated event when a new
// tool failure matches a previously logged attempt-log signature.
func emitAttemptRepeatedTelemetry(emit func(ipc.StreamEvent) error, repeatedCount int) error {
	if emit == nil || repeatedCount <= 0 {
		return nil
	}
	return emit(newEvent(ipc.EventAttemptRepeated, ipc.AttemptRepeatedPayload{
		RepeatedCount: repeatedCount,
	}))
}

// buildEditRetryNudge constructs a corrective user-role message when repeated
// edit failures are detected. It tells the model to re-read the target file(s)
// before retrying so it doesn't loop with stale content.
func buildEditRetryNudge(calls []api.ToolCall, results []api.ToolResult) string {
	callByID := make(map[string]api.ToolCall, len(calls))
	for _, call := range calls {
		callByID[call.ID] = call
	}

	seen := make(map[string]struct{})
	var files []string
	for _, result := range results {
		if !result.IsError {
			continue
		}
		// Collect file paths from the result or from the tool call input.
		path := result.FilePath
		if path == "" {
			call := callByID[result.ToolCallID]
			path = extractFilePathFromInput(call.Input)
		}
		if path != "" {
			if _, ok := seen[path]; !ok {
				seen[path] = struct{}{}
				files = append(files, path)
			}
		}
	}
	if len(files) == 0 {
		return "[system] Your previous file edit failed with the same error again. You are using stale file contents. Re-read the target file before retrying the edit."
	}
	return fmt.Sprintf("[system] Your previous file edit failed with the same error again. You are using stale file contents. Re-read the following file(s) before retrying: %s", strings.Join(files, ", "))
}

// extractFilePathFromInput tries to pull a file path from a tool call's JSON input.
func extractFilePathFromInput(input string) string {
	if input == "" {
		return ""
	}
	var params map[string]any
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return ""
	}
	for _, key := range []string{"file_path", "FilePath", "target_file", "TargetFile", "path"} {
		if v, ok := params[key]; ok {
			if s, ok := v.(string); ok && s != "" {
				return s
			}
		}
	}
	return ""
}

// collectTouchedFiles appends file paths referenced by tool calls and tool
// results to the session-scoped retrieval touched list so they score higher in
// future turns.
func collectTouchedFiles(state *QueryState, calls []api.ToolCall, results []api.ToolResult) {
	if state == nil {
		return
	}
	cwd := state.TurnContext.CurrentDir
	seen := make(map[string]struct{}, len(state.RetrievalTouched))
	for _, p := range state.RetrievalTouched {
		seen[p] = struct{}{}
	}
	addTouchedPath := func(path string) {
		for _, resolved := range resolveFilePath(path, cwd) {
			if _, ok := seen[resolved]; ok {
				continue
			}
			seen[resolved] = struct{}{}
			state.RetrievalTouched = append(state.RetrievalTouched, resolved)
		}
	}

	for _, call := range calls {
		paths := extractFilePathsFromToolCall(call)
		for _, p := range paths {
			addTouchedPath(p)
		}
	}
	for _, result := range results {
		addTouchedPath(result.FilePath)
		for _, path := range extractFilePathMatches(result.Output) {
			addTouchedPath(path)
		}
	}
	if len(state.RetrievalTouched) > retrievalTouchedLimit {
		state.RetrievalTouched = append([]string(nil), state.RetrievalTouched[len(state.RetrievalTouched)-retrievalTouchedLimit:]...)
	}
}

// invalidateGraphFiles marks files touched by tool calls as dirty in the
// retrieval graph so they are re-parsed on the next retrieval turn.
func invalidateGraphFiles(state *QueryState, calls []api.ToolCall, results []api.ToolResult) {
	if state == nil || state.Graph == nil {
		return
	}
	cwd := state.TurnContext.CurrentDir
	invalidated := make(map[string]struct{})
	invalidate := func(path string) {
		for _, resolved := range resolveFilePath(path, cwd) {
			if _, done := invalidated[resolved]; done {
				continue
			}
			invalidated[resolved] = struct{}{}
			state.Graph.Invalidate(resolved)
		}
	}
	for _, call := range calls {
		for _, p := range extractFilePathsFromToolCall(call) {
			invalidate(p)
		}
	}
	for _, result := range results {
		invalidate(result.FilePath)
	}
}

// extractFilePathsFromToolCall extracts file path arguments from common tool calls.
func extractFilePathsFromToolCall(call api.ToolCall) []string {
	// Tool inputs are JSON; try common field names used across tool implementations.
	type genericInput struct {
		Path            string `json:"path"`
		TargetFile      string `json:"target_file"`
		FilePath        string `json:"file_path"`
		FilePathCompat  string `json:"filePath"`
		File            string `json:"file"`
		DirPath         string `json:"dirPath"`
		WorkspaceFolder string `json:"workspaceFolder"`
		RootPath        string `json:"rootPath"`
		URI             string `json:"uri"`
	}
	var input genericInput
	if err := json.Unmarshal([]byte(call.Input), &input); err != nil {
		return nil
	}
	var paths []string
	for _, p := range []string{input.Path, input.TargetFile, input.FilePath, input.FilePathCompat, input.File, input.DirPath, input.WorkspaceFolder, input.RootPath, input.URI} {
		p = strings.TrimSpace(p)
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// recordFailedAttempts inspects tool results for errors and records them to the attempt log.
// Returns the number of repeated failures (matching previously logged signatures).
func recordFailedAttempts(log *AttemptLog, calls []api.ToolCall, results []api.ToolResult) int {
	if log == nil || len(results) == 0 {
		return 0
	}

	// Load existing entries to detect repeats.
	existing, _ := log.Load()
	existingSigs := make(map[string]struct{}, len(existing))
	for _, entry := range existing {
		if entry.ErrorSignature != "" {
			existingSigs[entry.ErrorSignature] = struct{}{}
		}
	}

	callByID := make(map[string]api.ToolCall, len(calls))
	for _, call := range calls {
		callByID[call.ID] = call
	}

	repeated := 0
	for _, result := range results {
		if !result.IsError {
			continue
		}
		call := callByID[result.ToolCallID]
		sig := errorSignatureFromOutput(result.Output)
		if sig != "" {
			if _, wasLogged := existingSigs[sig]; wasLogged {
				repeated++
			}
		}
		entry := AttemptEntry{
			Command:        call.Name,
			ErrorSignature: sig,
		}
		_ = log.Record(entry)
	}
	return repeated
}

// errorSignatureFromOutput extracts a compact error signature from tool output.
func errorSignatureFromOutput(output string) string {
	output = strings.TrimSpace(output)
	if output == "" {
		return ""
	}
	lines := strings.SplitN(output, "\n", 4)
	sig := strings.TrimSpace(lines[0])
	if len(sig) > 120 {
		sig = sig[:120]
	}
	return sig
}

// latestToolOutput returns the combined output of the most recent tool result messages.
func latestToolOutput(messages []api.Message) string {
	var b strings.Builder
	// Walk backwards; collect tool results until we hit a non-tool message.
	for i := len(messages) - 1; i >= 0; i-- {
		msg := messages[i]
		if msg.Role != api.RoleTool {
			break
		}
		if msg.ToolResult != nil && strings.TrimSpace(msg.ToolResult.Output) != "" {
			b.WriteString(msg.ToolResult.Output)
			b.WriteString("\n")
		}
	}
	return b.String()
}
