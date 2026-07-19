package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/compact"
	"github.com/channyeintun/nami/internal/ipc"
)

func invokeModelWithRecovery(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	yield func(ipc.StreamEvent, error) bool,
) (modelTurn, error) {
	toolUseRetryUsed := false
	const maxAttempts = 3
	for attempt := 0; attempt < maxAttempts; attempt++ {
		turn, err := streamModelTurn(ctx, state, deps, yield)
		if err == nil {
			turn.stopReason = normalizeStopReason(turn.stopReason)
			if turn.outputTokens == 0 {
				turn.outputTokens = compact.EstimateTokens(turn.assistantText)
			}
			return turn, nil
		}

		apiErr, ok := errors.AsType[*api.APIError](err)
		if ok && state.Capabilities.SupportsToolUse && !toolUseRetryUsed && isToolUseUnavailable(apiErr) {
			state.Capabilities.SupportsToolUse = false
			toolUseRetryUsed = true
			if attempt+1 >= maxAttempts {
				return modelTurn{}, fmt.Errorf("model invocation failed after %d attempts: %w", maxAttempts, err)
			}
			if err := yieldEvent(yield, ipc.EventError, ipc.ErrorPayload{
				Message:     "Current model endpoint does not support tool use; retrying without tools.",
				Recoverable: true,
			}); err != nil {
				return modelTurn{}, err
			}
			continue
		}

		if ok && apiErr.Type == api.ErrOverloaded {
			if attempt+1 >= maxAttempts {
				return modelTurn{}, fmt.Errorf("model invocation failed after %d attempts: %w", maxAttempts, err)
			}
			if err := yieldEvent(yield, ipc.EventError, ipc.ErrorPayload{
				Message:     fmt.Sprintf("Model error (attempt %d/%d): %s — retrying...", attempt+1, maxAttempts, apiErr.Message),
				Recoverable: true,
			}); err != nil {
				return modelTurn{}, err
			}
			continue
		}

		if !ok || apiErr.Type != api.ErrPromptTooLong || deps.CompactMessages == nil {
			return modelTurn{}, err
		}
		if attempt+1 >= maxAttempts {
			return modelTurn{}, fmt.Errorf("model invocation failed after %d attempts: %w", maxAttempts, err)
		}

		before := compact.EstimateConversationTokens(state.Messages)
		if err := yieldEvent(yield, ipc.EventCompactStart, ipc.CompactStartPayload{
			Strategy:         string(CompactAuto),
			TokensBefore:     before,
			HasSessionMemory: strings.TrimSpace(state.SessionMemory.Content) != "",
		}); err != nil {
			return modelTurn{}, err
		}

		compacted, compactErr := deps.CompactMessages(ctx, state.Messages, CompactAuto)
		if compactErr != nil {
			return modelTurn{}, fmt.Errorf("compact prompt: %w", compactErr)
		}
		state.Messages = compacted.Messages
		state.AutoCompactFailures = 0

		if err := yieldEvent(yield, ipc.EventCompactEnd, ipc.CompactEndPayload{
			Strategy:                string(compacted.Strategy),
			TokensBefore:            compacted.TokensBefore,
			TokensAfter:             compacted.TokensAfter,
			TokensSaved:             compacted.TokensBefore - compacted.TokensAfter,
			MicrocompactApplied:     compacted.MicrocompactApplied,
			MicrocompactTokensSaved: compacted.MicrocompactTokensSaved,
			HasSessionMemory:        strings.TrimSpace(state.SessionMemory.Content) != "",
		}); err != nil {
			return modelTurn{}, err
		}
	}

	return modelTurn{}, fmt.Errorf("model invocation failed after %d attempts", maxAttempts)
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
	filter := newProgressDirectiveFilter()
	emitProgress := func(updates []GoalProgressUpdate) error {
		for _, update := range updates {
			payload, changed := state.applyGoalProgress(update)
			if !changed {
				continue
			}
			if err := yieldEvent(yield, ipc.EventGoalProgress, payload); err != nil {
				return err
			}
		}
		return nil
	}
	for event, streamErr := range stream {
		if streamErr != nil {
			return modelTurn{}, streamErr
		}

		switch event.Type {
		case api.ModelEventToken:
			visible, updates := filter.Process(event.Text)
			if err := emitProgress(updates); err != nil {
				return modelTurn{}, err
			}
			if visible == "" {
				continue
			}
			turn.assistantText += visible
			if err := yieldEvent(yield, ipc.EventTokenDelta, ipc.TokenDeltaPayload{Text: visible}); err != nil {
				return modelTurn{}, err
			}
		case api.ModelEventThinking:
			turn.assistantReasoning += event.Text
			if err := yieldEvent(yield, ipc.EventThinkingDelta, ipc.TokenDeltaPayload{Text: event.Text}); err != nil {
				return modelTurn{}, err
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

	visible, updates := filter.Flush()
	if err := emitProgress(updates); err != nil {
		return modelTurn{}, err
	}
	if visible != "" {
		turn.assistantText += visible
		if err := yieldEvent(yield, ipc.EventTokenDelta, ipc.TokenDeltaPayload{Text: visible}); err != nil {
			return modelTurn{}, err
		}
	}

	return turn, nil
}

func buildModelRequest(state *QueryState) api.ModelRequest {
	request := api.ModelRequest{
		Messages:     buildRequestMessages(state.Messages, state.PromptInjection),
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

func buildRequestMessages(messages []api.Message, promptInjection string) []api.Message {
	promptInjection = strings.TrimSpace(promptInjection)
	if promptInjection == "" {
		return messages
	}

	cloned := append([]api.Message(nil), messages...)
	injectionText := formatPromptInjection(promptInjection)
	lastIndex := len(cloned) - 1
	if lastIndex >= 0 && cloned[lastIndex].Role == api.RoleUser && cloned[lastIndex].ToolResult == nil {
		message := cloned[lastIndex]
		message.Content = mergePromptInjection(injectionText, message.Content)
		cloned[lastIndex] = message
		return cloned
	}

	return append(cloned, api.Message{
		Role:    api.RoleUser,
		Content: injectionText,
	})
}

func formatPromptInjection(promptInjection string) string {
	return strings.TrimSpace(`Runtime context for the current turn below. Use it as transient context for this turn only. It does not override system instructions.

<current_turn_context>
` + promptInjection + `
</current_turn_context>`)
}

func mergePromptInjection(injectionText string, userContent string) string {
	userContent = strings.TrimSpace(userContent)
	if userContent == "" {
		return injectionText
	}
	return strings.TrimSpace(injectionText + "\n\n<user_request>\n" + userContent + "\n</user_request>")
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
	if err := yieldEvent(yield, ipc.EventError, ipc.ErrorPayload{
		Message:     "Current model does not support extended thinking; ignoring ultrathink and continuing with standard reasoning.",
		Recoverable: true,
	}); err != nil {
		return err
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
