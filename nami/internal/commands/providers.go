package commands

import (
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/ipc"
)

const (
	ansiReset = "\x1b[0m"
	ansiBold  = "\x1b[1m"
)

type ProviderStatus struct {
	ID           string
	Label        string
	DefaultModel string
	AuthSource   string
	Configured   bool
	Usable       bool
	SetupHint    string
	LastError    string
	Current      bool
}

type ProviderSnapshot struct {
	ActiveProvider string
	ActiveModel    string
	Providers      []ProviderStatus
}

func FormatProviderSnapshot(snapshot ProviderSnapshot) string {
	lines := make([]string, 0, len(snapshot.Providers)*2+4)
	if snapshot.ActiveProvider != "" && snapshot.ActiveModel != "" {
		lines = append(lines, fmt.Sprintf("Active selection: %s/%s", colorProviderName(snapshot.ActiveProvider), snapshot.ActiveModel))
	} else if snapshot.ActiveModel != "" {
		lines = append(lines, fmt.Sprintf("Active selection: %s", snapshot.ActiveModel))
	}

	if firstUsable, ok := snapshot.FirstUsable(); ok {
		lines = append(lines, fmt.Sprintf("First usable: %s/%s", colorProviderName(firstUsable.ID), firstUsable.DefaultModel))
	} else {
		lines = append(lines, "First usable: none")
	}

	lines = append(lines, "", "Providers:")
	for _, status := range snapshot.Providers {
		marker := "  "
		if status.Current {
			marker = "* "
		}

		providerName := colorProviderName(padRight(status.ID, 16))
		line := fmt.Sprintf(
			"%s%s %-24s default %s · source %s",
			marker,
			providerName,
			ProviderStateLabel(status),
			status.DefaultModel,
			status.AuthSource,
		)
		lines = append(lines, strings.TrimRight(line, " "))
		if status.LastError != "" {
			lines = append(lines, "  Last error: "+status.LastError)
		}
		if !status.Usable && status.SetupHint != "" {
			lines = append(lines, "  Next: "+status.SetupHint)
		}
	}

	return strings.Join(lines, "\n")
}

func colorProviderName(name string) string {
	trimmed := normalizeProviderID(name)
	color := "\x1b[36m"
	switch trimmed {
	case "github-copilot":
		color = "\x1b[96m"
	case "openai":
		color = "\x1b[92m"
	case "anthropic":
		color = "\x1b[95m"
	case "gemini":
		color = "\x1b[93m"
	case "deepseek":
		color = "\x1b[94m"
	case "mistral":
		color = "\x1b[35m"
	case "groq":
		color = "\x1b[32m"
	case "qwen":
		color = "\x1b[96m"
	case "glm":
		color = "\x1b[94m"
	case "ollama":
		color = "\x1b[33m"
	}
	return ansiBold + color + name + ansiReset
}

func padRight(value string, width int) string {
	trimmed := strings.TrimSpace(value)
	if len(trimmed) >= width {
		return trimmed
	}
	return trimmed + strings.Repeat(" ", width-len(trimmed))
}

func BuildModelSelectionOptions(snapshot ProviderSnapshot, currentSelection string) []ipc.ModelSelectionOptionPayload {
	currentProvider, currentModel := ResolveModelSelection(currentSelection)
	currentRef := providerModelRef(currentProvider, currentModel)
	options := make([]ipc.ModelSelectionOptionPayload, 0, len(snapshot.Providers)+1)
	seen := make(map[string]struct{}, len(snapshot.Providers)+1)

	if currentModel != "" && !matchesProviderDefault(snapshot, currentProvider, currentModel) {
		label := "Current selection"
		description := "Current session model"
		if status, ok := snapshot.LookupProvider(currentProvider); ok {
			label = fmt.Sprintf("Current · %s · %s", status.Label, ProviderStateLabel(status))
			description = formatModelSelectionDescription(status)
		}
		options = append(options, ipc.ModelSelectionOptionPayload{
			Label:       label,
			Model:       currentModel,
			Provider:    currentProvider,
			Description: description,
			Active:      true,
		})
		seen[currentRef] = struct{}{}
	}

	appendProviderOptions := func(match func(ProviderStatus) bool) {
		for _, status := range snapshot.Providers {
			if !match(status) {
				continue
			}
			ref := providerModelRef(status.ID, status.DefaultModel)
			if _, exists := seen[ref]; exists {
				continue
			}
			options = append(options, ipc.ModelSelectionOptionPayload{
				Label:       fmt.Sprintf("%s · %s", status.Label, ProviderStateLabel(status)),
				Model:       status.DefaultModel,
				Provider:    status.ID,
				Description: formatModelSelectionDescription(status),
				Active:      strings.EqualFold(ref, currentRef),
			})
		}
	}

	appendProviderOptions(func(status ProviderStatus) bool { return status.Usable })
	appendProviderOptions(func(status ProviderStatus) bool { return !status.Usable })

	return options
}

