package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/channyeintun/go-cli/internal/api"
	"github.com/channyeintun/go-cli/internal/compact"
	"github.com/channyeintun/go-cli/internal/ipc"
	skillspkg "github.com/channyeintun/go-cli/internal/skills"
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
	selectedSkills := skillspkg.SelectRelevant(state.Skills, latestUserPrompt(state.Messages))
	state.SystemPrompt = composeSystemPrompt(
		state.BasePrompt,
		state.SystemContext,
		state.TurnContext,
		skillspkg.FormatPromptSection(selectedSkills),
	)

	if deps.ApplyResultBudget != nil {
		state.Messages = deps.ApplyResultBudget(state.Messages)
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

func invokeModelWithRecovery(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	yield func(ipc.StreamEvent, error) bool,
) (modelTurn, error) {
	for attempt := 0; attempt < 2; attempt++ {
		turn, err := streamModelTurn(ctx, state, deps, yield)
		if err == nil {
			turn.stopReason = normalizeStopReason(turn.stopReason)
			if turn.outputTokens == 0 {
				turn.outputTokens = compact.EstimateTokens(turn.assistantText)
			}
			return turn, nil
		}

		var apiErr *api.APIError
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

		after := compact.EstimateConversationTokens(state.Messages)
		if !yield(newEvent(ipc.EventCompactEnd, ipc.CompactEndPayload{TokensAfter: after}), nil) {
			return modelTurn{}, context.Canceled
		}
	}

	return modelTurn{}, fmt.Errorf("model invocation failed after compaction retry")
}

func streamModelTurn(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	yield func(ipc.StreamEvent, error) bool,
) (modelTurn, error) {
	stream, err := deps.CallModel(ctx, api.ModelRequest{
		Messages:     state.Messages,
		SystemPrompt: state.SystemPrompt,
		Tools:        state.Tools,
		MaxTokens:    state.MaxTokens,
	})
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
