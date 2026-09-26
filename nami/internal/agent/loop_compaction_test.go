package agent

import (
	"context"
	"iter"
	"testing"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/compact"
)

type fixedSummarizer struct{}

func (fixedSummarizer) Summarize(context.Context, []api.Message) (string, error) {
	return "summary of the earlier work", nil
}

// compactingModel scripts one query: a first reply that ends as given, a
// prompt-too-long rejection that forces compaction, then a final answer. It
// records every request the loop sends.
type compactingModel struct {
	first    []api.ModelEvent
	requests []api.ModelRequest
}

func (m *compactingModel) call(_ context.Context, req api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
	m.requests = append(m.requests, req)
	events := []api.ModelEvent{{Type: api.ModelEventToken, Text: "all done"}, {Type: api.ModelEventStop, StopReason: "end_turn"}}
	switch len(m.requests) {
	case 1:
		events = m.first
	case 2:
		return nil, &api.APIError{Type: api.ErrPromptTooLong, Message: "prompt is too long"}
	}
	return func(yield func(api.ModelEvent, error) bool) {
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}, nil
}

func runCompactingQuery(t *testing.T, model *compactingModel) api.ModelRequest {
	t.Helper()
	pipeline := compact.NewPipeline(200_000, fixedSummarizer{}, false)
	deps := QueryDeps{
		CallModel: model.call,
		ExecuteToolBatch: func(_ context.Context, calls []api.ToolCall) ([]api.ToolResult, error) {
			results := make([]api.ToolResult, 0, len(calls))
			for _, call := range calls {
				results = append(results, api.ToolResult{ToolCallID: call.ID, Output: "fresh tool output"})
			}
			return results, nil
		},
		CompactMessages: func(ctx context.Context, messages []api.Message, reason CompactReason) (compact.CompactResult, error) {
			return pipeline.Compact(ctx, messages, string(reason))
		},
	}
	req := QueryRequest{
		Messages:     []api.Message{{Role: api.RoleUser, Content: "fix the failing build"}},
		Capabilities: api.ModelCapabilities{SupportsToolUse: true},
		Tools:        []api.ToolDefinition{{Name: "bash"}},
	}
	for _, err := range QueryStream(context.Background(), req, deps) {
		if err != nil {
			t.Fatalf("unexpected stream error: %v", err)
		}
	}
	if len(model.requests) != 3 {
		t.Fatalf("expected a first call, a rejected call and its retry, got %d calls", len(model.requests))
	}
	return model.requests[2]
}

// firstTurnRole reports the role of the first message a provider keeps as a
// conversation turn; system messages are folded into the system prompt.
func firstTurnRole(messages []api.Message) api.Role {
	for _, message := range messages {
		if message.Role != api.RoleSystem {
			return message.Role
		}
	}
	return ""
}

func TestCompactionMidToolLoopKeepsTheToolExchange(t *testing.T) {
	model := &compactingModel{first: []api.ModelEvent{
		{Type: api.ModelEventToolCall, ToolCall: &api.ToolCall{ID: "call-1", Name: "bash", Input: `{"command":"go build ./..."}`}},
		{Type: api.ModelEventStop, StopReason: "tool_use"},
	}}
	retry := runCompactingQuery(t, model)

	if role := firstTurnRole(retry.Messages); role != api.RoleUser {
		t.Fatalf("retried request must open with a user turn, got %q in %+v", role, retry.Messages)
	}
	var callIndex, resultIndex = -1, -1
	for i, message := range retry.Messages {
		if message.Role == api.RoleAssistant && len(message.ToolCalls) == 1 && message.ToolCalls[0].ID == "call-1" {
			callIndex = i
		}
		if message.ToolResult != nil && message.ToolResult.ToolCallID == "call-1" && message.ToolResult.Output == "fresh tool output" {
			resultIndex = i
		}
	}
	if callIndex < 0 || resultIndex < 0 || resultIndex < callIndex {
		t.Fatalf("expected the tool call followed by its fresh result to survive compaction, got %+v", retry.Messages)
	}
}

func TestCompactionMidQueryLeavesAUserTurn(t *testing.T) {
	// A reply cut off at max_tokens leaves the transcript ending on an
	// assistant turn, so compaction keeps nothing but its summary.
	model := &compactingModel{first: []api.ModelEvent{
		{Type: api.ModelEventToken, Text: "Here is the first half of a long answer"},
		{Type: api.ModelEventStop, StopReason: "max_tokens"},
	}}
	retry := runCompactingQuery(t, model)

	if role := firstTurnRole(retry.Messages); role != api.RoleUser {
		t.Fatalf("retried request must open with a user turn, got %q in %+v", role, retry.Messages)
	}
}
