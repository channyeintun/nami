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

func TestBuildRequestAsksOpenAIForStreamedUsage(t *testing.T) {
	build := func(provider string) map[string]any {
		client := &OpenAICompatClient{provider: provider, model: "gpt-5.4"}
		payload, _, err := client.buildRequest(ModelRequest{Messages: []Message{{Role: RoleUser, Content: "hi"}}})
		if err != nil {
			t.Fatalf("%s: buildRequest: %v", provider, err)
		}
		return requestFields(t, payload)
	}

	// OpenAI streams no usage at all unless the request asks for it.
	options, ok := build("openai")["stream_options"].(map[string]any)
	if !ok || options["include_usage"] != true {
		t.Fatalf("openai stream_options = %v, want include_usage", options)
	}
	// Other servers are not sent a field they may not know.
	if options, present := build("mistral")["stream_options"]; present {
		t.Fatalf("mistral stream_options = %v, want none", options)
	}
}
