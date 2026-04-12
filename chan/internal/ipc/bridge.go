package ipc

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"
)

const maxIPCMessageSize = 10 * 1024 * 1024

// Bridge manages NDJSON communication between Go engine and Ink frontend.
type Bridge struct {
	reader  *bufio.Scanner
	writer  io.Writer
	writeMu sync.Mutex

	readMu sync.Mutex
	readCh chan readResult
}

type readResult struct {
	msg ClientMessage
	err error
}

// NewBridge creates a Bridge reading from r and writing to w.
func NewBridge(r io.Reader, w io.Writer) *Bridge {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 1024*1024), maxIPCMessageSize)
	return &Bridge{
		reader: scanner,
		writer: w,
	}
}

// ReadMessage blocks until the next ClientMessage arrives or context is cancelled.
// It reuses a pending read goroutine if one is already blocked on Scan().
func (b *Bridge) ReadMessage(ctx context.Context) (ClientMessage, error) {
	b.readMu.Lock()
	if b.readCh == nil {
		ch := make(chan readResult, 1)
		b.readCh = ch

		go func() {
			if b.reader.Scan() {
				var msg ClientMessage
				if err := json.Unmarshal(b.reader.Bytes(), &msg); err != nil {
					ch <- readResult{err: fmt.Errorf("invalid NDJSON: %w", err)}
					return
				}
				ch <- readResult{msg: msg}
				return
			}
			if err := b.reader.Err(); err != nil {
				ch <- readResult{err: fmt.Errorf("read error: %w", err)}
			} else {
				ch <- readResult{err: io.EOF}
			}
		}()
	}
	ch := b.readCh
	b.readMu.Unlock()

	select {
	case <-ctx.Done():
		return ClientMessage{}, ctx.Err()
	case r := <-ch:
		b.readMu.Lock()
		b.readCh = nil
		b.readMu.Unlock()
		return r.msg, r.err
	}
}

// EmitEvent writes a StreamEvent as one NDJSON line to stdout.
func (b *Bridge) EmitEvent(event StreamEvent) error {
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}
	b.writeMu.Lock()
	defer b.writeMu.Unlock()
	_, err = fmt.Fprintf(b.writer, "%s\n", data)
	return err
}

// Emit is a convenience for emitting a typed payload.
func (b *Bridge) Emit(eventType EventType, payload any) error {
	var raw json.RawMessage
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		raw = data
	}
	return b.EmitEvent(StreamEvent{
		Type:    eventType,
		Payload: raw,
	})
}

// EmitReady sends the ready event with protocol version and startup metadata.
func (b *Bridge) EmitReady(slashCommands []SlashCommandDescriptorPayload) error {
	return b.Emit(EventReady, ReadyPayload{
		ProtocolVersion: ProtocolVersion,
		SlashCommands:   slashCommands,
	})
}

// EmitError sends an error event.
func (b *Bridge) EmitError(message string, recoverable bool) error {
	return b.Emit(EventError, ErrorPayload{
		Message:     message,
		Recoverable: recoverable,
	})
}
