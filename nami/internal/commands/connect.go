package commands

import (
	"fmt"
	"strings"

	"github.com/channyeintun/nami/internal/api"
)

const connectUsage = "/connect [provider|status|help]"

type ConnectAction string

const (
	ConnectActionOverview ConnectAction = "overview"
	ConnectActionHelp     ConnectAction = "help"
	ConnectActionStatus   ConnectAction = "status"
	ConnectActionProvider ConnectAction = "provider"
)

type ConnectRequest struct {
	Action   ConnectAction
	Provider string
	Extra    string
}

type ConnectAuthMethod struct {
	Type        string
	Label       string
	EnvVar      string
	Description string
}

type ConnectProviderSpec struct {
	ID           string
	Label        string
	DefaultModel string
	Methods      []ConnectAuthMethod
}

func ParseConnectArgs(args string) (ConnectRequest, error) {
	parts := strings.Fields(strings.TrimSpace(args))
	switch len(parts) {
	case 0:
		return ConnectRequest{Action: ConnectActionOverview}, nil
	case 1:
		keyword := normalizeProviderID(parts[0])
		switch keyword {
		case "help":
			return ConnectRequest{Action: ConnectActionHelp}, nil
		case "status":
			return ConnectRequest{Action: ConnectActionStatus}, nil
		}
		if _, ok := LookupConnectProvider(keyword); ok {
			return ConnectRequest{Action: ConnectActionProvider, Provider: keyword}, nil
		}
		return ConnectRequest{}, fmt.Errorf("usage: %s", connectUsage)
	case 2:
		providerID := normalizeProviderID(parts[0])
		if providerID == "github-copilot" {
			return ConnectRequest{Action: ConnectActionProvider, Provider: providerID, Extra: parts[1]}, nil
		}
		return ConnectRequest{}, fmt.Errorf("usage: %s", connectUsage)
	default:
		return ConnectRequest{}, fmt.Errorf("usage: %s", connectUsage)
	}
}

func ConnectProviderCatalog() []ConnectProviderSpec {
	providers := make([]ConnectProviderSpec, 0, len(api.Presets))
	for _, providerID := range orderedProviderIDs() {
		preset, ok := api.Presets[providerID]
		if !ok {
			continue
		}
		providers = append(providers, ConnectProviderSpec{
			ID:           providerID,
			Label:        providerDisplayLabel(providerID),
			DefaultModel: preset.DefaultModel,
			Methods:      connectMethodsForProvider(providerID, preset.EnvKeyVar),
		})
	}
	return providers
}

func LookupConnectProvider(providerID string) (ConnectProviderSpec, bool) {
	providerID = normalizeProviderID(providerID)
	for _, spec := range ConnectProviderCatalog() {
		if spec.ID == providerID {
			return spec, true
		}
	}
	return ConnectProviderSpec{}, false
}

func FormatConnectOverviewText(snapshot ProviderSnapshot) string {
	var b strings.Builder
	b.WriteString("Connect a provider:\n\n")
	for _, spec := range ConnectProviderCatalog() {
		state := "needs setup"
		if status, ok := snapshot.LookupProvider(spec.ID); ok {
			state = ProviderStateLabel(status)
		}
		b.WriteString(fmt.Sprintf("  %-28s %s (%s)\n", "/connect "+spec.ID, connectMethodSummary(spec.Methods), state))
	}
	b.WriteString("\nOther commands:\n")
	b.WriteString("  /connect status             Show provider setup status\n")
	b.WriteString("  /connect help               Show this provider onboarding help")
	return strings.TrimRight(b.String(), "\n")
}

func FormatConnectProviderGuidance(spec ConnectProviderSpec, snapshot ProviderSnapshot) string {
	status, _ := snapshot.LookupProvider(spec.ID)
	lines := []string{
		fmt.Sprintf("%s setup", spec.Label),
		"",
		fmt.Sprintf("Default model: %s/%s", spec.ID, spec.DefaultModel),
		fmt.Sprintf("Current state: %s", ProviderStateLabel(status)),
		fmt.Sprintf("Auth source: %s", status.AuthSource),
	}
	if status.LastError != "" {
		lines = append(lines, "Current issue: "+status.LastError)
	}
	lines = append(lines, "", "Auth methods:")
	for _, method := range spec.Methods {
		lines = append(lines, "  - "+formatConnectMethod(spec.ID, method))
	}
	if status.Usable {
		lines = append(lines, "", fmt.Sprintf("Provider is ready. Run /connect %s to switch the session to %s/%s.", spec.ID, spec.ID, spec.DefaultModel))
	} else if status.SetupHint != "" {
		lines = append(lines, "", "Next: "+status.SetupHint, fmt.Sprintf("Then run /connect %s again or /providers to verify.", spec.ID))
	}
	return strings.Join(lines, "\n")
}

func connectMethodsForProvider(providerID string, envKey string) []ConnectAuthMethod {
	switch providerID {
	case "github-copilot":
		return []ConnectAuthMethod{{
			Type:        "device",
			Label:       "Device login",
			Description: "Open the GitHub device flow in the browser and store refreshed Copilot credentials.",
		}}
	case "ollama":
		return []ConnectAuthMethod{{
			Type:        "local",
			Label:       "Local runtime",
			EnvVar:      "OLLAMA_API_KEY",
			Description: "No API key is required unless your Ollama instance is protected. Ensure the local server is running.",
		}}
	default:
		return []ConnectAuthMethod{{
			Type:        "api_key",
			Label:       "API key",
			EnvVar:      envKey,
			Description: "Read the provider key from the environment and switch the session once it is available.",
		}}
	}
}

func connectMethodSummary(methods []ConnectAuthMethod) string {
	if len(methods) == 0 {
		return "setup required"
	}
	parts := make([]string, 0, len(methods))
	for _, method := range methods {
		parts = append(parts, strings.ToLower(strings.TrimSpace(method.Label)))
	}
	return strings.Join(parts, ", ")
}

func formatConnectMethod(providerID string, method ConnectAuthMethod) string {
	label := strings.TrimSpace(method.Label)
	detail := strings.TrimSpace(method.Description)
	if method.Type == "api_key" && strings.TrimSpace(method.EnvVar) != "" {
		return fmt.Sprintf("%s: set %s, then run /connect %s again.", label, method.EnvVar, providerID)
	}
	if method.Type == "device" {
		return fmt.Sprintf("%s: run /connect %s to start browser login.", label, providerID)
	}
	if method.Type == "local" {
		if strings.TrimSpace(method.EnvVar) != "" {
			return fmt.Sprintf("%s: ensure Ollama is running. Optional auth uses %s.", label, method.EnvVar)
		}
		return fmt.Sprintf("%s: ensure Ollama is running.", label)
	}
	if detail != "" {
		return fmt.Sprintf("%s: %s", label, detail)
	}
	return label
}
