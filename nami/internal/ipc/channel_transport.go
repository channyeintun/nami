package ipc

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
)

// ChannelTransport carries IPC messages in memory for embedded engine use.
type ChannelTransport struct {
	messages <-chan ClientMessage
	events   chan<- StreamEvent
}

// NewChannelTransport creates a transport that reads client messages from
// messages and writes engine events to events.
func NewChannelTransport(messages <-chan ClientMessage, events chan<- StreamEvent) *ChannelTransport {
	return &ChannelTransport{
		messages: messages,
		events:   events,
	}
}

// ReadMessage blocks until the next client message arrives or ctx is canceled.
func (t *ChannelTransport) ReadMessage(ctx context.Context) (ClientMessage, error) {
	select {
	case <-ctx.Done():
		return ClientMessage{}, ctx.Err()
	case msg, ok := <-t.messages:
		if !ok {
			return ClientMessage{}, io.EOF
		}
		return msg, nil
	}
}

// EmitEvent sends a stream event to the event channel.
func (t *ChannelTransport) EmitEvent(event StreamEvent) (err error) {
	if t.events == nil {
		return fmt.Errorf("emit event %q: event channel is nil", event.Type)
	}
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("emit event %q: event channel is closed", event.Type)
		}
	}()
	t.events <- event
	return nil
}

// Emit is a convenience for emitting a typed payload.
func (t *ChannelTransport) Emit(eventType EventType, payload any) error {
	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = data
	}
	return t.EmitEvent(StreamEvent{
		Type:    eventType,
		Payload: raw,
	})
}

// EmitReady sends the ready event with protocol version and startup metadata.
func (t *ChannelTransport) EmitReady(slashCommands []SlashCommandDescriptorPayload) error {
	return t.Emit(EventReady, ReadyPayload{
		ProtocolVersion: ProtocolVersion,
		SlashCommands:   slashCommands,
	})
}

// EmitError sends an error event.
func (t *ChannelTransport) EmitError(message string, recoverable bool) error {
	return t.Emit(EventError, ErrorPayload{
		Message:     message,
		Recoverable: recoverable,
	})
}

// EmitNotice sends a non-error status notice.
func (t *ChannelTransport) EmitNotice(message string) error {
	return t.Emit(EventNotice, NoticePayload{Message: message})
}
