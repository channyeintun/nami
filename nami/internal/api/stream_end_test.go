package api

import (
	"errors"
	"testing"
)

// Each stream is cut off before its provider's final event, the way a proxy
// or load balancer that closes the connection cleanly leaves it.
func TestStreamsReportAResponseCutOffBeforeItsFinalEvent(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		newClient func(baseURL string) (LLMClient, error)
	}{
		{
			name: "anthropic",
			body: `event: message_start
data: {"type":"message_start","message":{"usage":{"input_tokens":10,"output_tokens":1}}}

event: content_block_start
data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}

event: content_block_delta
data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"The answer is"}}

`,
			newClient: func(baseURL string) (LLMClient, error) {
				return NewAnthropicClientForProvider("anthropic", "claude-sonnet-5", "key", baseURL)
			},
		},
		{
			name: "openai-compatible",
			body: `data: {"choices":[{"delta":{"content":"The answer is"}}]}

`,
			newClient: func(baseURL string) (LLMClient, error) {
				return NewOpenAICompatClient("deepseek", "deepseek-v4-flash", "key", baseURL)
			},
		},
		{
			name: "responses",
			body: `data: {"type":"response.output_item.added","item":{"type":"message"}}

data: {"type":"response.output_text.delta","delta":"The answer is"}

`,
			newClient: func(baseURL string) (LLMClient, error) {
				return NewOpenAIResponsesClient("openai", "gpt-5.5", "key", baseURL)
			},
		},
		{
			name: "gemini",
			body: `data: {"candidates":[{"content":{"parts":[{"text":"The answer is"}],"role":"model"}}]}

`,
			newClient: func(baseURL string) (LLMClient, error) {
				return NewGeminiClient("gemini-2.5-pro", "key", baseURL)
			},
		},
		{
			name: "ollama",
			body: `{"message":{"role":"assistant","content":"The answer is"},"done":false}
`,
			newClient: func(baseURL string) (LLMClient, error) {
				return NewOllamaClient("gemma4-e4b", "", baseURL)
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client, err := tc.newClient(serveStream(t, tc.body).URL)
			if err != nil {
				t.Fatalf("new client: %v", err)
			}
			// A partial reply taken as complete would end the turn on half an
			// answer, so the stream has to fail instead.
			events, err := streamOutcome(t, client)
			apiErr, ok := errors.AsType[*APIError](err)
			if !ok || apiErr.Type != ErrNetwork {
				t.Fatalf("err = %v after %v, want a network error for the incomplete stream", err, eventTypes(events))
			}
			if got := stopReasonOf(events); got != "" {
				t.Fatalf("stop reason = %q, want none for a cut-off stream", got)
			}
		})
	}
}
