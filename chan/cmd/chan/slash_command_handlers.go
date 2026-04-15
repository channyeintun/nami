package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
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
	SessionID       string
	StartedAt       time.Time
	Mode            agent.ExecutionMode
	ActiveModelID   string
	SubagentModelID string
	CWD             string
	Messages        []api.Message
}

type slashCommandContext struct {
	ctx             context.Context
	bridge          *ipc.Bridge
	router          *ipc.MessageRouter
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
	"debug":     slashCommandHandlerFunc(handleDebugSlashCommand),
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
	"subagent":  slashCommandHandlerFunc(handleSubagentSlashCommand),
}

type modelSelectionPreset struct {
	Label       string
	Model       string
	Provider    string
	Description string
}

type modelSelectionChoice struct {
	Model    string
	Provider string
}

var curatedModelSelectionPresets = []modelSelectionPreset{
	{
		Label:       "Claude Sonnet 4.6",
		Model:       "claude-sonnet-4.6",
		Provider:    "anthropic",
		Description: "Sonnet preset",
	},
	{
		Label:       "Claude Opus 4.6",
		Model:       "claude-opus-4.6",
		Provider:    "anthropic",
		Description: "Opus preset",
	},
	{
		Label:       "Claude Haiku 4.5",
		Model:       "claude-haiku-4.5",
		Provider:    "anthropic",
		Description: "Haiku preset",
	},
	{
		Label:       "GPT 5.4",
		Model:       "gpt-5.4",
		Provider:    "openai",
		Description: "GPT preset",
	},
	{
		Label:       "GPT 5.4 Mini",
		Model:       "gpt-5.4-mini",
		Provider:    "openai",
		Description: "GPT mini preset",
	},
	{
		Label:       "Gemini 3 Flash",
		Model:       "gemini-3.0-flash",
		Provider:    "gemini",
		Description: "Gemini flash preset",
	},
	{
		Label:       "Gemini 3.1 Pro",
		Model:       "gemini-3.1-pro",
		Provider:    "gemini",
		Description: "Gemini pro preset",
	},
}