func DiscoverProviderSnapshot(cfg config.Config) ProviderSnapshot {
	activeProvider, activeModel := ResolveModelSelection(cfg.Model)
	snapshot := ProviderSnapshot{
		ActiveProvider: activeProvider,
		ActiveModel:    activeModel,
		Providers:      make([]ProviderStatus, 0, len(api.Presets)),
	}

	for _, providerID := range orderedProviderIDs() {
		preset := api.Presets[providerID]
		status := ProviderStatus{
			ID:           providerID,
			Label:        providerDisplayLabel(providerID),
			DefaultModel: preset.DefaultModel,
			AuthSource:   "none",
			SetupHint:    providerSetupHint(providerID, preset.EnvKeyVar),
			Current:      providerID == activeProvider,
		}
		populateProviderStatus(&status, cfg, activeProvider, preset)
		snapshot.Providers = append(snapshot.Providers, status)
	}

	return snapshot
}

func (snapshot ProviderSnapshot) FirstUsable() (ProviderStatus, bool) {
	for _, status := range snapshot.Providers {
		if status.Current && status.Usable {
			return status, true
		}
	}
	for _, status := range snapshot.Providers {
		if status.Usable {
			return status, true
		}
	}
	return ProviderStatus{}, false
}

func (snapshot ProviderSnapshot) LookupProvider(providerID string) (ProviderStatus, bool) {
	providerID = normalizeProviderID(providerID)
	for _, status := range snapshot.Providers {
		if status.ID == providerID {
			return status, true
		}
	}
	return ProviderStatus{}, false
}

