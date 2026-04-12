package api

import (
	"fmt"
	"strings"
)

type ClientFactory func(provider, model, apiKey, baseURL string) (LLMClient, error)

var clientFactories = map[ClientType]ClientFactory{
	AnthropicAPI: func(provider, model, apiKey, baseURL string) (LLMClient, error) {
		return NewAnthropicClientForProvider(provider, model, apiKey, baseURL)
	},
	GeminiAPI: func(_ string, model, apiKey, baseURL string) (LLMClient, error) {
		return NewGeminiClient(model, apiKey, baseURL)
	},
	OpenAICompatAPI: func(provider, model, apiKey, baseURL string) (LLMClient, error) {
		return NewOpenAICompatClient(provider, model, apiKey, baseURL)
	},
	OllamaAPI: func(_ string, model, apiKey, baseURL string) (LLMClient, error) {
		return NewOllamaClient(model, apiKey, baseURL)
	},
}

func NewClientForProvider(provider, model, apiKey, baseURL string) (LLMClient, error) {
	provider = strings.TrimSpace(provider)
	if provider == "" {
		return nil, fmt.Errorf("provider is required")
	}

	preset, ok := Presets[provider]
	if !ok {
		return nil, fmt.Errorf("unsupported provider %q", provider)
	}

	factory, ok := clientFactories[preset.ClientType]
	if !ok {
		return nil, fmt.Errorf("unsupported client type %d for provider %q", preset.ClientType, provider)
	}

	return factory(provider, model, apiKey, baseURL)
}
