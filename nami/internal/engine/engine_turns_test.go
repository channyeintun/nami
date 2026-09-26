package engine

import (
	"context"
	"io"
	"iter"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/compact"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/session"
	"github.com/channyeintun/nami/internal/timing"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

// scriptedTurn is one main-loop model response.
type scriptedTurn struct {
	text      string
	toolCalls []api.ToolCall
}

// scriptedClient plays back main-loop turns in order. Requests without tools
// are side calls (compaction, goal checks, titles) and get a fixed summary,
// so they never consume the script.
type scriptedClient struct {
	mu    sync.Mutex
	turns []scriptedTurn
	caps  api.ModelCapabilities
	// sideUsage is the usage every side call reports.
	sideUsage api.Usage
	requests  []api.ModelRequest
}

func (c *scriptedClient) ModelID() string                     { return "scripted-model" }
func (c *scriptedClient) Capabilities() api.ModelCapabilities { return c.caps }

func (c *scriptedClient) Stream(_ context.Context, req api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
	c.mu.Lock()
	turn := scriptedTurn{text: "summary of earlier work"}
	usage := c.sideUsage
	if len(req.Tools) > 0 {
		c.requests = append(c.requests, req)
		if len(c.turns) == 0 {
			c.mu.Unlock()
			return nil, io.ErrUnexpectedEOF
		}
		turn = c.turns[0]
		c.turns = c.turns[1:]
		usage = api.Usage{}
	}
	c.mu.Unlock()

	return func(yield func(api.ModelEvent, error) bool) {
		if turn.text != "" && !yield(api.ModelEvent{Type: api.ModelEventToken, Text: turn.text}, nil) {
			return
		}
		for _, call := range turn.toolCalls {
			if !yield(api.ModelEvent{Type: api.ModelEventToolCall, ToolCall: &call}, nil) {
				return
			}
		}
		if usage != (api.Usage{}) && !yield(api.ModelEvent{Type: api.ModelEventUsage, Usage: &usage}, nil) {
			return
		}
		stopReason := "end_turn"
		if len(turn.toolCalls) > 0 {
			stopReason = "tool_use"
		}
		yield(api.ModelEvent{Type: api.ModelEventStop, StopReason: stopReason}, nil)
	}, nil
}

// mainRequests returns the main-loop requests the model has received.
func (c *scriptedClient) mainRequests() []api.ModelRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]api.ModelRequest(nil), c.requests...)
}

// blockingTool runs until its context ends, standing in for a long command
// the user stops.
type blockingTool struct {
	started chan struct{}
	once    sync.Once
}

func (*blockingTool) Name() string        { return "block" }
func (*blockingTool) Description() string { return "blocks until cancelled" }
func (*blockingTool) InputSchema() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (*blockingTool) Permission() toolpkg.PermissionLevel { return toolpkg.PermissionReadOnly }
func (*blockingTool) Concurrency(toolpkg.ToolInput) toolpkg.ConcurrencyDecision {
	return toolpkg.ConcurrencySerial
}
func (b *blockingTool) Execute(ctx context.Context, _ toolpkg.ToolInput) (toolpkg.ToolOutput, error) {
	b.once.Do(func() { close(b.started) })
	<-ctx.Done()
	return toolpkg.ToolOutput{}, ctx.Err()
}

// echoTool is a read-only tool that answers immediately.
type echoTool struct{}

func (echoTool) Name() string        { return "echo" }
func (echoTool) Description() string { return "answers immediately" }
func (echoTool) InputSchema() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}
func (echoTool) Permission() toolpkg.PermissionLevel { return toolpkg.PermissionReadOnly }
func (echoTool) Concurrency(toolpkg.ToolInput) toolpkg.ConcurrencyDecision {
	return toolpkg.ConcurrencySerial
}
func (echoTool) Execute(context.Context, toolpkg.ToolInput) (toolpkg.ToolOutput, error) {
	return toolpkg.ToolOutput{Output: "echoed"}, nil
}

type turnHarness struct {
	deps  engineLoopDeps
	state *engineLoopState
	// input feeds client messages to the router, as the TUI would.
	input *io.PipeWriter
}

func (h *turnHarness) send(t *testing.T, msgType ipc.ClientMessageType) {
	t.Helper()
	if _, err := io.WriteString(h.input, `{"type":"`+string(msgType)+`"}`+"\n"); err != nil {
		t.Errorf("send %s: %v", msgType, err)
	}
}

