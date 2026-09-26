package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/clientdebug"
	commandspkg "github.com/channyeintun/nami/internal/commands"
	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/ipc"
)

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

	handler, ok := connectProviderHandler(request.Provider)
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

func connectProviderHandler(providerID string) (connectProviderFunc, bool) {
	switch normalizeProvider(providerID) {
	case "github-copilot":
		return connectGitHubCopilot, true
	case "codex":
		return connectCodex, true
	}
	if _, ok := commandspkg.LookupConnectProvider(providerID); !ok {
		return nil, false
	}
	return makeStaticConnectProviderHandler(providerID), true
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

	model := modelRef(providerID, spec.DefaultModel)
	if err := config.Update(func(cfg *config.Config) { cfg.Model = model }); err != nil {
		return nil, emitTextResponse(cmd.bridge, fmt.Sprintf("save %s configuration: %v", spec.Label, err))
	}

	currentCfg.Model = model
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
	return connectProviderFromChoice(selected), nil
}

// connectProviderFromChoice names the provider a picker choice selects, or ""
// when the picker was cancelled. It must not use normalizeProvider, which
// turns the empty choice of a cancelled picker into "anthropic".
func connectProviderFromChoice(choice modelSelectionChoice) string {
	if provider := strings.TrimSpace(choice.Provider); provider != "" {
		return provider
	}
	return strings.TrimSpace(choice.Model)
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
	persisted := config.LoadUser()
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

	// Saved onto the config as it is now, not as it was loaded before the
	// login, which can take minutes.
	reasoningEffort := ""
	if err := config.Update(func(cfg *config.Config) {
		cfg.GitHubCopilot = copilotAuth
		cfg.Model = modelRef("github-copilot", api.Presets["github-copilot"].DefaultModel)
		cfg.SubagentModel = modelRef("github-copilot", api.GitHubCopilotDefaultSubagentModel)
		if strings.TrimSpace(cfg.ReasoningEffort) == "" {
			cfg.ReasoningEffort = api.ReasoningEffortMedium
		}
		reasoningEffort = cfg.ReasoningEffort
	}); err != nil {
		return nil, emitTextResponse(cmd.bridge, fmt.Sprintf("save GitHub Copilot credentials: %v", err))
	}

	return &connectResult{
		Provider: "github-copilot",
		Model:    api.Presets["github-copilot"].DefaultModel,
		// The client runs on what was just saved plus this process's
		// environment overrides, which the saved copy deliberately lacks.
		Config: config.Load(),
		FormatMessage: func(activeModelID string) string {
			return fmt.Sprintf(
				"GitHub Copilot connected. Set main model to %s, subagent model to github-copilot/%s, and reasoning effort to %s.%s",
				activeModelID,
				api.GitHubCopilotDefaultSubagentModel,
				reasoningEffort,
				policySummary,
			)
		},
	}, nil
}
