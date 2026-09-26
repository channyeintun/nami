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
// are side calls (compaction, session memory, goal checks) and get a fixed
// summary, so they never consume the script.
type scriptedClient struct {
	mu       sync.Mutex
	turns    []scriptedTurn
	caps     api.ModelCapabilities
	requests []api.ModelRequest
}

func (c *scriptedClient) ModelID() string                     { return "scripted-model" }
func (c *scriptedClient) Capabilities() api.ModelCapabilities { return c.caps }

func (c *scriptedClient) Stream(_ context.Context, req api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
	c.mu.Lock()
	turn := scriptedTurn{text: "summary of earlier work"}
	if len(req.Tools) > 0 {
		c.requests = append(c.requests, req)
		if len(c.turns) == 0 {
			c.mu.Unlock()
			return nil, io.ErrUnexpectedEOF
		}
		turn = c.turns[0]
		c.turns = c.turns[1:]
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
		stopReason := "end_turn"
		if len(turn.toolCalls) > 0 {
			stopReason = "tool_use"
		}
		yield(api.ModelEvent{Type: api.ModelEventStop, StopReason: stopReason}, nil)
	}, nil
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
