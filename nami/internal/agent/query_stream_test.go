package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"testing"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/ipc"
)

func streamingTextModel(text string) func(context.Context, api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
	return func(context.Context, api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
		return func(yield func(api.ModelEvent, error) bool) {
			if !yield(api.ModelEvent{Type: api.ModelEventToken, Text: text}, nil) {
				return
			}
			yield(api.ModelEvent{Type: api.ModelEventStop, StopReason: "end_turn"}, nil)
		}, nil
	}
}

func TestQueryStreamStopsYieldingOnceConsumerStops(t *testing.T) {
	// The engine returns from its range loop when it cannot forward an event.
	// Go panics if the iterator calls yield again after that, so the stream
	// must wind down without reporting the resulting error to the consumer.
	req := QueryRequest{Messages: []api.Message{{Role: api.RoleUser, Content: "hello"}}}
	deps := QueryDeps{CallModel: streamingTextModel("partial answer")}

	received := 0
	for event, err := range QueryStream(context.Background(), req, deps) {
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
		if event.Type != ipc.EventTokenDelta {
			t.Fatalf("expected the first event to be a token delta, got %q", event.Type)
		}
		received++
		break
	}
	if received != 1 {
		t.Fatalf("expected exactly one event before breaking, got %d", received)
	}
}

func TestQueryStreamKeepsTheUserRequestAcrossLoopFollowUps(t *testing.T) {
	// A stop hook that keeps the turn open (as a /goal does) appends its own
	// user-role follow-up. Context chosen from the user's request, such as
	// the ultrathink budget, must still come from that request afterwards.
	req := QueryRequest{
		Messages:     []api.Message{{Role: api.RoleUser, Content: "ultrathink about the retry loop"}},
		Capabilities: api.ModelCapabilities{SupportsExtendedThinking: true},
		MaxTokens:    32_000,
	}
	var budgets []int
	answer := streamingTextModel("done")
	stops := 0
	deps := QueryDeps{
		CallModel: func(ctx context.Context, modelReq api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
			budgets = append(budgets, modelReq.ThinkingBudget)
			return answer(ctx, modelReq)
		},
		BeforeStop: func(context.Context, StopRequest) (StopDecision, error) {
			stops++
			return StopDecision{Continue: stops == 1, FollowUpMessage: "The session goal is not satisfied yet."}, nil
		},
	}

	for _, err := range QueryStream(context.Background(), req, deps) {
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
	}
	if len(budgets) != 2 {
		t.Fatalf("expected two model calls, got %d", len(budgets))
	}
	for i, budget := range budgets {
		if budget <= 0 {
			t.Fatalf("model call %d lost the ultrathink budget: %v", i+1, budgets)
		}
	}
}

// scriptedTurn is one model reply in a scripted conversation: a tool call when
// tool is set, otherwise text, with the output tokens the provider reports.
type scriptedTurn struct {
	tool         bool
	text         string
	stopReason   string
	outputTokens int
}

// scriptedModel replies with turns in order and repeats the last one.
func scriptedModel(turns ...scriptedTurn) (func(context.Context, api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error), *int) {
	calls := 0
	return func(context.Context, api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
		turn := turns[min(calls, len(turns)-1)]
		calls++
		id := fmt.Sprintf("call_%d", calls)
		return func(yield func(api.ModelEvent, error) bool) {
			event := api.ModelEvent{Type: api.ModelEventToken, Text: turn.text}
			if turn.tool {
				event = api.ModelEvent{Type: api.ModelEventToolCall, ToolCall: &api.ToolCall{ID: id, Name: "bash", Input: `{"command":"ls"}`}}
			}
			if !yield(event, nil) {
				return
			}
			if !yield(api.ModelEvent{Type: api.ModelEventUsage, Usage: &api.Usage{OutputTokens: turn.outputTokens}}, nil) {
				return
			}
			stopReason := turn.stopReason
			if stopReason == "" {
				stopReason = "end_turn"
				if turn.tool {
					stopReason = "tool_use"
				}
			}
			yield(api.ModelEvent{Type: api.ModelEventStop, StopReason: stopReason}, nil)
		}, nil
	}, &calls
}

