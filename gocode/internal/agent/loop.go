package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/channyeintun/gocode/internal/api"
	"github.com/channyeintun/gocode/internal/compact"
	"github.com/channyeintun/gocode/internal/ipc"
	skillspkg "github.com/channyeintun/gocode/internal/skills"
)

type modelTurn struct {
	assistantText string
	toolCalls     []api.ToolCall
	stopReason    string
	outputTokens  int
}

func runIteration(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	yield func(ipc.StreamEvent, error) bool,
) error {
	state.TurnCount++
	state.TurnContext = LoadTurnContext()
	currentUserPrompt := latestUserPrompt(state.Messages)
	memoryRecalls := recallMemoryIndexes(ctx, deps.RecallMemory, state.SystemContext.MemoryFiles, currentUserPrompt)
	selectedSkills := skillspkg.SelectRelevant(state.Skills, currentUserPrompt)
	basePrompt := state.BasePrompt
	if capabilityPrompt := capabilitySystemPrompt(state.Capabilities); capabilityPrompt != "" {
		basePrompt = strings.TrimSpace(basePrompt + "\n\n" + capabilityPrompt)
	}
	state.SystemPrompt = composeSystemPrompt(
		basePrompt,
		state.SystemContext,
		state.TurnContext,
		currentUserPrompt,
		memoryRecalls,
		skillspkg.FormatPromptSection(selectedSkills),
	)

	if deps.ApplyResultBudget != nil {
		state.Messages = deps.ApplyResultBudget(state.Messages)
	}

	if err := runProactiveCompaction(ctx, state, deps, yield); err != nil {
		return err
	}

	if err := warnUnsupportedThinking(currentUserPrompt, state.Capabilities, yield); err != nil {
		return err
	}

	turn, err := invokeModelWithRecovery(ctx, state, deps, yield)
	if err != nil {
		return err
	}

	assistantMessage := api.Message{
		Role:      api.RoleAssistant,
		Content:   strings.TrimSpace(turn.assistantText),
		ToolCalls: turn.toolCalls,
	}
	if assistantMessage.Content != "" || len(assistantMessage.ToolCalls) > 0 {
		state.Messages = append(state.Messages, assistantMessage)
	}

	if turn.outputTokens > 0 {
		state.Continuation.Record(turn.outputTokens)
	}

	if turn.stopReason == "tool_use" && len(turn.toolCalls) > 0 {
		results, err := deps.ExecuteToolBatch(ctx, turn.toolCalls)
		if err != nil {
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
		return nil
	}

	if turn.stopReason != "max_tokens" {
		state.StopRequested = true
		if !yield(newEvent(ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: normalizeStopReason(turn.stopReason)}), nil) {
			return context.Canceled
		}
	}

	return nil
}

func recallMemoryIndexes(
	ctx context.Context,
	recall func(context.Context, []MemoryFile, string) ([]MemoryRecallResult, error),
	files []MemoryFile,
	currentUserPrompt string,
) []MemoryRecallResult {
	if recall == nil || strings.TrimSpace(currentUserPrompt) == "" {
		return nil
	}

	hasMemoryIndexes := false
	for _, file := range files {
		if file.Type == memoryTypeProjectIndex || file.Type == memoryTypeUserIndex {
			hasMemoryIndexes = true
			break
		}
	}
	if !hasMemoryIndexes {
		return nil
	}

	results, err := recall(ctx, files, currentUserPrompt)
	if err != nil {
		return nil
	}
	if len(results) == 0 {
		return nil
	}
	return results
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
			Strategy:     string(CompactAuto),
			TokensBefore: before,
		}), nil) {
			return modelTurn{}, context.Canceled
		}

		compacted, compactErr := deps.CompactMessages(ctx, state.Messages, CompactAuto)
		if compactErr != nil {
			return modelTurn{}, fmt.Errorf("compact prompt: %w", compactErr)
		}
		state.Messages = compacted
		state.AutoCompactFailures = 0

		after := compact.EstimateConversationTokens(state.Messages)
		if !yield(newEvent(ipc.EventCompactEnd, ipc.CompactEndPayload{TokensAfter: after}), nil) {
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
	if budget := thinkingBudgetForPrompt(latestUserPrompt(state.Messages), state.Capabilities, state.MaxTokens); budget > 0 {
		request.ThinkingBudget = budget
	}
	return request
}

func capabilitySystemPrompt(capabilities api.ModelCapabilities) string {
	if capabilities.SupportsToolUse {
		return ""
	}
	return "Native tool use is unavailable for the current model. Do not emit tool calls. Respond with text only, explain limitations plainly, and avoid pretending a tool was executed."
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

	effectiveWindow := compact.EffectiveContextWindow(state.ContextWindow, state.MaxTokens)
	threshold := compact.AutocompactThreshold(effectiveWindow)
	if threshold <= 0 {
		return nil
	}

	tokensBefore := compact.EstimateConversationTokens(state.Messages)
	if tokensBefore < threshold {
		return nil
	}

	if !yield(newEvent(ipc.EventCompactStart, ipc.CompactStartPayload{
		Strategy:     string(CompactAuto),
		TokensBefore: tokensBefore,
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
	state.Messages = compacted

	tokensAfter := compact.EstimateConversationTokens(state.Messages)
	if !yield(newEvent(ipc.EventCompactEnd, ipc.CompactEndPayload{TokensAfter: tokensAfter}), nil) {
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
