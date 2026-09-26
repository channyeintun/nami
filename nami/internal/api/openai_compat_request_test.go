package api

import (
	"encoding/json"
	"testing"
)

// requestFields renders a payload the way it goes on the wire.
func requestFields(t *testing.T, payload any) map[string]any {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	return fields
}

func TestBuildRequestNamesTheOutputCapTheProviderAccepts(t *testing.T) {
	cases := []struct {
		provider string
		model    string
		want     string
		absent   string
	}{
		// OpenAI rejects max_tokens for its reasoning models, the GPT-5 series
		// included, and takes max_completion_tokens for every model.
		{"openai", "gpt-5.4", "max_completion_tokens", "max_tokens"},
		{"openai", "gpt-4.1", "max_completion_tokens", "max_tokens"},
		// The other compatible servers document max_tokens.
		{"deepseek", "deepseek-v4-flash", "max_tokens", "max_completion_tokens"},
		{"mistral", "mistral-large-latest", "max_tokens", "max_completion_tokens"},
		{"github-copilot", "gemini-2.5-pro", "max_tokens", "max_completion_tokens"},
	}
	for _, tc := range cases {
		client := &OpenAICompatClient{provider: tc.provider, model: tc.model}
		payload, _, err := client.buildRequest(ModelRequest{
			Messages:  []Message{{Role: RoleUser, Content: "hi"}},
			MaxTokens: 8000,
		})
		if err != nil {
			t.Fatalf("%s: buildRequest: %v", tc.provider, err)
		}
		fields := requestFields(t, payload)
		if fields[tc.want] != float64(8000) {
			t.Errorf("%s/%s: %s = %v, want 8000", tc.provider, tc.model, tc.want, fields[tc.want])
		}
		if value, present := fields[tc.absent]; present {
			t.Errorf("%s/%s: unexpected %s = %v", tc.provider, tc.model, tc.absent, value)
		}
	}
}
