package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/channyeintun/chan/internal/agent"
	"github.com/channyeintun/chan/internal/api"
	artifactspkg "github.com/channyeintun/chan/internal/artifacts"
	"github.com/channyeintun/chan/internal/compact"
	"github.com/channyeintun/chan/internal/config"
	costpkg "github.com/channyeintun/chan/internal/cost"
	"github.com/channyeintun/chan/internal/debuglog"
	"github.com/channyeintun/chan/internal/ipc"
	"github.com/channyeintun/chan/internal/session"
	"github.com/channyeintun/chan/internal/timing"
)

type slashCommandState struct {
	SessionID     string
	StartedAt     time.Time
	Mode          agent.ExecutionMode
	ActiveModelID string
	CWD           string
	Messages      []api.Message
}

type slashCommandContext struct {
	ctx             context.Context
	bridge          *ipc.Bridge
	store           *session.Store
	timingLogger    *timing.Logger
	cfg             config.Config
	artifactManager *artifactspkg.Manager
	tracker         *costpkg.Tracker
	command         string
	args            string
	state           slashCommandState
	client          *api.LLMClient
}

type slashCommandHandler interface {
	Handle(*slashCommandContext) error
}

type slashCommandHandlerFunc func(*slashCommandContext) error

func (fn slashCommandHandlerFunc) Handle(cmd *slashCommandContext) error {
	return fn(cmd)
}

var slashCommandRegistry = map[string]slashCommandHandler{
	"clear":     slashCommandHandlerFunc(handleClearSlashCommand),
	"compact":   slashCommandHandlerFunc(handleCompactSlashCommand),
	"connect":   slashCommandHandlerFunc(handleConnectSlashCommand),
	"cost":      slashCommandHandlerFunc(handleCostSlashCommand),
	"diff":      slashCommandHandlerFunc(handleDiffSlashCommand),
	"fast":      slashCommandHandlerFunc(handleFastSlashCommand),
	"help":      slashCommandHandlerFunc(handleHelpSlashCommand),
	"model":     slashCommandHandlerFunc(handleModelSlashCommand),
	"plan":      slashCommandHandlerFunc(handlePlanSlashCommand),
	"plan-mode": slashCommandHandlerFunc(handlePlanSlashCommand),
	"reasoning": slashCommandHandlerFunc(handleReasoningSlashCommand),
	"resume":    slashCommandHandlerFunc(handleResumeSlashCommand),
	"sessions":  slashCommandHandlerFunc(handleSessionsSlashCommand),
	"status":    slashCommandHandlerFunc(handleStatusSlashCommand),
	"usage":     slashCommandHandlerFunc(handleCostSlashCommand),
}

func newSlashCommandContext(
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
) *slashCommandContext {
	return &slashCommandContext{
		ctx:             ctx,
		bridge:          bridge,
		store:           store,
		timingLogger:    timingLogger,
		cfg:             cfg,
		artifactManager: artifactManager,
		tracker:         tracker,
		command:         strings.ToLower(strings.TrimSpace(payload.Command)),
		args:            strings.TrimSpace(payload.Args),
		state: slashCommandState{
			SessionID:     sessionID,
			StartedAt:     startedAt,
			Mode:          mode,
			ActiveModelID: activeModelID,
			CWD:           cwd,
			Messages:      messages,
		},
		client: client,
	}
}

func lookupSlashCommandHandler(command string) (slashCommandHandler, bool) {
	handler, ok := slashCommandRegistry[command]
	return handler, ok
}

func (cmd *slashCommandContext) persistState() error {
	return persistSessionState(cmd.store, sessionStateParams{
		SessionID: cmd.state.SessionID,
		CreatedAt: cmd.state.StartedAt,
		Mode:      cmd.state.Mode,
		Model:     cmd.state.ActiveModelID,
		CWD:       cmd.state.CWD,
		Branch:    agent.LoadTurnContext().GitBranch,
		Tracker:   cmd.tracker,
		Messages:  cmd.state.Messages,
	})
}

type connectResult struct {
	Provider      string
	Model         string
	Config        config.Config
	FormatMessage func(activeModelID string) string
}

type connectProviderFunc func(cmd *slashCommandContext, extraArgs string) (*connectResult, error)

var connectProviderRegistry = map[string]connectProviderFunc{
	"github-copilot": connectGitHubCopilot,
}