func ResolveModelSelection(selection string) (string, string) {
	provider, model := config.ParseModel(strings.TrimSpace(selection))
	provider = normalizeProviderID(provider)
	model = strings.TrimSpace(model)
	if model == "" && provider != "" {
		model = provider
		provider = ""
	}
	if provider == "" && model != "" {
		provider = InferProviderFromModel(model)
	}
	return provider, model
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

func orderedProviderIDs() []string {
	preferred := []string{
		"github-copilot",
		"openai",
		"anthropic",
		"gemini",
		"deepseek",
		"mistral",
		"groq",
		"qwen",
		"glm",
		"ollama",
	}
	ordered := make([]string, 0, len(api.Presets))
	seen := make(map[string]struct{}, len(api.Presets))
	for _, providerID := range preferred {
		if _, ok := api.Presets[providerID]; !ok {
			continue
		}
		ordered = append(ordered, providerID)
		seen[providerID] = struct{}{}
	}

	extra := make([]string, 0, len(api.Presets)-len(ordered))
	for providerID := range api.Presets {
		if _, ok := seen[providerID]; ok {
			continue
		}
		extra = append(extra, providerID)
	}
	sort.Strings(extra)
	ordered = append(ordered, extra...)
	return ordered
}

func populateProviderStatus(status *ProviderStatus, cfg config.Config, activeProvider string, preset api.ProviderPreset) {
	if status == nil {
		return
	}

	switch status.ID {
	case "github-copilot":
		populateGitHubCopilotStatus(status, cfg, activeProvider)
	case "ollama":
		populateOllamaStatus(status, cfg, activeProvider)
	default:
		populateAPIKeyProviderStatus(status, cfg, activeProvider, preset.EnvKeyVar)
	}
}

func populateGitHubCopilotStatus(status *ProviderStatus, cfg config.Config, activeProvider string) {
	if activeProvider == status.ID && strings.TrimSpace(cfg.APIKey) != "" {
		status.AuthSource = "env:NAMI_API_KEY"
		status.Configured = true
		status.Usable = true
		status.SetupHint = ""
		return
	}

	creds := cfg.GitHubCopilot
	if strings.TrimSpace(creds.GitHubToken) != "" {
		status.AuthSource = "stored device auth"
		status.Configured = true
		status.Usable = true
		status.SetupHint = ""
		return
	}

	if strings.TrimSpace(creds.AccessToken) != "" {
		status.AuthSource = "stored access token"
		status.Configured = true
		if creds.ExpiresAtUnixMS > 0 && time.Now().UnixMilli() > creds.ExpiresAtUnixMS {
			status.LastError = "saved access token expired"
			status.SetupHint = "Run /connect github-copilot to refresh credentials."
			return
		}
		status.Usable = true
		status.SetupHint = ""
		return
	}
}

func populateAPIKeyProviderStatus(status *ProviderStatus, cfg config.Config, activeProvider string, envKey string) {
	if activeProvider == status.ID && strings.TrimSpace(cfg.APIKey) != "" {
		status.AuthSource = "env:NAMI_API_KEY"
		status.Configured = true
		status.Usable = true
		status.SetupHint = ""
		return
	}

	if envKey != "" && strings.TrimSpace(os.Getenv(envKey)) != "" {
		status.AuthSource = "env:" + envKey
		status.Configured = true
		status.Usable = true
		status.SetupHint = ""
		return
	}
}

func populateOllamaStatus(status *ProviderStatus, cfg config.Config, activeProvider string) {
	if activeProvider == status.ID && strings.TrimSpace(cfg.APIKey) != "" {
		status.AuthSource = "env:NAMI_API_KEY"
	} else if strings.TrimSpace(os.Getenv("OLLAMA_API_KEY")) != "" {
		status.AuthSource = "env:OLLAMA_API_KEY"
	} else {
		status.AuthSource = "local"
	}
	status.Configured = true
	status.Usable = true
	status.SetupHint = "Ensure Ollama is running on http://localhost:11434."
}

func providerDisplayLabel(providerID string) string {
	switch providerID {
	case "github-copilot":
		return "GitHub Copilot"
	case "openai":
		return "OpenAI"
	case "anthropic":
		return "Anthropic"
	case "gemini":
		return "Gemini"
	case "deepseek":
		return "DeepSeek"
	case "qwen":
		return "Qwen"
	case "glm":
		return "GLM"
	case "mistral":
		return "Mistral"
	case "groq":
		return "Groq"
	case "ollama":
		return "Ollama"
	default:
		return strings.TrimSpace(providerID)
	}
}

func providerSetupHint(providerID string, envKey string) string {
	switch providerID {
	case "github-copilot":
		return "Run /connect github-copilot."
	case "ollama":
		return "Ensure Ollama is running on http://localhost:11434."
	default:
		if envKey == "" {
			return "Provider setup is required."
		}
		return fmt.Sprintf("Set %s.", envKey)
	}
}

func normalizeProviderID(provider string) string {
	return strings.ToLower(strings.TrimSpace(provider))
}

func ProviderStateLabel(status ProviderStatus) string {
	switch {
	case status.Usable:
		return "usable"
	case status.Configured:
		return "configured, needs attention"
	default:
		return "needs setup"
	}
}

func matchesProviderDefault(snapshot ProviderSnapshot, providerID string, model string) bool {
	status, ok := snapshot.LookupProvider(providerID)
	if !ok {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(model), strings.TrimSpace(status.DefaultModel))
}

func formatModelSelectionDescription(status ProviderStatus) string {
	parts := make([]string, 0, 2)
	if status.AuthSource != "" && status.AuthSource != "none" {
		parts = append(parts, status.AuthSource)
	}
	if !status.Usable && status.SetupHint != "" {
		parts = append(parts, status.SetupHint)
	}
	if len(parts) == 0 {
		parts = append(parts, "default provider model")
	}
	return strings.Join(parts, " · ")
}

func providerModelRef(providerID string, model string) string {
	providerID = normalizeProviderID(providerID)
	model = strings.TrimSpace(model)
	if providerID == "" {
		return model
	}
	if model == "" {
		return providerID
	}
	return providerID + "/" + model
}