// newTurnHarness wires a real engine turn to a scripted model, with every
// on-disk location pointed into the test's temp directory.
func newTurnHarness(t *testing.T, client *scriptedClient, tools ...toolpkg.Tool) *turnHarness {
	t.Helper()
	root := t.TempDir()
	t.Setenv("HOME", root)
	t.Setenv("XDG_CONFIG_HOME", root+"/config")
	t.Setenv("XDG_CACHE_HOME", root+"/cache")
	t.Setenv("NAMI_ENABLE_SESSION_MEMORY", "false")

	inputReader, inputWriter := io.Pipe()
	t.Cleanup(func() { _ = inputWriter.Close() })
	bridge := ipc.NewBridge(inputReader, io.Discard)
	routerCtx, cancelRouter := context.WithCancel(context.Background())
	t.Cleanup(cancelRouter)

	registry := toolpkg.NewEmptyRegistry()
	for _, tool := range tools {
		registry.Register(tool)
	}
	store := session.NewStore(root + "/sessions")
	sessionID := "test-session"
	sessionDir := store.SessionDir(sessionID)

	return &turnHarness{
		deps: engineLoopDeps{
			bridge:             bridge,
			router:             ipc.NewMessageRouter(routerCtx, bridge),
			registry:           registry,
			permissionCtx:      newPermissionContext("bypassPermissions", false),
			tracker:            costpkg.NewTracker(),
			sessionStore:       store,
			timingLogger:       timing.NewSessionLogger(sessionDir),
			modelState:         NewActiveModelState(client, "test/scripted-model"),
			subagentModelState: NewActiveSubagentModelState(""),
		},
		state: &engineLoopState{
			client:         client,
			sessionID:      sessionID,
			sessionDir:     sessionDir,
			startedAt:      time.Now(),
			mode:           agent.ModeFast,
			activeModelID:  "test/scripted-model",
			cwd:            root,
			timeline:       newConversationTimeline(),
			titleGenerated: true,
		},
		input: inputWriter,
	}
}

// longConversation returns enough history to push a 20k context window past
// its auto-compaction threshold.
func longConversation(pairs int) []api.Message {
	filler := strings.Repeat("context that fills the window. ", 40)
	messages := make([]api.Message, 0, pairs*2)
	for range pairs {
		messages = append(messages,
			api.Message{Role: api.RoleUser, Content: "question " + filler},
			api.Message{Role: api.RoleAssistant, Content: "answer " + filler},
		)
	}
	return messages
}

// assertHydratedTimelineMatches checks what a resumed session would render:
// every transcript entry must name something in the payload, and every message
// and tool call must be reachable from the transcript, messages in order.
func assertHydratedTimelineMatches(t *testing.T, payload ipc.ConversationHydratedPayload) {
	t.Helper()
	var wantOrder []string
	messageIDs := map[string]bool{}
	for _, message := range payload.Messages {
		messageIDs[message.ID] = true
		wantOrder = append(wantOrder, message.ID)
	}
	toolIDs := map[string]bool{}
	for _, call := range payload.ToolCalls {
		toolIDs[call.ID] = true
	}

	var gotOrder []string
	listedTools := map[string]bool{}
	for _, entry := range payload.Transcript {
		switch entry.Kind {
		case "message":
			if !messageIDs[entry.RefID] {
				t.Errorf("transcript names message %q, which the conversation does not have", entry.RefID)
				continue
			}
			gotOrder = append(gotOrder, entry.RefID)
		case "tool_call":
			if !toolIDs[entry.RefID] {
				t.Errorf("transcript names tool call %q, which the conversation does not have", entry.RefID)
			}
			listedTools[entry.RefID] = true
		}
	}
	if !slices.Equal(gotOrder, wantOrder) {
		t.Errorf("transcript renders messages %v, want %v", gotOrder, wantOrder)
	}
	for id := range toolIDs {
		if !listedTools[id] {
			t.Errorf("tool call %q is missing from the transcript, so a resumed session never shows it", id)
		}
	}
}

