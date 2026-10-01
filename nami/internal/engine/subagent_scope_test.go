package engine

import (
	"context"
	"errors"
	"io"
	"iter"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/permissions"
	"github.com/channyeintun/nami/internal/session"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

// childModel plays a child agent's model. It answers each request with the
// next scripted turn and bills every turn, so a test can see where the
// child's spend went.
type childModel struct {
	mu       sync.Mutex
	turns    []scriptedTurn
	requests []api.ModelRequest
}

func (m *childModel) ModelID() string { return "claude-sonnet-5" }
func (m *childModel) Capabilities() api.ModelCapabilities {
	return api.ModelCapabilities{SupportsToolUse: true, MaxContextWindow: 200_000, MaxOutputTokens: 8_000}
}

func (m *childModel) Stream(_ context.Context, req api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
	m.mu.Lock()
	m.requests = append(m.requests, req)
	if len(m.turns) == 0 {
		m.mu.Unlock()
		return nil, errors.New("the child asked for more turns than the test scripted")
	}
	turn := m.turns[0]
	m.turns = m.turns[1:]
	m.mu.Unlock()

	return func(yield func(api.ModelEvent, error) bool) {
		if turn.text != "" && !yield(api.ModelEvent{Type: api.ModelEventToken, Text: turn.text}, nil) {
			return
		}
		for _, call := range turn.toolCalls {
			if !yield(api.ModelEvent{Type: api.ModelEventToolCall, ToolCall: &call}, nil) {
				return
			}
		}
		if !yield(api.ModelEvent{Type: api.ModelEventUsage, Usage: &api.Usage{InputTokens: 2_000, OutputTokens: 100}}, nil) {
			return
		}
		stopReason := "end_turn"
		if len(turn.toolCalls) > 0 {
			stopReason = "tool_use"
		}
		yield(api.ModelEvent{Type: api.ModelEventStop, StopReason: stopReason}, nil)
	}, nil
}

// modelRequests returns the requests the child has made.
func (m *childModel) modelRequests() []api.ModelRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]api.ModelRequest(nil), m.requests...)
}

// newTestSubagentDeps returns runner dependencies whose children run on model,
// with only the given tools to call.
func newTestSubagentDeps(t *testing.T, model *childModel, store *session.Store, parentTracker *costpkg.Tracker, tools ...toolpkg.Tool) subagentRunnerDeps {
	t.Helper()
	registry := toolpkg.NewEmptyRegistry()
	for _, tool := range tools {
		registry.Register(tool)
	}
	bridge := ipc.NewBridge(strings.NewReader(""), io.Discard)
	deps := newSubagentRunnerDeps(bridge, registry, parentTracker, store, nil, nil, nil, nil)
	deps.chooseClient = func(string) (api.LLMClient, string, error) {
		return model, "anthropic/claude-sonnet-5", nil
	}
	return deps
}

// A workflow schedules its children itself and cancels them with the run. A
// background child would run on a context the run cannot cancel, so the
// scoped runner refuses to start one.
func TestScopedSubagentRunnerRejectsBackgroundLaunches(t *testing.T) {
	scope := subagentScope{ownerSessionID: "session-a", cwd: t.TempDir(), permissionCtx: permissions.NewContext()}
	runner := makeScopedSubagentRunner(nil, nil, nil, nil, nil, nil, nil, nil, scope)

	result, err := runner(t.Context(), toolpkg.AgentRunRequest{Description: "survey", Prompt: "survey the parser", Background: true})
	if err == nil {
		t.Fatalf("a background launch succeeded: %+v", result)
	}
	if !strings.Contains(err.Error(), "background") {
		t.Fatalf("error %q does not say background launches are refused", err)
	}
}

