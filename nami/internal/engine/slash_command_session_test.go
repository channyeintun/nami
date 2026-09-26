package engine

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/config"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/session"
	toolpkg "github.com/channyeintun/nami/internal/tools"
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
	// Commands that switch sessions switch the active one too.
	useActiveSession(t, "old-session")
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
	useActiveSession(t, "old-session")
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

// rewindTestConversation has two user turns, the first with a tool call.
func rewindTestConversation() []api.Message {
	return []api.Message{
		{Role: api.RoleUser, Content: "first question"},
		{Role: api.RoleAssistant, Content: "looking", ToolCalls: []api.ToolCall{{ID: "call-1", Name: "read_file", Input: "{}"}}},
		{Role: api.RoleTool, ToolResult: &api.ToolResult{ToolCallID: "call-1", Output: "contents"}},
		{Role: api.RoleAssistant, Content: "first answer"},
		{Role: api.RoleUser, Content: "second question"},
		{Role: api.RoleAssistant, Content: "second answer"},
	}
}

// The rewind picker's reply names a message by index. Only a user turn is a
// place to rewind to: cutting after an assistant message would keep its tool
// call without the result, which every provider rejects from then on.
func TestRewindRefusesATargetThatIsNotAUserTurn(t *testing.T) {
	isolateUserConfig(t)
	messages := rewindTestConversation()
	cmd, picker := newPickerSlashCommandContext(t, session.NewStore(t.TempDir()), messages, rebuildConversationTimeline(messages))

	events, err := picker.run(t, cmd, handleRewindSlashCommand, ipc.EventRewindSelectionRequested, ipc.MsgRewindSelectionResponse,
		func(requestID string) any {
			return ipc.RewindSelectionResponsePayload{RequestID: requestID, MessageIndex: 1}
		})
	if err != nil {
		t.Fatalf("handleRewindSlashCommand: %v", err)
	}
	if len(cmd.state.Messages) != len(messages) {
		t.Fatalf("rewind to an assistant message cut the conversation to %d messages", len(cmd.state.Messages))
	}
	if missing := unansweredToolCalls(cmd.state.Messages); len(missing) > 0 {
		t.Fatalf("the conversation has unanswered tool calls %v", missing)
	}
	reported := false
	for _, event := range events {
		if event.Type == ipc.EventError {
			reported = true
		}
		if event.Type == ipc.EventSessionRewound {
			t.Fatal("the session was reported as rewound")
		}
	}
	if !reported {
		t.Fatal("the invalid target was not reported")
	}
}

func TestRewindToAUserTurn(t *testing.T) {
	isolateUserConfig(t)
	messages := rewindTestConversation()
	cmd, picker := newPickerSlashCommandContext(t, session.NewStore(t.TempDir()), messages, rebuildConversationTimeline(messages))

	events, err := picker.run(t, cmd, handleRewindSlashCommand, ipc.EventRewindSelectionRequested, ipc.MsgRewindSelectionResponse,
		func(requestID string) any {
			return ipc.RewindSelectionResponsePayload{RequestID: requestID, MessageIndex: 4}
		})
	if err != nil {
		t.Fatalf("handleRewindSlashCommand: %v", err)
	}
	if len(cmd.state.Messages) != 5 || cmd.state.Messages[4].Content != "second question" {
		t.Fatalf("conversation after rewind = %+v, want it to end at the second question", cmd.state.Messages)
	}
	if text := responseText(t, events); !strings.Contains(text, "user turn 2") {
		t.Fatalf("response = %q, want it to name user turn 2", text)
	}
}

// /compact replaces the conversation with a summary. The timeline names
// messages by position, so keeping the old one saved entries for messages
// the session no longer has, and a later /resume rendered the wrong history.
func TestCompactSlashCommandSavesATimelineForTheCompactedConversation(t *testing.T) {
	isolateUserConfig(t)
	messages := longConversation(30)
	cmd, _ := newTestSlashCommandContext(t, session.NewStore(t.TempDir()), messages, rebuildConversationTimeline(messages))
	*cmd.client = &scriptedClient{caps: api.ModelCapabilities{SupportsToolUse: true, MaxContextWindow: 20_000, MaxOutputTokens: 1_000}}

	if err := handleCompactSlashCommand(cmd); err != nil {
		t.Fatalf("handleCompactSlashCommand: %v", err)
	}
	if len(cmd.state.Messages) >= len(messages) {
		t.Fatalf("compaction kept %d of %d messages; the test no longer exercises the rewrite", len(cmd.state.Messages), len(messages))
	}

	saved, err := cmd.store.LoadConversationTimeline(cmd.state.SessionID)
	if err != nil {
		t.Fatalf("LoadConversationTimeline: %v", err)
	}
	assertHydratedTimelineMatches(t, saved)
}

