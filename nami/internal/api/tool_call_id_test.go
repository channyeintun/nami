package api

import (
	"testing"
)

// Gemini usually leaves functionCall.id out, and Ollama's chat API has no call
// ids at all.
const geminiUnnamedCallsStream = `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"read","args":{"path":"a.go"}}},{"functionCall":{"name":"read","args":{"path":"b.go"}}}],"role":"model"},"finishReason":"STOP"}]}

`

const ollamaUnnamedCallsStream = `{"message":{"role":"assistant","tool_calls":[{"function":{"name":"read","arguments":{"path":"a.go"}}},{"function":{"name":"read","arguments":{"path":"b.go"}}}]},"done":false}
{"message":{"role":"assistant"},"done":true,"done_reason":"stop"}
`

func TestUnnamedToolCallsGetIDsUniqueAcrossTheSession(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		newClient func(baseURL string) (LLMClient, error)
	}{
		{
			name: "gemini",
			body: geminiUnnamedCallsStream,
			newClient: func(baseURL string) (LLMClient, error) {
				return NewGeminiClient("gemini-2.5-pro", "key", baseURL)
			},
		},
		{
			name: "ollama",
			body: ollamaUnnamedCallsStream,
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
			// The engine and the UI track calls by id for the whole session:
			// two calls to the same tool, in one response or in the next,
			// must not share one.
			seen := make(map[string]bool)
			for range 2 {
				events := drainStream(t, client, ModelRequest{Messages: []Message{{Role: RoleUser, Content: "read both"}}})
				ids := toolCallIDs(events)
				if len(ids) != 2 {
					t.Fatalf("tool calls = %v, want two", ids)
				}
				for _, id := range ids {
					if id == "" || id == "read" || seen[id] {
						t.Fatalf("tool call id %q is empty, the tool name, or reused (seen %v)", id, seen)
					}
					seen[id] = true
				}
			}
		})
	}
}

func TestToolCallsWithoutArgumentsCarryAnEmptyObject(t *testing.T) {
	cases := []struct {
		name      string
		body      string
		newClient func(baseURL string) (LLMClient, error)
	}{
		{
			name: "gemini",
			body: `data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"now"}}],"role":"model"},"finishReason":"STOP"}]}

`,
			newClient: func(baseURL string) (LLMClient, error) {
				return NewGeminiClient("gemini-2.5-pro", "key", baseURL)
			},
		},
		{
			name: "ollama",
			body: `{"message":{"role":"assistant","tool_calls":[{"function":{"name":"now"}}]},"done":true,"done_reason":"stop"}
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
			events := drainStream(t, client, ModelRequest{Messages: []Message{{Role: RoleUser, Content: "what time is it"}}})
			var calls []*ToolCall
			for _, event := range events {
				if event.Type == ModelEventToolCall {
					calls = append(calls, event.ToolCall)
				}
			}
			// The input is replayed to whichever provider the session uses
			// next, and Anthropic rejects a tool_use input that is not an
			// object.
			if len(calls) != 1 || calls[0].Input != "{}" {
				t.Fatalf("tool calls = %+v, want one with input {}", calls)
			}
		})
	}
}

func TestGeminiRequestMapsSynthesizedIDsBackToToolNames(t *testing.T) {
	// A replayed call and its result are matched by id, and Gemini needs the
	// function name on the response.
	contents, _, err := buildGeminiContents("gemini-2.5-pro", "", []Message{
		{Role: RoleUser, Content: "read a.go"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "call_1", Name: "read", Input: `{"path":"a.go"}`}}},
		{Role: RoleTool, ToolResult: &ToolResult{ToolCallID: "call_1", Output: "package a"}},
	})
	if err != nil {
		t.Fatalf("buildGeminiContents: %v", err)
	}
	response := contents[len(contents)-1].Parts[0].FunctionResponse
	if response == nil || response.Name != "read" || response.ID != "call_1" {
		t.Fatalf("function response = %+v, want name read and id call_1", response)
	}
}
