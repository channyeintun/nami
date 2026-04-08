package api

// ProviderPreset holds defaults for a known model provider.
type ProviderPreset struct {
	Name         string
	ClientType   ClientType
	BaseURL      string
	EnvKeyVar    string
	DefaultModel string
	Capabilities ModelCapabilities
}

// Presets defines built-in provider configurations.
var Presets = map[string]ProviderPreset{
	"anthropic": {
		Name:         "anthropic",
		ClientType:   AnthropicAPI,
		BaseURL:      "https://api.anthropic.com",
		EnvKeyVar:    "ANTHROPIC_API_KEY",
		DefaultModel: "claude-sonnet-4-20250514",
		Capabilities: ModelCapabilities{
			SupportsToolUse:          true,
			SupportsExtendedThinking: true,
			SupportsVision:           true,
			MaxContextWindow:         200000,
			MaxOutputTokens:          8192,
		},
	},
	"openai": {
		Name:         "openai",
		ClientType:   OpenAICompatAPI,
		BaseURL:      "https://api.openai.com/v1",
		EnvKeyVar:    "OPENAI_API_KEY",
		DefaultModel: "gpt-4o",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			SupportsVision:   true,
			SupportsJsonMode: true,
			MaxContextWindow: 128000,
			MaxOutputTokens:  16384,
		},
	},
	"gemini": {
		Name:         "gemini",
		ClientType:   GeminiAPI,
		BaseURL:      "https://generativelanguage.googleapis.com/v1beta",
		EnvKeyVar:    "GEMINI_API_KEY",
		DefaultModel: "gemini-2.5-pro",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			SupportsVision:   true,
			SupportsJsonMode: true,
			MaxContextWindow: 1000000,
			MaxOutputTokens:  8192,
		},
	},
	"deepseek": {
		Name:         "deepseek",
		ClientType:   OpenAICompatAPI,
		BaseURL:      "https://api.deepseek.com/v1",
		EnvKeyVar:    "DEEPSEEK_API_KEY",
		DefaultModel: "deepseek-chat",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			MaxContextWindow: 64000,
			MaxOutputTokens:  8192,
		},
	},
	"qwen": {
		Name:         "qwen",
		ClientType:   OpenAICompatAPI,
		BaseURL:      "https://dashscope.aliyuncs.com/compatible-mode/v1",
		EnvKeyVar:    "DASHSCOPE_API_KEY",
		DefaultModel: "qwen3-235b-a22b",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			MaxContextWindow: 131072,
			MaxOutputTokens:  8192,
		},
	},
	"glm": {
		Name:         "glm",
		ClientType:   OpenAICompatAPI,
		BaseURL:      "https://open.bigmodel.cn/api/paas/v4",
		EnvKeyVar:    "GLM_API_KEY",
		DefaultModel: "glm-4-plus",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			MaxContextWindow: 128000,
			MaxOutputTokens:  4096,
		},
	},
	"mistral": {
		Name:         "mistral",
		ClientType:   OpenAICompatAPI,
		BaseURL:      "https://api.mistral.ai/v1",
		EnvKeyVar:    "MISTRAL_API_KEY",
		DefaultModel: "mistral-large-latest",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			SupportsJsonMode: true,
			MaxContextWindow: 128000,
			MaxOutputTokens:  8192,
		},
	},
	"groq": {
		Name:         "groq",
		ClientType:   OpenAICompatAPI,
		BaseURL:      "https://api.groq.com/openai/v1",
		EnvKeyVar:    "GROQ_API_KEY",
		DefaultModel: "llama-4-maverick-17b-128e",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			MaxContextWindow: 131072,
			MaxOutputTokens:  8192,
		},
	},
	"ollama": {
		Name:         "ollama",
		ClientType:   OllamaAPI,
		BaseURL:      "http://localhost:11434",
		DefaultModel: "gemma4-e4b",
		Capabilities: ModelCapabilities{
			SupportsToolUse:  true,
			MaxContextWindow: 32000,
			MaxOutputTokens:  4096,
		},
	},
}