func handleConnectSlashCommand(cmd *slashCommandContext) error {
	providerName, extraArgs, err := parseConnectArgs(cmd.args)
	if err != nil {
		return emitTextResponse(cmd.bridge, err.Error())
	}

	handler, ok := connectProviderRegistry[providerName]
	if !ok {
		return emitTextResponse(cmd.bridge, fmt.Sprintf("unsupported connect provider: %s", providerName))
	}

	result, err := handler(cmd, extraArgs)
	if err != nil {
		return err
	}
	if result == nil {
		return nil
	}

	nextClient, err := newLLMClient(result.Provider, result.Model, result.Config)
	if err != nil {
		return emitTextResponse(cmd.bridge, fmt.Sprintf("initialize %s client: %v", result.Provider, err))
	}
	if debuglog.Enabled {
		nextClient = newDebugClientProxy(nextClient)
	}
	*cmd.client = nextClient
	cmd.state.ActiveModelID = modelRef(result.Provider, nextClient.ModelID())
	if err := emitToolUseCapabilityNotice(cmd.bridge, cmd.state.ActiveModelID, *cmd.client, nil); err != nil {
		return err
	}
	if err := cmd.persistState(); err != nil {
		return err
	}
	if err := emitModelChanged(cmd.bridge, cmd.state.ActiveModelID, *cmd.client); err != nil {
		return err
	}
	if err := emitContextWindowUsage(cmd.bridge, *cmd.client, cmd.state.Messages); err != nil {
		return err
	}
	return emitTextResponse(cmd.bridge, result.FormatMessage(cmd.state.ActiveModelID))
}

func connectGitHubCopilot(cmd *slashCommandContext, enterpriseInput string) (*connectResult, error) {
	persisted := config.Load()
	domain, err := api.NormalizeGitHubCopilotDomain(enterpriseInput)
	if err != nil {
		return nil, emitTextResponse(cmd.bridge, err.Error())
	}

	copilotAuth := persisted.GitHubCopilot
	if strings.TrimSpace(domain) != "" {
		copilotAuth.EnterpriseDomain = domain
	}

	appendSlashResponse(cmd.bridge, "Connecting GitHub Copilot...\n\n")

	refreshCtx, cancel := context.WithTimeout(cmd.ctx, 2*time.Minute)
	defer cancel()

	if strings.TrimSpace(copilotAuth.GitHubToken) != "" {
		appendSlashResponse(cmd.bridge, "Refreshing saved credentials...\n\n")
		refreshed, refreshErr := api.RefreshGitHubCopilotToken(refreshCtx, copilotAuth.GitHubToken, copilotAuth.EnterpriseDomain)
		if refreshErr == nil {
			copilotAuth.AccessToken = refreshed.AccessToken
			copilotAuth.ExpiresAtUnixMS = refreshed.ExpiresAt.UnixMilli()
		} else {
			appendSlashResponse(cmd.bridge, "Saved credentials could not be refreshed. Starting device login...\n\n")
			copilotAuth.AccessToken = ""
			copilotAuth.ExpiresAtUnixMS = 0
		}
	}

	if strings.TrimSpace(copilotAuth.AccessToken) == "" {
		device, err := api.StartGitHubCopilotDeviceFlow(refreshCtx, copilotAuth.EnterpriseDomain)
		if err != nil {
			return nil, emitTextResponse(cmd.bridge, fmt.Sprintf("GitHub Copilot connect failed: %v", err))
		}

		browserMessage := ""
		if err := openBrowserURL(device.VerificationURI); err == nil {
			browserMessage = "Opened the browser automatically.\n"
		}
		appendSlashResponse(cmd.bridge, fmt.Sprintf("%sVisit: %s\nEnter code: %s\n\nWaiting for GitHub authorization...\n\n", browserMessage, device.VerificationURI, device.UserCode))

		githubToken, err := api.PollGitHubCopilotGitHubToken(
			refreshCtx,
			copilotAuth.EnterpriseDomain,
			device.DeviceCode,
			device.IntervalSeconds,
			device.ExpiresIn,
		)
		if err != nil {
			return nil, emitTextResponse(cmd.bridge, fmt.Sprintf("GitHub Copilot connect failed: %v", err))
		}

		refreshed, err := api.RefreshGitHubCopilotToken(refreshCtx, githubToken, copilotAuth.EnterpriseDomain)
		if err != nil {
			return nil, emitTextResponse(cmd.bridge, fmt.Sprintf("GitHub Copilot token exchange failed: %v", err))
		}

		copilotAuth.GitHubToken = githubToken
		copilotAuth.AccessToken = refreshed.AccessToken
		copilotAuth.ExpiresAtUnixMS = refreshed.ExpiresAt.UnixMilli()
	}

	policySummary := ""
	if strings.TrimSpace(copilotAuth.AccessToken) != "" {
		appendSlashResponse(cmd.bridge, "Enabling GitHub Copilot model policies...\n\n")
		policyCtx, policyCancel := context.WithTimeout(cmd.ctx, 20*time.Second)
		modelIDs := gitHubCopilotPolicyModels(persisted)
		if discovered, discoverErr := api.ListGitHubCopilotModelIDs(policyCtx, copilotAuth.AccessToken, copilotAuth.EnterpriseDomain); discoverErr == nil {
			modelIDs = mergeGitHubCopilotModelIDs(modelIDs, discovered)
		}
		failures := api.EnableGitHubCopilotModels(policyCtx, copilotAuth.AccessToken, copilotAuth.EnterpriseDomain, modelIDs)
		policyCancel()
		if total := len(modelIDs); total > 0 {
			policySummary = fmt.Sprintf(" Enabled policy for %d/%d Copilot models.", total-len(failures), total)
		}
	}

	persisted.GitHubCopilot = copilotAuth
	persisted.Model = modelRef("github-copilot", api.Presets["github-copilot"].DefaultModel)
	persisted.SubagentModel = modelRef("github-copilot", api.GitHubCopilotDefaultSubagentModel)
	if strings.TrimSpace(persisted.ReasoningEffort) == "" {
		persisted.ReasoningEffort = api.ReasoningEffortMedium
	}
	if err := config.Save(persisted); err != nil {
		return nil, emitTextResponse(cmd.bridge, fmt.Sprintf("save GitHub Copilot credentials: %v", err))
	}

	return &connectResult{
		Provider: "github-copilot",
		Model:    api.Presets["github-copilot"].DefaultModel,
		Config:   persisted,
		FormatMessage: func(activeModelID string) string {
			return fmt.Sprintf(
				"GitHub Copilot connected. Set main model to %s, subagent model to github-copilot/%s, and reasoning effort to %s.%s",
				activeModelID,
				api.GitHubCopilotDefaultSubagentModel,
				persisted.ReasoningEffort,
				policySummary,
			)
		},
	}, nil
}

