package catalog

import (
	"context"
	"errors"
	"testing"

	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/modelsdev"
)

type stubLoader struct {
	snapshot modelsdev.Snapshot
	err      error
	calls    int
}

func (l *stubLoader) Load(context.Context) (modelsdev.Snapshot, error) {
	l.calls++
	return l.snapshot, l.err
}

func sampleSnapshot() modelsdev.Snapshot {
	return modelsdev.Snapshot{
		Providers: map[string]modelsdev.Provider{
			"anthropic": {
				ID:   "anthropic",
				Name: "Anthropic",
				API:  "https://api.anthropic.com",
				Env:  []string{"ANTHROPIC_API_KEY"},
				Models: map[string]modelsdev.Model{
					"claude-opus-5": {
						ID:     "claude-opus-5",
						Name:   "Claude Opus 5",
						Family: "claude",
						Limit:  modelsdev.Limit{Context: 200000, Output: 64000},
					},
					"claude-alpha": {
						ID:     "claude-alpha",
						Name:   "Claude Alpha",
						Status: "alpha",
					},
				},
			},
			"openai": {
				ID:   "openai",
				Name: "OpenAI",
				Models: map[string]modelsdev.Model{
					"gpt-5": {ID: "gpt-5", Name: "GPT-5"},
				},
			},
		},
	}
}

func TestSnapshotWithoutLoader(t *testing.T) {
	var service *Service
	got, err := service.Snapshot(context.Background(), config.Config{})
	if err != nil || len(got.Providers) != 0 {
		t.Fatalf("Snapshot on a nil service = %+v, %v", got, err)
	}

	empty := NewService(nil)
	got, err = empty.Snapshot(context.Background(), config.Config{})
	if err != nil || len(got.Providers) != 0 {
		t.Fatalf("Snapshot without a loader = %+v, %v", got, err)
	}
}

func TestSnapshotPropagatesLoaderError(t *testing.T) {
	service := NewService(&stubLoader{err: errors.New("network down")})
	if _, err := service.Snapshot(context.Background(), config.Config{}); err == nil {
		t.Fatal("Snapshot swallowed the loader error")
	}
}

func TestSnapshotBuildsProvidersFromCatalog(t *testing.T) {
	service := NewService(&stubLoader{snapshot: sampleSnapshot()})
	snapshot, err := service.Snapshot(context.Background(), config.Config{})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(snapshot.Providers) == 0 {
		t.Fatal("Snapshot returned no providers")
	}

	anthropic, ok := providerForSnapshot(snapshot, "anthropic")
	if !ok {
		t.Fatal("anthropic missing from the snapshot")
	}
	if anthropic.Name != "Anthropic" {
		t.Errorf("name = %q", anthropic.Name)
	}
	if anthropic.Source != ProviderSourceModelsDev {
		t.Errorf("source = %q", anthropic.Source)
	}
	if len(anthropic.EnvKeys) == 0 || anthropic.EnvKeys[0] != "ANTHROPIC_API_KEY" {
		t.Errorf("env keys = %#v", anthropic.EnvKeys)
	}
	if _, found := modelForProvider(anthropic, "claude-alpha"); found {
		t.Error("alpha models must not appear in the catalog")
	}
	if _, found := modelForProvider(anthropic, "claude-opus-5"); !found {
		t.Error("active model missing from the provider")
	}
	if snapshot.Defaults["anthropic"] == "" {
		t.Error("provider default model was not recorded")
	}
}

func TestSnapshotOrdersProvidersDeterministically(t *testing.T) {
	service := NewService(&stubLoader{snapshot: sampleSnapshot()})
	first, err := service.Snapshot(context.Background(), config.Config{})
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	for range 10 {
		next, err := service.Snapshot(context.Background(), config.Config{})
		if err != nil {
			t.Fatalf("Snapshot: %v", err)
		}
		if len(next.Providers) != len(first.Providers) {
			t.Fatalf("provider count changed between calls")
		}
		for i := range first.Providers {
			if next.Providers[i].ID != first.Providers[i].ID {
				t.Fatalf("provider order changed: %q vs %q", next.Providers[i].ID, first.Providers[i].ID)
			}
		}
	}
}

