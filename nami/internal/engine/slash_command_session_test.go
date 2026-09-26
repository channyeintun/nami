package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/config"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/session"
)

// isolateUserConfig points the user config and home directories at empty
// temporary ones so a handler that loads config reads defaults only.
func isolateUserConfig(t *testing.T) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("NAMI_API_KEY", "")
}

// newTestSlashCommandContext builds a command context whose emitted events are
// written to the returned buffer.
func newTestSlashCommandContext(t *testing.T, store *session.Store, messages []api.Message, timeline *conversationTimeline) (*slashCommandContext, *bytes.Buffer) {
	t.Helper()
	var client api.LLMClient
	var emitted bytes.Buffer
	return newSlashCommandContext(
		t.Context(),
		ipc.NewBridge(strings.NewReader(""), &emitted),
		nil,
		store,
		nil,
		config.DefaultConfig(),
		nil,
		nil,
		costpkg.NewTracker(),
		ipc.SlashCommandPayload{},
		"old-session",
		time.Now(),
		agent.ModeFast,
		"anthropic/claude-sonnet-5",
		"",
		t.TempDir(),
		messages,
		timeline,
		nil,
		&client,
	), &emitted
}

// pickerHarness plays the TUI's side of a picker a slash command opens.
type pickerHarness struct {
	input  *io.PipeWriter
	output *lockedBuffer
}

// newPickerSlashCommandContext is newTestSlashCommandContext with a message
// router, so a handler can wait on a picker that the test answers.
func newPickerSlashCommandContext(t *testing.T, store *session.Store, messages []api.Message, timeline *conversationTimeline) (*slashCommandContext, *pickerHarness) {
	t.Helper()
	inputReader, inputWriter := io.Pipe()
	t.Cleanup(func() { _ = inputWriter.Close() })
	output := &lockedBuffer{}
	bridge := ipc.NewBridge(inputReader, output)
	routerCtx, cancelRouter := context.WithCancel(context.Background())
	t.Cleanup(cancelRouter)
	var client api.LLMClient
	cmd := newSlashCommandContext(
		t.Context(),
		bridge,
		ipc.NewMessageRouter(routerCtx, bridge),
		store,
		nil,
		config.DefaultConfig(),
		nil,
		nil,
		costpkg.NewTracker(),
		ipc.SlashCommandPayload{},
		"old-session",
		time.Now(),
		agent.ModeFast,
		"anthropic/claude-sonnet-5",
		"",
		t.TempDir(),
		messages,
		timeline,
		nil,
		&client,
	)
	return cmd, &pickerHarness{input: inputWriter, output: output}
}

// run calls handler and answers the picker it opens: it waits for the
// requestType event, then sends responseType with the payload reply builds
// from the request's id. It returns every event and the handler's error.
func (h *pickerHarness) run(
	t *testing.T,
	cmd *slashCommandContext,
	handler func(*slashCommandContext) error,
	requestType ipc.EventType,
	responseType ipc.ClientMessageType,
	reply func(requestID string) any,
) ([]ipc.StreamEvent, error) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- handler(cmd) }()

	requestID := h.waitForRequest(t, requestType)
	payload, err := json.Marshal(reply(requestID))
	if err != nil {
		t.Fatalf("encode %s: %v", responseType, err)
	}
	message, err := json.Marshal(ipc.ClientMessage{Type: responseType, Payload: payload})
	if err != nil {
		t.Fatalf("encode %s message: %v", responseType, err)
	}
	if _, err := h.input.Write(append(message, '\n')); err != nil {
		t.Fatalf("send %s: %v", responseType, err)
	}

	select {
	case handlerErr := <-done:
		return emittedEvents(t, bytes.NewBufferString(h.output.String())), handlerErr
	case <-time.After(5 * time.Second):
		t.Fatalf("the handler did not finish after its picker was answered; it emitted:\n%s", h.output.String())
		return nil, nil
	}
}

// waitForRequest returns the request id of the first requestType event.
func (h *pickerHarness) waitForRequest(t *testing.T, requestType ipc.EventType) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		for _, event := range emittedEvents(t, bytes.NewBufferString(h.output.String())) {
			if event.Type != requestType {
				continue
			}
			var request struct {
				RequestID string `json:"request_id"`
			}
			if err := json.Unmarshal(event.Payload, &request); err != nil {
				t.Fatalf("decode %s: %v", requestType, err)
			}
			return request.RequestID
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("no %s event; the handler emitted:\n%s", requestType, h.output.String())
	return ""
}

// responseText joins the text a handler streamed as its reply.
func responseText(t *testing.T, events []ipc.StreamEvent) string {
	t.Helper()
	var text strings.Builder
	for _, event := range events {
		if event.Type != ipc.EventTokenDelta {
			continue
		}
		var delta ipc.TokenDeltaPayload
		if err := json.Unmarshal(event.Payload, &delta); err != nil {
			t.Fatalf("decode token delta: %v", err)
		}
		text.WriteString(delta.Text)
	}
	return text.String()
}

