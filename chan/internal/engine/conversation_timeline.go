package engine

import (
	"fmt"
	"strings"

	"github.com/channyeintun/chan/internal/api"
	"github.com/channyeintun/chan/internal/ipc"
)

type conversationTimeline struct {
	progress           []ipc.ConversationHydratedProgressPayload
	progressIndex      map[string]int
	transcript         []ipc.ConversationHydratedTranscriptEntryPayload
	transcriptIndex    map[string]struct{}
	syncedMessageCount int
}

func newConversationTimeline() *conversationTimeline {
	return &conversationTimeline{
		progress:        make([]ipc.ConversationHydratedProgressPayload, 0, 8),
		progressIndex:   make(map[string]int),
		transcript:      make([]ipc.ConversationHydratedTranscriptEntryPayload, 0, 32),
		transcriptIndex: make(map[string]struct{}),
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

func (t *conversationTimeline) RecordAssistantMessage(messageIndex int) {
	if t == nil || messageIndex < 0 {
		return
	}
	messageID := conversationTimelineMessageID(messageIndex)
	t.appendTranscriptEntry(ipc.ConversationHydratedTranscriptEntryPayload{
		ID:    messageID,
		Kind:  "message",
		RefID: messageID,
	})
}

func (t *conversationTimeline) RecordUserMessage(messageIndex int) {
	if t == nil || messageIndex < 0 {
		return
	}
	messageID := conversationTimelineMessageID(messageIndex)
	t.appendTranscriptEntry(ipc.ConversationHydratedTranscriptEntryPayload{
		ID:    messageID,
		Kind:  "message",
		RefID: messageID,
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
			toolIDs := make([]string, 0, len(message.ToolCalls))
			for toolIndex, call := range message.ToolCalls {
				toolID := hydratedToolID(index, toolIndex, call.ID)
				toolIDs = append(toolIDs, toolID)
			}
			if len(hydratedAssistantBlocks(message)) > 0 {
				t.appendTranscriptEntry(ipc.ConversationHydratedTranscriptEntryPayload{
					ID:    messageID,
					Kind:  "message",
					RefID: messageID,
				})
			}
			for _, toolID := range toolIDs {
				t.appendTranscriptEntry(ipc.ConversationHydratedTranscriptEntryPayload{
					ID:    toolID,
					Kind:  "tool_call",
					RefID: toolID,
				})
			}
		}
	}
	t.syncedMessageCount = len(messages)
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

func trimConversationTimelineToMessage(
	current *conversationTimeline,
	messageID string,
	messages []api.Message,
) *conversationTimeline {
	trimmedMessageID := strings.TrimSpace(messageID)
	if current == nil || trimmedMessageID == "" || len(current.transcript) == 0 {
		return rebuildConversationTimeline(messages)
	}

	progressByID := make(map[string]ipc.ConversationHydratedProgressPayload, len(current.progress))
	for _, progress := range current.progress {
		progressID := strings.TrimSpace(progress.ID)
		if progressID == "" {
			continue
		}
		progressByID[progressID] = progress
	}

	next := newConversationTimeline()
	found := false
	for _, entry := range current.transcript {
		if strings.TrimSpace(entry.Kind) == "progress" {
			progress, ok := progressByID[transcriptEntryRefID(entry)]
			if !ok {
				continue
			}
			next.recordProgress(progress)
		}
		next.appendTranscriptEntry(entry)
		if strings.TrimSpace(entry.Kind) == "message" && transcriptEntryRefID(entry) == trimmedMessageID {
			found = true
			break
		}
	}
	if !found {
		return rebuildConversationTimeline(messages)
	}
	next.syncedMessageCount = len(messages)
	return next
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
	normalizedEntry, key, ok := normalizeTranscriptEntry(entry)
	if !ok {
		return
	}
	if _, exists := t.transcriptIndex[key]; exists {
		return
	}
	t.transcriptIndex[key] = struct{}{}
	t.transcript = append(t.transcript, normalizedEntry)
}

func normalizeTranscriptEntry(
	entry ipc.ConversationHydratedTranscriptEntryPayload,
) (ipc.ConversationHydratedTranscriptEntryPayload, string, bool) {
	id := strings.TrimSpace(entry.ID)
	kind := strings.TrimSpace(entry.Kind)
	if id == "" || kind == "" {
		return ipc.ConversationHydratedTranscriptEntryPayload{}, "", false
	}
	entry.ID = id
	entry.Kind = kind
	if strings.TrimSpace(entry.RefID) == "" {
		entry.RefID = id
	} else {
		entry.RefID = strings.TrimSpace(entry.RefID)
	}
	return entry, fmt.Sprintf("%s:%s", kind, id), true
}

func transcriptEntryRefID(entry ipc.ConversationHydratedTranscriptEntryPayload) string {
	if refID := strings.TrimSpace(entry.RefID); refID != "" {
		return refID
	}
	return strings.TrimSpace(entry.ID)
}

func conversationTimelineMessageID(index int) string {
	return fmt.Sprintf("history-msg-%d", index)
}