func handlePlanSlashCommand(cmd *slashCommandContext) error {
	cmd.state.Mode = agent.ModePlan
	if err := cmd.persistState(); err != nil {
		return err
	}
	return cmd.bridge.Emit(ipc.EventModeChanged, ipc.ModeChangedPayload{Mode: string(cmd.state.Mode)})
}

func handleFastSlashCommand(cmd *slashCommandContext) error {
	cmd.state.Mode = agent.ModeFast
	if err := cmd.persistState(); err != nil {
		return err
	}
	return cmd.bridge.Emit(ipc.EventModeChanged, ipc.ModeChangedPayload{Mode: string(cmd.state.Mode)})
}

func handleModelSlashCommand(cmd *slashCommandContext) error {
	if cmd.args == "" {
		return emitTextResponse(cmd.bridge, fmt.Sprintf("Current model: %s", cmd.state.ActiveModelID))
	}

	selectedModel := cmd.args
	if strings.EqualFold(strings.TrimSpace(cmd.args), "default") {
		selectedModel = cmd.cfg.Model
	}

	currentProvider, _ := config.ParseModel(cmd.state.ActiveModelID)
	provider, model := resolveModelSelection(selectedModel, currentProvider)
	nextClient, err := newLLMClient(provider, model, cmd.cfg)
	if err != nil {
		return cmd.bridge.EmitError(fmt.Sprintf("switch model %q: %v", cmd.args, err), true)
	}

	if debuglog.Enabled {
		nextClient = newDebugClientProxy(nextClient)
	}
	*cmd.client = nextClient
	cmd.state.ActiveModelID = modelRef(provider, nextClient.ModelID())
	if err := emitToolUseCapabilityNotice(cmd.bridge, cmd.state.ActiveModelID, *cmd.client, nil); err != nil {
		return err
	}
	if err := cmd.persistState(); err != nil {
		return err
	}
	if err := emitModelChanged(cmd.bridge, cmd.state.ActiveModelID, *cmd.client); err != nil {
		return err
	}
	if err := emitContextWindowUsage(cmd.bridge, *cmd.client, cmd.state.Messages); err != nil {
		return err
	}
	return emitTextResponse(cmd.bridge, fmt.Sprintf("Set model to %s", cmd.state.ActiveModelID))
}

