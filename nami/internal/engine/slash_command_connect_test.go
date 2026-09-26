package engine

import (
	"os"
	"path/filepath"
	"testing"

	commandspkg "github.com/channyeintun/nami/internal/commands"
	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/session"
)

func TestConnectProviderFromChoice(t *testing.T) {
	tests := []struct {
		name   string
		choice modelSelectionChoice
		want   string
	}{
		// A cancelled picker returns an empty choice. That has to read as
		// "cancelled", not as a request to connect a default provider.
		{name: "cancelled picker", choice: modelSelectionChoice{}, want: ""},
		{name: "blank choice", choice: modelSelectionChoice{Provider: "  ", Model: " "}, want: ""},
		{name: "provider option", choice: modelSelectionChoice{Provider: "openai", Model: "gpt-5"}, want: "openai"},
		{name: "model-only option", choice: modelSelectionChoice{Model: " ollama "}, want: "ollama"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := connectProviderFromChoice(tt.choice); got != tt.want {
				t.Fatalf("connectProviderFromChoice(%+v) = %q, want %q", tt.choice, got, tt.want)
			}
		})
	}
}

// useEmptyModelsCatalog gives the provider catalog an empty but fresh
// models.dev cache, so it lists the built-in providers without a download.
func useEmptyModelsCatalog(t *testing.T) {
	t.Helper()
	cacheHome := t.TempDir()
	t.Setenv("XDG_CACHE_HOME", cacheHome)
	cachePath := filepath.Join(cacheHome, "nami", "models.dev", "api.json")
	if err := os.MkdirAll(filepath.Dir(cachePath), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(cachePath, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write catalog cache: %v", err)
	}
}

// config.json keeps the provider in its own field. /connect saved the joined
// "openai/gpt-5" as the model and left the provider it had loaded -
// "anthropic", from the defaults - so the next start read model id
// "openai/gpt-5" on Anthropic.
func TestConnectSavesTheProviderAndModelApart(t *testing.T) {
	isolateUserConfig(t)
	useEmptyModelsCatalog(t)
	t.Setenv("OPENAI_API_KEY", "test-key")
	t.Setenv("NAMI_PROVIDER", "")
	t.Setenv("NAMI_MODEL", "")
	spec, ok := commandspkg.LookupConnectProvider("openai")
	if !ok {
		t.Fatal("openai is not a connect provider")
	}
	cmd, _ := newTestSlashCommandContext(t, session.NewStore(t.TempDir()), nil, newConversationTimeline())

	result, err := connectStaticProvider(cmd, "openai", "")
	if err != nil || result == nil {
		t.Fatalf("connectStaticProvider = %+v, %v; want a connection", result, err)
	}

	saved := config.LoadUser()
	if saved.Provider != "openai" || saved.Model != spec.DefaultModel {
		t.Fatalf("saved provider %q and model %q, want openai and %q", saved.Provider, saved.Model, spec.DefaultModel)
	}
	if provider, model := commandspkg.ResolveActiveSelection(config.Load()); provider != "openai" || model != spec.DefaultModel {
		t.Fatalf("the next start would use %q on %q, want %q on openai", model, provider, spec.DefaultModel)
	}
}