func answerEveryToolCall(_ context.Context, calls []api.ToolCall) ([]api.ToolResult, error) {
	results := make([]api.ToolResult, 0, len(calls))
	for _, call := range calls {
		results = append(results, api.ToolResult{ToolCallID: call.ID, Output: "ok"})
	}
	return results, nil
}

// queryOutcome is what a query streamed: its turn_complete stop reasons and
// the notices that followed them.
type queryOutcome struct {
	stopReasons []string
	notices     []string
}

func runScriptedQuery(t *testing.T, ctx context.Context, maxTokens int, deps QueryDeps) (queryOutcome, error) {
	t.Helper()
	req := QueryRequest{
		Messages:  []api.Message{{Role: api.RoleUser, Content: "fix the failing tests"}},
		MaxTokens: maxTokens,
	}
	var outcome queryOutcome
	for event, err := range QueryStream(ctx, req, deps) {
		if err != nil {
			return outcome, err
		}
		switch event.Type {
		case ipc.EventTurnComplete:
			var payload ipc.TurnCompletePayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("decode turn_complete: %v", err)
			}
			outcome.stopReasons = append(outcome.stopReasons, payload.StopReason)
		case ipc.EventNotice:
			if len(outcome.stopReasons) == 0 {
				continue
			}
			var payload ipc.NoticePayload
			if err := json.Unmarshal(event.Payload, &payload); err != nil {
				t.Fatalf("decode notice: %v", err)
			}
			outcome.notices = append(outcome.notices, payload.Message)
		}
	}
	return outcome, nil
}

func TestQueryStreamKeepsWorkingPastOneReplyOfToolOutput(t *testing.T) {
	// The output budget is the model's cap on one reply. Charging every tool
	// turn to it ended a task on a 4096-token model after four tool calls.
	turns := make([]scriptedTurn, 0, 9)
	for range 8 {
		turns = append(turns, scriptedTurn{tool: true, outputTokens: 1000})
	}
	turns = append(turns, scriptedTurn{text: "All tests pass.", outputTokens: 20})
	model, calls := scriptedModel(turns...)

	outcome, err := runScriptedQuery(t, context.Background(), 4096, QueryDeps{CallModel: model, ExecuteToolBatch: answerEveryToolCall})
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	if *calls != len(turns) || !slices.Equal(outcome.stopReasons, []string{"end_turn"}) {
		t.Fatalf("expected all %d turns and a normal finish, got %d turns and stop reasons %q", len(turns), *calls, outcome.stopReasons)
	}
}

func TestQueryStreamStopsAtALimitThroughBeforeStop(t *testing.T) {
	// A query ended by a limit must still let the stop hooks and the goal see
	// the stop, and must tell the user why it ended.
	tests := []struct {
		name       string
		turn       scriptedTurn
		wantReason string
		wantNotice string
	}{
		{
			name:       "turn limit",
			turn:       scriptedTurn{tool: true, outputTokens: 50},
			wantReason: "max_turns",
			wantNotice: "50 model turns",
		},
		{
			name:       "output budget",
			turn:       scriptedTurn{text: "a very long answer", stopReason: "max_tokens", outputTokens: 4096},
			wantReason: ContinuationStopBudgetExhausted,
			wantNotice: "4096 output tokens",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model, _ := scriptedModel(tt.turn)
			var stops []StopRequest
			deps := QueryDeps{
				CallModel:        model,
				ExecuteToolBatch: answerEveryToolCall,
				BeforeStop: func(_ context.Context, req StopRequest) (StopDecision, error) {
					stops = append(stops, req)
					return StopDecision{Continue: true, Reason: "the goal is not met"}, nil
				},
			}

			outcome, err := runScriptedQuery(t, context.Background(), 4096, deps)
			if err != nil {
				t.Fatalf("unexpected stream error: %v", err)
			}
			if len(stops) != 1 || stops[0].StopReason != tt.wantReason {
				t.Fatalf("expected BeforeStop once with %q, got %+v", tt.wantReason, stops)
			}
			if !slices.Equal(outcome.stopReasons, []string{tt.wantReason}) {
				t.Fatalf("expected turn_complete %q, got %q", tt.wantReason, outcome.stopReasons)
			}
			if len(outcome.notices) != 1 || !strings.Contains(outcome.notices[0], tt.wantNotice) || !strings.Contains(outcome.notices[0], "the goal is not met") {
				t.Fatalf("expected a notice explaining the stop, got %q", outcome.notices)
			}
		})
	}
}

