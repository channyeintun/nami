package engine

import (
	"testing"

	"github.com/channyeintun/nami/internal/config"
)

func TestResolveSelectedModelChoice(t *testing.T) {
	tests := []struct {
		name            string
		model           string
		providerHint    string
		currentProvider string
		wantProvider    string
		wantModel       string
	}{
		{name: "explicit prefix wins", model: "openai/gpt-5", providerHint: "anthropic", currentProvider: "github-copilot", wantProvider: "openai", wantModel: "gpt-5"},
		{name: "picker provider hint", model: "gpt-5", providerHint: "github-copilot", currentProvider: "anthropic", wantProvider: "github-copilot", wantModel: "gpt-5"},
		// "/model <name>" carries no hint. The provider must come from the
		// model name or the session, never from an implied anthropic default.
		{name: "bare model infers its provider", model: "gpt-5", currentProvider: "anthropic", wantProvider: "openai", wantModel: "gpt-5"},
		{name: "bare model infers its provider on openai", model: "gpt-5", currentProvider: "openai", wantProvider: "openai", wantModel: "gpt-5"},
		{name: "bare model stays on copilot", model: "claude-opus-4", currentProvider: "github-copilot", wantProvider: "github-copilot", wantModel: "claude-opus-4"},
		{name: "bare model stays on codex", model: "gpt-5-codex", currentProvider: "codex", wantProvider: "codex", wantModel: "gpt-5-codex"},
		{name: "unknown model keeps the current provider", model: "my-local-model", currentProvider: "ollama", wantProvider: "ollama", wantModel: "my-local-model"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider, model := resolveSelectedModelChoice(tt.model, tt.providerHint, tt.currentProvider)
			if provider != tt.wantProvider || model != tt.wantModel {
				t.Fatalf("resolveSelectedModelChoice(%q, %q, %q) = (%q, %q), want (%q, %q)",
					tt.model, tt.providerHint, tt.currentProvider, provider, model, tt.wantProvider, tt.wantModel)
			}
		})
	}
}

func TestConfiguredModelChoice(t *testing.T) {
	tests := []struct {
		name    string
		cfg     config.Config
		want    modelSelectionChoice
		wantErr bool
	}{
		{name: "bare model takes the configured provider", cfg: config.Config{Provider: "openai", Model: "gpt-5"}, want: modelSelectionChoice{Model: "gpt-5", Provider: "openai"}},
		{name: "explicit prefix wins over the provider field", cfg: config.Config{Provider: "anthropic", Model: "openai/gpt-5"}, want: modelSelectionChoice{Model: "gpt-5", Provider: "openai"}},
		{name: "no provider leaves the choice to inference", cfg: config.Config{Model: "gpt-5"}, want: modelSelectionChoice{Model: "gpt-5"}},
		{name: "no model is an error", cfg: config.Config{Provider: "openai"}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := configuredModelChoice(tt.cfg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("configuredModelChoice(%+v) = %+v, want an error", tt.cfg, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("configuredModelChoice(%+v): %v", tt.cfg, err)
			}
			if got != tt.want {
				t.Fatalf("configuredModelChoice(%+v) = %+v, want %+v", tt.cfg, got, tt.want)
			}
		})
	}
}
