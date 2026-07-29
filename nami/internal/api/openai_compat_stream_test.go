package api

import (
	"errors"
	"testing"
)

func newTestStreamState() *openAICompatStreamState {
	return &openAICompatStreamState{toolCalls: map[int]*openAICompatToolCallState{}}
}

// collectEvents drains a state emitter into a slice, mimicking a consumer that
// never stops early.
func collectEvents(emit func(func(ModelEvent, error) bool) error) ([]ModelEvent, error) {
	var events []ModelEvent
	err := emit(func(event ModelEvent, _ error) bool {
		events = append(events, event)
		return true
	})
	return events, err
}

func TestApplyToolCallDeltaAssemblesFragmentedArguments(t *testing.T) {
	state := newTestStreamState()

	// Providers stream tool arguments a few characters at a time; the name and
	// id usually arrive only on the first fragment.
	state.applyToolCallDelta(openAICompatDeltaToolCall{
		Index: 0, ID: "call_1", Type: "function",
		Function: openAICompatFunctionCall{Name: "read", Arguments: `{"pa`},
	})
	state.applyToolCallDelta(openAICompatDeltaToolCall{
		Index:    0,
		Function: openAICompatFunctionCall{Arguments: `th":"a.go"}`},
	})

	got := state.toolCalls[0]
	if got.ID != "call_1" || got.Name != "read" {
		t.Fatalf("state = %+v", got)
	}
	if args := got.Arguments.String(); args != `{"path":"a.go"}` {
		t.Fatalf("arguments = %q", args)
	}
}

func TestApplyToolCallDeltaKeepsParallelCallsSeparate(t *testing.T) {
	state := newTestStreamState()

	// Parallel tool calls interleave on the wire, keyed only by index.
	state.applyToolCallDelta(openAICompatDeltaToolCall{Index: 0, ID: "a", Function: openAICompatFunctionCall{Name: "read", Arguments: `{"x`}})
	state.applyToolCallDelta(openAICompatDeltaToolCall{Index: 1, ID: "b", Function: openAICompatFunctionCall{Name: "grep", Arguments: `{"y`}})
	state.applyToolCallDelta(openAICompatDeltaToolCall{Index: 0, Function: openAICompatFunctionCall{Arguments: `":1}`}})
	state.applyToolCallDelta(openAICompatDeltaToolCall{Index: 1, Function: openAICompatFunctionCall{Arguments: `":2}`}})

	if got := state.toolCalls[0].Arguments.String(); got != `{"x":1}` {
		t.Fatalf("call 0 arguments = %q", got)
	}
	if got := state.toolCalls[1].Arguments.String(); got != `{"y":2}` {
		t.Fatalf("call 1 arguments = %q", got)
	}
	if state.toolCalls[0].Name != "read" || state.toolCalls[1].Name != "grep" {
		t.Fatalf("names crossed over: %+v %+v", state.toolCalls[0], state.toolCalls[1])
	}
}

func TestApplyLegacyFunctionDeltaDefaultsToFunctionType(t *testing.T) {
	state := newTestStreamState()
	state.applyLegacyFunctionDelta(openAICompatFunctionCall{Name: "read", Arguments: `{"a":1}`})

	got := state.toolCalls[0]
	if got.Type != "function" {
		t.Fatalf("type = %q, want function", got.Type)
	}
	if got.Name != "read" || got.Arguments.String() != `{"a":1}` {
		t.Fatalf("state = %+v", got)
	}
}

func TestEmitToolCallsSynthesizesMissingIDAndEmptyArguments(t *testing.T) {
	state := newTestStreamState()
	// Some providers omit the id entirely and send no arguments for a
	// zero-parameter tool.
	state.applyToolCallDelta(openAICompatDeltaToolCall{Index: 3, Function: openAICompatFunctionCall{Name: "now"}})

	events, err := collectEvents(state.emitToolCalls)
	if err != nil {
		t.Fatalf("emitToolCalls: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("events = %+v, want 1", events)
	}
	call := events[0].ToolCall
	if call.ID != "call_3" {
		t.Fatalf("id = %q, want a synthesized call_3", call.ID)
	}
	if call.Input != "{}" {
		t.Fatalf("input = %q, want {}", call.Input)
	}
	// Emitted calls are consumed so a later stop event cannot re-send them.
	if len(state.toolCalls) != 0 {
		t.Fatalf("tool calls not drained: %+v", state.toolCalls)
	}
}

func TestEmitToolCallsRejectsMalformedArguments(t *testing.T) {
	state := newTestStreamState()
	state.applyToolCallDelta(openAICompatDeltaToolCall{
		Index: 0, ID: "call_1",
		Function: openAICompatFunctionCall{Name: "read", Arguments: `{"truncated`},
	})

	if _, err := collectEvents(state.emitToolCalls); err == nil {
		t.Fatal("expected an error for truncated tool arguments")
	}
}

func TestEmitStopFlushesPendingToolCalls(t *testing.T) {
	state := newTestStreamState()
	state.applyToolCallDelta(openAICompatDeltaToolCall{
		Index: 0, ID: "call_1",
		Function: openAICompatFunctionCall{Name: "read", Arguments: `{"a":1}`},
	})

	// emitStop always terminates the stream, so errStopStream is the success path.
	events, err := collectEvents(state.emitStop)
	if err != nil && !errors.Is(err, errStopStream) {
		t.Fatalf("emitStop: %v", err)
	}
	if len(events) != 2 {
		t.Fatalf("events = %+v, want tool call then stop", events)
	}
	if events[0].Type != ModelEventToolCall {
		t.Fatalf("first event = %+v, want the flushed tool call", events[0])
	}
	// A stream that ends with unflushed calls stopped to run tools.
	if events[1].StopReason != "tool_use" {
		t.Fatalf("stop reason = %q, want tool_use", events[1].StopReason)
	}
}

func TestEmitStopDefaultsToEndTurn(t *testing.T) {
	state := newTestStreamState()
	events, err := collectEvents(state.emitStop)
	if err != nil && !errors.Is(err, errStopStream) {
		t.Fatalf("emitStop: %v", err)
	}
	if len(events) != 1 || events[0].StopReason != "end_turn" {
		t.Fatalf("events = %+v, want a single end_turn stop", events)
	}
}

func TestEmitStopIsIdempotent(t *testing.T) {
	state := newTestStreamState()
	if _, err := collectEvents(state.emitStop); err != nil && !errors.Is(err, errStopStream) {
		t.Fatalf("first emitStop: %v", err)
	}

	// A second stop must not double-emit; both the SSE [DONE] sentinel and the
	// finish_reason path can reach this.
	events, err := collectEvents(state.emitStop)
	if err != nil {
		t.Fatalf("second emitStop: %v", err)
	}
	if len(events) != 0 {
		t.Fatalf("second emitStop produced %+v, want nothing", events)
	}
}
