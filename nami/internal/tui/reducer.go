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
		payload, err := decodePayload[ipc.ReadyPayload](event)
		if err != nil {
			return state.withDecodeError("ready", err)
		}
		state.Ready = true
		state.Status = "ready"
		state.SlashCommands = slashCommandsFromPayload(payload.SlashCommands)
	case ipc.EventError:
		state.ErrorMessage = summarizeEvent(event)
	case ipc.EventTokenDelta:
		return applyAssistantDelta(state, summarizeEvent(event))
	case ipc.EventTurnComplete:
		state.TurnActive = false
		state.Status = "ready"
		state.PermissionRequest = nil
		state.QuestionRequest = nil
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
	case ipc.EventArtifactCreated:
		payload, err := decodePayload[ipc.ArtifactCreatedPayload](event)
		if err != nil {
			return state.withDecodeError("artifact", err)
		}
		state = state.withArtifact(artifactState{
			ID:      strings.TrimSpace(payload.ID),
			Kind:    strings.TrimSpace(payload.Kind),
			Title:   strings.TrimSpace(payload.Title),
			Version: payload.Version,
			Status:  strings.TrimSpace(payload.Status),
		})
	case ipc.EventArtifactUpdated:
		payload, err := decodePayload[ipc.ArtifactUpdatedPayload](event)
		if err != nil {
			return state.withDecodeError("artifact update", err)
		}
		artifact := state.Artifacts[strings.TrimSpace(payload.ID)]
		artifact.ID = strings.TrimSpace(payload.ID)
		if payload.Version > 0 {
			artifact.Version = payload.Version
		}
		if strings.TrimSpace(payload.Status) != "" {
			artifact.Status = strings.TrimSpace(payload.Status)
		}
		state = state.withArtifact(artifact)
	case ipc.EventArtifactFocused:
		payload, err := decodePayload[ipc.ArtifactFocusedPayload](event)
		if err != nil {
			return state.withDecodeError("artifact focus", err)
		}
		artifact := artifactState{
			ID:      strings.TrimSpace(payload.ID),
			Kind:    strings.TrimSpace(payload.Kind),
			Title:   strings.TrimSpace(payload.Title),
			Version: payload.Version,
			Status:  strings.TrimSpace(payload.Status),
		}
		state = state.withArtifact(artifact)
		state.FocusedArtifactID = artifact.ID
	case ipc.EventArtifactStatusChanged:
		payload, err := decodePayload[ipc.ArtifactStatusChangedPayload](event)
		if err != nil {
			return state.withDecodeError("artifact status", err)
		}
		artifact := state.Artifacts[strings.TrimSpace(payload.ID)]
		artifact.ID = strings.TrimSpace(payload.ID)
		artifact.Status = strings.TrimSpace(payload.Status)
		state = state.withArtifact(artifact)
	case ipc.EventArtifactReviewRequested:
		payload, err := decodePayload[ipc.ArtifactReviewRequestedPayload](event)
		if err != nil {
			return state.withDecodeError("artifact review", err)
		}
		artifact := artifactState{
			ID:      strings.TrimSpace(payload.ID),
			Kind:    strings.TrimSpace(payload.Kind),
			Title:   strings.TrimSpace(payload.Title),
			Version: payload.Version,
			Status:  "review_requested",
		}
		state = state.withArtifact(artifact)
		state.ArtifactReview = &artifactReviewState{
			RequestID: strings.TrimSpace(payload.RequestID),
			Artifact:  artifact,
		}
	case ipc.EventArtifactReviewResolved:
		state.ArtifactReview = nil
	case ipc.EventBackgroundCommandUpdated, ipc.EventBackgroundCommandDetail:
		command, err := backgroundCommandFromEvent(event)
		if err != nil {
			return state.withDecodeError("background command", err)
		}
		state = state.withBackgroundCommand(command)
	case ipc.EventBackgroundAgentUpdated, ipc.EventBackgroundAgentDetail:
		agent, err := backgroundAgentFromEvent(event)
		if err != nil {
			return state.withDecodeError("background agent", err)
		}
		state = state.withBackgroundAgent(agent)
	case ipc.EventPermissionRequest:
		payload, err := decodePayload[ipc.PermissionRequestPayload](event)
		if err != nil {
			return state.withDecodeError("permission", err)
		}
		state.PermissionRequest = &permissionRequestState{
			RequestID: strings.TrimSpace(payload.RequestID),
			Tool:      strings.TrimSpace(payload.Tool),
			Risk:      strings.TrimSpace(payload.Risk),
			Command:   strings.TrimSpace(payload.Command),
		}
	case ipc.EventAskUserQuestionRequested:
		payload, err := decodePayload[ipc.AskUserQuestionRequestedPayload](event)
		if err != nil {
			return state.withDecodeError("question", err)
		}
		state.QuestionRequest = &questionRequestState{
			RequestID: strings.TrimSpace(payload.RequestID),
			Count:     len(payload.Questions),
		}
	case ipc.EventModelSelectionRequested:
		payload, err := decodePayload[ipc.ModelSelectionRequestedPayload](event)
		if err != nil {
			return state.withDecodeError("model selection", err)
		}
		state.SelectionRequest = &selectionRequestState{
			Kind:      "model",
			RequestID: strings.TrimSpace(payload.RequestID),
			Title:     strings.TrimSpace(payload.Title),
			Count:     len(payload.Options),
		}
	case ipc.EventReasoningSelectionRequested:
		payload, err := decodePayload[ipc.ReasoningSelectionRequestedPayload](event)
		if err != nil {
			return state.withDecodeError("reasoning selection", err)
		}
		state.SelectionRequest = &selectionRequestState{
			Kind:      "reasoning",
			RequestID: strings.TrimSpace(payload.RequestID),
			Title:     strings.TrimSpace(payload.Title),
			Count:     len(payload.Options),
		}
	case ipc.EventResumeSelectionRequested:
		payload, err := decodePayload[ipc.ResumeSelectionRequestedPayload](event)
		if err != nil {
			return state.withDecodeError("resume selection", err)
		}
		state.SelectionRequest = &selectionRequestState{
			Kind:      "resume",
			RequestID: strings.TrimSpace(payload.RequestID),
			Title:     "Resume session",
			Count:     len(payload.Sessions),
		}
	case ipc.EventRewindSelectionRequested:
		payload, err := decodePayload[ipc.RewindSelectionRequestedPayload](event)
		if err != nil {
			return state.withDecodeError("rewind selection", err)
		}
		state.SelectionRequest = &selectionRequestState{
			Kind:      "rewind",
			RequestID: strings.TrimSpace(payload.RequestID),
			Title:     "Rewind conversation",
			Count:     len(payload.Turns),
		}
	case ipc.EventConversationHydrated:
		payload, err := decodePayload[ipc.ConversationHydratedPayload](event)
		if err != nil {
			return state.withDecodeError("conversation hydration", err)
		}
		state.Transcript = hydratedTranscriptEntries(payload)
		if len(state.Transcript) == 0 {
			state.Transcript = []transcriptEntry{{Kind: "system", Text: "Conversation restored."}}
		}
		state.Assistant = ""
		state.TurnActive = false
		state.Hydrated = true
		state.ErrorMessage = ""
		return state
	case ipc.EventMemoryRecalled:
		payload, err := decodePayload[ipc.MemoryRecalledPayload](event)
		if err != nil {
			return state.withDecodeError("memory", err)
		}
		state.MemoryCount = payload.Count
	case ipc.EventRetrievalUsed:
		payload, err := decodePayload[ipc.RetrievalUsedPayload](event)
		if err != nil {
			return state.withDecodeError("retrieval", err)
		}
		if payload.Skipped {
			state.RetrievalSummary = "skipped"
		} else {
			state.RetrievalSummary = fmt.Sprintf("%d snippets", payload.SnippetCount)
		}
		return state
	case ipc.EventCompactStart:
		payload, err := decodePayload[ipc.CompactStartPayload](event)
		if err != nil {
			return state.withDecodeError("compact start", err)
		}
		state.Compacting = true
		state.CompactSummary = fmt.Sprintf("%s from %d tokens", strings.TrimSpace(payload.Strategy), payload.TokensBefore)
	case ipc.EventCompactEnd:
		payload, err := decodePayload[ipc.CompactEndPayload](event)
		if err != nil {
			return state.withDecodeError("compact end", err)
		}
		state.Compacting = false
		state.CompactSummary = fmt.Sprintf("%d tokens saved", payload.TokensSaved)
	case ipc.EventTurnTiming:
		payload, err := decodePayload[ipc.TurnTimingPayload](event)
		if err != nil {
			return state.withDecodeError("timing", err)
		}
		state.LastTiming = fmt.Sprintf("%s %dms", strings.TrimSpace(payload.Checkpoint), payload.ElapsedMS)
		return state
	case ipc.EventSessionUpdated:
		payload, err := decodePayload[ipc.SessionUpdatedPayload](event)
		if err != nil {
			return state.withDecodeError("session", err)
		}
		state.SessionID = strings.TrimSpace(payload.SessionID)
		state.SessionTitle = strings.TrimSpace(payload.Title)
		return state
	case ipc.EventSessionRestored:
		payload, err := decodePayload[ipc.SessionRestoredPayload](event)
		if err != nil {
			return state.withDecodeError("session restore", err)
		}
		state.SessionID = strings.TrimSpace(payload.SessionID)
		state.Mode = strings.TrimSpace(payload.Mode)
	case ipc.EventSessionRewound:
		payload, err := decodePayload[ipc.SessionRewoundPayload](event)
		if err != nil {
			return state.withDecodeError("session rewind", err)
		}
		state.SessionID = strings.TrimSpace(payload.SessionID)
		state.SelectionRequest = nil
	}

	if summary := summarizeEvent(event); strings.TrimSpace(summary) != "" {
		return state.appendLine(summary)
	}
	return state
}

