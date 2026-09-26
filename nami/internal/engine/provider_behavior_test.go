package engine

import "testing"

// A subagent model named without a provider must run on the provider its name
// or the session implies, never on an anthropic default that a missing prefix
// used to turn into.
func TestResolveSubagentSelection(t *testing.T) {
	tests := []struct {
		name            string
		selection       string
		activeProvider  string
		defaultProvider string
		wantProvider    string
		wantModel       string
	}{
		{name: "explicit prefix wins", selection: "openai/gpt-5", activeProvider: "anthropic", defaultProvider: "github-copilot", wantProvider: "openai", wantModel: "gpt-5"},
		{name: "default provider applies to a bare model", selection: "claude-opus-4", activeProvider: "github-copilot", defaultProvider: "github-copilot", wantProvider: "github-copilot", wantModel: "claude-opus-4"},
		{name: "configured provider applies to a bare model", selection: "gpt-5", activeProvider: "anthropic", defaultProvider: "openai", wantProvider: "openai", wantModel: "gpt-5"},
		{name: "bare model infers its provider", selection: "gpt-5", activeProvider: "anthropic", wantProvider: "openai", wantModel: "gpt-5"},
		{name: "bare model stays on codex", selection: "gpt-5-mini", activeProvider: "codex", wantProvider: "codex", wantModel: "gpt-5-mini"},
		{name: "unknown model keeps the active provider", selection: "my-local-model", activeProvider: "ollama", wantProvider: "ollama", wantModel: "my-local-model"},
		{name: "empty selection", selection: "  ", activeProvider: "anthropic", wantProvider: "", wantModel: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, model := resolveSubagentSelection(tt.selection, tt.activeProvider, tt.defaultProvider)
			if provider != tt.wantProvider || model != tt.wantModel {
				t.Fatalf("resolveSubagentSelection(%q, %q, %q) = (%q, %q), want (%q, %q)",
					tt.selection, tt.activeProvider, tt.defaultProvider, provider, model, tt.wantProvider, tt.wantModel)
			}
		})
	}
}

// Copilot and Codex keep a bare model on the session's provider. With no
// provider to keep, the model's own name decides, as for any other provider.
func TestResolveSelectionRetainingProvider(t *testing.T) {
	tests := []struct {
		name             string
		input            string
		fallbackProvider string
		wantProvider     string
		wantModel        string
	}{
		{name: "explicit prefix wins", input: "openai/gpt-5", fallbackProvider: "github-copilot", wantProvider: "openai", wantModel: "gpt-5"},
		{name: "bare model keeps the fallback", input: "claude-opus-4", fallbackProvider: "github-copilot", wantProvider: "github-copilot", wantModel: "claude-opus-4"},
		{name: "no fallback infers the provider", input: "gpt-5", fallbackProvider: "", wantProvider: "openai", wantModel: "gpt-5"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, model := resolveSelectionRetainingProvider(tt.input, tt.fallbackProvider)
			if provider != tt.wantProvider || model != tt.wantModel {
				t.Fatalf("resolveSelectionRetainingProvider(%q, %q) = (%q, %q), want (%q, %q)",
					tt.input, tt.fallbackProvider, provider, model, tt.wantProvider, tt.wantModel)
			}
		})
	}
}
