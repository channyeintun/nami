package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	artifactspkg "github.com/channyeintun/nami/internal/artifacts"
	"github.com/channyeintun/nami/internal/clientdebug"
	commandspkg "github.com/channyeintun/nami/internal/commands"
	"github.com/channyeintun/nami/internal/compact"
	"github.com/channyeintun/nami/internal/config"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/debuglog"
	"github.com/channyeintun/nami/internal/ipc"
	mcppkg "github.com/channyeintun/nami/internal/mcp"
	"github.com/channyeintun/nami/internal/session"
	"github.com/channyeintun/nami/internal/timing"
)

type slashCommandState struct {
	SessionID       string
	StartedAt       time.Time
	Mode            agent.ExecutionMode
	ActiveModelID   string
	SubagentModelID string
	CWD             string
	Messages        []api.Message
	Timeline        *conversationTimeline
}

type slashCommandContext struct {
	ctx             context.Context
	bridge          *ipc.Bridge
	router          *ipc.MessageRouter
	store           *session.Store
	timingLogger    *timing.Logger
	cfg             config.Config
	artifactManager *artifactspkg.Manager
	mcpManager      *mcppkg.Manager
	tracker         *costpkg.Tracker
	command         string
	args            string
	tools           []api.ToolDefinition
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
		Model:       "claude-sonnet-4-6",
		Provider:    "anthropic",
		Description: "Sonnet preset",
	},
	{
		Label:       "Claude Opus 4.7",
		Model:       "claude-opus-4-7",
		Provider:    "anthropic",
		Description: "Latest Opus preset",
	},
	{
		Label:       "Claude Opus 4.6",
		Model:       "claude-opus-4-6",
		Provider:    "anthropic",
		Description: "Previous Opus preset",
	},
	{
		Label:       "Claude Haiku 4.5",
		Model:       "claude-haiku-4-5",
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

func appendCuratedModelSelectionOptions(options []ipc.ModelSelectionOptionPayload, snapshot commandspkg.ProviderSnapshot, currentSelection string) []ipc.ModelSelectionOptionPayload {
	if len(curatedModelSelectionPresets) == 0 {
		return options
	}

	currentProvider, currentModel := commandspkg.ResolveModelSelection(currentSelection)
	currentRef := modelSelectionOptionRef(currentProvider, currentModel)
	merged := append([]ipc.ModelSelectionOptionPayload(nil), options...)
	seen := make(map[string]struct{}, len(merged)+len(curatedModelSelectionPresets))
	for _, option := range merged {
		ref := modelSelectionOptionRef(option.Provider, option.Model)
		if ref == "" {
			continue
		}
		seen[ref] = struct{}{}
	}

	appendMatchingPresets := func(match func(commandspkg.ProviderStatus) bool) {
		for _, preset := range curatedModelSelectionPresets {
			providerID := normalizeProvider(strings.TrimSpace(preset.Provider))
			status, ok := snapshot.LookupProvider(providerID)
			if !ok || !match(status) {
				continue
			}

			ref := modelSelectionOptionRef(providerID, preset.Model)
			if _, exists := seen[ref]; exists {
				continue
			}

			merged = append(merged, ipc.ModelSelectionOptionPayload{
				Label:       fmt.Sprintf("%s · %s · %s", preset.Label, status.Label, commandspkg.ProviderStateLabel(status)),
				Model:       strings.TrimSpace(preset.Model),
				Provider:    providerID,
				Description: formatCuratedModelSelectionDescription(preset.Description, status),
				Active:      strings.EqualFold(ref, currentRef),
			})
			seen[ref] = struct{}{}
		}
	}

	appendMatchingPresets(func(status commandspkg.ProviderStatus) bool { return status.Usable })
	appendMatchingPresets(func(status commandspkg.ProviderStatus) bool { return !status.Usable })
	return merged
}

func formatCuratedModelSelectionDescription(summary string, status commandspkg.ProviderStatus) string {
	parts := make([]string, 0, 3)
	if trimmed := strings.TrimSpace(summary); trimmed != "" {
		parts = append(parts, trimmed)
	}
	if status.AuthSource != "" && status.AuthSource != "none" {
		parts = append(parts, status.AuthSource)
	}
	if !status.Usable && status.SetupHint != "" {
		parts = append(parts, status.SetupHint)
	}
	if len(parts) == 0 {
		parts = append(parts, "Curated model preset")
	}
	return strings.Join(parts, " · ")
}

func modelSelectionOptionRef(provider string, model string) string {
	provider = normalizeProvider(strings.TrimSpace(provider))
	model = strings.TrimSpace(model)
	if provider == "" && model == "" {
		return ""
	}
	return strings.ToLower(modelRef(provider, model))
}

func newSlashCommandContext(
	ctx context.Context,
	bridge *ipc.Bridge,
	router *ipc.MessageRouter,
	store *session.Store,
	timingLogger *timing.Logger,
	cfg config.Config,
	artifactManager *artifactspkg.Manager,
	mcpManager *mcppkg.Manager,
	tracker *costpkg.Tracker,
	payload ipc.SlashCommandPayload,
	sessionID string,
	startedAt time.Time,
	mode agent.ExecutionMode,
	activeModelID string,
	subagentModelID string,
	cwd string,
	messages []api.Message,
	timeline *conversationTimeline,
	tools []api.ToolDefinition,
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
		mcpManager:      mcpManager,
		tracker:         tracker,
		command:         strings.ToLower(strings.TrimSpace(payload.Command)),
		args:            strings.TrimSpace(payload.Args),
		tools:           append([]api.ToolDefinition(nil), tools...),
		state: slashCommandState{
			SessionID:       sessionID,
			StartedAt:       startedAt,
			Mode:            mode,
			ActiveModelID:   activeModelID,
			SubagentModelID: subagentModelID,
			CWD:             cwd,
			Messages:        messages,
			Timeline:        timeline,
		},
		client: client,
	}
}

