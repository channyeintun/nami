package commands

import (
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/channyeintun/chan/internal/agent"
	"github.com/channyeintun/chan/internal/api"
	"github.com/channyeintun/chan/internal/config"
	costpkg "github.com/channyeintun/chan/internal/cost"
	"github.com/channyeintun/chan/internal/ipc"
	"github.com/channyeintun/chan/internal/session"
)

type Descriptor struct {
	Name           string
	Description    string
	Usage          string
	TakesArguments bool
	Hidden         bool
}

func Descriptors(catalog []Descriptor) []ipc.SlashCommandDescriptorPayload {
	descriptors := make([]ipc.SlashCommandDescriptorPayload, 0, len(catalog))
	for _, descriptor := range catalog {
		if descriptor.Hidden {
			continue
		}
		descriptors = append(descriptors, ipc.SlashCommandDescriptorPayload{
			Name:           descriptor.Name,
			Description:    descriptor.Description,
			Usage:          descriptor.Usage,
			TakesArguments: descriptor.TakesArguments,
		})
	}
	return descriptors
}

func MergeGitHubCopilotModelIDs(existing []string, extra []string) []string {
	merged := make([]string, 0, len(existing)+len(extra))
	seen := make(map[string]struct{}, len(existing)+len(extra))
	for _, model := range append(append([]string(nil), existing...), extra...) {
		model = strings.TrimSpace(model)
		if model == "" {
			continue
		}
		if _, ok := seen[model]; ok {
			continue
		}
		seen[model] = struct{}{}
		merged = append(merged, model)
	}
	return merged
}

func ParseReasoningArgs(args string) (string, bool, error) {
	selection := strings.ToLower(strings.TrimSpace(args))
	if selection == "default" {
		return "", true, nil
	}
	normalized, ok := api.NormalizeReasoningEffort(selection)
	if !ok {
		return "", false, fmt.Errorf("usage: /reasoning [low|medium|high|xhigh|default]")
	}
	return normalized, false, nil
}

func EffectiveReasoningEffort(configured string, modelID string) string {
	effective := api.ClampReasoningEffort(modelID, configured)
	if effective != "" {
		return effective
	}
	return api.DefaultReasoningEffort(modelID)
}

func DescribeReasoningEffort(configured string, modelID string) string {
	effective := EffectiveReasoningEffort(configured, modelID)
	if effective == "" {
		if configured == "" {
			return "default"
		}
		return configured + " (saved for supported models)"
	}
	if configured == "" {
		return effective + " (default)"
	}
	if configured != effective {
		return fmt.Sprintf("%s (clamped from %s for %s)", effective, configured, modelID)
	}
	return effective
}

func OpenBrowserURL(target string) error {
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("empty browser URL")
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", target)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", target)
	default:
		cmd = exec.Command("xdg-open", target)
	}
	return cmd.Start()
}

func FormatHelpText(catalog []Descriptor) string {
	var b strings.Builder
	b.WriteString("Available slash commands:\n\n")
	for _, descriptor := range visibleDescriptors(catalog) {
		usage := descriptor.Usage
		if strings.TrimSpace(usage) == "" {
			usage = "/" + descriptor.Name
		}
		b.WriteString(fmt.Sprintf("  %-38s %s\n", usage, descriptor.Description))
	}
	return strings.TrimRight(b.String(), "\n")
}

func FormatStatusText(sessionID string, startedAt time.Time, mode agent.ExecutionMode, model string, subagentModel string, cwd string, msgCount int, tracker *costpkg.Tracker, cfg config.Config, snapshot ProviderSnapshot) string {
	elapsed := time.Since(startedAt).Round(time.Second)
	snap := tracker.Snapshot()
	reasoning := DescribeReasoningEffort(strings.TrimSpace(cfg.ReasoningEffort), model)
	lines := []string{
		fmt.Sprintf("Session: %s", sessionID),
		fmt.Sprintf("Started: %s (%s ago)", startedAt.Format(time.RFC3339), elapsed),
		fmt.Sprintf("Mode: %s", string(mode)),
		fmt.Sprintf("Model: %s", FormatModelSelectionLabel(model)),
		fmt.Sprintf("Subagent: %s", FormatModelSelectionLabel(subagentModel)),
		fmt.Sprintf("Reasoning: %s", reasoning),
		formatActiveProviderStatusLine(snapshot),
		formatFirstUsableProviderLine(snapshot),
		fmt.Sprintf("CWD: %s", cwd),
		fmt.Sprintf("Messages: %d", msgCount),
		fmt.Sprintf("Cost: $%.4f", snap.TotalCostUSD),
		fmt.Sprintf("Tokens: %d in / %d out", snap.TotalInputTokens, snap.TotalOutputTokens),
		formatCacheStatusLine(snap),
	}
	if childLine := formatChildCacheStatusLine(snap); childLine != "" {
		lines = append(lines, childLine)
	}
	return strings.Join(lines, "\n")
}