// A long session compacts mid-turn. Everything the turn recorded by position
// before that must follow the shorter conversation: in plan mode the review
// gate used to slice past its end and panic the engine, and the saved timeline
// kept naming the pre-compaction messages.
func TestTurnFollowsTheConversationThroughMidTurnCompaction(t *testing.T) {
	for _, mode := range []agent.ExecutionMode{agent.ModeFast, agent.ModePlan} {
		t.Run(string(mode), func(t *testing.T) {
			client := &scriptedClient{
				caps: api.ModelCapabilities{SupportsToolUse: true, MaxContextWindow: 20_000, MaxOutputTokens: 1_000},
				turns: []scriptedTurn{
					{text: "Checking.", toolCalls: []api.ToolCall{{ID: "call-echo", Name: "echo", Input: "{}"}}},
					{text: "Here is what I found."},
				},
			}
			h := newTurnHarness(t, client, echoTool{})
			h.state.mode = mode
			h.state.messages = longConversation(30)
			h.state.timeline = rebuildConversationTimeline(h.state.messages)

			if err := handleUserInputMessage(t.Context(), ipc.UserInputPayload{Text: "keep going"}, h.deps, h.state); err != nil {
				t.Fatalf("handleUserInputMessage: %v", err)
			}
			if !compact.IsSummaryMessage(h.state.messages[0]) {
				t.Fatalf("the turn did not compact; the test no longer exercises the rewrite")
			}

			saved, err := h.deps.sessionStore.LoadConversationTimeline(h.state.sessionID)
			if err != nil {
				t.Fatalf("LoadConversationTimeline: %v", err)
			}
			assertHydratedTimelineMatches(t, saved)
		})
	}
}

func TestRebaseAfterCompactionPointsTheQueryAtTheNewSummary(t *testing.T) {
	before := longConversation(5)
	priorSummary := compact.BuildSummaryMessages("earlier", nil)[0]
	prompt := api.Message{Role: api.RoleUser, Content: "keep going"}

	tests := []struct {
		name      string
		result    compact.CompactResult
		wantStart int
		rewritten bool
	}{
		{
			name:      "full summary",
			result:    compact.CompactResult{Strategy: compact.StrategySummarize, Messages: compact.BuildSummaryMessages("recap", []api.Message{prompt})},
			wantStart: 0,
			rewritten: true,
		},
		{
			name: "partial summary after an older one",
			result: compact.CompactResult{
				Strategy: compact.StrategyPartial,
				Messages: compact.BuildSummaryMessagesWithPrefix([]api.Message{priorSummary, prompt}, "recap", nil),
			},
			wantStart: 2,
			rewritten: true,
		},
		{
			// Truncation rewrites tool output in place, so positions still hold.
			name:      "tool output truncation",
			result:    compact.CompactResult{Strategy: compact.StrategyToolTruncate, Messages: before},
			wantStart: len(before),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			timeline := rebuildConversationTimeline(before)
			turn := &userTurnContext{
				state:      &engineLoopState{messages: before, timeline: timeline},
				queryStart: len(before),
			}

			turn.rebaseAfterCompaction(tc.result)

			if turn.queryStart != tc.wantStart {
				t.Fatalf("queryStart = %d, want %d", turn.queryStart, tc.wantStart)
			}
			if !tc.rewritten {
				if turn.state.timeline != timeline || len(turn.state.messages) != len(before) {
					t.Fatal("a rewrite that kept positions still replaced the turn's state")
				}
				return
			}
			if len(turn.state.messages) != len(tc.result.Messages) {
				t.Fatalf("turn holds %d messages, want the %d compaction produced", len(turn.state.messages), len(tc.result.Messages))
			}
			assertHydratedTimelineMatches(t, turn.state.timeline.HydratedPayload(turn.state.messages, "model"))
		})
	}
}

// A start index can never outrun the conversation now, but these helpers once
// sliced by it directly; an out-of-range index must read as "nothing".
func TestTurnScopedHelpersTolerateAStartPastTheEnd(t *testing.T) {
	messages := []api.Message{
		{Role: api.RoleAssistant, ToolCalls: []api.ToolCall{{ID: "a", Name: "save_implementation_plan"}}},
	}
	if turnUsedToolName(messages, 5, "save_implementation_plan") {
		t.Fatal("turnUsedToolName found a call before the start index")
	}
	if !turnUsedToolName(messages, -1, "save_implementation_plan") {
		t.Fatal("turnUsedToolName missed a call with a negative start")
	}
	if got := buildRecentTranscriptCorpus(messages, 5); got != "" {
		t.Fatalf("buildRecentTranscriptCorpus = %q, want empty", got)
	}
}

// unansweredToolCalls lists the tool calls in messages that no tool result
// answers.
func unansweredToolCalls(messages []api.Message) []string {
	answered := map[string]bool{}
	for _, message := range messages {
		if message.ToolResult != nil {
			answered[message.ToolResult.ToolCallID] = true
		}
	}
	var missing []string
	for _, message := range messages {
		for _, call := range message.ToolCalls {
			if !answered[call.ID] {
				missing = append(missing, call.ID)
			}
		}
	}
	return missing
}

