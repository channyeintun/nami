package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/channyeintun/nami/internal/ipc"
)

func yieldEvent(yield func(ipc.StreamEvent, error) bool, eventType ipc.EventType, payload any) error {
	event, err := newEvent(eventType, payload)
	if err != nil {
		return err
	}
	if !yield(event, nil) {
		return context.Canceled
	}
	return nil
}

func newEvent(eventType ipc.EventType, payload any) (ipc.StreamEvent, error) {
	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return ipc.StreamEvent{}, fmt.Errorf("marshal %s event payload: %w", eventType, err)
		}
		raw = data
	}
	return ipc.StreamEvent{
		Type:    eventType,
		Payload: raw,
	}, nil
}

func emitNoticeTelemetry(emit func(ipc.StreamEvent) error, message string) error {
	if emit == nil || strings.TrimSpace(message) == "" {
		return nil
	}
	event, err := newEvent(ipc.EventNotice, ipc.NoticePayload{Message: message})
	if err != nil {
		return err
	}
	return emit(event)
}

// emitSkippedMemoryFilesNotice tells the user which instruction files were
// left out of the system prompt and why, so an AGENTS.md that is not being
// followed says so instead of being dropped without a word.
func emitSkippedMemoryFilesNotice(emit func(ipc.StreamEvent) error, skipped []SkippedMemoryFile) error {
	if len(skipped) == 0 {
		return nil
	}
	files := make([]string, 0, len(skipped))
	for _, file := range skipped {
		files = append(files, fmt.Sprintf("%s (%s)", file.Path, file.Reason))
	}
	return emitNoticeTelemetry(emit, "Ignored instruction files another user could have written: "+strings.Join(files, "; ")+".")
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

	event, err := newEvent(ipc.EventMemoryRecalled, payload)
	if err != nil {
		return err
	}
	return emit(event)
}

func emitRetrievalTelemetry(emit func(ipc.StreamEvent) error, meta retrievalMeta) error {
	if emit == nil {
		return nil
	}
	event, err := newEvent(ipc.EventRetrievalUsed, ipc.RetrievalUsedPayload{
		SnippetCount:  meta.SnippetCount,
		TokensUsed:    meta.TokensUsed,
		AnchorCount:   meta.AnchorCount,
		EdgesExpanded: meta.EdgesExpanded,
		Skipped:       meta.Skipped,
	})
	if err != nil {
		return err
	}
	return emit(event)
}

func emitAttemptLogTelemetry(emit func(ipc.StreamEvent) error, entries []AttemptEntry, section string) error {
	if emit == nil {
		return nil
	}
	event, err := newEvent(ipc.EventAttemptLogSurfaced, ipc.AttemptLogSurfacedPayload{
		EntryCount: len(entries),
		TokensUsed: len(section) / 4,
		Injected:   section != "",
	})
	if err != nil {
		return err
	}
	return emit(event)
}

func emitAttemptRepeatedTelemetry(emit func(ipc.StreamEvent) error, repeatedCount int) error {
	if emit == nil || repeatedCount <= 0 {
		return nil
	}
	event, err := newEvent(ipc.EventAttemptRepeated, ipc.AttemptRepeatedPayload{
		RepeatedCount: repeatedCount,
	})
	if err != nil {
		return err
	}
	return emit(event)
}
