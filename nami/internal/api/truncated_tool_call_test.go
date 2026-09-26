package api

import (
	"context"
	"strings"
	"testing"
)

// anthropicToolCallStream answers with a text block and then a write tool call
// whose input is cut off, stopping for stopReason.
func anthropicToolCallStream(stopReason string) string {
	return `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"toolu_1","name":"write","input":{}}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"path\":\"a.go\",\"content\":\"package ma"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"` + stopReason + `"},"usage":{"output_tokens":8000}}

event: message_stop
data: {"type":"message_stop"}

`
}

// openAICompatToolCallStream streams one complete call and one whose arguments
// are cut off, finishing for finishReason.
func openAICompatToolCallStream(finishReason string) string {
	return `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"a.go\"}"}}]}}]}

data: {"choices":[{"delta":{"tool_calls":[{"index":1,"id":"call_2","type":"function","function":{"name":"write","arguments":"{\"path\":\"b.go\",\"content\":\"package ma"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"` + finishReason + `"}]}

data: [DONE]

`
}

// streamOutcome runs one request and returns its events and the first error.
func streamOutcome(t *testing.T, client LLMClient) ([]ModelEvent, error) {
	t.Helper()
	stream, err := client.Stream(context.Background(), ModelRequest{Messages: []Message{{Role: RoleUser, Content: "write a.go"}}})
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var events []ModelEvent
	for event, err := range stream {
		if err != nil {
			return events, err
		}
		events = append(events, event)
	}
	return events, nil
}

func toolCallIDs(events []ModelEvent) []string {
	var ids []string
	for _, event := range events {
		if event.Type == ModelEventToolCall {
			ids = append(ids, event.ToolCall.ID)
		}
	}
	return ids
}

func stopReasonOf(events []ModelEvent) string {
	for _, event := range events {
		if event.Type == ModelEventStop {
			return event.StopReason
		}
	}
	return ""
}

func TestStreamsDropAToolCallCutOffByMaxTokens(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		newClient func(baseURL string) (LLMClient, error)
		wantCalls []string
	}{
		{
			name: "anthropic",
			body: anthropicToolCallStream("max_tokens"),
			newClient: func(baseURL string) (LLMClient, error) {
				return NewAnthropicClientForProvider("anthropic", "claude-sonnet-5", "key", baseURL)
			},
		},
		{
			name: "openai-compatible",
			body: openAICompatToolCallStream("length"),
			newClient: func(baseURL string) (LLMClient, error) {
				return NewOpenAICompatClient("deepseek", "deepseek-v4-flash", "key", baseURL)
			},
			// The call that finished before the cutoff can still run.
			wantCalls: []string{"call_1"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := tc.newClient(serveStream(t, tc.body).URL)
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			// The agent answers a max_tokens stop by raising the output budget
			// and trying again, so the cut-off call must not fail the turn.
			events, err := streamOutcome(t, client)
			if err != nil {
				t.Fatalf("stream error: %v", err)
			}
			if got := stopReasonOf(events); got != "max_tokens" {
				t.Fatalf("stop reason = %q, want max_tokens", got)
			}
			if got := toolCallIDs(events); strings.Join(got, ",") != strings.Join(tc.wantCalls, ",") {
				t.Fatalf("tool calls = %v, want %v", got, tc.wantCalls)
			}
		})
	}
}

func TestStreamsStillRejectMalformedToolInputOtherwise(t *testing.T) {
	cases := map[string]struct {
		body      string
		newClient func(baseURL string) (LLMClient, error)
	}{
		"anthropic": {
			body: anthropicToolCallStream("tool_use"),
			newClient: func(baseURL string) (LLMClient, error) {
				return NewAnthropicClientForProvider("anthropic", "claude-sonnet-5", "key", baseURL)
			},
		},
		"openai-compatible": {
			body: openAICompatToolCallStream("tool_calls"),
			newClient: func(baseURL string) (LLMClient, error) {
				return NewOpenAICompatClient("deepseek", "deepseek-v4-flash", "key", baseURL)
			},
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			client, err := tc.newClient(serveStream(t, tc.body).URL)
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			// Broken input on a response that did not run out of tokens is a
			// real fault and must not be passed off as a call to run.
			if _, err := streamOutcome(t, client); err == nil {
				t.Fatal("expected an error for malformed tool input")
			}
		})
	}
}
