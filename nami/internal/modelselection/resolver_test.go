package modelselection

import "testing"

func TestResolveKeepsExplicitProvider(t *testing.T) {
	got := Resolve("anthropic/claude-sonnet-5", "openai", "cli")
	if got.Resolved.ProviderID != "anthropic" {
		t.Fatalf("provider = %q, want anthropic", got.Resolved.ProviderID)
	}
	if got.Resolved.ModelID != "claude-sonnet-5" {
		t.Fatalf("model = %q, want claude-sonnet-5", got.Resolved.ModelID)
	}
	if got.Reason != "explicit provider" {
		t.Fatalf("reason = %q, want explicit provider", got.Reason)
	}
}

func TestResolveInfersProviderFromBareModel(t *testing.T) {
	cases := map[string]string{
		"claude-opus-5":    "anthropic",
		"gpt-5":            "openai",
		"o3-mini":          "openai",
		"gemini-2.5-pro":   "gemini",
		"deepseek-chat":    "deepseek",
		"qwen3-coder":      "qwen",
		"glm-4.6":          "glm",
		"mistral-large":    "mistral",
		"llama-3.3-70b":    "groq",
		"gemma3":           "ollama",
		"sonnet-latest":    "anthropic",
		"maverick-preview": "groq",
	}
	for model, wantProvider := range cases {
		got := Resolve(model, "", "cli")
		if got.Resolved.ProviderID != wantProvider {
			t.Errorf("Resolve(%q) provider = %q, want %q", model, got.Resolved.ProviderID, wantProvider)
		}
		if got.Reason != "inferred provider from model" {
			t.Errorf("Resolve(%q) reason = %q, want inference", model, got.Reason)
		}
		if got.Resolved.ExplicitProvider {
			t.Errorf("Resolve(%q) marked the inferred provider as explicit", model)
		}
	}
}

// Inference outranks the session's current provider: a model that names its
// vendor is routed there even when the session was on another provider.
func TestResolveInferenceOutranksFallback(t *testing.T) {
	got := Resolve("some-claude-variant", "github-copilot", "session")
	if got.Resolved.ProviderID != "anthropic" {
		t.Fatalf("provider = %q, want anthropic", got.Resolved.ProviderID)
	}
	if got.Reason != "inferred provider from model" {
		t.Fatalf("reason = %q, want inference", got.Reason)
	}
}

func TestResolveFallsBackForUnknownModel(t *testing.T) {
	got := Resolve("some-unknown-model", "ollama", "session")
	if got.Resolved.ProviderID != "ollama" {
		t.Fatalf("provider = %q, want the fallback ollama", got.Resolved.ProviderID)
	}
	if got.Reason != "used fallback provider for unknown model" {
		t.Fatalf("reason = %q, want the fallback reason", got.Reason)
	}
}

func TestResolveNormalizesProviderCasing(t *testing.T) {
	got := Resolve("Anthropic/Claude-Sonnet-5", "", "cli")
	if got.Resolved.ProviderID != "anthropic" {
		t.Fatalf("provider = %q, want lowercased", got.Resolved.ProviderID)
	}
}

func TestResolveHandlesEmptyInput(t *testing.T) {
	got := Resolve("", "anthropic", "cli")
	if got.Resolved.ModelID != "" {
		t.Fatalf("model = %q, want empty", got.Resolved.ModelID)
	}
	if got.Reason != "explicit provider" {
		t.Fatalf("reason = %q", got.Reason)
	}
}

func TestNormalizeProvider(t *testing.T) {
	for input, want := range map[string]string{
		"  Anthropic ": "anthropic",
		"OPENAI":       "openai",
		"":             "",
		"   ":          "",
	} {
		if got := NormalizeProvider(input); got != want {
			t.Errorf("NormalizeProvider(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestInferProviderFromModelReturnsEmptyForUnknown(t *testing.T) {
	for _, model := range []string{"", "   ", "totally-made-up", "gpt"} {
		got := InferProviderFromModel(model)
		if model == "gpt" {
			if got != "openai" {
				t.Errorf("InferProviderFromModel(%q) = %q, want openai", model, got)
			}
			continue
		}
		if got != "" {
			t.Errorf("InferProviderFromModel(%q) = %q, want empty", model, got)
		}
	}
}

func TestIsModelCompatibleWithProvider(t *testing.T) {
	compatible := []struct {
		model    string
		provider string
	}{
		{"claude-opus-5", "anthropic"},
		{"gpt-5-codex", "codex"},
		{"o4-mini", "openai"},
		{"claude-sonnet-5", "github-copilot"},
		{"gpt-5", "github-copilot"},
		{"gemini-2.5-flash", "gemini"},
		{"deepseek-r1", "deepseek"},
		{"qwen3", "qwen"},
		{"glm-4.6", "glm"},
		{"mistral-small", "mistral"},
		{"llama-4", "groq"},
		{"gemma3:4b", "ollama"},
	}
	for _, tc := range compatible {
		if !IsModelCompatibleWithProvider(tc.model, tc.provider) {
			t.Errorf("IsModelCompatibleWithProvider(%q, %q) = false, want true", tc.model, tc.provider)
		}
	}

	incompatible := []struct {
		model    string
		provider string
	}{
		{"claude-opus-5", "openai"},
		{"gpt-5", "anthropic"},
		{"gemini-2.5-pro", "groq"},
		{"anything", "unknown-provider"},
		{"", "anthropic"},
	}
	for _, tc := range incompatible {
		if IsModelCompatibleWithProvider(tc.model, tc.provider) {
			t.Errorf("IsModelCompatibleWithProvider(%q, %q) = true, want false", tc.model, tc.provider)
		}
	}
}

// A model the resolver claims for a provider must also be judged compatible
// with it, or the session can end up routing a model to a provider that
// rejects it.
func TestInferenceAgreesWithCompatibility(t *testing.T) {
	models := []string{
		"claude-opus-5", "gpt-5", "o1-preview", "gemini-2.5-pro", "deepseek-chat",
		"qwen3-coder", "glm-4.6", "mistral-large", "llama-3.3", "gemma3", "haiku-4-5",
	}
	for _, model := range models {
		provider := InferProviderFromModel(model)
		if provider == "" {
			continue
		}
		if !IsModelCompatibleWithProvider(model, provider) {
			t.Errorf("model %q infers provider %q but is not compatible with it", model, provider)
		}
	}
}