// The cost tracker's total is what every save records as the session's cost.
// /resume left it holding the previous session's spend, so the first save of
// the resumed session replaced that session's own total with the other one.
func TestResumeSlashCommandKeepsTheResumedSessionsCost(t *testing.T) {
	isolateUserConfig(t)
	store := session.NewStore(t.TempDir())
	resumedSpend := costpkg.NewTracker()
	resumedSpend.RecordAPICall("claude-sonnet-5", 100, 10, 0, 0, time.Second, 1.25)
	if err := persistSessionState(store, sessionStateParams{
		SessionID: "target-session",
		CreatedAt: time.Now(),
		Mode:      agent.ModeFast,
		// A model that cannot start keeps the test away from the network.
		Model:    "github-copilot/gpt-5",
		Tracker:  resumedSpend,
		Messages: []api.Message{{Role: api.RoleUser, Content: "restored question"}},
	}); err != nil {
		t.Fatalf("persist target session: %v", err)
	}
	cmd, emitted := newTestSlashCommandContext(t, store, []api.Message{{Role: api.RoleUser, Content: "current question"}}, newConversationTimeline())
	cmd.tracker.RecordAPICall("claude-sonnet-5", 1000, 100, 0, 0, time.Second, 3.00)
	cmd.args = "target-session"

	if err := handleResumeSlashCommand(cmd); err != nil {
		t.Fatalf("handleResumeSlashCommand: %v", err)
	}

	meta, err := store.LoadMetadata("target-session")
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.TotalCostUSD != 1.25 {
		t.Fatalf("resumed session's saved cost = %v, want its own 1.25", meta.TotalCostUSD)
	}
	if got := cmd.tracker.Snapshot().TotalCostUSD; got != 1.25 {
		t.Fatalf("tracker total after resume = %v, want 1.25", got)
	}
	// The session left behind keeps what it spent up to the switch.
	left, err := store.LoadMetadata("old-session")
	if err != nil {
		t.Fatalf("LoadMetadata for the session left: %v", err)
	}
	if left.TotalCostUSD != 3.00 {
		t.Fatalf("session left's saved cost = %v, want its 3.00", left.TotalCostUSD)
	}
	shown := -1.0
	for _, event := range emittedEvents(t, emitted) {
		if event.Type != ipc.EventCostUpdate {
			continue
		}
		var update ipc.CostUpdatePayload
		if err := json.Unmarshal(event.Payload, &update); err != nil {
			t.Fatalf("decode cost update: %v", err)
		}
		shown = update.TotalUSD
	}
	if shown != 1.25 {
		t.Fatalf("the TUI was last shown a cost of %v, want 1.25", shown)
	}
}

// rememberFileRead installs a fresh read state holding one read of a new file.
// The returned check reports whether read_file would still answer a re-read
// with its "unchanged since last read" stub instead of the content.
func rememberFileRead(t *testing.T) func() bool {
	t.Helper()
	previous := toolpkg.GetGlobalFileReadState()
	t.Cleanup(func() { toolpkg.SetGlobalFileReadState(previous) })
	state := toolpkg.NewFileReadState()
	toolpkg.SetGlobalFileReadState(state)

	path := filepath.Join(t.TempDir(), "read.txt")
	if err := os.WriteFile(path, []byte("contents\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	state.Remember(path, 1, 2000, info)
	return func() bool { return state.SeenUnchanged(path, 1, 2000, info) }
}

// read_file answers a re-read of an unchanged file with a stub that points at
// the earlier result. A command that leaves the conversation without that
// result has to forget the read, or the model can no longer see the file.
func TestSessionCommandsForgetFileReadsTheConversationLost(t *testing.T) {
	isolateUserConfig(t)

	t.Run("clear", func(t *testing.T) {
		stillSeen := rememberFileRead(t)
		cmd, _ := newTestSlashCommandContext(t, session.NewStore(t.TempDir()), rewindTestConversation(), newConversationTimeline())
		if err := handleClearSlashCommand(cmd); err != nil {
			t.Fatalf("handleClearSlashCommand: %v", err)
		}
		if stillSeen() {
			t.Fatal("/clear kept the read, so the new session gets the stub for a file it never read")
		}
	})

	t.Run("resume", func(t *testing.T) {
		store := session.NewStore(t.TempDir())
		if err := persistSessionState(store, sessionStateParams{
			SessionID: "target-session",
			CreatedAt: time.Now(),
			Mode:      agent.ModeFast,
			Model:     "github-copilot/gpt-5",
			Messages:  []api.Message{{Role: api.RoleUser, Content: "restored question"}},
		}); err != nil {
			t.Fatalf("persist target session: %v", err)
		}
		stillSeen := rememberFileRead(t)
		cmd, _ := newTestSlashCommandContext(t, store, rewindTestConversation(), newConversationTimeline())
		cmd.args = "target-session"
		if err := handleResumeSlashCommand(cmd); err != nil {
			t.Fatalf("handleResumeSlashCommand: %v", err)
		}
		if stillSeen() {
			t.Fatal("/resume kept the read, so the resumed session gets the stub for a result it does not hold")
		}
	})

	t.Run("rewind", func(t *testing.T) {
		stillSeen := rememberFileRead(t)
		messages := rewindTestConversation()
		cmd, picker := newPickerSlashCommandContext(t, session.NewStore(t.TempDir()), messages, rebuildConversationTimeline(messages))
		if _, err := picker.run(t, cmd, handleRewindSlashCommand, ipc.EventRewindSelectionRequested, ipc.MsgRewindSelectionResponse,
			func(requestID string) any {
				return ipc.RewindSelectionResponsePayload{RequestID: requestID, MessageIndex: 0}
			}); err != nil {
			t.Fatalf("handleRewindSlashCommand: %v", err)
		}
		if stillSeen() {
			t.Fatal("/rewind kept the read, though it dropped the result")
		}
	})

	t.Run("compact", func(t *testing.T) {
		stillSeen := rememberFileRead(t)
		messages := longConversation(30)
		cmd, _ := newTestSlashCommandContext(t, session.NewStore(t.TempDir()), messages, rebuildConversationTimeline(messages))
		*cmd.client = &scriptedClient{caps: api.ModelCapabilities{SupportsToolUse: true, MaxContextWindow: 20_000, MaxOutputTokens: 1_000}}
		if err := handleCompactSlashCommand(cmd); err != nil {
			t.Fatalf("handleCompactSlashCommand: %v", err)
		}
		if stillSeen() {
			t.Fatal("/compact kept the read, though the summary replaced the result")
		}
	})
}
