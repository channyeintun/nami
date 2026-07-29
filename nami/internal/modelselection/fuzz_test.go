package modelselection

import (
	"strings"
	"testing"
)

var knownProviders = []string{
	"anthropic", "openai", "codex", "github-copilot", "gemini",
	"deepseek", "qwen", "glm", "mistral", "groq", "ollama",
}

// FuzzInferenceCoversCompatibility pins the property Resolve relies on: if any
// provider accepts a model, the model also names a provider on its own. Without
// it, an unqualified model could be compatible with the session's provider yet
// get routed somewhere else.
func FuzzInferenceCoversCompatibility(f *testing.F) {
	for _, seed := range []string{
		"claude-opus-5", "gpt-5", "o1", "o3-mini", "gemini-2.5-pro", "deepseek-r1",
		"qwen3", "glm-4.6", "mistral-large", "llama-4", "gemma3", "sonnet", "haiku",
		"", "unknown-model", "GPT-5", "  claude  ",
	} {
		f.Add(seed)
	}

	f.Fuzz(func(t *testing.T, model string) {
		inferred := InferProviderFromModel(model)
		if inferred != "" {
			return
		}
		for _, provider := range knownProviders {
			if IsModelCompatibleWithProvider(model, provider) {
				t.Fatalf("model %q is compatible with %q but infers no provider", model, provider)
			}
		}
	})
}

// FuzzResolveProducesUsableSelection checks that resolution never invents a
// model or leaves a provider in mixed case, whatever the caller passes in.
func FuzzResolveProducesUsableSelection(f *testing.F) {
	f.Add("anthropic/claude-opus-5", "openai")
	f.Add("gpt-5", "")
	f.Add("", "anthropic")
	f.Add("///", "OPENAI")
	f.Add("  provider/model  ", "  Groq ")

	f.Fuzz(func(t *testing.T, input, fallback string) {
		got := Resolve(input, fallback, "fuzz")

		provider := got.Resolved.ProviderID
		if provider != strings.ToLower(provider) {
			t.Fatalf("Resolve(%q, %q) provider = %q, want lowercase", input, fallback, provider)
		}
		if strings.TrimSpace(provider) != provider {
			t.Fatalf("Resolve(%q, %q) provider = %q, want it trimmed", input, fallback, provider)
		}

		model := got.Resolved.ModelID
		if strings.TrimSpace(model) != model {
			t.Fatalf("Resolve(%q, %q) model = %q, want it trimmed", input, fallback, model)
		}
		if model != "" && !strings.Contains(strings.ToLower(input), strings.ToLower(model)) {
			t.Fatalf("Resolve(%q, %q) invented model %q", input, fallback, model)
		}
		if got.Reason == "" {
			t.Fatalf("Resolve(%q, %q) returned no reason", input, fallback)
		}
	})
}
