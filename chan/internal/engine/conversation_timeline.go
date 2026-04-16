package engine

import (
	"fmt"
	"strings"

	"github.com/channyeintun/chan/internal/api"
	"github.com/channyeintun/chan/internal/ipc"
)

type conversationTimeline struct {
	progress                 []ipc.ConversationHydratedProgressPayload
	progressIndex            map[string]int
	transcript               []ipc.ConversationHydratedTranscriptEntryPayload
	transcriptIndex          map[string]int
	pendingAssistantMessages []string
	pendingAssistantIndex    map[string]struct{}
	syncedMessageCount       int
}

func newConversationTimeline() *conversationTimeline {
	return &conversationTimeline{
		progress:                 make([]ipc.ConversationHydratedProgressPayload, 0, 8),
		progressIndex:            make(map[string]int),
		transcript:               make([]ipc.ConversationHydratedTranscriptEntryPayload, 0, 32),
		transcriptIndex:          make(map[string]int),
		pendingAssistantMessages: make([]string, 0, 8),
		pendingAssistantIndex:    make(map[string]struct{}),
	}
}

func newConversationTimelineFromHydrated(
	payload ipc.ConversationHydratedPayload,
	messages []api.Message,
) *conversationTimeline {
	timeline := newConversationTimeline()
	for _, progress := range payload.Progress {
		timeline.recordProgress(progress)
	}
	for _, entry := range payload.Transcript {
		timeline.appendTranscriptEntry(entry)
	}
	timeline.syncedMessageCount = len(messages)
	return timeline
}

func rebuildConversationTimeline(messages []api.Message) *conversationTimeline {
	timeline := newConversationTimeline()
	timeline.SyncMessages(messages)
	timeline.FlushPendingAssistantMessages()
	return timeline
}

func (t *conversationTimeline) RecordProgress(payload ipc.ProgressPayload) {
	if t == nil {
		return
	}
	id := strings.TrimSpace(payload.ID)
	message := strings.TrimSpace(payload.Message)
	if id == "" || message == "" {
		return
	}
	t.recordProgress(ipc.ConversationHydratedProgressPayload{
		ID:      id,
		Message: message,
	})
	t.appendTranscriptEntry(ipc.ConversationHydratedTranscriptEntryPayload{
		ID:    id,
		Kind:  "progress",
		RefID: id,
	})
}

func (t *conversationTimeline) RecordToolStart(payload ipc.ToolStartPayload) {
	if t == nil {
		return
	}
	toolID := strings.TrimSpace(payload.ToolID)
	if toolID == "" {
		return
	}
	t.appendTranscriptEntry(ipc.ConversationHydratedTranscriptEntryPayload{
		ID:    toolID,
		Kind:  "tool_call",
		RefID: toolID,
	})
}

func (t *conversationTimeline) SyncMessages(messages []api.Message) {
	if t == nil {
		return
	}
	for index := t.syncedMessageCount; index < len(messages); index++ {
		message := messages[index]
		messageID := conversationTimelineMessageID(index)

		switch message.Role {
		case api.RoleUser:
			if hydratedUserText(message) == "" {
				continue
			}
			t.appendTranscriptEntry(ipc.ConversationHydratedTranscriptEntryPayload{
				ID:    messageID,
				Kind:  "message",
				RefID: messageID,
			})
		case api.RoleSystem:
			if strings.TrimSpace(message.Content) == "" {
				continue
			}
			t.appendTranscriptEntry(ipc.ConversationHydratedTranscriptEntryPayload{
				ID:    messageID,
				Kind:  "message",
				RefID: messageID,
			})
		case api.RoleAssistant:
			for toolIndex, call := range message.ToolCalls {
				toolID := hydratedToolID(index, toolIndex, call.ID)
				t.appendTranscriptEntry(ipc.ConversationHydratedTranscriptEntryPayload{
					ID:    toolID,
					Kind:  "tool_call",
					RefID: toolID,
				})
			}
			if len(hydratedAssistantBlocks(message)) == 0 {
				continue
			}
			if len(message.ToolCalls) > 0 {
				t.queuePendingAssistantMessage(messageID)
				continue
			}
			t.appendTranscriptEntry(ipc.ConversationHydratedTranscriptEntryPayload{
				ID:    messageID,
				Kind:  "message",
				RefID: messageID,
			})
		}
	}
	t.syncedMessageCount = len(messages)
}

func (t *conversationTimeline) FlushPendingAssistantMessages() {
	if t == nil || len(t.pendingAssistantMessages) == 0 {
		return
	}
	for _, messageID := range t.pendingAssistantMessages {
		t.appendTranscriptEntry(ipc.ConversationHydratedTranscriptEntryPayload{
			ID:    messageID,
			Kind:  "message",
			RefID: messageID,
		})
	}
	t.pendingAssistantMessages = t.pendingAssistantMessages[:0]
	t.pendingAssistantIndex = make(map[string]struct{})
}

func (t *conversationTimeline) HydratedPayload(
	messages []api.Message,
	model string,
) ipc.ConversationHydratedPayload {
	payload := buildConversationHydratedPayload(messages, model)
	if len(t.progress) > 0 {
		payload.Progress = append([]ipc.ConversationHydratedProgressPayload(nil), t.progress...)
	}
	if len(t.transcript) > 0 {
		payload.Transcript = append([]ipc.ConversationHydratedTranscriptEntryPayload(nil), t.transcript...)
	}
	return payload
}

func (t *conversationTimeline) queuePendingAssistantMessage(messageID string) {
	trimmedID := strings.TrimSpace(messageID)
	if trimmedID == "" {
		return
	}
	if _, exists := t.pendingAssistantIndex[trimmedID]; exists {
		return
	}
	t.pendingAssistantIndex[trimmedID] = struct{}{}
	t.pendingAssistantMessages = append(t.pendingAssistantMessages, trimmedID)
}

func (t *conversationTimeline) recordProgress(
	progress ipc.ConversationHydratedProgressPayload,
) {
	id := strings.TrimSpace(progress.ID)
	message := strings.TrimSpace(progress.Message)
	if id == "" || message == "" {
		return
	}
	if index, exists := t.progressIndex[id]; exists {
		t.progress[index] = ipc.ConversationHydratedProgressPayload{
			ID:      id,
			Message: message,
		}
		return
	}
	t.progressIndex[id] = len(t.progress)
	t.progress = append(t.progress, ipc.ConversationHydratedProgressPayload{
		ID:      id,
		Message: message,
	})
}

func (t *conversationTimeline) appendTranscriptEntry(
	entry ipc.ConversationHydratedTranscriptEntryPayload,
) {
	id := strings.TrimSpace(entry.ID)
	kind := strings.TrimSpace(entry.Kind)
	if id == "" || kind == "" {
		return
	}
	entry.ID = id
	entry.Kind = kind
	if strings.TrimSpace(entry.RefID) == "" {
		entry.RefID = id
	}
	key := fmt.Sprintf("%s:%s", kind, id)
	if _, exists := t.transcriptIndex[key]; exists {
		return
	}
	t.transcriptIndex[key] = len(t.transcript)
	t.transcript = append(t.transcript, entry)
}

func conversationTimelineMessageID(index int) string {
	return fmt.Sprintf("history-msg-%d", index)
}