func handleReasoningSlashCommand(cmd *slashCommandContext) error {
	persisted := config.Load()
	currentModelID := cmd.state.ActiveModelID
	if cmd.client != nil && *cmd.client != nil {
		currentModelID = strings.TrimSpace((*cmd.client).ModelID())
	}
	current := describeReasoningEffort(strings.TrimSpace(persisted.ReasoningEffort), currentModelID)
	if strings.TrimSpace(cmd.args) == "" {
		return emitTextResponse(cmd.bridge, fmt.Sprintf("Current reasoning effort: %s", current))
	}

	nextEffort, clearSetting, err := parseReasoningArgs(cmd.args)
	if err != nil {
		return emitTextResponse(cmd.bridge, err.Error())
	}

	if clearSetting {
		persisted.ReasoningEffort = ""
	} else {
		persisted.ReasoningEffort = nextEffort
	}
	if err := config.Save(persisted); err != nil {
		return emitTextResponse(cmd.bridge, fmt.Sprintf("save reasoning effort: %v", err))
	}

	updated := describeReasoningEffort(strings.TrimSpace(persisted.ReasoningEffort), currentModelID)
	return emitTextResponse(cmd.bridge, fmt.Sprintf("Set reasoning effort to %s", updated))
}

func handleCostSlashCommand(cmd *slashCommandContext) error {
	return emitTextResponse(cmd.bridge, formatCostSummary(cmd.tracker.Snapshot(), cmd.state.ActiveModelID))
}

func handleCompactSlashCommand(cmd *slashCommandContext) error {
	if len(cmd.state.Messages) == 0 {
		return cmd.bridge.EmitError("no messages to compact", true)
	}

	resolvedClient, nextModelID, err := ensureClientForSelection(cmd.state.ActiveModelID, cmd.cfg, *cmd.client)
	if err != nil {
		return cmd.bridge.EmitError(fmt.Sprintf("initialize model %q: %v", cmd.state.ActiveModelID, err), true)
	}
	*cmd.client = resolvedClient
	cmd.state.ActiveModelID = nextModelID

	tokensBefore := compact.EstimateConversationTokens(cmd.state.Messages)
	if err := cmd.bridge.Emit(ipc.EventCompactStart, ipc.CompactStartPayload{
		Strategy:     string(agent.CompactManual),
		TokensBefore: tokensBefore,
	}); err != nil {
		return err
	}

	result, err := compactWithMetrics(cmd.ctx, cmd.bridge, cmd.tracker, *cmd.client, cmd.timingLogger, cmd.state.SessionID, 0, string(agent.CompactManual), cmd.state.Messages)
	if err != nil {
		return cmd.bridge.EmitError(fmt.Sprintf("compact conversation: %v", err), true)
	}

	cmd.state.Messages = result.Messages
	tokensAfter := compact.EstimateConversationTokens(cmd.state.Messages)
	if err := cmd.persistState(); err != nil {
		return err
	}
	if err := cmd.bridge.Emit(ipc.EventCompactEnd, ipc.CompactEndPayload{TokensAfter: tokensAfter}); err != nil {
		return err
	}
	if err := emitContextWindowUsage(cmd.bridge, *cmd.client, cmd.state.Messages); err != nil {
		return err
	}
	return emitTextResponse(cmd.bridge, fmt.Sprintf("Compacted conversation with %s. Tokens %d -> %d.", result.Strategy, tokensBefore, tokensAfter))
}

