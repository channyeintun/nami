package engine

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"testing"
	"time"

	"github.com/channyeintun/nami/internal/ipc"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

// ipcHarness plays the client side of the IPC protocol: it feeds messages to
// the engine's router and collects the events the engine emits.
type ipcHarness struct {
	bridge *ipc.Bridge
	router *ipc.MessageRouter
	input  *io.PipeWriter
	events chan ipc.StreamEvent
}

func newIPCHarness(t *testing.T) *ipcHarness {
	t.Helper()
	inputReader, inputWriter := io.Pipe()
	outputReader, outputWriter := io.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	bridge := ipc.NewBridge(inputReader, outputWriter)
	harness := &ipcHarness{
		bridge: bridge,
		router: ipc.NewMessageRouter(ctx, bridge),
		input:  inputWriter,
		events: make(chan ipc.StreamEvent, 64),
	}
	go func() {
		defer close(harness.events)
		scanner := bufio.NewScanner(outputReader)
		for scanner.Scan() {
			var event ipc.StreamEvent
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				continue
			}
			harness.events <- event
		}
	}()
	t.Cleanup(func() {
		cancel()
		_ = inputWriter.Close()
		_ = outputWriter.Close()
	})
	return harness
}

func (h *ipcHarness) send(t *testing.T, msgType ipc.ClientMessageType, payload any) {
	t.Helper()
	data, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s payload: %v", msgType, err)
	}
	line, err := json.Marshal(ipc.ClientMessage{Type: msgType, Payload: data})
	if err != nil {
		t.Fatalf("marshal %s message: %v", msgType, err)
	}
	if _, err := h.input.Write(append(line, '\n')); err != nil {
		t.Fatalf("send %s: %v", msgType, err)
	}
}

// waitForEvent returns the next emitted event of the given type, skipping
// any others.
func (h *ipcHarness) waitForEvent(t *testing.T, eventType ipc.EventType) ipc.StreamEvent {
	t.Helper()
	timeout := time.After(5 * time.Second)
	for {
		select {
		case event, ok := <-h.events:
			if !ok {
				t.Fatalf("event stream closed before %s", eventType)
			}
			if event.Type == eventType {
				return event
			}
		case <-timeout:
			t.Fatalf("timed out waiting for %s", eventType)
		}
	}
}

// Messages that arrive while a permission prompt is open are not the prompt's
// to consume. They must reach the next reader of the router - otherwise a
// background-agent stop pressed just before a prompt appeared is lost.
func TestWaitForPermissionDecisionRequeuesUnrelatedMessages(t *testing.T) {
	harness := newIPCHarness(t)
	pending := toolpkg.PendingCall{
		Tool:  &fakeTool{name: "fake_write", permission: toolpkg.PermissionWrite},
		Input: toolpkg.ToolInput{Name: "fake_write", Params: map[string]any{}, Raw: "{}"},
	}

	type outcome struct {
		response permissionResponse
		err      error
	}
	done := make(chan outcome, 1)
	go func() {
		response, err := waitForPermissionDecision(t.Context(), harness.bridge, harness.router, "call-1", pending)
		done <- outcome{response: response, err: err}
	}()

	var request ipc.PermissionRequestPayload
	if err := json.Unmarshal(harness.waitForEvent(t, ipc.EventPermissionRequest).Payload, &request); err != nil {
		t.Fatalf("decode permission request: %v", err)
	}

	harness.send(t, ipc.MsgBackgroundAgentStop, ipc.BackgroundAgentStopPayload{AgentID: "agent_1"})
	harness.send(t, ipc.MsgPermissionResponse, ipc.PermissionResponsePayload{RequestID: "perm-stale", Decision: "deny"})
	harness.send(t, ipc.MsgPermissionResponse, ipc.PermissionResponsePayload{RequestID: request.RequestID, Decision: "allow"})

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("waitForPermissionDecision: %v", got.err)
		}
		if got.response.Decision != "allow" {
			t.Fatalf("decision = %q, want the answer to this prompt, not the stale one", got.response.Decision)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("waitForPermissionDecision did not return")
	}

	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	msg, err := harness.router.Next(ctx)
	if err != nil {
		t.Fatalf("the background agent stop was not handed back to the router: %v", err)
	}
	if msg.Type != ipc.MsgBackgroundAgentStop {
		t.Fatalf("next message = %s, want %s", msg.Type, ipc.MsgBackgroundAgentStop)
	}
}