// emittedEvents decodes the NDJSON event stream a bridge wrote.
func emittedEvents(t *testing.T, stream *bytes.Buffer) []ipc.StreamEvent {
	t.Helper()
	var events []ipc.StreamEvent
	for line := range strings.SplitSeq(strings.TrimSpace(stream.String()), "\n") {
		if line == "" {
			continue
		}
		var event ipc.StreamEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode event %q: %v", line, err)
		}
		events = append(events, event)
	}
	return events
}

// A session whose model cannot start - here a Copilot model with Copilot not
// connected - must still be resumed completely, or the engine ends up on the
// restored session while the UI never hears about it.
func TestResumeSlashCommandCompletesWhenTheModelCannotStart(t *testing.T) {
	isolateUserConfig(t)
	store := session.NewStore(t.TempDir())
	restoredMessages := []api.Message{
		{Role: api.RoleUser, Content: "restored question"},
		{Role: api.RoleAssistant, Content: "restored answer"},
	}
	if err := persistSessionState(store, sessionStateParams{
		SessionID: "target-session",
		CreatedAt: time.Now(),
		Mode:      agent.ModePlan,
		Model:     "github-copilot/gpt-5",
		Messages:  restoredMessages,
	}); err != nil {
		t.Fatalf("persist target session: %v", err)
	}
	cmd, emitted := newTestSlashCommandContext(t, store, []api.Message{{Role: api.RoleUser, Content: "current question"}}, newConversationTimeline())
	cmd.args = "target-session"

	if err := handleResumeSlashCommand(cmd); err != nil {
		t.Fatalf("handleResumeSlashCommand: %v", err)
	}

	if cmd.state.SessionID != "target-session" || len(cmd.state.Messages) != len(restoredMessages) {
		t.Fatalf("state = session %q with %d messages, want the restored session", cmd.state.SessionID, len(cmd.state.Messages))
	}
	if *cmd.client != nil {
		t.Fatal("a client was installed for a model that failed to start")
	}
	if cmd.state.ActiveModelID != "github-copilot/gpt-5" {
		t.Fatalf("active model = %q, want the restored model kept for the next turn to retry", cmd.state.ActiveModelID)
	}

	seen := make(map[ipc.EventType]bool)
	var response strings.Builder
	for _, event := range emittedEvents(t, emitted) {
		seen[event.Type] = true
		if event.Type == ipc.EventTokenDelta {
			var delta ipc.TokenDeltaPayload
			if err := json.Unmarshal(event.Payload, &delta); err != nil {
				t.Fatalf("decode token delta: %v", err)
			}
			response.WriteString(delta.Text)
		}
	}
	for _, want := range []ipc.EventType{ipc.EventError, ipc.EventSessionRestored, ipc.EventConversationHydrated, ipc.EventModeChanged, ipc.EventTurnComplete} {
		if !seen[want] {
			t.Fatalf("resume did not emit %s; emitted %v", want, seen)
		}
	}
	if !strings.Contains(response.String(), "model is unavailable") {
		t.Fatalf("response = %q, want it to say the model is unavailable", response.String())
	}
}

func TestClearSlashCommandStartsAFreshTimeline(t *testing.T) {
	isolateUserConfig(t)
	store := session.NewStore(t.TempDir())
	oldMessages := []api.Message{
		{Role: api.RoleUser, Content: "first question"},
		{Role: api.RoleAssistant, Content: "looking", ToolCalls: []api.ToolCall{{ID: "old-tool", Name: "read_file", Input: "{}"}}},
		{Role: api.RoleTool, ToolResult: &api.ToolResult{ToolCallID: "old-tool", Output: "contents"}},
		{Role: api.RoleAssistant, Content: "first answer"},
	}
	oldTimeline := rebuildConversationTimeline(oldMessages)
	oldTimeline.RecordProgress(ipc.ProgressPayload{ID: "old-progress", Message: "working on the old task"})
	cmd, _ := newTestSlashCommandContext(t, store, oldMessages, oldTimeline)

	if err := handleClearSlashCommand(cmd); err != nil {
		t.Fatalf("handleClearSlashCommand: %v", err)
	}
	if cmd.state.SessionID == "old-session" {
		t.Fatal("/clear kept the old session id")
	}

	saved, err := store.LoadConversationTimeline(cmd.state.SessionID)
	if err != nil {
		t.Fatalf("LoadConversationTimeline: %v", err)
	}
	if len(saved.Transcript) != 0 || len(saved.Progress) != 0 {
		t.Fatalf("new session saved the old timeline: transcript=%+v progress=%+v", saved.Transcript, saved.Progress)
	}

	// The first message of the new session reuses position 0. It must be
	// recorded, not deduplicated against the old session's entry.
	cmd.state.Messages = append(cmd.state.Messages, api.Message{Role: api.RoleUser, Content: "new question"})
	cmd.state.Timeline.RecordUserMessage(0)
	payload := cmd.state.Timeline.HydratedPayload(cmd.state.Messages, "")
	if len(payload.Transcript) != 1 || payload.Transcript[0].ID != conversationTimelineMessageID(0) {
		t.Fatalf("new session transcript = %+v, want just the new message", payload.Transcript)
	}
}