func formatActiveProviderStatusLine(snapshot ProviderSnapshot) string {
	if snapshot.ActiveProvider == "" {
		return "Provider: unknown"
	}
	status, ok := snapshot.LookupProvider(snapshot.ActiveProvider)
	if !ok {
		return fmt.Sprintf("Provider: %s", snapshot.ActiveProvider)
	}
	return fmt.Sprintf("Provider: %s (%s, source %s)", status.ID, ProviderStateLabel(status), status.AuthSource)
}

func formatFirstUsableProviderLine(snapshot ProviderSnapshot) string {
	if first, ok := snapshot.FirstUsable(); ok {
		return fmt.Sprintf("First usable provider: %s/%s", first.ID, first.DefaultModel)
	}
	return "First usable provider: none"
}

func formatCacheStatusLine(snapshot costpkg.TrackerSnapshot) string {
	cacheRead := snapshot.TotalCacheReadTokens
	cacheWrite := snapshot.TotalCacheCreationTokens
	if cacheRead == 0 && cacheWrite == 0 {
		return "Prompt cache: inactive"
	}
	return fmt.Sprintf(
		"Prompt cache: %.1f%% hit · %s read / %s write · main %s / %s",
		snapshot.CacheHitRate()*100,
		formatTokenTotal(cacheRead),
		formatTokenTotal(cacheWrite),
		formatTokenTotal(snapshot.MainAgentCacheReadTokens()),
		formatTokenTotal(snapshot.MainAgentCacheCreationTokens()),
	)
}

func formatChildCacheStatusLine(snapshot costpkg.TrackerSnapshot) string {
	if snapshot.ChildAgentCacheReadTokens == 0 && snapshot.ChildAgentCacheCreationTokens == 0 {
		return ""
	}
	return fmt.Sprintf(
		"Child cache: %s read / %s write",
		formatTokenTotal(snapshot.ChildAgentCacheReadTokens),
		formatTokenTotal(snapshot.ChildAgentCacheCreationTokens),
	)
}

func formatTokenTotal(value int) string {
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(value)/1_000_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return fmt.Sprintf("%d", value)
	}
}

func FormatModelSelectionLabel(selection string) string {
	provider, model := config.ParseModel(strings.TrimSpace(selection))
	if strings.TrimSpace(model) == "" {
		model = provider
	}
	if strings.TrimSpace(model) == "" {
		return "unknown model"
	}
	return model
}

func FormatSubagentHelpText(currentSelection string) string {
	return fmt.Sprintf(
		"Current subagent model: %s\nUsage: /subagent [model|default|help]\nRun /subagent with no arguments to open the model picker.",
		FormatModelSelectionLabel(currentSelection),
	)
}

func FormatSessionList(sessions []session.Metadata, currentID string) string {
	if len(sessions) == 0 {
		return "No sessions found."
	}
	var b strings.Builder
	b.WriteString("Recent sessions:\n\n")
	shown := 0
	for _, meta := range sessions {
		if shown >= 20 {
			break
		}
		marker := "  "
		if meta.SessionID == currentID {
			marker = "* "
		}
		title := meta.Title
		if title == "" {
			title = "(untitled)"
		}
		b.WriteString(fmt.Sprintf("%s%s  %s  %s  %s  $%.4f\n",
			marker,
			meta.SessionID[:8],
			meta.UpdatedAt.Format("2006-01-02 15:04"),
			meta.Model,
			title,
			meta.TotalCostUSD,
		))
		shown++
	}
	return strings.TrimSpace(b.String())
}


func visibleDescriptors(catalog []Descriptor) []Descriptor {
	visible := make([]Descriptor, 0, len(catalog))
	for _, descriptor := range catalog {
		if descriptor.Hidden {
			continue
		}
		visible = append(visible, descriptor)
	}
	return visible
}
