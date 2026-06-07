package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/channyeintun/nami/internal/ipc"
)

func applyEvent(state uiState, event ipc.StreamEvent) uiState {
	switch event.Type {
	case ipc.EventReady:
		state.Ready = true
		state.Status = "ready"
	case ipc.EventError:
		state.ErrorMessage = summarizeEvent(event)
	case ipc.EventTokenDelta:
		return applyAssistantDelta(state, summarizeEvent(event))
	case ipc.EventTurnComplete:
		state.TurnActive = false
		state.Status = "ready"
	case ipc.EventModelChanged:
		payload, err := decodePayload[ipc.ModelChangedPayload](event)
		if err != nil {
			return state.withDecodeError("model", err)
		}
		state.Model = strings.TrimSpace(payload.Model)
		state.Reasoning = strings.TrimSpace(payload.ReasoningEffort)
		state.ContextMax = payload.MaxContextWindow
	case ipc.EventModeChanged:
		payload, err := decodePayload[ipc.ModeChangedPayload](event)
		if err != nil {
			return state.withDecodeError("mode", err)
		}
		state.Mode = strings.TrimSpace(payload.Mode)
	case ipc.EventContextWindow:
		payload, err := decodePayload[ipc.ContextWindowPayload](event)
		if err != nil {
			return state.withDecodeError("context", err)
		}
		state.ContextUsage = payload.CurrentUsage
		return state
	case ipc.EventCostUpdate:
		payload, err := decodePayload[ipc.CostUpdatePayload](event)
		if err != nil {
			return state.withDecodeError("cost", err)
		}
		state.TotalUSD = payload.TotalUSD
		state.InputTokens = payload.InputTokens
		state.OutputTokens = payload.OutputTokens
		return state
	case ipc.EventRateLimitUpdate:
		payload, err := decodePayload[ipc.RateLimitUpdatePayload](event)
		if err != nil {
			return state.withDecodeError("rate limit", err)
		}
		state.RateLimit = formatRateLimit(payload)
		return state
	}

	if summary := summarizeEvent(event); strings.TrimSpace(summary) != "" {
		return state.appendLine(summary)
	}
	return state
}

func decodePayload[T any](event ipc.StreamEvent) (T, error) {
	var payload T
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		return payload, err
	}
	return payload, nil
}

func (s uiState) withDecodeError(label string, err error) uiState {
	s.ErrorMessage = fmt.Sprintf("%s: decode failed: %v", label, err)
	return s.appendLine(s.ErrorMessage)
}

func formatRateLimit(payload ipc.RateLimitUpdatePayload) string {
	parts := make([]string, 0, 2)
	if payload.FiveHour != nil {
		parts = append(parts, fmt.Sprintf("5h %.0f%%", payload.FiveHour.UsedPercentage))
	}
	if payload.SevenDay != nil {
		parts = append(parts, fmt.Sprintf("7d %.0f%%", payload.SevenDay.UsedPercentage))
	}
	return strings.Join(parts, " ")
}

func applyAssistantDelta(state uiState, delta string) uiState {
	if delta == "" {
		return state
	}
	if state.Assistant == "" {
		state.Lines = append(state.Lines, "assistant: ")
	}
	state.Assistant += delta
	state.Lines[len(state.Lines)-1] = "assistant: " + strings.TrimRight(state.Assistant, "\n")
	return state
}
