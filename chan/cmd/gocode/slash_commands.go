package main

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
	"strings"
	"time"

	"github.com/channyeintun/gocode/internal/agent"
	"github.com/channyeintun/gocode/internal/api"
	artifactspkg "github.com/channyeintun/gocode/internal/artifacts"
	"github.com/channyeintun/gocode/internal/config"
	costpkg "github.com/channyeintun/gocode/internal/cost"
	"github.com/channyeintun/gocode/internal/ipc"
	"github.com/channyeintun/gocode/internal/session"
	"github.com/channyeintun/gocode/internal/timing"
)

type slashCommandDescriptor struct {
	Name           string
	Description    string
	Usage          string
	TakesArguments bool
	Hidden         bool
}

var slashCommandCatalog = []slashCommandDescriptor{
	{
		Name:           "connect",
		Description:    "Connect GitHub Copilot with device login",
		Usage:          "/connect [github-copilot [enterprise-domain]]",
		TakesArguments: true,
	},
	{
		Name:        "plan",
		Description: "Switch to plan mode (read-only until approved)",
		Usage:       "/plan",
	},
	{
		Name:        "fast",
		Description: "Switch to fast mode (direct execution)",
		Usage:       "/fast",
	},
	{
		Name:           "model",
		Description:    "Show or switch the active model",
		Usage:          "/model [name]",
		TakesArguments: true,
	},
	{
		Name:           "reasoning",
		Description:    "Show or set GPT-5 reasoning effort",
		Usage:          "/reasoning [low|medium|high|xhigh|default]",
		TakesArguments: true,
	},
	{
		Name:        "cost",
		Description: "Show token usage and cost breakdown",
		Usage:       "/cost",
	},
	{
		Name:        "usage",
		Description: "Alias for /cost",
		Usage:       "/usage",
	},
	{
		Name:        "compact",
		Description: "Compact the conversation to save context",
		Usage:       "/compact",
	},
	{
		Name:           "resume",
		Description:    "Resume a previous session",
		Usage:          "/resume [id]",
		TakesArguments: true,
	},
	{
		Name:        "clear",
		Description: "Clear the conversation and start a new session",
		Usage:       "/clear",
	},
	{
		Name:        "status",
		Description: "Show the current session status",
		Usage:       "/status",
	},
	{
		Name:        "sessions",
		Description: "List recent sessions",
		Usage:       "/sessions",
	},
	{
		Name:           "diff",
		Description:    "Show git diff (for example /diff --staged)",
		Usage:          "/diff [args]",
		TakesArguments: true,
	},
	{
		Name:        "help",
		Description: "Show the slash-command help text",
		Usage:       "/help",
	},
	{
		Name:        "plan-mode",
		Description: "Alias for /plan",
		Usage:       "/plan-mode",
		Hidden:      true,
	},
}

func slashCommandDescriptors() []ipc.SlashCommandDescriptorPayload {
	descriptors := make([]ipc.SlashCommandDescriptorPayload, 0, len(slashCommandCatalog))
	for _, descriptor := range slashCommandCatalog {
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

func sortedVisibleSlashCommands() []slashCommandDescriptor {
	visible := make([]slashCommandDescriptor, 0, len(slashCommandCatalog))
	for _, descriptor := range slashCommandCatalog {
		if descriptor.Hidden {
			continue
		}
		visible = append(visible, descriptor)
	}
	return visible
}

func handleSlashCommand(
	ctx context.Context,
	bridge *ipc.Bridge,
	store *session.Store,
	timingLogger *timing.Logger,
	cfg config.Config,
	artifactManager *artifactspkg.Manager,
	tracker *costpkg.Tracker,
	payload ipc.SlashCommandPayload,
	sessionID string,
	startedAt time.Time,
	mode agent.ExecutionMode,
	activeModelID string,
	cwd string,
	messages []api.Message,
	client *api.LLMClient,
) (slashCommandState, error) {
	cmd := newSlashCommandContext(
		ctx,
		bridge,
		store,
		timingLogger,
		cfg,
		artifactManager,
		tracker,
		payload,
		sessionID,
		startedAt,
		mode,
		activeModelID,
		cwd,
		messages,
		client,
	)

	handler, ok := lookupSlashCommandHandler(cmd.command)
	if !ok {
		if err := bridge.EmitError(fmt.Sprintf("unknown slash command: %s", payload.Command), true); err != nil {
			return cmd.state, err
		}
		if err := bridge.Emit(ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: "end_turn"}); err != nil {
			return cmd.state, err
		}
		return cmd.state, nil
	}

	if err := handler.Handle(cmd); err != nil {
		return cmd.state, err
	}
	return cmd.state, nil
}

func emitTextResponse(bridge *ipc.Bridge, text string) error {
	if strings.TrimSpace(text) != "" {
		if err := bridge.Emit(ipc.EventTokenDelta, ipc.TokenDeltaPayload{Text: text}); err != nil {
			return err
		}
	}
	return bridge.Emit(ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: "end_turn"})
}

func appendSlashResponse(bridge *ipc.Bridge, text string) {
	if bridge == nil || strings.TrimSpace(text) == "" {
		return
	}
	_ = bridge.Emit(ipc.EventTokenDelta, ipc.TokenDeltaPayload{Text: text})
}