func lookupSlashCommandHandler(command string) (slashCommandHandler, bool) {
	for _, spec := range slashCommandSpecs() {
		if spec.Descriptor.Name == command {
			return spec.Handler, true
		}
	}
	return nil, false
}

func (cmd *slashCommandContext) persistState() error {
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
	return persistConversationHydratedPayload(cmd.store, cmd.state.SessionID, cmd.state.Timeline, cmd.state.Messages, cmd.state.ActiveModelID)
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
	"anthropic":      makeStaticConnectProviderHandler("anthropic"),
	"openai":         makeStaticConnectProviderHandler("openai"),
	"gemini":         makeStaticConnectProviderHandler("gemini"),
	"deepseek":       makeStaticConnectProviderHandler("deepseek"),
	"qwen":           makeStaticConnectProviderHandler("qwen"),
	"glm":            makeStaticConnectProviderHandler("glm"),
	"mistral":        makeStaticConnectProviderHandler("mistral"),
	"groq":           makeStaticConnectProviderHandler("groq"),
	"ollama":         makeStaticConnectProviderHandler("ollama"),
}

func handleConnectSlashCommand(cmd *slashCommandContext) error {
	if strings.TrimSpace(cmd.args) == "" {
		currentCfg := config.LoadForWorkingDir(cmd.state.CWD)
		currentCfg.Model = cmd.state.ActiveModelID
		snapshot := commandspkg.DiscoverProviderSnapshot(currentCfg)
		providerID, err := promptConnectProviderSelection(cmd, snapshot)
		if err != nil {
			return err
		}
		if providerID == "" {
			return emitTextResponse(cmd.bridge, "Connect cancelled.")
		}
		cmd.args = providerID
	}

	request, err := commandspkg.ParseConnectArgs(cmd.args)
	if err != nil {
		return emitTextResponse(cmd.bridge, err.Error())
	}

	currentCfg := config.LoadForWorkingDir(cmd.state.CWD)
	currentCfg.Model = cmd.state.ActiveModelID
	snapshot := commandspkg.DiscoverProviderSnapshot(currentCfg)
	switch request.Action {
	case commandspkg.ConnectActionOverview, commandspkg.ConnectActionHelp:
		return emitTextResponse(cmd.bridge, commandspkg.FormatConnectOverviewText(snapshot))
	case commandspkg.ConnectActionStatus:
		return emitTextResponse(cmd.bridge, commandspkg.FormatProviderSnapshot(snapshot))
	}

	handler, ok := connectProviderRegistry[request.Provider]
	if !ok {
		return emitTextResponse(cmd.bridge, fmt.Sprintf("unsupported connect provider: %s", request.Provider))
	}

	result, err := handler(cmd, request.Extra)
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
	nextClient = clientdebug.WrapClient(nextClient)
	*cmd.client = nextClient
	previousModelID := cmd.state.ActiveModelID
	cmd.state.ActiveModelID = modelRef(result.Provider, nextClient.ModelID())
	rememberSuccessfulModelSelection(cmd.state.ActiveModelID)
	cmd.state.SubagentModelID = coerceSessionSubagentModel(result.Config, cmd.state.ActiveModelID, cmd.state.SubagentModelID)
	if err := emitCacheBustNoticeOnModelSwitch(cmd.bridge, cmd.tracker, previousModelID, cmd.state.ActiveModelID); err != nil {
		return err
	}
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

func makeStaticConnectProviderHandler(providerID string) connectProviderFunc {
	return func(cmd *slashCommandContext, extraArgs string) (*connectResult, error) {
		return connectStaticProvider(cmd, providerID, extraArgs)
	}
}

func connectStaticProvider(cmd *slashCommandContext, providerID string, extraArgs string) (*connectResult, error) {
	if strings.TrimSpace(extraArgs) != "" {
		return nil, emitTextResponse(cmd.bridge, fmt.Sprintf("usage: /connect %s", providerID))
	}

	spec, ok := commandspkg.LookupConnectProvider(providerID)
	if !ok {
		return nil, emitTextResponse(cmd.bridge, fmt.Sprintf("unsupported connect provider: %s", providerID))
	}

	currentCfg := config.LoadForWorkingDir(cmd.state.CWD)
	snapshot := commandspkg.DiscoverProviderSnapshot(currentCfg)
	status, _ := snapshot.LookupProvider(providerID)
	if !status.Usable {
		return nil, emitTextResponse(cmd.bridge, commandspkg.FormatConnectProviderGuidance(spec, snapshot))
	}

	persisted := config.Load()
	persisted.Model = modelRef(providerID, spec.DefaultModel)
	if err := config.Save(persisted); err != nil {
		return nil, emitTextResponse(cmd.bridge, fmt.Sprintf("save %s configuration: %v", spec.Label, err))
	}

	currentCfg.Model = persisted.Model
	return &connectResult{
		Provider: providerID,
		Model:    spec.DefaultModel,
		Config:   currentCfg,
		FormatMessage: func(activeModelID string) string {
			if status.AuthSource == "local" {
				return fmt.Sprintf("%s selected. Set main model to %s. Ensure the local runtime is running before the next turn.", spec.Label, activeModelID)
			}
			return fmt.Sprintf("%s is ready via %s. Set main model to %s.", spec.Label, status.AuthSource, activeModelID)
		},
	}, nil
}

func promptConnectProviderSelection(cmd *slashCommandContext, snapshot commandspkg.ProviderSnapshot) (string, error) {
	currentProvider, _ := config.ParseModel(strings.TrimSpace(cmd.state.ActiveModelID))
	currentProvider = normalizeProvider(currentProvider)
	selected, err := promptSelection(
		cmd,
		currentProvider,
		buildConnectProviderSelectionOptions(snapshot, currentProvider),
		"Connect Provider",
		"Choose a provider to connect for this session. Providers that still need setup stay visible so you can inspect or retry them.",
	)
	if err != nil {
		return "", err
	}
	providerID := normalizeProvider(strings.TrimSpace(selected.Provider))
	if providerID == "" {
		providerID = normalizeProvider(strings.TrimSpace(selected.Model))
	}
	return providerID, nil
}

func buildConnectProviderSelectionOptions(snapshot commandspkg.ProviderSnapshot, currentProvider string) []ipc.ModelSelectionOptionPayload {
	options := make([]ipc.ModelSelectionOptionPayload, 0, len(commandspkg.ConnectProviderCatalog()))
	for _, spec := range commandspkg.ConnectProviderCatalog() {
		status, _ := snapshot.LookupProvider(spec.ID)
		descriptionParts := []string{
			fmt.Sprintf("Default model: %s/%s", spec.ID, spec.DefaultModel),
			commandspkg.ProviderStateLabel(status),
		}
		if authSource := strings.TrimSpace(status.AuthSource); authSource != "" {
			descriptionParts = append(descriptionParts, fmt.Sprintf("Auth: %s", authSource))
		}
		if issue := strings.TrimSpace(status.LastError); issue != "" {
			descriptionParts = append(descriptionParts, issue)
		} else if hint := strings.TrimSpace(status.SetupHint); hint != "" {
			descriptionParts = append(descriptionParts, hint)
		}
		options = append(options, ipc.ModelSelectionOptionPayload{
			Label:       spec.Label,
			Model:       spec.DefaultModel,
			Provider:    spec.ID,
			Description: strings.Join(descriptionParts, " · "),
			Active:      spec.ID == currentProvider,
		})
	}
	return options
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
		if err := commandspkg.OpenBrowserURL(device.VerificationURI); err == nil {
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
			modelIDs = commandspkg.MergeGitHubCopilotModelIDs(modelIDs, discovered)
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

func handleProvidersSlashCommand(cmd *slashCommandContext) error {
	if strings.TrimSpace(cmd.args) != "" {
		return emitTextResponse(cmd.bridge, "usage: /providers")
	}

	statusCfg := config.LoadForWorkingDir(cmd.state.CWD)
	statusCfg.Model = cmd.state.ActiveModelID
	snapshot := commandspkg.DiscoverProviderSnapshot(statusCfg)
	return emitTextResponse(cmd.bridge, commandspkg.FormatProviderSnapshot(snapshot))
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
	if providerHint != "" && (!retainSelectionProvider(currentProvider) || requestedDefault) {
		provider = normalizeProvider(providerHint)
		model = selectedModel
	}
	nextClient, err := newLLMClient(provider, model, cmd.cfg)
	if err != nil {
		return cmd.bridge.EmitError(fmt.Sprintf("switch model %q: %v", selectedModel, err), true)
	}

	nextClient = clientdebug.WrapClient(nextClient)
	*cmd.client = nextClient
	previousModelID := cmd.state.ActiveModelID
	cmd.state.ActiveModelID = modelRef(provider, nextClient.ModelID())
	rememberSuccessfulModelSelection(cmd.state.ActiveModelID)
	cmd.state.SubagentModelID = coerceSessionSubagentModel(config.LoadForWorkingDir(cmd.state.CWD), cmd.state.ActiveModelID, cmd.state.SubagentModelID)
	if err := emitCacheBustNoticeOnModelSwitch(cmd.bridge, cmd.tracker, previousModelID, cmd.state.ActiveModelID); err != nil {
		return err
	}
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

func emitCacheBustNoticeOnModelSwitch(bridge *ipc.Bridge, tracker *costpkg.Tracker, previousModelID string, nextModelID string) error {
	if bridge == nil || tracker == nil {
		return nil
	}
	if strings.EqualFold(strings.TrimSpace(previousModelID), strings.TrimSpace(nextModelID)) {
		return nil
	}
	snapshot := tracker.Snapshot()
	cacheRead := snapshot.TotalCacheReadTokens
	cacheWrite := snapshot.TotalCacheCreationTokens
	if cacheRead == 0 && cacheWrite == 0 {
		return nil
	}
	return bridge.EmitNotice(fmt.Sprintf(
		"Switching model from %s to %s invalidates the current prompt cache. Cache usage so far: %s read / %s write.",
		commandspkg.FormatModelSelectionLabel(previousModelID),
		commandspkg.FormatModelSelectionLabel(nextModelID),
		formatCacheTokenCount(cacheRead),
		formatCacheTokenCount(cacheWrite),
	))
}

func formatCacheTokenCount(value int) string {
	switch {
	case value >= 1_000_000:
		return fmt.Sprintf("%.1fM", float64(value)/1_000_000)
	case value >= 1_000:
		return fmt.Sprintf("%.1fk", float64(value)/1_000)
	default:
		return fmt.Sprintf("%d", value)
	}
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
		return emitTextResponse(cmd.bridge, commandspkg.FormatSubagentHelpText(currentSelection))
	case strings.EqualFold(selected.Model, "default"):
		cmd.state.SubagentModelID = defaultSessionSubagentModel(config.Load(), cmd.state.ActiveModelID)
		if err := cmd.persistState(); err != nil {
			return err
		}
		return emitTextResponse(cmd.bridge, fmt.Sprintf("Reset subagent model to %s", commandspkg.FormatModelSelectionLabel(cmd.state.SubagentModelID)))
	}

	selectedModel, err := normalizeModelSlashInput(selected.Model)
	if err != nil {
		return emitTextResponse(cmd.bridge, err.Error())
	}

	currentProvider, _ := config.ParseModel(cmd.state.ActiveModelID)
	currentProvider = normalizeProvider(currentProvider)
	providerHint := strings.TrimSpace(selected.Provider)
	provider, model := resolveModelSelection(selectedModel, currentProvider)
	if providerHint != "" && !retainSelectionProvider(currentProvider) {
		provider = normalizeProvider(providerHint)
		model = selectedModel
	}
	cmd.state.SubagentModelID = modelRef(provider, model)
	if err := cmd.persistState(); err != nil {
		return err
	}
	return emitTextResponse(cmd.bridge, fmt.Sprintf("Set subagent model to %s", commandspkg.FormatModelSelectionLabel(cmd.state.SubagentModelID)))
}

func normalizeModelSlashInput(input string) (string, error) {
	compact := strings.TrimSpace(input)
	if compact == "" {
		return "", fmt.Errorf("model cannot be empty")
	}
	if strings.Contains(compact, "/") {
		provider, model := config.ParseModel(compact)
		provider = normalizeProvider(strings.TrimSpace(provider))
		model = strings.TrimSpace(model)
		if provider == "" || model == "" {
			return "", fmt.Errorf("model must use provider/model format")
		}
		return modelRef(provider, model), nil
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

	selectionCfg := config.LoadForWorkingDir(cmd.state.CWD)
	selectionCfg.Model = currentSelection
	snapshot := commandspkg.DiscoverProviderSnapshot(selectionCfg)
	options := commandspkg.BuildModelSelectionOptions(snapshot, currentSelection)
	options = appendCuratedModelSelectionOptions(options, snapshot, currentSelection)
	options = append(options, ipc.ModelSelectionOptionPayload{
		Label:       "Custom model",
		Description: "Enter a model id or provider/model",
		IsCustom:    true,
	})
	return promptSelection(
		cmd,
		activeModel,
		options,
		"Select Model",
		"Choose the active model, a curated preset, or a provider default for the session.",
	)
}

func promptSelection(
	cmd *slashCommandContext,
	currentSelection string,
	options []ipc.ModelSelectionOptionPayload,
	title string,
	description string,
) (modelSelectionChoice, error) {
	requestID := fmt.Sprintf("model-%d", time.Now().UnixNano())
	if err := cmd.bridge.Emit(ipc.EventModelSelectionRequested, ipc.ModelSelectionRequestedPayload{
		RequestID:    requestID,
		CurrentModel: strings.TrimSpace(currentSelection),
		Title:        strings.TrimSpace(title),
		Description:  strings.TrimSpace(description),
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
	current := commandspkg.DescribeReasoningEffort(strings.TrimSpace(persisted.ReasoningEffort), currentModelID)
	if strings.TrimSpace(cmd.args) == "" {
		return emitTextResponse(cmd.bridge, fmt.Sprintf("Current reasoning effort: %s", current))
	}

	nextEffort, clearSetting, err := commandspkg.ParseReasoningArgs(cmd.args)
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
	var activeClient api.LLMClient
	if cmd.client != nil {
		activeClient = *cmd.client
	}
	if err := emitModelChanged(cmd.bridge, cmd.state.ActiveModelID, activeClient); err != nil {
		return err
	}

	updated := commandspkg.DescribeReasoningEffort(strings.TrimSpace(persisted.ReasoningEffort), currentModelID)
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
	sessionMemory, _ := loadSessionMemorySnapshot(cmd.ctx, cmd.artifactManager, cmd.state.SessionID)

	tokensBefore := compact.EstimateConversationTokens(cmd.state.Messages)
	if err := cmd.bridge.Emit(ipc.EventCompactStart, ipc.CompactStartPayload{
		Strategy:         string(agent.CompactManual),
		TokensBefore:     tokensBefore,
		HasSessionMemory: strings.TrimSpace(sessionMemory.Content) != "",
	}); err != nil {
		return err
	}

	result, err := compactWithMetrics(cmd.ctx, cmd.bridge, cmd.tracker, *cmd.client, cmd.timingLogger, cmd.state.SessionID, 0, string(agent.CompactManual), sessionMemory, systemPromptForMode(cmd.state.Mode), cmd.tools, cmd.state.Messages)
	if err != nil {
		return cmd.bridge.EmitError(fmt.Sprintf("compact conversation: %v", err), true)
	}

	cmd.state.Messages = result.Messages
	tokensAfter := compact.EstimateConversationTokens(cmd.state.Messages)
	if err := cmd.persistState(); err != nil {
		return err
	}
	if err := maybeRefreshSessionMemory(cmd.ctx, cmd.bridge, cmd.artifactManager, cmd.state.SessionID, 0, cmd.state.Messages, 0, newSessionMemoryRefiner(cmd.bridge, cmd.tracker, *cmd.client)); err != nil {
		return err
	}
	if err := cmd.bridge.Emit(ipc.EventCompactEnd, ipc.CompactEndPayload{
		Strategy:                string(result.Strategy),
		TokensBefore:            result.TokensBefore,
		TokensAfter:             tokensAfter,
		TokensSaved:             result.TokensBefore - tokensAfter,
		MicrocompactApplied:     result.MicrocompactApplied,
		MicrocompactTokensSaved: result.MicrocompactTokensSaved,
		HasSessionMemory:        strings.TrimSpace(sessionMemory.Content) != "",
	}); err != nil {
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
	timelinePayload, err := cmd.store.LoadConversationTimeline(cmd.state.SessionID)
	if err != nil {
		return err
	}
	if conversationHydratedPayloadHasContent(timelinePayload) {
		cmd.state.Timeline = newConversationTimelineFromHydrated(timelinePayload, cmd.state.Messages)
	} else {
		cmd.state.Timeline = rebuildConversationTimeline(cmd.state.Messages)
	}
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
		*cmd.client = clientdebug.WrapClient(restoredClient)
		cmd.state.ActiveModelID = modelRef(provider, restoredClient.ModelID())
		rememberSuccessfulModelSelection(cmd.state.ActiveModelID)
		if err := emitToolUseCapabilityNotice(cmd.bridge, cmd.state.ActiveModelID, *cmd.client, nil); err != nil {
			return err
		}
	}
	cmd.state.SubagentModelID = coerceSessionSubagentModel(config.Load(), cmd.state.ActiveModelID, restored.Metadata.SubagentModel)

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
	if err := emitConversationHydrated(cmd.bridge, cmd.store, cmd.state.SessionID, cmd.state.Messages, cmd.state.ActiveModelID); err != nil {
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

func handleRewindSlashCommand(cmd *slashCommandContext) error {
	turns := buildRewindSelectionTurns(cmd.state.Messages)
	if len(turns) == 0 {
		return cmd.bridge.EmitError("no user turns available to rewind", true)
	}

	selectedIndex, err := promptRewindSelection(cmd, turns)
	if err != nil {
		return err
	}
	if selectedIndex < 0 {
		return emitTextResponse(cmd.bridge, "Rewind cancelled.")
	}
	if selectedIndex >= len(cmd.state.Messages) {
		return cmd.bridge.EmitError("invalid rewind target", true)
	}
	if selectedIndex == len(cmd.state.Messages)-1 {
		return emitTextResponse(cmd.bridge, "Conversation is already at the selected turn.")
	}

	cmd.state.Messages = append(cmd.state.Messages[:0], cmd.state.Messages[:selectedIndex+1]...)
	cmd.state.Timeline = trimConversationTimelineToMessage(
		cmd.state.Timeline,
		conversationTimelineMessageID(selectedIndex),
		cmd.state.Messages,
	)
	if err := cmd.persistState(); err != nil {
		return err
	}
	if err := syncSessionMemoryAfterRewind(cmd.ctx, cmd.bridge, cmd.artifactManager, cmd.state.SessionID, cmd.state.Messages); err != nil {
		return err
	}

	title := ""
	if meta, err := cmd.store.LoadMetadata(cmd.state.SessionID); err == nil {
		title = strings.TrimSpace(meta.Title)
	}

	if err := cmd.bridge.Emit(ipc.EventSessionRewound, ipc.SessionRewoundPayload{
		SessionID:    cmd.state.SessionID,
		MessageCount: len(cmd.state.Messages),
	}); err != nil {
		return err
	}
	if err := emitConversationHydrated(cmd.bridge, cmd.store, cmd.state.SessionID, cmd.state.Messages, cmd.state.ActiveModelID); err != nil {
		return err
	}
	if err := emitSessionUpdated(cmd.bridge, cmd.state.SessionID, title); err != nil {
		return err
	}
	if err := emitContextWindowUsage(cmd.bridge, *cmd.client, cmd.state.Messages); err != nil {
		return err
	}
	if err := emitSessionArtifacts(cmd.ctx, cmd.bridge, cmd.artifactManager, cmd.state.SessionID); err != nil {
		return err
	}

	targetTurn := 0
	for _, turn := range turns {
		if turn.MessageIndex == selectedIndex {
			targetTurn = turn.TurnNumber
			break
		}
	}
	if targetTurn <= 0 {
		targetTurn = 1
	}
	return emitTextResponse(cmd.bridge, fmt.Sprintf("Rewound conversation to user turn %d. Later messages were removed from context.", targetTurn))
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

func promptRewindSelection(cmd *slashCommandContext, turns []ipc.RewindSelectionTurnPayload) (int, error) {
	requestID := fmt.Sprintf("rewind-%d", time.Now().UnixNano())
	if err := cmd.bridge.Emit(ipc.EventRewindSelectionRequested, ipc.RewindSelectionRequestedPayload{
		RequestID: requestID,
		Turns:     turns,
	}); err != nil {
		return -1, err
	}

	deferred := make([]ipc.ClientMessage, 0, 4)
	defer func() {
		cmd.router.Requeue(deferred...)
	}()

	for {
		msg, err := cmd.router.Next(cmd.ctx)
		if err != nil {
			return -1, err
		}

		switch msg.Type {
		case ipc.MsgRewindSelectionResponse:
			var payload ipc.RewindSelectionResponsePayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return -1, fmt.Errorf("decode rewind selection response: %w", err)
			}
			if payload.RequestID != requestID {
				deferred = append(deferred, msg)
				continue
			}
			if payload.Cancel {
				return -1, nil
			}
			return payload.MessageIndex, nil
		case ipc.MsgShutdown:
			return -1, context.Canceled
		default:
			deferred = append(deferred, msg)
		}
	}
}

func buildRewindSelectionTurns(messages []api.Message) []ipc.RewindSelectionTurnPayload {
	turns := make([]ipc.RewindSelectionTurnPayload, 0, 16)
	turnNumber := 0
	for index, message := range messages {
		if message.Role != api.RoleUser {
			continue
		}
		if strings.TrimSpace(message.Content) == "" && len(message.Images) == 0 {
			continue
		}
		turnNumber++
		turns = append(turns, ipc.RewindSelectionTurnPayload{
			MessageIndex: index,
			TurnNumber:   turnNumber,
			Preview:      rewindPreview(message),
		})
	}
	return turns
}

func rewindPreview(message api.Message) string {
	content := strings.TrimSpace(message.Content)
	if content == "" {
		if len(message.Images) == 1 {
			return "[image attachment]"
		}
		if len(message.Images) > 1 {
			return fmt.Sprintf("[%d image attachments]", len(message.Images))
		}
		return "[empty prompt]"
	}
	content = strings.ReplaceAll(content, "\n", " ")
	content = strings.Join(strings.Fields(content), " ")
	return truncateRewindPreview(content, 96)
}

func truncateRewindPreview(value string, maxRunes int) string {
	if maxRunes <= 0 {
		return ""
	}
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= maxRunes {
		return string(runes)
	}
	if maxRunes == 1 {
		return string(runes[:1])
	}
	return string(runes[:maxRunes-1]) + "…"
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
	catalog, err := slashCommandCatalog(cmd.state.CWD)
	if err != nil {
		if noticeErr := cmd.bridge.EmitNotice(fmt.Sprintf("load slash skills: %v", err)); noticeErr != nil {
			return noticeErr
		}
	}
	return emitTextResponse(cmd.bridge, commandspkg.FormatHelpText(catalog))
}

func handleStatusSlashCommand(cmd *slashCommandContext) error {
	statusCfg := config.LoadForWorkingDir(cmd.state.CWD)
	statusCfg.Model = cmd.state.ActiveModelID
	snapshot := commandspkg.DiscoverProviderSnapshot(statusCfg)
	statusText := commandspkg.FormatStatusText(cmd.state.SessionID, cmd.state.StartedAt, cmd.state.Mode, cmd.state.ActiveModelID, cmd.state.SubagentModelID, cmd.state.CWD, len(cmd.state.Messages), cmd.tracker, statusCfg, snapshot)
	statusText += fmt.Sprintf("\nSession memory: %s\nMicrocompact: %s", enabledDisabled(statusCfg.EnableSessionMemory), enabledDisabled(statusCfg.EnableMicrocompact))
	if mcpText := formatMCPStatusText(cmd.mcpManager); mcpText != "" {
		statusText += "\n\n" + mcpText
	}
	return emitTextResponse(cmd.bridge, statusText)
}

func handleTasksSlashCommand(cmd *slashCommandContext) error {
	if strings.TrimSpace(cmd.args) != "" {
		return emitTextResponse(cmd.bridge, "usage: /tasks")
	}
	if err := cmd.bridge.Emit(ipc.EventBackgroundTasksRequested, nil); err != nil {
		return err
	}
	return emitTextResponse(cmd.bridge, "")
}

func enabledDisabled(value bool) string {
	if value {
		return "enabled"
	}
	return "disabled"
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
		if err := debuglog.OpenMonitorPopup(path); err != nil {
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
	return emitTextResponse(cmd.bridge, commandspkg.FormatSessionList(sessions, cmd.state.SessionID))
}
