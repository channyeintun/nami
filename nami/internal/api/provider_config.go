package api

import (
	"cmp"
	"slices"
	"strings"
)

// ProviderPreset keeps backward-compatible provider defaults for existing callers.
type ProviderPreset struct {
	Name         string
	ClientType   ClientType
	BaseURL      string
	EnvKeyVar    string
	DefaultModel string
	Capabilities ModelCapabilities
}

type ProviderSpec struct {
	ID           string
	DisplayName  string
	Protocol     ClientType
	BaseURL      string
	EnvKeyVar    string
	DefaultModel string
	Priority     int
}

type ModelSpec struct {
	ID           string
	DisplayName  string
	Family       string
	Capabilities ModelCapabilities
}

var ProviderSpecs = map[string]ProviderSpec{
	"anthropic": {
		ID:           "anthropic",
		DisplayName:  "Anthropic",
		Protocol:     AnthropicAPI,
		BaseURL:      "https://api.anthropic.com",
		EnvKeyVar:    "ANTHROPIC_API_KEY",
		DefaultModel: "claude-sonnet-4-20250514",
		Priority:     40,
	},
	"openai": {
		ID:           "openai",
		DisplayName:  "OpenAI",
		Protocol:     OpenAICompatAPI,
		BaseURL:      "https://api.openai.com/v1",
		EnvKeyVar:    "OPENAI_API_KEY",
		DefaultModel: "gpt-5.4",
		Priority:     30,
	},
	"codex": {
		ID:           "codex",
		DisplayName:  "Codex",
		Protocol:     OpenAIResponsesAPI,
		BaseURL:      "https://chatgpt.com/backend-api/codex",
		EnvKeyVar:    "CODEX_ACCESS_TOKEN",
		DefaultModel: "gpt-5.5",
		Priority:     20,
	},
	"gemini": {
		ID:           "gemini",
		DisplayName:  "Gemini",
		Protocol:     GeminiAPI,
		BaseURL:      "https://generativelanguage.googleapis.com/v1beta",
		EnvKeyVar:    "GEMINI_API_KEY",
		DefaultModel: "gemini-2.5-pro",
		Priority:     50,
	},
	"deepseek": {
		ID:           "deepseek",
		DisplayName:  "DeepSeek",
		Protocol:     OpenAICompatAPI,
		BaseURL:      "https://api.deepseek.com/v1",
		EnvKeyVar:    "DEEPSEEK_API_KEY",
		DefaultModel: "deepseek-v4-flash",
		Priority:     60,
	},
	"qwen": {
		ID:           "qwen",
		DisplayName:  "Qwen",
		Protocol:     OpenAICompatAPI,
		BaseURL:      "https://dashscope.aliyuncs.com/compatible-mode/v1",
		EnvKeyVar:    "DASHSCOPE_API_KEY",
		DefaultModel: "qwen3-235b-a22b",
		Priority:     90,
	},
	"glm": {
		ID:           "glm",
		DisplayName:  "GLM",
		Protocol:     OpenAICompatAPI,
		BaseURL:      "https://open.bigmodel.cn/api/paas/v4",
		EnvKeyVar:    "GLM_API_KEY",
		DefaultModel: "glm-4-plus",
		Priority:     100,
	},
	"mistral": {
		ID:           "mistral",
		DisplayName:  "Mistral",
		Protocol:     OpenAICompatAPI,
		BaseURL:      "https://api.mistral.ai/v1",
		EnvKeyVar:    "MISTRAL_API_KEY",
		DefaultModel: "mistral-large-latest",
		Priority:     70,
	},
	"groq": {
		ID:           "groq",
		DisplayName:  "Groq",
		Protocol:     OpenAICompatAPI,
		BaseURL:      "https://api.groq.com/openai/v1",
		EnvKeyVar:    "GROQ_API_KEY",
		DefaultModel: "llama-4-maverick-17b-128e",
		Priority:     80,
	},
	"github-copilot": {
		ID:           "github-copilot",
		DisplayName:  "GitHub Copilot",
		Protocol:     OpenAICompatAPI,
		BaseURL:      githubCopilotDefaultBaseURL,
		EnvKeyVar:    "GITHUB_COPILOT_ACCESS_TOKEN",
		DefaultModel: GitHubCopilotDefaultMainModel,
		Priority:     10,
	},
	"ollama": {
		ID:           "ollama",
		DisplayName:  "Ollama",
		Protocol:     OllamaAPI,
		BaseURL:      "http://localhost:11434",
		DefaultModel: "gemma4-e4b",
		Priority:     110,
	},
}

