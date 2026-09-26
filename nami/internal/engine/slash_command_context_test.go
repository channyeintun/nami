package engine

import (
	"testing"

	commandspkg "github.com/channyeintun/nami/internal/commands"
	"github.com/channyeintun/nami/internal/modelselection"
)

// A curated preset may be reached through another provider only if that
// provider serves the model; otherwise the picker offers, say, GPT through
// Anthropic and the selected model fails on its first request.
func TestAppendCuratedModelSelectionOptionsUsesProvidersThatServeTheModel(t *testing.T) {
	tests := []struct {
		name             string
		currentSelection string
		providers        []commandspkg.ProviderStatus
		want             map[string]string // model -> provider its first option uses
	}{
		{
			name:             "on anthropic",
			currentSelection: "anthropic/claude-sonnet-5",
			providers: []commandspkg.ProviderStatus{
				{ID: "anthropic", Label: "Anthropic", Usable: true},
				{ID: "openai", Label: "OpenAI", Usable: true},
				{ID: "gemini", Label: "Gemini"},
			},
			want: map[string]string{
				"claude-sonnet-5":        "anthropic",
				"gpt-5.4":                "openai",
				"gemini-3.1-pro-preview": "gemini",
			},
		},
		{
			// Copilot serves both Claude and GPT models, and the session's
			// own provider is preferred when it can serve the preset.
			name:             "on github copilot",
			currentSelection: "github-copilot/gpt-5.4",
			providers: []commandspkg.ProviderStatus{
				{ID: "github-copilot", Label: "GitHub Copilot", Usable: true},
				{ID: "anthropic", Label: "Anthropic", Usable: true},
				{ID: "openai", Label: "OpenAI"},
				{ID: "gemini", Label: "Gemini", Usable: true},
			},
			want: map[string]string{
				"claude-sonnet-5":        "github-copilot",
				"gpt-5.4":                "github-copilot",
				"gemini-3.1-pro-preview": "gemini",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			options := appendCuratedModelSelectionOptions(nil, commandspkg.ProviderSnapshot{Providers: tt.providers}, tt.currentSelection)
			offered := make(map[string][]string)
			for _, option := range options {
				if option.Provider != option.DisplayProvider && !modelselection.IsModelCompatibleWithProvider(option.Model, option.Provider) {
					t.Errorf("%q offers %s through %s, which does not serve it", option.Label, option.Model, option.Provider)
				}
				offered[option.Model] = append(offered[option.Model], option.Provider)
			}
			// Usable routes are listed before ones that still need setup.
			for model, wantProvider := range tt.want {
				if got := offered[model]; len(got) == 0 || got[0] != wantProvider {
					t.Errorf("%s offered through %v, want %s first", model, got, wantProvider)
				}
			}
		})
	}
}