func gitHubCopilotPolicyModels(cfg config.Config) []string {
	models := []string{
		api.GitHubCopilotDefaultMainModel,
		api.GitHubCopilotDefaultSubagentModel,
	}
	if provider, model := config.ParseModel(strings.TrimSpace(cfg.Model)); normalizeProvider(provider) == "github-copilot" && strings.TrimSpace(model) != "" {
		models = append(models, model)
	}
	if provider, model := config.ParseModel(strings.TrimSpace(cfg.SubagentModel)); normalizeProvider(provider) == "github-copilot" && strings.TrimSpace(model) != "" {
		models = append(models, model)
	}
	return mergeGitHubCopilotModelIDs(nil, models)
}

func mergeGitHubCopilotModelIDs(existing []string, extra []string) []string {
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

func parseConnectArgs(args string) (string, string, error) {
	parts := strings.Fields(args)
	switch len(parts) {
	case 0:
		return "github-copilot", "", nil
	case 1:
		if strings.EqualFold(parts[0], "github-copilot") {
			return "github-copilot", "", nil
		}
		return "", "", fmt.Errorf("usage: /connect [github-copilot [enterprise-domain]]")
	case 2:
		if !strings.EqualFold(parts[0], "github-copilot") {
			return "", "", fmt.Errorf("usage: /connect [github-copilot [enterprise-domain]]")
		}
		return "github-copilot", parts[1], nil
	default:
		return "", "", fmt.Errorf("usage: /connect [github-copilot [enterprise-domain]]")
	}
}

func parseReasoningArgs(args string) (string, bool, error) {
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

func describeReasoningEffort(configured string, modelID string) string {
	effective := api.ClampReasoningEffort(modelID, configured)
	if effective == "" {
		effective = api.DefaultReasoningEffort(modelID)
		if effective == "" {
			if configured == "" {
				return "default"
			}
			return configured + " (saved for supported models)"
		}
		if configured == "" {
			return effective + " (default)"
		}
	}
	if configured != "" && configured != effective {
		return fmt.Sprintf("%s (clamped from %s for %s)", effective, configured, modelID)
	}
	return effective
}

func openBrowserURL(target string) error {
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

func emitSessionArtifacts(ctx context.Context, bridge *ipc.Bridge, artifactManager *artifactspkg.Manager, sessionID string) error {
	if artifactManager == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}

	artifacts, err := artifactManager.LoadSessionArtifacts(ctx, sessionID)
	if err != nil {
		if warning, ok := err.(*artifactspkg.ArtifactLoadWarning); ok {
			if emitErr := bridge.Emit(ipc.EventError, ipc.ErrorPayload{Message: warning.Error(), Recoverable: true}); emitErr != nil {
				return emitErr
			}
		} else {
			return err
		}
	}

	for index := len(artifacts) - 1; index >= 0; index-- {
		artifact := artifacts[index]
		if err := emitArtifactCreated(bridge, artifact.Artifact); err != nil {
			return err
		}
		if err := emitArtifactUpdated(bridge, artifact.Artifact, artifact.Content); err != nil {
			return err
		}
	}

	for _, artifact := range artifacts {
		if artifact.Artifact.Kind == artifactspkg.KindImplementationPlan && strings.TrimSpace(artifact.Content) != "" {
			return emitArtifactFocused(bridge, artifact.Artifact)
		}
	}

	return nil
}

func formatCostSummary(snapshot costpkg.TrackerSnapshot, activeModelID string) string {
	return fmt.Sprintf(
		"Model: %s\nTotal cost: $%.4f\nInput tokens: %d\nOutput tokens: %d\nAPI duration: %s\nTool duration: %s",
		activeModelID,
		snapshot.TotalCostUSD,
		snapshot.TotalInputTokens,
		snapshot.TotalOutputTokens,
		snapshot.TotalAPIDuration.Round(time.Millisecond),
		snapshot.TotalToolDuration.Round(time.Millisecond),
	)
}

func formatHelpText() string {
	var b strings.Builder
	b.WriteString("Available slash commands:\n\n")
	for _, descriptor := range sortedVisibleSlashCommands() {
		usage := descriptor.Usage
		if strings.TrimSpace(usage) == "" {
			usage = "/" + descriptor.Name
		}
		b.WriteString(fmt.Sprintf("  %-38s %s\n", usage, descriptor.Description))
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatStatusText(sessionID string, startedAt time.Time, mode agent.ExecutionMode, model string, cwd string, msgCount int, tracker *costpkg.Tracker) string {
	elapsed := time.Since(startedAt).Round(time.Second)
	snap := tracker.Snapshot()
	reasoning := describeReasoningEffort(strings.TrimSpace(config.Load().ReasoningEffort), model)
	return fmt.Sprintf(
		"Session: %s\nStarted: %s (%s ago)\nMode: %s\nModel: %s\nReasoning: %s\nCWD: %s\nMessages: %d\nCost: $%.4f\nTokens: %d in / %d out",
		sessionID,
		startedAt.Format(time.RFC3339),
		elapsed,
		string(mode),
		model,
		reasoning,
		cwd,
		msgCount,
		snap.TotalCostUSD,
		snap.TotalInputTokens,
		snap.TotalOutputTokens,
	)
}

func formatSessionList(sessions []session.Metadata, currentID string) string {
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

func gitDiff(args string) string {
	parts := []string{"diff", "--stat"}
	if strings.TrimSpace(args) != "" {
		parts = strings.Fields("diff " + args)
	}
	cmd := exec.Command("git", parts...)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Sprintf("git diff error: %v", err)
	}
	result := strings.TrimSpace(string(out))
	if len(result) > 5000 {
		result = result[:5000] + "\n[truncated]"
	}
	return result
}