func TestSnapshotIncludesConfigOnlyProviders(t *testing.T) {
	cfg := config.Config{
		Providers: map[string]config.ProviderOverride{
			"my-gateway": {BaseURL: "https://gateway.internal", APIKeyEnv: "GATEWAY_KEY", DefaultModel: "custom-1"},
		},
	}
	service := NewService(&stubLoader{snapshot: sampleSnapshot()})
	snapshot, err := service.Snapshot(context.Background(), cfg)
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}

	provider, ok := providerForSnapshot(snapshot, "my-gateway")
	if !ok {
		t.Fatal("config-only provider missing from the snapshot")
	}
	if provider.Source != ProviderSourceConfig {
		t.Errorf("source = %q, want config", provider.Source)
	}
	if provider.BaseURL != "https://gateway.internal" {
		t.Errorf("base url = %q", provider.BaseURL)
	}
	if provider.DefaultModel != "custom-1" {
		t.Errorf("default model = %q", provider.DefaultModel)
	}
}

func TestRouteResolvesModel(t *testing.T) {
	service := NewService(&stubLoader{snapshot: sampleSnapshot()})
	route, err := service.Route(context.Background(), config.Config{}, "anthropic", "claude-opus-5")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if route.ProviderID != "anthropic" || route.ModelID != "claude-opus-5" {
		t.Fatalf("route = %+v", route)
	}
	if route.BaseURL == "" {
		t.Error("route has no base url")
	}
}

func TestRouteFallsBackForUnknownModel(t *testing.T) {
	service := NewService(&stubLoader{snapshot: sampleSnapshot()})
	route, err := service.Route(context.Background(), config.Config{}, "anthropic", "claude-from-the-future")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if route.ModelID != "claude-from-the-future" {
		t.Fatalf("route = %+v, want the requested model kept", route)
	}
}

func TestRouteUsesProviderDefaultForBlankModel(t *testing.T) {
	service := NewService(&stubLoader{snapshot: sampleSnapshot()})
	route, err := service.Route(context.Background(), config.Config{}, "anthropic", "  ")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if route.ModelID == "" {
		t.Fatal("route did not fall back to the provider default")
	}
}

func TestRouteRejectsUnknownProvider(t *testing.T) {
	service := NewService(&stubLoader{snapshot: sampleSnapshot()})
	if _, err := service.Route(context.Background(), config.Config{}, "not-a-provider", "x"); err == nil {
		t.Fatal("Route accepted an unknown provider")
	}
}

func TestRouteForCodexBorrowsOpenAIModels(t *testing.T) {
	service := NewService(&stubLoader{snapshot: sampleSnapshot()})
	route, err := service.Route(context.Background(), config.Config{}, "codex", "gpt-5")
	if err != nil {
		t.Fatalf("Route: %v", err)
	}
	if route.ProviderID != "codex" {
		t.Fatalf("provider = %q, want codex", route.ProviderID)
	}
	if route.ModelID != "gpt-5" {
		t.Fatalf("model = %q", route.ModelID)
	}
}

func TestProviderLookup(t *testing.T) {
	service := NewService(&stubLoader{snapshot: sampleSnapshot()})
	provider, found, err := service.Provider(context.Background(), config.Config{}, " Anthropic ")
	if err != nil {
		t.Fatalf("Provider: %v", err)
	}
	if !found || provider.ID != "anthropic" {
		t.Fatalf("provider = %+v, found = %v", provider, found)
	}

	if _, found, err = service.Provider(context.Background(), config.Config{}, "nope"); err != nil || found {
		t.Fatalf("Provider(nope) = found %v, err %v", found, err)
	}
}

func TestChooseDefaultModel(t *testing.T) {
	models := []Model{{ID: "a"}, {ID: "b"}}
	if got := chooseDefaultModel(models, "b"); got != "b" {
		t.Errorf("chooseDefaultModel = %q, want the preferred model", got)
	}
	if got := chooseDefaultModel(models, "missing"); got != "a" {
		t.Errorf("chooseDefaultModel = %q, want the first model", got)
	}
	if got := chooseDefaultModel(nil, "x"); got != "" {
		t.Errorf("chooseDefaultModel = %q, want empty", got)
	}
}