// Stopping a turn while a tool runs used to leave that call without a result.
// Every provider rejects a request carrying an unanswered tool call, so every
// later message in the session failed.
func TestStoppingMidToolLeavesTheSessionUsable(t *testing.T) {
	client := &scriptedClient{
		caps: api.ModelCapabilities{SupportsToolUse: true, MaxContextWindow: 200_000, MaxOutputTokens: 8_000},
		turns: []scriptedTurn{
			{text: "Running it.", toolCalls: []api.ToolCall{{ID: "call-block", Name: "block", Input: "{}"}}},
			{text: "Okay."},
		},
	}
	tool := &blockingTool{started: make(chan struct{})}
	h := newTurnHarness(t, client, tool)

	go func() {
		<-tool.started
		h.send(t, ipc.MsgCancel)
	}()
	if err := handleUserInputMessage(t.Context(), ipc.UserInputPayload{Text: "run the long thing"}, h.deps, h.state); err != nil {
		t.Fatalf("stopped turn: %v", err)
	}
	if missing := unansweredToolCalls(h.state.messages); len(missing) > 0 {
		t.Fatalf("the stopped turn left tool calls %v unanswered", missing)
	}
	saved, err := h.deps.sessionStore.LoadTranscript(h.state.sessionID)
	if err != nil {
		t.Fatalf("LoadTranscript: %v", err)
	}
	if missing := unansweredToolCalls(saved); len(missing) > 0 {
		t.Fatalf("the saved transcript left tool calls %v unanswered, so a resumed session fails too", missing)
	}

	if err := handleUserInputMessage(t.Context(), ipc.UserInputPayload{Text: "never mind"}, h.deps, h.state); err != nil {
		t.Fatalf("next turn: %v", err)
	}
	requests := client.mainRequests()
	if missing := unansweredToolCalls(requests[len(requests)-1].Messages); len(missing) > 0 {
		t.Fatalf("the next request carries unanswered tool calls %v", missing)
	}
}

func TestAnswerUnfinishedToolCalls(t *testing.T) {
	calls := api.Message{Role: api.RoleAssistant, ToolCalls: []api.ToolCall{{ID: "a"}, {ID: "b"}}}
	resultFor := func(id string) api.Message {
		return api.Message{Role: api.RoleTool, Content: "done", ToolResult: &api.ToolResult{ToolCallID: id, Output: "done"}}
	}
	prompt := api.Message{Role: api.RoleUser, Content: "go"}
	nudge := api.Message{Role: api.RoleUser, Content: "try a different edit"}

	tests := []struct {
		name string
		in   []api.Message
		// want lists each message's tool call ID for results, or its role.
		want []string
	}{
		{name: "no assistant turn", in: []api.Message{prompt}, want: []string{"user"}},
		{name: "final answer without calls", in: []api.Message{prompt, {Role: api.RoleAssistant, Content: "hi"}}, want: []string{"user", "assistant"}},
		{name: "every call answered", in: []api.Message{prompt, calls, resultFor("a"), resultFor("b")}, want: []string{"user", "assistant", "a", "b"}},
		// A stop mid-batch appends nothing after the calls.
		{name: "stopped before any result", in: []api.Message{prompt, calls}, want: []string{"user", "assistant", "a", "b"}},
		// A plan-review pause can record some results and a retry nudge; the
		// missing result still has to come before the nudge.
		{name: "paused after one result", in: []api.Message{prompt, calls, resultFor("a"), nudge}, want: []string{"user", "assistant", "a", "b", "user"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, changed := answerUnfinishedToolCalls(slices.Clone(tc.in))
			var shape []string
			for _, message := range got {
				if message.ToolResult != nil {
					shape = append(shape, message.ToolResult.ToolCallID)
					continue
				}
				shape = append(shape, string(message.Role))
			}
			if !slices.Equal(shape, tc.want) {
				t.Fatalf("conversation = %v, want %v", shape, tc.want)
			}
			if changed != (len(got) > len(tc.in)) {
				t.Fatalf("changed = %v for %d -> %d messages", changed, len(tc.in), len(got))
			}
			for _, message := range got {
				if message.ToolResult != nil && message.ToolResult.Output == unfinishedToolCallOutput && !message.ToolResult.IsError {
					t.Fatal("an unfinished call's result is not marked as an error")
				}
			}
		})
	}
}
