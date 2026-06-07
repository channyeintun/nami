package ipc

import "context"

// MessageSource provides client messages to the engine.
type MessageSource interface {
	ReadMessage(ctx context.Context) (ClientMessage, error)
}

// EventSink receives engine stream events.
type EventSink interface {
	EmitEvent(event StreamEvent) error
	Emit(eventType EventType, payload any) error
	EmitReady(slashCommands []SlashCommandDescriptorPayload) error
	EmitError(message string, recoverable bool) error
	EmitNotice(message string) error
}
