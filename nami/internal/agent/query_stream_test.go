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