func handleResumeSlashCommand(cmd *slashCommandContext) error {
	targetID := cmd.args
	if targetID == "" {
		meta, err := cmd.store.LatestResumeCandidate(cmd.state.SessionID)
		if err != nil {
			return cmd.bridge.EmitError(err.Error(), true)
		}
		targetID = meta.SessionID
	}

	restored, err := cmd.store.Restore(targetID)
	if err != nil {
		return cmd.bridge.EmitError(fmt.Sprintf("restore session %q: %v", targetID, err), true)
	}

	cmd.state.Messages = append(cmd.state.Messages[:0], restored.Messages...)
	cmd.state.SessionID = restored.Metadata.SessionID
	if !restored.Metadata.CreatedAt.IsZero() {
		cmd.state.StartedAt = restored.Metadata.CreatedAt
	}
	cmd.state.Mode = parseExecutionMode(restored.Metadata.Mode)

	if restored.Metadata.Model != "" {
		provider, model := config.ParseModel(restored.Metadata.Model)
		provider = normalizeProvider(provider)
		restoredClient, err := newLLMClient(provider, model, cmd.cfg)
		if err != nil {
			*cmd.client = nil
			cmd.state.ActiveModelID = modelRef(provider, model)
			return cmd.bridge.EmitError(fmt.Sprintf("restore model %q: %v", restored.Metadata.Model, err), true)
		}
		*cmd.client = restoredClient
		cmd.state.ActiveModelID = modelRef(provider, restoredClient.ModelID())
		if err := emitToolUseCapabilityNotice(cmd.bridge, cmd.state.ActiveModelID, *cmd.client, nil); err != nil {
			return err
		}
	}

	if restored.Metadata.CWD != "" {
		if err := os.Chdir(restored.Metadata.CWD); err == nil {
			cmd.state.CWD = restored.Metadata.CWD
		}
	}

	if err := cmd.persistState(); err != nil {
		return err
	}
	if err := cmd.bridge.Emit(ipc.EventSessionRestored, ipc.SessionRestoredPayload{
		SessionID: cmd.state.SessionID,
		Mode:      string(cmd.state.Mode),
	}); err != nil {
		return err
	}
	if err := emitSessionUpdated(cmd.bridge, cmd.state.SessionID, restored.Metadata.Title); err != nil {
		return err
	}
	if err := emitModelChanged(cmd.bridge, cmd.state.ActiveModelID, *cmd.client); err != nil {
		return err
	}
	if err := emitContextWindowUsage(cmd.bridge, *cmd.client, cmd.state.Messages); err != nil {
		return err
	}
	if err := cmd.bridge.Emit(ipc.EventModeChanged, ipc.ModeChangedPayload{Mode: string(cmd.state.Mode)}); err != nil {
		return err
	}
	if err := emitSessionArtifacts(cmd.ctx, cmd.bridge, cmd.artifactManager, cmd.state.SessionID); err != nil {
		return err
	}
	return emitTextResponse(cmd.bridge, fmt.Sprintf("Resumed session %s with %d messages.", cmd.state.SessionID, len(cmd.state.Messages)))
}

func handleClearSlashCommand(cmd *slashCommandContext) error {
	cmd.state.Messages = cmd.state.Messages[:0]
	newID, err := newSessionID()
	if err != nil {
		return err
	}
	cmd.state.SessionID = newID
	cmd.state.StartedAt = time.Now()
	if err := cmd.persistState(); err != nil {
		return err
	}
	if err := emitSessionUpdated(cmd.bridge, cmd.state.SessionID, ""); err != nil {
		return err
	}
	if err := emitContextWindowUsage(cmd.bridge, *cmd.client, cmd.state.Messages); err != nil {
		return err
	}
	return emitTextResponse(cmd.bridge, "Conversation cleared. New session started.")
}

func handleHelpSlashCommand(cmd *slashCommandContext) error {
	return emitTextResponse(cmd.bridge, formatHelpText())
}

func handleStatusSlashCommand(cmd *slashCommandContext) error {
	statusText := formatStatusText(cmd.state.SessionID, cmd.state.StartedAt, cmd.state.Mode, cmd.state.ActiveModelID, cmd.state.CWD, len(cmd.state.Messages), cmd.tracker)
	return emitTextResponse(cmd.bridge, statusText)
}

func handleSessionsSlashCommand(cmd *slashCommandContext) error {
	sessions, err := cmd.store.ListSessions()
	if err != nil {
		return cmd.bridge.EmitError(fmt.Sprintf("list sessions: %v", err), true)
	}
	return emitTextResponse(cmd.bridge, formatSessionList(sessions, cmd.state.SessionID))
}

func handleDiffSlashCommand(cmd *slashCommandContext) error {
	diffOutput := gitDiff(cmd.args)
	if strings.TrimSpace(diffOutput) == "" {
		diffOutput = "No changes detected."
	}
	return emitTextResponse(cmd.bridge, diffOutput)
}
