package tui

import (
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
	}

	if summary := summarizeEvent(event); strings.TrimSpace(summary) != "" {
		return state.appendLine(summary)
	}
	return state
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
