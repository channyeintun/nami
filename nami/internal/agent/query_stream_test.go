package agent

import (
	"context"
	"iter"
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