func slashCommandsFromPayload(payload []ipc.SlashCommandDescriptorPayload) []slashCommandState {
	commands := make([]slashCommandState, 0, len(payload))
	for _, command := range payload {
		name := strings.TrimSpace(command.Name)
		if name == "" {
			continue
		}
		commands = append(commands, slashCommandState{
			Name:           name,
			Description:    strings.TrimSpace(command.Description),
			Usage:          strings.TrimSpace(command.Usage),
			TakesArguments: command.TakesArguments,
		})
	}
	return commands
}

func hydratedTranscriptEntries(payload ipc.ConversationHydratedPayload) []transcriptEntry {
	messages := make(map[string]ipc.ConversationHydratedMessagePayload, len(payload.Messages))
	for _, message := range payload.Messages {
		if id := strings.TrimSpace(message.ID); id != "" {
			messages[id] = message
		}
	}
	progress := make(map[string]ipc.ConversationHydratedProgressPayload, len(payload.Progress))
	for _, entry := range payload.Progress {
		if id := strings.TrimSpace(entry.ID); id != "" {
			progress[id] = entry
		}
	}
	tools := make(map[string]ipc.ConversationHydratedToolCallPayload, len(payload.ToolCalls))
	for _, tool := range payload.ToolCalls {
		if id := strings.TrimSpace(tool.ID); id != "" {
			tools[id] = tool
		}
	}

	if len(payload.Transcript) == 0 {
		entries := make([]transcriptEntry, 0, len(payload.Messages))
		for _, message := range payload.Messages {
			if entry, ok := hydratedMessageEntry(message); ok {
				entries = append(entries, entry)
			}
		}
		return entries
	}

	entries := make([]transcriptEntry, 0, len(payload.Transcript))
	for _, entry := range payload.Transcript {
		refID := strings.TrimSpace(entry.RefID)
		if refID == "" {
			refID = strings.TrimSpace(entry.ID)
		}
		switch entry.Kind {
		case "message":
			if item, ok := hydratedMessageEntry(messages[refID]); ok {
				entries = append(entries, item)
			}
		case "progress":
			if item, ok := progress[refID]; ok {
				entries = append(entries, transcriptEntry{Kind: "system", Text: "progress: " + strings.TrimSpace(item.Message)})
			}
		case "tool_call":
			if item, ok := tools[refID]; ok {
				entries = append(entries, transcriptEntry{Kind: "tool", Text: hydratedToolLine(item)})
			}
		}
	}
	return entries
}