// A workflow outlives the turn that launched it. A child it starts after
// /clear or /resume belongs to the session that launched the workflow: its
// spend goes into that session's saved total, never into the session the
// engine moved to, and it works in the directory captured at launch.
func TestScopedSubagentRunnerUsesTheCapturedSessionNotTheCurrentOne(t *testing.T) {
	store := session.NewStore(t.TempDir())
	if err := persistSessionState(store, sessionStateParams{SessionID: "session-a", CreatedAt: time.Now(), Mode: agent.ModeFast, Tracker: costpkg.NewTracker()}); err != nil {
		t.Fatalf("persist session-a: %v", err)
	}
	useActiveSession(t, "session-a")
	launchDir := t.TempDir()
	state := &engineLoopState{cwd: launchDir}
	scope := captureSubagentScope(state, "", permissions.NewContext())

	// The engine moves on before the workflow starts its child.
	setActiveSession("session-b")
	state.setCWD(t.TempDir())

	model := &childModel{turns: []scriptedTurn{{text: "<final_answer>done</final_answer>"}}}
	newSessionTracker := costpkg.NewTracker()
	deps := newTestSubagentDeps(t, model, store, newSessionTracker)

	result, err := deps.scopedRunner(scope)(t.Context(), toolpkg.AgentRunRequest{Description: "survey", Prompt: "survey the parser", ForWorkflow: true})
	if err != nil {
		t.Fatalf("run child: %v", err)
	}
	if result.Status != "completed" || result.Summary != "done" {
		t.Fatalf("result = status %q summary %q, want the completed child's answer", result.Status, result.Summary)
	}
	if result.TotalCostUSD <= 0 {
		t.Fatalf("the child cost %v, so this test cannot tell where the spend went", result.TotalCostUSD)
	}
	if got := newSessionTracker.Snapshot(); got.TotalCostUSD != 0 || got.TotalInputTokens != 0 {
		t.Fatalf("the session the engine moved to was charged %v (%d input tokens) for the workflow's child", got.TotalCostUSD, got.TotalInputTokens)
	}
	owner, err := store.LoadMetadata("session-a")
	if err != nil {
		t.Fatalf("LoadMetadata(session-a): %v", err)
	}
	if owner.TotalCostUSD != result.TotalCostUSD {
		t.Fatalf("session-a's saved cost = %v, want the child's %v", owner.TotalCostUSD, result.TotalCostUSD)
	}
	childMeta, err := store.LoadMetadata(result.SessionID)
	if err != nil {
		t.Fatalf("LoadMetadata(child): %v", err)
	}
	if childMeta.CWD != launchDir {
		t.Fatalf("child worked in %q, want the directory captured at launch %q", childMeta.CWD, launchDir)
	}
}

func TestNormalizeReasoningEffortOverride(t *testing.T) {
	tests := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{input: "low", want: "low"},
		{input: "medium", want: "medium"},
		{input: " High ", want: "high"},
		{input: "XHIGH", want: "xhigh"},
		{input: "max", want: "max"},
		{input: "", wantErr: true},
		{input: "extreme", wantErr: true},
		{input: "x-high", wantErr: true},
	}
	for _, tt := range tests {
		got, err := normalizeReasoningEffortOverride(tt.input)
		if tt.wantErr {
			if err == nil {
				t.Errorf("normalizeReasoningEffortOverride(%q) = %q, want an error", tt.input, got)
				continue
			}
			// The caller is a script author choosing again; the error has to
			// name what is valid.
			if !strings.Contains(err.Error(), "low, medium, high, xhigh, max") {
				t.Errorf("error %q does not name the valid efforts", err)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Errorf("normalizeReasoningEffortOverride(%q) = %q, %v; want %q", tt.input, got, err, tt.want)
		}
	}
}

// No provider has a "max" effort. Sent as is, the client would drop it and
// fall back to the model's default, the opposite of what max asks for.
func TestQueryReasoningEffortAsksForTheHighestLevelForMax(t *testing.T) {
	if got := queryReasoningEffort("max"); got != api.ReasoningEffortXHigh {
		t.Errorf("max asks for %q, want %q", got, api.ReasoningEffortXHigh)
	}
	if got := queryReasoningEffort("low"); got != "low" {
		t.Errorf("low asks for %q, want low", got)
	}
}

func TestCheckSubagentModelOverrideRejectsABlankModel(t *testing.T) {
	if got, err := checkSubagentModelOverride("  ", nil); err == nil {
		t.Fatalf("a blank override was accepted as %q", got)
	}
}