func TestEnsureModelAddsAndSorts(t *testing.T) {
	models := []Model{{ID: "b", Name: "b"}}
	models = ensureModel(models, "a", Model{ID: "a", Name: "a"})
	if len(models) != 2 || models[0].ID != "a" {
		t.Fatalf("models = %+v, want the new model sorted in", models)
	}
	models = ensureModel(models, "a", Model{ID: "a", Name: "duplicate"})
	if len(models) != 2 {
		t.Fatalf("models = %+v, want no duplicate", models)
	}
	if got := ensureModel(models, "  ", Model{}); len(got) != 2 {
		t.Fatalf("models = %+v, want a blank id ignored", got)
	}
}

func TestProviderBaseURLPrecedence(t *testing.T) {
	cfg := config.Config{
		Provider: "anthropic",
		BaseURL:  "https://session.example",
		Providers: map[string]config.ProviderOverride{
			"anthropic": {BaseURL: "https://override.example"},
		},
	}
	if got := providerBaseURL(cfg, "anthropic", "https://fallback.example"); got != "https://override.example" {
		t.Fatalf("base url = %q, want the provider override to win", got)
	}

	sessionOnly := config.Config{Provider: "anthropic", BaseURL: "https://session.example"}
	if got := providerBaseURL(sessionOnly, "anthropic", "https://fallback.example"); got != "https://session.example" {
		t.Fatalf("base url = %q, want the session value", got)
	}
	if got := providerBaseURL(config.Config{}, "anthropic", "https://fallback.example"); got != "https://fallback.example" {
		t.Fatalf("base url = %q, want the fallback", got)
	}
}

func TestAppendUniqueIgnoresBlanksAndCase(t *testing.T) {
	values := appendUnique(nil, "  KEY  ")
	values = appendUnique(values, "key")
	values = appendUnique(values, "   ")
	if len(values) != 1 || values[0] != "KEY" {
		t.Fatalf("values = %#v", values)
	}
}

func TestIncludeModelStatus(t *testing.T) {
	for _, status := range []string{"", "stable", "preview", "beta"} {
		if !includeModelStatus(status) {
			t.Errorf("includeModelStatus(%q) = false, want true", status)
		}
	}
	for _, status := range []string{"alpha", "deprecated", " DEPRECATED "} {
		if includeModelStatus(status) {
			t.Errorf("includeModelStatus(%q) = true, want false", status)
		}
	}
}

func TestResolveActivePrefersExplicitConfig(t *testing.T) {
	defaults := map[string]string{"anthropic": "claude-opus-5", "openai": "gpt-5"}

	explicit := resolveActive(config.Config{Provider: "Anthropic", Model: "claude-sonnet-5"}, defaults)
	if explicit.ProviderID != "anthropic" || explicit.ModelID != "claude-sonnet-5" {
		t.Fatalf("active = %+v", explicit)
	}

	providerOnly := resolveActive(config.Config{Provider: "anthropic"}, defaults)
	if providerOnly.ModelID != "claude-opus-5" {
		t.Fatalf("active = %+v, want the provider default", providerOnly)
	}

	modelOnly := resolveActive(config.Config{Model: "gpt-5"}, defaults)
	if modelOnly.ProviderID != "openai" {
		t.Fatalf("active = %+v, want the provider inferred from the model", modelOnly)
	}
}

func TestFallbackModelCarriesCapabilities(t *testing.T) {
	model := fallbackModel("anthropic", "claude-opus-5")
	if model.ID != "claude-opus-5" || model.Name != "claude-opus-5" {
		t.Fatalf("model = %+v", model)
	}
	if model.Limits.ContextWindow == 0 && model.Limits.OutputTokens == 0 {
		t.Fatalf("model = %+v, want resolved capability limits", model)
	}
}