var ModelCatalog = map[string]ModelSpec{
	"claude-sonnet-4-20250514": {
		ID:          "claude-sonnet-4-20250514",
		DisplayName: "Claude Sonnet 4",
		Family:      "claude",
		Capabilities: ModelCapabilities{
			SupportsToolUse:          true,
			SupportsExtendedThinking: true,
			SupportsVision:           true,
			SupportsCaching:          true,
			MaxContextWindow:         200000,
			MaxOutputTokens:          8192,
		},
	},
	"gpt-5.4": {
		ID:          "gpt-5.4",
		DisplayName: "GPT 5.4",
		Family:      "gpt",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			SupportsVision:   true,
			SupportsJsonMode: true,
			MaxContextWindow: 128000,
			MaxOutputTokens:  16384,
		},
	},
	"gpt-5.5": {
		ID:          "gpt-5.5",
		DisplayName: "GPT 5.5",
		Family:      "gpt",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			SupportsJsonMode: true,
			MaxContextWindow: 400000,
			MaxPromptTokens:  272000,
			MaxOutputTokens:  128000,
		},
	},
	"gemini-2.5-pro": {
		ID:          "gemini-2.5-pro",
		DisplayName: "Gemini 2.5 Pro",
		Family:      "gemini",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			SupportsVision:   true,
			SupportsJsonMode: true,
			MaxContextWindow: 1000000,
			MaxOutputTokens:  8192,
		},
	},
	"deepseek-v4-flash": {
		ID:          "deepseek-v4-flash",
		DisplayName: "DeepSeek V4 Flash",
		Family:      "deepseek",
		Capabilities: ModelCapabilities{
			SupportsToolUse:          true,
			SupportsExtendedThinking: true,
			MaxContextWindow:         1000000,
			MaxOutputTokens:          384000,
		},
	},
	"qwen3-235b-a22b": {
		ID:          "qwen3-235b-a22b",
		DisplayName: "Qwen3 235B A22B",
		Family:      "qwen",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			MaxContextWindow: 131072,
			MaxOutputTokens:  8192,
		},
	},
	"glm-4-plus": {
		ID:          "glm-4-plus",
		DisplayName: "GLM 4 Plus",
		Family:      "glm",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			MaxContextWindow: 128000,
			MaxOutputTokens:  4096,
		},
	},
	"mistral-large-latest": {
		ID:          "mistral-large-latest",
		DisplayName: "Mistral Large",
		Family:      "mistral",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			SupportsJsonMode: true,
			MaxContextWindow: 128000,
			MaxOutputTokens:  8192,
		},
	},
	"llama-4-maverick-17b-128e": {
		ID:          "llama-4-maverick-17b-128e",
		DisplayName: "Llama 4 Maverick",
		Family:      "llama",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			MaxContextWindow: 131072,
			MaxOutputTokens:  8192,
		},
	},
	"gemma4-e4b": {
		ID:          "gemma4-e4b",
		DisplayName: "Gemma 4 E4B",
		Family:      "gemma",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			MaxContextWindow: 32000,
			MaxOutputTokens:  4096,
		},
	},
}

// Presets defines built-in provider configurations for existing code paths.
var Presets = buildProviderPresets()

