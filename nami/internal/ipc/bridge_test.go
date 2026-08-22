package ipc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestEmitWritesOneNDJSONLine(t *testing.T) {
	var out bytes.Buffer
	bridge := NewBridge(strings.NewReader(""), &out)

	if err := bridge.Emit(EventTokenDelta, TokenDeltaPayload{Text: "hello"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	raw := out.String()
	if strings.Count(raw, "\n") != 1 || !strings.HasSuffix(raw, "\n") {
		t.Fatalf("expected exactly one newline-terminated line, got %q", raw)
	}

	var event StreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &event); err != nil {
		t.Fatalf("emitted line is not valid JSON: %v", err)
	}
	if event.Type != EventTokenDelta {
		t.Fatalf("type = %q, want %q", event.Type, EventTokenDelta)
	}

	var payload TokenDeltaPayload
	if err := json.Unmarshal(event.Payload, &payload); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if payload.Text != "hello" {
		t.Fatalf("payload = %+v", payload)
	}
}

func TestEmitEscapesNewlinesInPayload(t *testing.T) {
	// NDJSON framing breaks if a payload newline reaches the wire unescaped.
	var out bytes.Buffer
	bridge := NewBridge(strings.NewReader(""), &out)

	if err := bridge.Emit(EventTokenDelta, TokenDeltaPayload{Text: "line one\nline two"}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if strings.Count(out.String(), "\n") != 1 {
		t.Fatalf("payload newline broke the framing: %q", out.String())
	}
}

func TestEmitOmitsNilPayload(t *testing.T) {
	var out bytes.Buffer
	bridge := NewBridge(strings.NewReader(""), &out)

	if err := bridge.Emit(EventTurnComplete, nil); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	var event StreamEvent
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &event); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(event.Payload) != 0 {
		t.Fatalf("payload = %s, want omitted", event.Payload)
	}
}

func TestEmitIsSafeForConcurrentWriters(t *testing.T) {
	// Tool execution emits from multiple goroutines; interleaved writes would
	// corrupt the NDJSON stream.
	var out bytes.Buffer
	bridge := NewBridge(strings.NewReader(""), &out)

	var wg sync.WaitGroup
	for i := range 50 {
		wg.Go(func() {
			_ = bridge.Emit(EventTokenDelta, TokenDeltaPayload{Text: strings.Repeat("x", i+1)})
		})
	}
	wg.Wait()

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 50 {
		t.Fatalf("got %d lines, want 50", len(lines))
	}
	for i, line := range lines {
		var event StreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("line %d is not valid JSON (%v): %q", i, err, line)
		}
	}
}

func TestReadMessageDecodesNDJSON(t *testing.T) {
	input := `{"type":"user_input","payload":{"text":"hi"}}` + "\n"
	bridge := NewBridge(strings.NewReader(input), io.Discard)

	msg, err := bridge.ReadMessage(t.Context())
	if err != nil {
		t.Fatalf("ReadMessage: %v", err)
	}
	if msg.Type != MsgUserInput {
		t.Fatalf("type = %q, want %q", msg.Type, MsgUserInput)
	}
	if !strings.Contains(string(msg.Payload), `"hi"`) {
		t.Fatalf("payload = %s", msg.Payload)
	}
}

func TestReadMessageReturnsEOFWhenExhausted(t *testing.T) {
	bridge := NewBridge(strings.NewReader(""), io.Discard)
	if _, err := bridge.ReadMessage(t.Context()); !errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want io.EOF", err)
	}
}

func TestReadMessageRejectsMalformedJSON(t *testing.T) {
	bridge := NewBridge(strings.NewReader("{not json\n"), io.Discard)
	_, err := bridge.ReadMessage(t.Context())
	if err == nil {
		t.Fatal("expected an error for malformed NDJSON")
	}
	if errors.Is(err, io.EOF) {
		t.Fatalf("err = %v, want a parse error rather than EOF", err)
	}
}

func TestReadMessageHonoursContextCancellation(t *testing.T) {
	// A reader that never yields must not wedge the caller.
	reader, writer := io.Pipe()
	defer writer.Close()
	bridge := NewBridge(reader, io.Discard)

	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()

	if _, err := bridge.ReadMessage(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want context deadline exceeded", err)
	}
}

func TestReadMessageResumesAfterCancellation(t *testing.T) {
	// The pending read goroutine is reused, so a cancelled call must not drop
	// the message that arrives afterwards.
	reader, writer := io.Pipe()
	bridge := NewBridge(reader, io.Discard)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := bridge.ReadMessage(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("first read err = %v, want deadline exceeded", err)
	}

	go func() {
		_, _ = writer.Write([]byte(`{"type":"cancel"}` + "\n"))
	}()

	msg, err := bridge.ReadMessage(t.Context())
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if msg.Type != MsgCancel {
		t.Fatalf("type = %q, want %q", msg.Type, MsgCancel)
	}
}

func TestEmitErrorAndNoticeCarryTheirMessage(t *testing.T) {
	var out bytes.Buffer
	bridge := NewBridge(strings.NewReader(""), &out)

	if err := bridge.EmitError("boom", true); err != nil {
		t.Fatalf("EmitError: %v", err)
	}
	if err := bridge.EmitNotice("heads up"); err != nil {
		t.Fatalf("EmitNotice: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2", len(lines))
	}
	if !strings.Contains(lines[0], "boom") {
		t.Errorf("error line = %s", lines[0])
	}
	if !strings.Contains(lines[1], "heads up") {
		t.Errorf("notice line = %s", lines[1])
	}
}
