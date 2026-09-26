package api

import (
	"slices"
	"testing"
)

const anthropicUsageStream = `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":100,"cache_read_input_tokens":50,"cache_creation_input_tokens":20,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}

event: content_block_stop
data: {"type":"content_block_stop","index":0}

event: message_delta
data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":12}}

event: message_stop
data: {"type":"message_stop"}

`

// Gemini repeats usageMetadata on every chunk, with the prompt count each time.
const geminiUsageStream = `data: {"candidates":[{"content":{"parts":[{"text":"Once"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":8,"totalTokenCount":8}}

data: {"candidates":[{"content":{"parts":[{"text":" upon"}],"role":"model"}}],"usageMetadata":{"promptTokenCount":8,"totalTokenCount":8}}

data: {"candidates":[{"content":{"parts":[{"text":" a time"}],"role":"model"},"finishReason":"STOP"}],"usageMetadata":{"promptTokenCount":8,"candidatesTokenCount":5,"totalTokenCount":13}}

`

// Some OpenAI-compatible servers attach running usage to every chunk.
const openAICompatUsageStream = `data: {"choices":[{"delta":{"content":"Hi"}}],"usage":{"prompt_tokens":10,"completion_tokens":1,"total_tokens":11}}

data: {"choices":[{"delta":{"content":" there"},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":2,"total_tokens":12}}

data: [DONE]

`

// OpenAI sends usage on its own chunk after the finish chunk, and counts the
// cached prefix inside prompt_tokens.
const openAIUsageStream = `data: {"choices":[{"delta":{"content":"Hi"}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}]}

data: {"choices":[],"usage":{"prompt_tokens":1200,"completion_tokens":30,"total_tokens":1230,"prompt_tokens_details":{"cached_tokens":1000}}}

data: [DONE]

`

func TestOpenAICompatStreamFinishesWithoutDone(t *testing.T) {
	// Some servers close the stream after the finish chunk and never send
	// [DONE]. The response is complete, so its tool calls, usage and stop
	// still have to come through.
	body := `data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"read","arguments":"{\"path\":\"a.go\"}"}}]}}]}

data: {"choices":[{"delta":{},"finish_reason":"stop"}],"usage":{"prompt_tokens":10,"completion_tokens":5}}

`
	client, err := NewOpenAICompatClient("deepseek", "deepseek-v4-flash", "key", serveStream(t, body).URL)
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	events := drainStream(t, client, ModelRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})

	want := []ModelEventType{ModelEventToolCall, ModelEventUsage, ModelEventStop}
	if got := eventTypes(events); !slices.Equal(got, want) {
		t.Fatalf("event types = %v, want %v", got, want)
	}
	if got := events[1].Usage; *got != (Usage{InputTokens: 10, OutputTokens: 5}) {
		t.Fatalf("usage = %+v", got)
	}
}

func TestStreamsReportUsageOncePerCall(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		newClient func(baseURL string) (LLMClient, error)
		want      Usage
	}{
		{
			name: "anthropic",
			body: anthropicUsageStream,
			newClient: func(baseURL string) (LLMClient, error) {
				return NewAnthropicClientForProvider("anthropic", "claude-sonnet-5", "key", baseURL)
			},
			want: Usage{InputTokens: 100, OutputTokens: 12, CacheReadTokens: 50, CacheCreationTokens: 20},
		},
		{
			name: "gemini",
			body: geminiUsageStream,
			newClient: func(baseURL string) (LLMClient, error) {
				return NewGeminiClient("gemini-2.5-pro", "key", baseURL)
			},
			want: Usage{InputTokens: 8, OutputTokens: 5},
		},
		{
			name: "openai-compatible",
			body: openAICompatUsageStream,
			newClient: func(baseURL string) (LLMClient, error) {
				return NewOpenAICompatClient("deepseek", "deepseek-v4-flash", "key", baseURL)
			},
			want: Usage{InputTokens: 10, OutputTokens: 2},
		},
		{
			name: "openai",
			body: openAIUsageStream,
			newClient: func(baseURL string) (LLMClient, error) {
				return NewOpenAICompatClient("openai", "gpt-5.4", "key", baseURL)
			},
			// The cached prefix is billed at the cache-read rate, so it is
			// reported apart from the rest of the prompt.
			want: Usage{InputTokens: 200, OutputTokens: 30, CacheReadTokens: 1000},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := serveStream(t, tc.body)
			client, err := tc.newClient(server.URL)
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			events := drainStream(t, client, ModelRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})

			// The cost tracker adds up every usage event of a call, so a
			// stream has to report the call's totals exactly once.
			usages := usageEvents(events)
			if len(usages) != 1 {
				t.Fatalf("usage events = %+v, want exactly one", usages)
			}
			if usages[0] != tc.want {
				t.Fatalf("usage = %+v, want %+v", usages[0], tc.want)
			}

			// The totals are only known at the end, so they arrive just
			// before the stop event.
			types := eventTypes(events)
			usageAt := slices.Index(types, ModelEventUsage)
			if usageAt != len(types)-2 || types[len(types)-1] != ModelEventStop {
				t.Fatalf("event types = %v, want usage then stop at the end", types)
			}
		})
	}
}