func buildProviderPresets() map[string]ProviderPreset {
	presets := make(map[string]ProviderPreset, len(ProviderSpecs))
	for providerID, spec := range ProviderSpecs {
		model, _ := ModelCatalog[spec.DefaultModel]
		presets[providerID] = ProviderPreset{
			Name:         spec.ID,
			ClientType:   spec.Protocol,
			BaseURL:      spec.BaseURL,
			EnvKeyVar:    spec.EnvKeyVar,
			DefaultModel: spec.DefaultModel,
			Capabilities: model.Capabilities,
		}
	}
	return presets
}

func ProviderSpecFor(providerID string) (ProviderSpec, bool) {
	spec, ok := ProviderSpecs[providerID]
	return spec, ok
}

func OrderedProviderIDs() []string {
	providerIDs := make([]string, 0, len(ProviderSpecs))
	for providerID := range ProviderSpecs {
		providerIDs = append(providerIDs, providerID)
	}
	slices.SortFunc(providerIDs, func(a, b string) int {
		aSpec, _ := ProviderSpecFor(a)
		bSpec, _ := ProviderSpecFor(b)
		return cmp.Or(aSpec.Priority-bSpec.Priority, strings.Compare(a, b))
	})
	return providerIDs
}

func ModelSpecFor(modelID string) (ModelSpec, bool) {
	spec, ok := ModelCatalog[modelID]
	return spec, ok
}

func ResolveModelCapabilities(providerID, modelID string) ModelCapabilities {
	if spec, ok := ModelSpecFor(modelID); ok {
		return spec.Capabilities
	}
	if capabilities, ok := familyCapabilities(modelID); ok {
		return capabilities
	}
	if provider, ok := ProviderSpecFor(providerID); ok {
		if spec, ok := ModelSpecFor(provider.DefaultModel); ok {
			return spec.Capabilities
		}
	}
	return ModelCapabilities{
		SupportsToolUse:  true,
		MaxContextWindow: 32000,
		MaxOutputTokens:  4096,
	}
}

func familyCapabilities(modelID string) (ModelCapabilities, bool) {
	family := ""
	lower := strings.ToLower(modelID)
	switch {
	case strings.Contains(lower, "gpt"), strings.HasPrefix(lower, "o1"), strings.HasPrefix(lower, "o3"), strings.HasPrefix(lower, "o4"):
		family = "gpt"
	case strings.Contains(lower, "claude"), strings.Contains(lower, "sonnet"), strings.Contains(lower, "opus"), strings.Contains(lower, "haiku"):
		family = "claude"
	case strings.Contains(lower, "gemini"):
		family = "gemini"
	case strings.Contains(lower, "deepseek"):
		family = "deepseek"
	case strings.Contains(lower, "qwen"):
		family = "qwen"
	case strings.Contains(lower, "glm"):
		family = "glm"
	case strings.Contains(lower, "mistral"):
		family = "mistral"
	case strings.Contains(lower, "llama"), strings.Contains(lower, "maverick"):
		family = "llama"
	case strings.Contains(lower, "gemma"), strings.Contains(lower, "ollama"):
		family = "gemma"
	}
	if family == "" {
		return ModelCapabilities{}, false
	}
	// ModelCatalog is a map, so returning whichever member the range reaches
	// first would resolve differently on every run. Family members differ widely
	// in context window (the gpt entries span 128k to 400k), and over-promising
	// a budget the real model cannot honour fails the request, so the smallest
	// window wins and the model ID breaks ties.
	best, bestID := ModelCapabilities{}, ""
	for id, spec := range ModelCatalog {
		if spec.Family != family {
			continue
		}
		if bestID == "" ||
			spec.Capabilities.MaxContextWindow < best.MaxContextWindow ||
			(spec.Capabilities.MaxContextWindow == best.MaxContextWindow && id < bestID) {
			best, bestID = spec.Capabilities, id
		}
	}
	if bestID == "" {
		return ModelCapabilities{}, false
	}
	return best, true
}