func TestQueryStreamDoesNotAskBeforeStopTwiceAtALimit(t *testing.T) {
	// Three short replies in a row, each sent back by the goal, end the query.
	// The goal has just judged the last one, so it is not asked again.
	model, calls := scriptedModel(scriptedTurn{text: "Done.", outputTokens: 5})
	stops := 0
	deps := QueryDeps{
		CallModel: model,
		BeforeStop: func(context.Context, StopRequest) (StopDecision, error) {
			stops++
			return StopDecision{Continue: true, Reason: "tests still fail"}, nil
		},
	}

	outcome, err := runScriptedQuery(t, context.Background(), 4096, deps)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	if *calls != 3 || stops != 3 {
		t.Fatalf("expected three replies each judged once, got %d replies and %d BeforeStop calls", *calls, stops)
	}
	if !slices.Equal(outcome.stopReasons, []string{ContinuationStopDiminishingReturns}) {
		t.Fatalf("expected turn_complete %q, got %q", ContinuationStopDiminishingReturns, outcome.stopReasons)
	}
	if len(outcome.notices) != 1 || !strings.Contains(outcome.notices[0], "tests still fail") {
		t.Fatalf("expected a notice naming what the goal still wants, got %q", outcome.notices)
	}
}

func TestQueryStreamKeepsAGoalLoopThatDoesToolWork(t *testing.T) {
	// Short replies separated by tool calls are a goal loop making progress,
	// not diminishing returns.
	model, calls := scriptedModel(
		scriptedTurn{tool: true, outputTokens: 50}, scriptedTurn{text: "Done.", outputTokens: 5},
		scriptedTurn{tool: true, outputTokens: 50}, scriptedTurn{text: "Done.", outputTokens: 5},
		scriptedTurn{tool: true, outputTokens: 50}, scriptedTurn{text: "Done.", outputTokens: 5},
		scriptedTurn{tool: true, outputTokens: 50}, scriptedTurn{text: "Done.", outputTokens: 5},
		scriptedTurn{tool: true, outputTokens: 50}, scriptedTurn{text: "Done.", outputTokens: 5},
	)
	stops := 0
	deps := QueryDeps{
		CallModel:        model,
		ExecuteToolBatch: answerEveryToolCall,
		BeforeStop: func(context.Context, StopRequest) (StopDecision, error) {
			stops++
			return StopDecision{Continue: stops < 5, Reason: "tests still fail"}, nil
		},
	}

	outcome, err := runScriptedQuery(t, context.Background(), 4096, deps)
	if err != nil {
		t.Fatalf("unexpected stream error: %v", err)
	}
	if *calls != 10 || !slices.Equal(outcome.stopReasons, []string{"end_turn"}) {
		t.Fatalf("expected the goal loop to run its 10 turns to a normal finish, got %d turns and stop reasons %q", *calls, outcome.stopReasons)
	}
}

func TestQueryStreamDoesNotJudgeALimitStopOnceCancelled(t *testing.T) {
	// The goal judge fails open, so asking it on a cancelled context would
	// clear the goal as met. A query cancelled as it reaches its limit ends
	// as cancelled instead.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	model, _ := scriptedModel(scriptedTurn{tool: true, outputTokens: 50})
	batches := 0
	stops := 0
	deps := QueryDeps{
		CallModel: model,
		ExecuteToolBatch: func(ctx context.Context, calls []api.ToolCall) ([]api.ToolResult, error) {
			batches++
			if batches == 50 {
				cancel()
			}
			return answerEveryToolCall(ctx, calls)
		},
		BeforeStop: func(context.Context, StopRequest) (StopDecision, error) {
			stops++
			return StopDecision{}, nil
		},
	}

	_, err := runScriptedQuery(t, ctx, 4096, deps)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected the query to end cancelled, got %v", err)
	}
	if stops != 0 {
		t.Fatalf("BeforeStop ran %d times on a cancelled query", stops)
	}
}