func hydratedMessageEntry(message ipc.ConversationHydratedMessagePayload) (transcriptEntry, bool) {
	role := strings.TrimSpace(message.Role)
	if role == "" {
		return transcriptEntry{}, false
	}
	text := strings.TrimSpace(message.Text)
	if text == "" {
		parts := make([]string, 0, len(message.Blocks))
		for _, block := range message.Blocks {
			if blockText := strings.TrimSpace(block.Text); blockText != "" {
				parts = append(parts, blockText)
			}
		}
		text = strings.Join(parts, "\n")
	}
	if text == "" {
		return transcriptEntry{}, false
	}
	if role == "user" {
		return transcriptEntry{Kind: "user", Text: text}, true
	}
	if role == "assistant" {
		return transcriptEntry{Kind: "assistant", Text: text}, true
	}
	return transcriptEntry{Kind: "system", Text: role + ": " + text}, true
}

func hydratedToolLine(tool ipc.ConversationHydratedToolCallPayload) string {
	name := strings.TrimSpace(tool.Name)
	if name == "" {
		name = strings.TrimSpace(tool.ID)
	}
	status := strings.TrimSpace(tool.Status)
	if status == "" {
		status = "unknown"
	}
	return fmt.Sprintf("tool %s: %s", status, name)
}

