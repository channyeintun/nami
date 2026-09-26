package ipc

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sync"
)

const maxIPCMessageSize = 10 * 1024 * 1024

// MessageError is a problem with one client message — too large, or not
// valid JSON. The stream is framed by lines, so it stays in step past such a
// message: the caller can report the message and keep reading.
type MessageError struct {
	Err error
}

func (e *MessageError) Error() string { return e.Err.Error() }

func (e *MessageError) Unwrap() error { return e.Err }

// Bridge manages NDJSON communication between Go engine and Ink frontend.
type Bridge struct {
	reader  *bufio.Reader
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
	return &Bridge{
		reader: bufio.NewReaderSize(r, 64*1024),
		writer: w,
	}
}

// ReadMessage blocks until the next ClientMessage arrives or context is cancelled.
// It reuses a pending read goroutine if one is already blocked reading. A
// *MessageError reports one bad message; reading can continue after it.
func (b *Bridge) ReadMessage(ctx context.Context) (ClientMessage, error) {
	b.readMu.Lock()
	if b.readCh == nil {
		ch := make(chan readResult, 1)
		b.readCh = ch

		go func() {
			ch <- b.readNextMessage()
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

// readNextMessage reads the next non-blank line and decodes it.
func (b *Bridge) readNextMessage() readResult {
	for {
		line, err := readBoundedLine(b.reader, maxIPCMessageSize)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return readResult{err: io.EOF}
			}
			if _, ok := errors.AsType[*MessageError](err); ok {
				return readResult{err: err}
			}
			return readResult{err: fmt.Errorf("read error: %w", err)}
		}
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var msg ClientMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return readResult{err: &MessageError{Err: fmt.Errorf("invalid NDJSON: %w", err)}}
		}
		return readResult{msg: msg}
	}
}

// readBoundedLine returns the next line without its line ending. A line longer
// than limit is read to its end and discarded, and reported as a
// *MessageError, so the next read starts on the following line. A final line
// without a newline is returned as is; io.EOF means nothing was left.
func readBoundedLine(reader *bufio.Reader, limit int) ([]byte, error) {
	var line []byte
	tooLong := false
	for {
		chunk, err := reader.ReadSlice('\n')
		if !tooLong {
			if len(line)+len(chunk) > limit+2 { // room for "\r\n"
				tooLong = true
				line = nil
			} else {
				line = append(line, chunk...)
			}
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if tooLong {
			if err != nil && !errors.Is(err, io.EOF) {
				return nil, err
			}
			return nil, &MessageError{Err: fmt.Errorf("message exceeds the %d byte limit", limit)}
		}
		if err != nil && (!errors.Is(err, io.EOF) || len(line) == 0) {
			return nil, err
		}
		line = bytes.TrimSuffix(line, []byte("\n"))
		line = bytes.TrimSuffix(line, []byte("\r"))
		if len(line) > limit {
			return nil, &MessageError{Err: fmt.Errorf("message exceeds the %d byte limit", limit)}
		}
		return line, nil
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

// EmitNotice sends a non-error status notice.
func (b *Bridge) EmitNotice(message string) error {
	return b.Emit(EventNotice, NoticePayload{Message: message})
}
