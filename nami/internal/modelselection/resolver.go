package modelselection

import (
	"strings"

	"github.com/channyeintun/nami/internal/config"
)

func Resolve(input string, fallbackProvider string, source string) config.ResolvedModelSelection {
	requested := config.ParseModelSelection(input, source)
	provider := NormalizeProvider(requested.ProviderID)
	model := strings.TrimSpace(requested.ModelID)

	resolved := config.NewModelSelection(provider, model, source, requested.ExplicitProvider)
	reason := "explicit provider"
	if provider == "" && model != "" {
		// Every model a provider accepts also names that provider, so a model
		// the inference cannot place belongs to no known provider and stays on
		// whichever one the session is already using.
		if inferred := InferProviderFromModel(model); inferred != "" {
			resolved = config.NewModelSelection(inferred, model, source, false)
			reason = "inferred provider from model"
		} else {
			resolved = config.NewModelSelection(NormalizeProvider(fallbackProvider), model, source, false)
			reason = "used fallback provider for unknown model"
		}
	}

	return config.ResolvedModelSelection{
		Requested: requested,
		Resolved:  resolved,
		Reason:    reason,
	}
}

func NormalizeProvider(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func InferProviderFromModel(model string) string {
	lower := strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(lower, "gemini"):
		return "gemini"
	case strings.Contains(lower, "gpt"), strings.HasPrefix(lower, "o1"), strings.HasPrefix(lower, "o3"), strings.HasPrefix(lower, "o4"):
		return "openai"
	case strings.Contains(lower, "deepseek"):
		return "deepseek"
	case strings.Contains(lower, "qwen"):
		return "qwen"
	case strings.Contains(lower, "glm"):
		return "glm"
	case strings.Contains(lower, "mistral"):
		return "mistral"
	case strings.Contains(lower, "llama"), strings.Contains(lower, "maverick"):
		return "groq"
	case strings.Contains(lower, "gemma"), strings.Contains(lower, "ollama"):
		return "ollama"
	case strings.Contains(lower, "claude"), strings.Contains(lower, "sonnet"), strings.Contains(lower, "opus"), strings.Contains(lower, "haiku"):
		return "anthropic"
	default:
		return ""
	}
}

func IsModelCompatibleWithProvider(model, provider string) bool {
	lowerModel := strings.ToLower(strings.TrimSpace(model))
	lowerProvider := NormalizeProvider(provider)
	switch lowerProvider {
	case "github-copilot":
		return strings.Contains(lowerModel, "gpt") ||
			strings.HasPrefix(lowerModel, "o1") ||
			strings.HasPrefix(lowerModel, "o3") ||
			strings.HasPrefix(lowerModel, "o4") ||
			strings.Contains(lowerModel, "claude") ||
			strings.Contains(lowerModel, "sonnet") ||
			strings.Contains(lowerModel, "opus") ||
			strings.Contains(lowerModel, "haiku")
	case "codex", "openai":
		return strings.Contains(lowerModel, "gpt") ||
			strings.HasPrefix(lowerModel, "o1") ||
			strings.HasPrefix(lowerModel, "o3") ||
			strings.HasPrefix(lowerModel, "o4")
	case "anthropic":
		return strings.Contains(lowerModel, "claude") ||
			strings.Contains(lowerModel, "sonnet") ||
			strings.Contains(lowerModel, "opus") ||
			strings.Contains(lowerModel, "haiku")
	case "gemini":
		return strings.Contains(lowerModel, "gemini")
	case "deepseek":
		return strings.Contains(lowerModel, "deepseek")
	case "qwen":
		return strings.Contains(lowerModel, "qwen")
	case "glm":
		return strings.Contains(lowerModel, "glm")
	case "mistral":
		return strings.Contains(lowerModel, "mistral")
	case "groq":
		return strings.Contains(lowerModel, "llama") || strings.Contains(lowerModel, "maverick")
	case "ollama":
		return strings.Contains(lowerModel, "gemma") || strings.Contains(lowerModel, "ollama")
	default:
		return false
	}
}
