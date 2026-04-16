package engine

import (
	"fmt"
	"strings"

	"github.com/channyeintun/chan/internal/api"
	"github.com/channyeintun/chan/internal/compact"
	"github.com/channyeintun/chan/internal/ipc"
	"github.com/channyeintun/chan/internal/session"
)

func emitConversationHydrated(bridge *ipc.Bridge, store *session.Store, sessionID string, messages []api.Message, model string) error {
	if bridge == nil {
		return nil
	}
	payload, err := loadConversationHydratedPayload(store, sessionID, messages, model)
	if err != nil {
		return err
	}
	return bridge.Emit(ipc.EventConversationHydrated, payload)
}

func loadConversationHydratedPayload(
	store *session.Store,
	sessionID string,
	messages []api.Message,
	model string,
) (ipc.ConversationHydratedPayload, error) {
	if store != nil && strings.TrimSpace(sessionID) != "" {
		payload, err := store.LoadConversationTimeline(sessionID)
		if err != nil {
			return ipc.ConversationHydratedPayload{}, err
		}
		if conversationHydratedPayloadHasContent(payload) {
			return payload, nil
		}
	}
	return buildConversationHydratedPayload(messages, model), nil
}

func persistConversationHydratedPayload(
	store *session.Store,
	sessionID string,
	timeline *conversationTimeline,
	messages []api.Message,
	model string,
) error {
	if store == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	payload := buildConversationHydratedPayload(messages, model)
	if timeline != nil {
		payload = timeline.HydratedPayload(messages, model)
	}
	return store.SaveConversationTimeline(sessionID, payload)
}

func conversationHydratedPayloadHasContent(payload ipc.ConversationHydratedPayload) bool {
	return len(payload.Messages) > 0 || len(payload.Progress) > 0 || len(payload.ToolCalls) > 0 || len(payload.Transcript) > 0
}

func buildConversationHydratedPayload(messages []api.Message, model string) ipc.ConversationHydratedPayload {
	payload := ipc.ConversationHydratedPayload{
		Messages:   make([]ipc.ConversationHydratedMessagePayload, 0, len(messages)),
		Progress:   nil,
		ToolCalls:  make([]ipc.ConversationHydratedToolCallPayload, 0, len(messages)),
		Transcript: make([]ipc.ConversationHydratedTranscriptEntryPayload, 0, len(messages)),
	}
	toolIndexes := make(map[string]int, len(messages))

	for index, message := range messages {
		messageID := fmt.Sprintf("history-msg-%d", index)

		switch message.Role {
		case api.RoleUser:
			text := hydratedUserText(message)
			if text == "" {
				continue
			}
			payload.Messages = append(payload.Messages, ipc.ConversationHydratedMessagePayload{
				ID:   messageID,
				Role: "user",
				Text: text,
			})
			payload.Transcript = append(payload.Transcript, ipc.ConversationHydratedTranscriptEntryPayload{
				ID:    messageID,
				Kind:  "message",
				RefID: messageID,
			})
		case api.RoleAssistant:
			blocks := hydratedAssistantBlocks(message)
			if len(blocks) > 0 {
				payload.Messages = append(payload.Messages, ipc.ConversationHydratedMessagePayload{
					ID:     messageID,
					Role:   "assistant",
					Blocks: blocks,
					Model:  strings.TrimSpace(model),
				})
				payload.Transcript = append(payload.Transcript, ipc.ConversationHydratedTranscriptEntryPayload{
					ID:    messageID,
					Kind:  "message",
					RefID: messageID,
				})
			}
			for toolIndex, call := range message.ToolCalls {
				toolID := hydratedToolID(index, toolIndex, call.ID)
				if trimmedID := strings.TrimSpace(call.ID); trimmedID != "" {
					toolIndexes[trimmedID] = len(payload.ToolCalls)
				}
				payload.ToolCalls = append(payload.ToolCalls, ipc.ConversationHydratedToolCallPayload{
					ID:     toolID,
					Name:   strings.TrimSpace(call.Name),
					Input:  call.Input,
					Status: "completed",
				})
				payload.Transcript = append(payload.Transcript, ipc.ConversationHydratedTranscriptEntryPayload{
					ID:    toolID,
					Kind:  "tool_call",
					RefID: toolID,
				})
			}
		case api.RoleSystem:
			text := strings.TrimSpace(message.Content)
			if text == "" {
				continue
			}
			payload.Messages = append(payload.Messages, ipc.ConversationHydratedMessagePayload{
				ID:   messageID,
				Role: "system",
				Text: text,
				Tone: hydratedSystemTone(message),
			})
			payload.Transcript = append(payload.Transcript, ipc.ConversationHydratedTranscriptEntryPayload{
				ID:    messageID,
				Kind:  "message",
				RefID: messageID,
			})
		}

		if message.ToolResult != nil {
			applyHydratedToolResult(&payload, toolIndexes, index, *message.ToolResult)
		}
	}

	return payload
}

