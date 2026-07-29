package ipc

import (
	"context"
	"encoding/json"
	"io"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func userInput(text string) ClientMessage {
	payload, err := json.Marshal(UserInputPayload{Text: text})
	if err != nil {
		panic(err)
	}
	return ClientMessage{Type: MsgUserInput, Payload: payload}
}

func payloadText(t *testing.T, message ClientMessage) string {
	t.Helper()
	var payload UserInputPayload
	if err := json.Unmarshal(message.Payload, &payload); err != nil {
		t.Fatalf("decode user input payload: %v", err)
	}
	return payload.Text
}

func encodeMessages(messages ...ClientMessage) string {
	var builder strings.Builder
	for _, message := range messages {
		data, err := json.Marshal(message)
		if err != nil {
			panic(err)
		}
		builder.Write(data)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func newTestRouter(t *testing.T, input string) (*MessageRouter, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return NewMessageRouter(ctx, NewBridge(strings.NewReader(input), io.Discard)), cancel
}

func TestRouterDeliversMessagesInOrder(t *testing.T) {
	router, _ := newTestRouter(t, encodeMessages(
		userInput("first"),
		userInput("second"),
	))

	ctx := context.Background()
	for _, want := range []string{"first", "second"} {
		msg, err := router.Next(ctx)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if payloadText(t, msg) != want {
			t.Fatalf("payload = %q, want %q", payloadText(t, msg), want)
		}
	}
}

func TestRouterReturnsBridgeErrorAfterEOF(t *testing.T) {
	router, _ := newTestRouter(t, "")
	if _, err := router.Next(context.Background()); err == nil {
		t.Fatal("Next returned no error after the bridge closed")
	}
	// Later calls keep reporting the same shutdown reason.
	if _, err := router.Next(context.Background()); err == nil {
		t.Fatal("second Next returned no error after the bridge closed")
	}
}

func TestRouterRunsCancelFuncOnCancelMessage(t *testing.T) {
	router, _ := newTestRouter(t, encodeMessages(
		ClientMessage{Type: MsgCancel},
		userInput("after cancel"),
	))

	var cancelled atomic.Bool
	router.SetCancelFunc(func() { cancelled.Store(true) })

	msg, err := router.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if msg.Type == MsgCancel {
		t.Fatal("cancel messages must not be delivered to callers")
	}
	if payloadText(t, msg) != "after cancel" {
		t.Fatalf("payload = %q, want %q", payloadText(t, msg), "after cancel")
	}
	if !cancelled.Load() {
		t.Fatal("cancel function was not invoked")
	}
}

func TestRouterIgnoresCancelWithoutRegisteredFunc(t *testing.T) {
	router, _ := newTestRouter(t, encodeMessages(
		ClientMessage{Type: MsgCancel},
		userInput("still here"),
	))

	msg, err := router.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if payloadText(t, msg) != "still here" {
		t.Fatalf("payload = %q, want the message after the stale cancel", payloadText(t, msg))
	}
}

func TestRouterRequeuePrependsMessages(t *testing.T) {
	router, _ := newTestRouter(t, encodeMessages(userInput("from bridge")))

	router.Requeue(
		userInput("requeued one"),
		userInput("requeued two"),
	)

	ctx := context.Background()
	for _, want := range []string{"requeued one", "requeued two", "from bridge"} {
		msg, err := router.Next(ctx)
		if err != nil {
			t.Fatalf("Next: %v", err)
		}
		if payloadText(t, msg) != want {
			t.Fatalf("payload = %q, want %q", payloadText(t, msg), want)
		}
	}
}

func TestRouterRequeueIgnoresEmptyInput(t *testing.T) {
	router, _ := newTestRouter(t, encodeMessages(userInput("only")))
	router.Requeue()
	msg, err := router.Next(context.Background())
	if err != nil {
		t.Fatalf("Next: %v", err)
	}
	if payloadText(t, msg) != "only" {
		t.Fatalf("payload = %q, want %q", payloadText(t, msg), "only")
	}
}

func TestRouterNextHonoursCallerContext(t *testing.T) {
	// A reader that never produces a line keeps the router waiting.
	blocking, writer := io.Pipe()
	t.Cleanup(func() { _ = writer.Close() })
	router := NewMessageRouter(context.Background(), NewBridge(blocking, io.Discard))

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := router.Next(ctx); err == nil {
		t.Fatal("Next ignored the caller's context deadline")
	}
}

// The reader goroutine used to block forever on a full channel once nothing was
// consuming it, keeping the process alive after shutdown.
func TestRouterReaderGoroutineExitsWhenContextIsCancelled(t *testing.T) {
	messages := make([]ClientMessage, 64)
	for i := range messages {
		messages[i] = userInput("unread")
	}

	before := runtime.NumGoroutine()
	ctx, cancel := context.WithCancel(context.Background())
	router := NewMessageRouter(ctx, NewBridge(strings.NewReader(encodeMessages(messages...)), io.Discard))

	// Fill the buffer without draining it, then shut the router down.
	if _, err := router.Next(context.Background()); err != nil {
		t.Fatalf("Next: %v", err)
	}
	cancel()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if runtime.NumGoroutine() <= before {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("reader goroutine still running: %d goroutines, started from %d", runtime.NumGoroutine(), before)
}
