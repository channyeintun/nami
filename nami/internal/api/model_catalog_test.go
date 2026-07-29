package api

import (
	"strings"
	"testing"
)

// Every curated preset is what the model picker offers, so each one has to name
// a provider that exists and a model the capability lookup can resolve.
func TestCuratedModelCatalogEntriesAreResolvable(t *testing.T) {
	seen := make(map[string]struct{}, len(CuratedModelCatalog))
	for _, preset := range CuratedModelCatalog {
		if preset.Label == "" || preset.ModelID == "" || preset.Family == "" {
			t.Errorf("incomplete preset: %+v", preset)
		}
		if _, ok := ProviderSpecFor(preset.ProviderID); !ok {
			t.Errorf("preset %q names unknown provider %q", preset.ModelID, preset.ProviderID)
		}
		key := preset.ProviderID + "/" + preset.ModelID
		if _, duplicate := seen[key]; duplicate {
			t.Errorf("duplicate preset %q", key)
		}
		seen[key] = struct{}{}

		if capabilities := ResolveModelCapabilities(preset.ProviderID, preset.ModelID); capabilities.MaxContextWindow <= 0 {
			t.Errorf("preset %q resolved no context window", preset.ModelID)
		}
	}
}

func TestCuratedModelCatalogOffersCurrentSeries(t *testing.T) {
	required := []string{
		"claude-sonnet-5",
		"claude-opus-5",
		"claude-fable-5",
		"gpt-5.6",
		"gpt-5.6-terra",
		"gpt-5.6-luna",
	}
	for _, modelID := range required {
		found := false
		for _, preset := range CuratedModelCatalog {
			if preset.ModelID == modelID {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("curated catalog is missing %q", modelID)
		}
	}
}

// The expensive presets carry a cost warning the picker renders; the cheaper
// ones must not, or the warning stops meaning anything.
func TestCuratedModelCatalogFlagsExpensivePresets(t *testing.T) {
	expensive := map[string]bool{
		"claude-opus-5":    true,
		"claude-fable-5":   true,
		"gpt-5.6":          true,
		"claude-sonnet-5":  false,
		"gpt-5.6-luna":     false,
		"claude-haiku-4-5": false,
	}
	for _, preset := range CuratedModelCatalog {
		want, tracked := expensive[preset.ModelID]
		if !tracked {
			continue
		}
		if got := preset.CostWarningLabel != ""; got != want {
			t.Errorf("preset %q cost warning = %v, want %v", preset.ModelID, got, want)
		}
	}
}

func TestModelCatalogCoversCurrentSeries(t *testing.T) {
	cases := map[string]struct {
		family    string
		context   int
		maxOutput int
	}{
		"claude-fable-5":  {"claude", 1000000, 128000},
		"claude-opus-5":   {"claude", 1000000, 128000},
		"claude-sonnet-5": {"claude", 1000000, 128000},
		"gpt-5.6":         {"gpt", 1050000, 128000},
		"gpt-5.6-terra":   {"gpt", 1050000, 128000},
		"gpt-5.6-sol":     {"gpt", 1050000, 128000},
		"gpt-5.6-luna":    {"gpt", 1050000, 128000},
	}
	for modelID, want := range cases {
		spec, ok := ModelSpecFor(modelID)
		if !ok {
			t.Errorf("ModelCatalog is missing %q", modelID)
			continue
		}
		if spec.Family != want.family {
			t.Errorf("%q family = %q, want %q", modelID, spec.Family, want.family)
		}
		if spec.Capabilities.MaxContextWindow != want.context {
			t.Errorf("%q context = %d, want %d", modelID, spec.Capabilities.MaxContextWindow, want.context)
		}
		if spec.Capabilities.MaxOutputTokens != want.maxOutput {
			t.Errorf("%q max output = %d, want %d", modelID, spec.Capabilities.MaxOutputTokens, want.maxOutput)
		}
		if !spec.Capabilities.SupportsToolUse {
			t.Errorf("%q does not report tool use", modelID)
		}
	}
}

// The Claude 5 models all support extended thinking and prompt caching; the
// agent gates behaviour on both.
func TestClaudeFiveCapabilities(t *testing.T) {
	for _, modelID := range []string{"claude-fable-5", "claude-opus-5", "claude-sonnet-5"} {
		spec, ok := ModelSpecFor(modelID)
		if !ok {
			t.Fatalf("ModelCatalog is missing %q", modelID)
		}
		if !spec.Capabilities.SupportsExtendedThinking {
			t.Errorf("%q does not report extended thinking", modelID)
		}
		if !spec.Capabilities.SupportsCaching {
			t.Errorf("%q does not report caching", modelID)
		}
		if !spec.Capabilities.SupportsVision {
			t.Errorf("%q does not report vision", modelID)
		}
	}
}

// Every provider default has to be a model that still exists — a retired
// default 404s on the first request of a session that did not pick a model.
func TestProviderDefaultModelsAreCurrent(t *testing.T) {
	retired := []string{"claude-sonnet-4-20250514", "claude-3-opus", "claude-3-5-sonnet"}
	for providerID, spec := range ProviderSpecs {
		if strings.TrimSpace(spec.DefaultModel) == "" {
			t.Errorf("provider %q has no default model", providerID)
			continue
		}
		for _, dead := range retired {
			if strings.Contains(spec.DefaultModel, dead) {
				t.Errorf("provider %q defaults to retired model %q", providerID, spec.DefaultModel)
			}
		}
	}
}

func TestAnthropicDefaultsToCurrentSonnet(t *testing.T) {
	spec, ok := ProviderSpecFor("anthropic")
	if !ok {
		t.Fatal("anthropic provider is missing")
	}
	if spec.DefaultModel != "claude-sonnet-5" {
		t.Fatalf("anthropic default = %q, want claude-sonnet-5", spec.DefaultModel)
	}
	if _, ok := ModelSpecFor(spec.DefaultModel); !ok {
		t.Fatalf("anthropic default %q has no catalog entry", spec.DefaultModel)
	}
}

// xhigh reasoning effort is a GPT 5.2+ feature; a new release that is not
// listed silently gets clamped down to high.
func TestGPTFiveSixSupportsXHighReasoning(t *testing.T) {
	for _, model := range []string{"gpt-5.6", "gpt-5.6-terra", "gpt-5.6-sol", "gpt-5.6-luna", "openai/gpt-5.6"} {
		if !SupportsOpenAIReasoningEffort(model) {
			t.Errorf("%q does not report reasoning-effort support", model)
		}
		if !SupportsXHighReasoningEffort(model) {
			t.Errorf("%q does not report xhigh support", model)
		}
		if got := ClampReasoningEffort(model, ReasoningEffortXHigh); got != ReasoningEffortXHigh {
			t.Errorf("ClampReasoningEffort(%q, xhigh) = %q", model, got)
		}
	}
}