func (s uiState) withArtifact(artifact artifactState) uiState {
	if s.Artifacts == nil {
		s.Artifacts = make(map[string]artifactState)
	}
	if artifact.ID != "" {
		s.Artifacts[artifact.ID] = artifact
	}
	return s
}

func (s uiState) withBackgroundCommand(command backgroundCommandState) uiState {
	if s.BackgroundCommands == nil {
		s.BackgroundCommands = make(map[string]backgroundCommandState)
	}
	if command.ID != "" {
		s.BackgroundCommands[command.ID] = command
	}
	return s
}

func (s uiState) withBackgroundAgent(agent backgroundAgentState) uiState {
	if s.BackgroundAgents == nil {
		s.BackgroundAgents = make(map[string]backgroundAgentState)
	}
	if agent.ID != "" {
		s.BackgroundAgents[agent.ID] = agent
	}
	return s
}

func backgroundCommandFromEvent(event ipc.StreamEvent) (backgroundCommandState, error) {
	if event.Type == ipc.EventBackgroundCommandDetail {
		payload, err := decodePayload[ipc.BackgroundCommandDetailPayload](event)
		if err != nil {
			return backgroundCommandState{}, err
		}
		return backgroundCommandState{
			ID:          strings.TrimSpace(payload.CommandID),
			Command:     strings.TrimSpace(payload.Command),
			Status:      strings.TrimSpace(payload.Status),
			Running:     payload.Running,
			Error:       strings.TrimSpace(payload.Error),
			UnreadBytes: payload.UnreadBytes,
		}, nil
	}
	payload, err := decodePayload[ipc.BackgroundCommandUpdatedPayload](event)
	if err != nil {
		return backgroundCommandState{}, err
	}
	return backgroundCommandState{
		ID:          strings.TrimSpace(payload.CommandID),
		Command:     strings.TrimSpace(payload.Command),
		Status:      strings.TrimSpace(payload.Status),
		Running:     payload.Running,
		Error:       strings.TrimSpace(payload.Error),
		UnreadBytes: payload.UnreadBytes,
	}, nil
}

func backgroundAgentFromEvent(event ipc.StreamEvent) (backgroundAgentState, error) {
	if event.Type == ipc.EventBackgroundAgentDetail {
		payload, err := decodePayload[ipc.BackgroundAgentDetailPayload](event)
		if err != nil {
			return backgroundAgentState{}, err
		}
		return backgroundAgentState{
			ID:          strings.TrimSpace(payload.AgentID),
			Description: strings.TrimSpace(payload.Description),
			Status:      strings.TrimSpace(payload.Status),
			Error:       strings.TrimSpace(payload.Error),
			TotalUSD:    payload.TotalCostUSD,
		}, nil
	}
	payload, err := decodePayload[ipc.BackgroundAgentUpdatedPayload](event)
	if err != nil {
		return backgroundAgentState{}, err
	}
	return backgroundAgentState{
		ID:          strings.TrimSpace(payload.AgentID),
		Description: strings.TrimSpace(payload.Description),
		Status:      strings.TrimSpace(payload.Status),
		Error:       strings.TrimSpace(payload.Error),
		TotalUSD:    payload.TotalCostUSD,
	}, nil
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
		state.Transcript = append(state.Transcript, transcriptEntry{Kind: "assistant"})
	}
	state.Assistant += delta
	state.Transcript[len(state.Transcript)-1].Text = strings.TrimRight(state.Assistant, "\n")
	return state
}