func hydratedUserText(message api.Message) string {
	content := strings.TrimSpace(message.Content)
	switch len(message.Images) {
	case 0:
		return content
	case 1:
		if content == "" {
			return "[image attachment]"
		}
		return content + "\n\n[image attachment]"
	default:
		attachmentText := fmt.Sprintf("[%d image attachments]", len(message.Images))
		if content == "" {
			return attachmentText
		}
		return content + "\n\n" + attachmentText
	}
}

func hydratedAssistantBlocks(message api.Message) []ipc.ConversationHydratedMessageBlockPayload {
	blocks := make([]ipc.ConversationHydratedMessageBlockPayload, 0, 2)
	if reasoning := strings.TrimSpace(message.ReasoningContent); reasoning != "" {
		blocks = append(blocks, ipc.ConversationHydratedMessageBlockPayload{
			Kind: "thinking",
			Text: reasoning,
		})
	}
	if text := strings.TrimSpace(message.Content); text != "" {
		blocks = append(blocks, ipc.ConversationHydratedMessageBlockPayload{
			Kind: "text",
			Text: text,
		})
	}
	return blocks
}

func hydratedSystemTone(message api.Message) string {
	if compact.IsSummaryMessage(message) {
		return "info"
	}
	return "info"
}

func hydratedToolID(messageIndex, toolIndex int, existing string) string {
	if trimmed := strings.TrimSpace(existing); trimmed != "" {
		return trimmed
	}
	return fmt.Sprintf("history-tool-%d-%d", messageIndex, toolIndex)
}

func applyHydratedToolResult(
	payload *ipc.ConversationHydratedPayload,
	toolIndexes map[string]int,
	messageIndex int,
	result api.ToolResult,
) {
	toolID := strings.TrimSpace(result.ToolCallID)
	toolIndex, found := toolIndexes[toolID]
	if !found {
		fallbackID := hydratedToolID(messageIndex, 0, toolID)
		toolIndex = len(payload.ToolCalls)
		payload.ToolCalls = append(payload.ToolCalls, ipc.ConversationHydratedToolCallPayload{
			ID:     fallbackID,
			Name:   "tool",
			Status: "completed",
		})
		payload.Transcript = append(payload.Transcript, ipc.ConversationHydratedTranscriptEntryPayload{
			ID:    fallbackID,
			Kind:  "tool_call",
			RefID: fallbackID,
		})
		if toolID != "" {
			toolIndexes[toolID] = toolIndex
		}
	}

	status := "completed"
	if result.IsError {
		status = "error"
	}
	payload.ToolCalls[toolIndex].Status = status
	payload.ToolCalls[toolIndex].Output = strings.TrimSpace(result.Output)
	payload.ToolCalls[toolIndex].Error = hydratedToolError(result)
	payload.ToolCalls[toolIndex].FilePath = strings.TrimSpace(result.FilePath)
}

func hydratedToolError(result api.ToolResult) string {
	if !result.IsError {
		return ""
	}
	return strings.TrimSpace(result.Output)
}
