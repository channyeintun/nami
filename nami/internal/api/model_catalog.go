package api

type CuratedModelSpec struct {
	Label            string
	ModelID          string
	ProviderID       string
	Description      string
	Family           string
	CostWarningLabel string
	Aliases          []string
}

var CuratedModelCatalog = []CuratedModelSpec{
	{Label: "Claude Sonnet 5", ModelID: "claude-sonnet-5", ProviderID: "anthropic", Description: "Default Sonnet preset", Family: "claude"},
	{Label: "Claude Opus 5 (Expensive)", ModelID: "claude-opus-5", ProviderID: "anthropic", Description: "Expensive latest Opus preset", Family: "claude", CostWarningLabel: "Expensive"},
	{Label: "Claude Fable 5 (Expensive)", ModelID: "claude-fable-5", ProviderID: "anthropic", Description: "Most capable Claude preset", Family: "claude", CostWarningLabel: "Expensive"},
	{Label: "Claude Sonnet 4.6", ModelID: "claude-sonnet-4-6", ProviderID: "anthropic", Description: "Previous Sonnet preset", Family: "claude"},
	{Label: "Claude Opus 4.7 (Expensive)", ModelID: "claude-opus-4-7", ProviderID: "anthropic", Description: "Expensive previous Opus preset", Family: "claude", CostWarningLabel: "Expensive"},
	{Label: "Claude Opus 4.6 (Expensive)", ModelID: "claude-opus-4-6", ProviderID: "anthropic", Description: "Expensive older Opus preset", Family: "claude", CostWarningLabel: "Expensive"},
	{Label: "Claude Haiku 4.5", ModelID: "claude-haiku-4-5", ProviderID: "anthropic", Description: "Haiku preset", Family: "claude"},
	{Label: "GPT 5.6 (Expensive)", ModelID: "gpt-5.6", ProviderID: "openai", Description: "Latest GPT preset", Family: "gpt", CostWarningLabel: "Expensive"},
	{Label: "GPT 5.6 Terra", ModelID: "gpt-5.6-terra", ProviderID: "openai", Description: "Balanced GPT 5.6 preset", Family: "gpt"},
	{Label: "GPT 5.6 Luna", ModelID: "gpt-5.6-luna", ProviderID: "openai", Description: "Fast, low-cost GPT 5.6 preset", Family: "gpt"},
	{Label: "GPT 5.4", ModelID: "gpt-5.4", ProviderID: "openai", Description: "Default GPT preset", Family: "gpt"},
	{Label: "GPT 5.5 (Expensive)", ModelID: "gpt-5.5", ProviderID: "openai", Description: "Expensive previous GPT preset", Family: "gpt", CostWarningLabel: "Expensive"},
	{Label: "GPT 5 Mini", ModelID: "gpt-5-mini", ProviderID: "openai", Description: "OpenAI GPT 5 mini", Family: "gpt", Aliases: []string{"gpt-5-mini"}},
	{Label: "GPT 4.1", ModelID: "gpt-4.1", ProviderID: "openai", Description: "OpenAI GPT 4.1", Family: "gpt"},
	{Label: "GPT 4o", ModelID: "gpt-4o", ProviderID: "openai", Description: "OpenAI GPT 4o", Family: "gpt"},
	{Label: "Gemini 3 Flash", ModelID: "gemini-3-flash-preview", ProviderID: "gemini", Description: "Gemini flash preset", Family: "gemini"},
	{Label: "Gemini 3.1 Flash Lite", ModelID: "gemini-3.1-flash-lite", ProviderID: "gemini", Description: "Gemini flash-lite preset", Family: "gemini"},
	{Label: "Gemini 3.1 Pro", ModelID: "gemini-3.1-pro-preview", ProviderID: "gemini", Description: "Gemini pro preset", Family: "gemini"},
	{Label: "Gemini 3.5 Flash", ModelID: "gemini-3.5-flash", ProviderID: "gemini", Description: "Gemini 3.5 flash preset", Family: "gemini"},
	{Label: "Gemma 4 31B", ModelID: "gemma-4-31B", ProviderID: "ollama", Description: "Gemma 4 31B preset", Family: "gemma"},
	{Label: "DeepSeek V4 Flash", ModelID: "deepseek-v4-flash", ProviderID: "deepseek", Description: "DeepSeek fast v4 preset", Family: "deepseek"},
	{Label: "DeepSeek V4 Pro", ModelID: "deepseek-v4-pro", ProviderID: "deepseek", Description: "DeepSeek pro v4 preset", Family: "deepseek"},
}