func newSlashCommandContext(
	ctx context.Context,
	bridge *ipc.Bridge,
	router *ipc.MessageRouter,
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
	subagentModelID string,
	cwd string,
	messages []api.Message,
	client *api.LLMClient,
) *slashCommandContext {
	return &slashCommandContext{
		ctx:             ctx,
		bridge:          bridge,
		router:          router,
		store:           store,
		timingLogger:    timingLogger,
		cfg:             cfg,
		artifactManager: artifactManager,
		tracker:         tracker,
		command:         strings.ToLower(strings.TrimSpace(payload.Command)),
		args:            strings.TrimSpace(payload.Args),
		state: slashCommandState{
			SessionID:       sessionID,
			StartedAt:       startedAt,
			Mode:            mode,
			ActiveModelID:   activeModelID,
			SubagentModelID: subagentModelID,
			CWD:             cwd,
			Messages:        messages,
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
		SessionID:     cmd.state.SessionID,
		CreatedAt:     cmd.state.StartedAt,
		Mode:          cmd.state.Mode,
		Model:         cmd.state.ActiveModelID,
		SubagentModel: cmd.state.SubagentModelID,
		CWD:           cmd.state.CWD,
		Branch:        agent.LoadTurnContext().GitBranch,
		Tracker:       cmd.tracker,
		Messages:      cmd.state.Messages,
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
	nextClient = wrapClientWithDebug(nextClient)
	*cmd.client = nextClient
	cmd.state.ActiveModelID = modelRef(result.Provider, nextClient.ModelID())
	cmd.state.SubagentModelID = defaultSessionSubagentModel(result.Config, cmd.state.ActiveModelID)
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
	selected := modelSelectionChoice{Model: strings.TrimSpace(cmd.args)}
	if selected.Model == "" {
		var err error
		selected, err = promptModelSelection(cmd, cmd.state.ActiveModelID)
		if err != nil {
			return err
		}
		if strings.TrimSpace(selected.Model) == "" {
			return emitTextResponse(cmd.bridge, "Model selection cancelled.")
		}
	}

	if strings.EqualFold(selected.Model, "default") {
		configuredChoice, err := configuredModelChoice(cmd.cfg.Model)
		if err != nil {
			return emitTextResponse(cmd.bridge, err.Error())
		}
		selected = configuredChoice
	}
	requestedDefault := strings.EqualFold(strings.TrimSpace(cmd.args), "default")

	selectedModel, err := normalizeModelSlashInput(selected.Model)
	if err != nil {
		return emitTextResponse(cmd.bridge, err.Error())
	}

	currentProvider, _ := config.ParseModel(cmd.state.ActiveModelID)
	currentProvider = normalizeProvider(currentProvider)
	providerHint := strings.TrimSpace(selected.Provider)
	provider, model := resolveModelSelection(selectedModel, currentProvider)
	if providerHint != "" && (currentProvider != "github-copilot" || requestedDefault) {
		provider = normalizeProvider(providerHint)
		model = selectedModel
	}
	nextClient, err := newLLMClient(provider, model, cmd.cfg)
	if err != nil {
		return cmd.bridge.EmitError(fmt.Sprintf("switch model %q: %v", selectedModel, err), true)
	}

	nextClient = wrapClientWithDebug(nextClient)
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

func handleSubagentSlashCommand(cmd *slashCommandContext) error {
	currentSelection := strings.TrimSpace(cmd.state.SubagentModelID)
	if currentSelection == "" {
		currentSelection = defaultSessionSubagentModel(config.Load(), cmd.state.ActiveModelID)
	}

	selected := modelSelectionChoice{Model: strings.TrimSpace(cmd.args)}
	if selected.Model == "" {
		var err error
		selected, err = promptModelSelection(cmd, currentSelection)
		if err != nil {
			return err
		}
		if strings.TrimSpace(selected.Model) == "" {
			return emitTextResponse(cmd.bridge, "Subagent model selection cancelled.")
		}
	}

	switch {
	case strings.EqualFold(selected.Model, "help"), strings.EqualFold(selected.Model, "status"), strings.EqualFold(selected.Model, "current"):
		return emitTextResponse(cmd.bridge, formatSubagentHelpText(currentSelection))
	case strings.EqualFold(selected.Model, "default"):
		cmd.state.SubagentModelID = defaultSessionSubagentModel(config.Load(), cmd.state.ActiveModelID)
		if err := cmd.persistState(); err != nil {
			return err
		}
		return emitTextResponse(cmd.bridge, fmt.Sprintf("Reset subagent model to %s", formatModelSelectionLabel(cmd.state.SubagentModelID)))
	}

	selectedModel, err := normalizeModelSlashInput(selected.Model)
	if err != nil {
		return emitTextResponse(cmd.bridge, err.Error())
	}

	currentProvider, _ := config.ParseModel(cmd.state.ActiveModelID)
	currentProvider = normalizeProvider(currentProvider)
	providerHint := strings.TrimSpace(selected.Provider)
	provider, model := resolveModelSelection(selectedModel, currentProvider)
	if currentProvider != "github-copilot" && providerHint != "" {
		provider = normalizeProvider(providerHint)
		model = selectedModel
	}
	cmd.state.SubagentModelID = modelRef(provider, model)
	if err := cmd.persistState(); err != nil {
		return err
	}
	return emitTextResponse(cmd.bridge, fmt.Sprintf("Set subagent model to %s", formatModelSelectionLabel(cmd.state.SubagentModelID)))
}

func normalizeModelSlashInput(input string) (string, error) {
	compact := strings.TrimSpace(input)
	if compact == "" {
		return "", fmt.Errorf("model cannot be empty")
	}
	if strings.Contains(compact, "/") {
		return "", fmt.Errorf("/model only accepts a model name. Remove the provider prefix and try again")
	}
	return compact, nil
}

func configuredModelChoice(raw string) (modelSelectionChoice, error) {
	provider, model := config.ParseModel(strings.TrimSpace(raw))
	provider = normalizeProvider(provider)
	if strings.TrimSpace(model) == "" {
		model = strings.TrimSpace(provider)
		provider = ""
	}
	if strings.TrimSpace(model) == "" {
		return modelSelectionChoice{}, fmt.Errorf("default model is not configured")
	}
	return modelSelectionChoice{Model: model, Provider: provider}, nil
}

func promptModelSelection(cmd *slashCommandContext, currentSelection string) (modelSelectionChoice, error) {
	activeModelID := strings.TrimSpace(currentSelection)
	_, activeModel := config.ParseModel(activeModelID)
	if strings.TrimSpace(activeModel) == "" {
		activeModel = activeModelID
	}

	options := make([]ipc.ModelSelectionOptionPayload, 0, len(curatedModelSelectionPresets)+1)
	for _, preset := range curatedModelSelectionPresets {
		options = append(options, ipc.ModelSelectionOptionPayload{
			Label:       preset.Label,
			Model:       preset.Model,
			Provider:    preset.Provider,
			Description: preset.Description,
			Active:      strings.EqualFold(strings.TrimSpace(preset.Model), strings.TrimSpace(activeModel)),
		})
	}
	options = append(options, ipc.ModelSelectionOptionPayload{
		Label:       "Custom model",
		Description: "Enter any model id",
		IsCustom:    true,
	})

	requestID := fmt.Sprintf("model-%d", time.Now().UnixNano())
	if err := cmd.bridge.Emit(ipc.EventModelSelectionRequested, ipc.ModelSelectionRequestedPayload{
		RequestID:    requestID,
		CurrentModel: activeModel,
		Options:      options,
	}); err != nil {
		return modelSelectionChoice{}, err
	}

	deferred := make([]ipc.ClientMessage, 0, 4)
	defer func() {
		cmd.router.Requeue(deferred...)
	}()

	for {
		msg, err := cmd.router.Next(cmd.ctx)
		if err != nil {
			return modelSelectionChoice{}, err
		}

		switch msg.Type {
		case ipc.MsgModelSelectionResponse:
			var payload ipc.ModelSelectionResponsePayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return modelSelectionChoice{}, fmt.Errorf("decode model selection response: %w", err)
			}
			if payload.RequestID != requestID {
				deferred = append(deferred, msg)
				continue
			}
			if payload.Cancel {
				return modelSelectionChoice{}, nil
			}
			selected := strings.TrimSpace(payload.Model)
			if selected == "" {
				return modelSelectionChoice{}, nil
			}
			return modelSelectionChoice{
				Model:    selected,
				Provider: strings.TrimSpace(payload.Provider),
			}, nil
		case ipc.MsgShutdown:
			return modelSelectionChoice{}, context.Canceled
		default:
			deferred = append(deferred, msg)
		}
	}
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
	targetID := strings.TrimSpace(cmd.args)
	if targetID == "" {
		targetIDs, err := promptResumeSelection(cmd)
		if err != nil {
			return err
		}
		if targetIDs == "" {
			return emitTextResponse(cmd.bridge, "Resume cancelled.")
		}
		targetID = targetIDs
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
		*cmd.client = wrapClientWithDebug(restoredClient)
		cmd.state.ActiveModelID = modelRef(provider, restoredClient.ModelID())
		if err := emitToolUseCapabilityNotice(cmd.bridge, cmd.state.ActiveModelID, *cmd.client, nil); err != nil {
			return err
		}
	}
	cmd.state.SubagentModelID = strings.TrimSpace(restored.Metadata.SubagentModel)
	if cmd.state.SubagentModelID == "" {
		cmd.state.SubagentModelID = defaultSessionSubagentModel(config.Load(), cmd.state.ActiveModelID)
	}

	if restored.Metadata.CWD != "" {
		if err := os.Chdir(restored.Metadata.CWD); err == nil {
			cmd.state.CWD = restored.Metadata.CWD
		}
	}
	if err := rebindDebugSession(cmd); err != nil && debuglog.IsEnabled() {
		appendSlashResponse(cmd.bridge, fmt.Sprintf("Debug logging rebind failed: %v\n\n", err))
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

func promptResumeSelection(cmd *slashCommandContext) (string, error) {
	sessions, err := cmd.store.ListSessions()
	if err != nil {
		return "", cmd.bridge.EmitError(fmt.Sprintf("list sessions: %v", err), true)
	}

	options := make([]ipc.ResumeSelectionSessionPayload, 0, 20)
	for _, meta := range sessions {
		if meta.SessionID == "" || meta.SessionID == cmd.state.SessionID {
			continue
		}
		options = append(options, ipc.ResumeSelectionSessionPayload{
			SessionID:    meta.SessionID,
			Title:        meta.Title,
			UpdatedAt:    meta.UpdatedAt.Format(time.RFC3339),
			Model:        meta.Model,
			TotalCostUSD: meta.TotalCostUSD,
		})
		if len(options) >= 20 {
			break
		}
	}

	if len(options) == 0 {
		return "", cmd.bridge.EmitError("no resumable sessions found", true)
	}

	requestID := fmt.Sprintf("resume-%d", time.Now().UnixNano())
	if err := cmd.bridge.Emit(ipc.EventResumeSelectionRequested, ipc.ResumeSelectionRequestedPayload{
		RequestID: requestID,
		Sessions:  options,
	}); err != nil {
		return "", err
	}

	deferred := make([]ipc.ClientMessage, 0, 4)
	defer func() {
		cmd.router.Requeue(deferred...)
	}()

	for {
		msg, err := cmd.router.Next(cmd.ctx)
		if err != nil {
			return "", err
		}

		switch msg.Type {
		case ipc.MsgResumeSelectionResponse:
			var payload ipc.ResumeSelectionResponsePayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return "", fmt.Errorf("decode resume selection response: %w", err)
			}
			if payload.RequestID != requestID {
				deferred = append(deferred, msg)
				continue
			}
			if payload.Cancel {
				return "", nil
			}
			selectedID := strings.TrimSpace(payload.SessionID)
			if selectedID == "" {
				return "", nil
			}
			return selectedID, nil
		case ipc.MsgShutdown:
			return "", context.Canceled
		default:
			deferred = append(deferred, msg)
		}
	}
}

func handleClearSlashCommand(cmd *slashCommandContext) error {
	// Archive the current session before clearing
	if len(cmd.state.Messages) > 0 {
		if err := persistSessionState(cmd.store, sessionStateParams{
			SessionID:     cmd.state.SessionID,
			CreatedAt:     cmd.state.StartedAt,
			Mode:          cmd.state.Mode,
			Model:         cmd.state.ActiveModelID,
			SubagentModel: cmd.state.SubagentModelID,
			CWD:           cmd.state.CWD,
			Branch:        agent.LoadTurnContext().GitBranch,
			Tracker:       cmd.tracker,
			Messages:      cmd.state.Messages,
		}); err != nil {
			return err
		}
	}

	// Start a new session
	cmd.state.Messages = cmd.state.Messages[:0]
	cmd.tracker.Reset()
	newID, err := newSessionID()
	if err != nil {
		return err
	}
	cmd.state.SessionID = newID
	cmd.state.StartedAt = time.Now()
	cmd.state.SubagentModelID = defaultSessionSubagentModel(config.Load(), cmd.state.ActiveModelID)
	if err := rebindDebugSession(cmd); err != nil && debuglog.IsEnabled() {
		appendSlashResponse(cmd.bridge, fmt.Sprintf("Debug logging rebind failed: %v\n\n", err))
	}
	if err := cmd.persistState(); err != nil {
		return err
	}
	if err := emitSessionUpdated(cmd.bridge, cmd.state.SessionID, ""); err != nil {
		return err
	}
	if err := emitCostUpdate(cmd.bridge, cmd.tracker); err != nil {
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
	statusText := formatStatusText(cmd.state.SessionID, cmd.state.StartedAt, cmd.state.Mode, cmd.state.ActiveModelID, cmd.state.SubagentModelID, cmd.state.CWD, len(cmd.state.Messages), cmd.tracker)
	return emitTextResponse(cmd.bridge, statusText)
}

func handleDebugSlashCommand(cmd *slashCommandContext) error {
	parts := strings.Fields(strings.TrimSpace(cmd.args))
	subcommand := ""
	if len(parts) > 0 {
		subcommand = strings.ToLower(parts[0])
	}

	switch subcommand {
	case "", "on":
		if err := rebindDebugSession(cmd); err != nil {
			return emitTextResponse(cmd.bridge, fmt.Sprintf("Enable debug logging failed: %v", err))
		}
		path, err := debuglog.Enable()
		if err != nil {
			return emitTextResponse(cmd.bridge, fmt.Sprintf("Enable debug logging failed: %v", err))
		}
		if err := openDebugMonitorPopup(path); err != nil {
			return emitTextResponse(cmd.bridge, fmt.Sprintf("Debug logging enabled at %s\n\nAutomatic monitor launch failed: %v\nRun manually: %s debug-view --file %s", path, err, os.Args[0], path))
		}
		return emitTextResponse(cmd.bridge, fmt.Sprintf("Debug logging enabled. Opened live monitor for %s", path))
	case "status":
		if err := rebindDebugSession(cmd); err != nil && debuglog.IsEnabled() {
			return emitTextResponse(cmd.bridge, fmt.Sprintf("Debug status unavailable: %v", err))
		}
		status := debuglog.CurrentStatus()
		path := status.Path
		if strings.TrimSpace(path) == "" {
			path = filepath.Join(cmd.store.SessionDir(cmd.state.SessionID), debuglog.DefaultPath)
		}
		state := "disabled"
		if status.Enabled {
			state = "enabled"
		}
		return emitTextResponse(cmd.bridge, fmt.Sprintf("Debug logging is %s\nSession: %s\nPath: %s\nEntries: %d", state, cmd.state.SessionID, path, status.Seq))
	case "path":
		if err := rebindDebugSession(cmd); err != nil && debuglog.IsEnabled() {
			return emitTextResponse(cmd.bridge, fmt.Sprintf("Debug path unavailable: %v", err))
		}
		path := debuglog.CurrentPath()
		if strings.TrimSpace(path) == "" {
			path = filepath.Join(cmd.store.SessionDir(cmd.state.SessionID), debuglog.DefaultPath)
		}
		return emitTextResponse(cmd.bridge, path)
	case "off":
		if err := debuglog.Disable(); err != nil {
			return emitTextResponse(cmd.bridge, fmt.Sprintf("Disable debug logging failed: %v", err))
		}
		return emitTextResponse(cmd.bridge, "Debug logging disabled.")
	default:
		return emitTextResponse(cmd.bridge, "usage: /debug [status|path|off]")
	}
}

func rebindDebugSession(cmd *slashCommandContext) error {
	return debuglog.ConfigureSession(cmd.state.SessionID, cmd.store.SessionDir(cmd.state.SessionID))
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
