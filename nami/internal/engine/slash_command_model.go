package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/clientdebug"
	commandspkg "github.com/channyeintun/nami/internal/commands"
	"github.com/channyeintun/nami/internal/config"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/ipc"
)

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
	selectedModel, err := normalizeModelSlashInput(selected.Model)
	if err != nil {
		return emitTextResponse(cmd.bridge, err.Error())
	}

	currentProvider, _ := config.ParseModel(cmd.state.ActiveModelID)
	currentProvider = normalizeProvider(currentProvider)
	provider, model := resolveSelectedModelChoice(selectedModel, selected.Provider, currentProvider)
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

func emitCacheBustNoticeOnModelSwitch(bridge ipc.EventSink, tracker *costpkg.Tracker, previousModelID string, nextModelID string) error {
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
	provider, model := resolveSelectedModelChoice(selectedModel, selected.Provider, currentProvider)
	cmd.state.SubagentModelID = modelRef(provider, model)
	if err := cmd.persistState(); err != nil {
		return err
	}
	return emitTextResponse(cmd.bridge, fmt.Sprintf("Set subagent model to %s", commandspkg.FormatModelSelectionLabel(cmd.state.SubagentModelID)))
}

func handleLogoutSlashCommand(cmd *slashCommandContext) error {
	provider := strings.ToLower(strings.TrimSpace(cmd.args))

	cfg := config.Load()

	switch provider {
	case "github-copilot", "copilot":
		cfg.GitHubCopilot = config.GitHubCopilotAuth{}
		if err := config.Save(cfg); err != nil {
			return emitTextResponse(cmd.bridge, fmt.Sprintf("Failed to save configuration: %v", err))
		}
		return emitTextResponse(cmd.bridge, "Successfully logged out of GitHub Copilot.")
	case "codex":
		cfg.Codex = config.CodexAuth{}
		if err := config.Save(cfg); err != nil {
			return emitTextResponse(cmd.bridge, fmt.Sprintf("Failed to save configuration: %v", err))
		}
		return emitTextResponse(cmd.bridge, "Successfully logged out of Codex.")
	case "all", "":
		cfg.GitHubCopilot = config.GitHubCopilotAuth{}
		cfg.Codex = config.CodexAuth{}
		if err := config.Save(cfg); err != nil {
			return emitTextResponse(cmd.bridge, fmt.Sprintf("Failed to save configuration: %v", err))
		}
		return emitTextResponse(cmd.bridge, "Successfully logged out of all providers.")
	default:
		return emitTextResponse(cmd.bridge, fmt.Sprintf("Unknown provider: %s. Supported: github-copilot, codex, all.", provider))
	}
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

func resolveSelectedModelChoice(selectedModel string, providerHint string, currentProvider string) (string, string) {
	if provider, model := config.ParseModel(strings.TrimSpace(selectedModel)); strings.TrimSpace(provider) != "" {
		return normalizeProvider(provider), strings.TrimSpace(model)
	}
	if provider := normalizeProvider(strings.TrimSpace(providerHint)); provider != "" {
		return provider, strings.TrimSpace(selectedModel)
	}
	return resolveModelSelection(selectedModel, currentProvider)
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
	options := commandspkg.BuildCatalogModelSelectionOptions(selectionCfg, snapshot, currentSelection)
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

func promptReasoningSelection(
	cmd *slashCommandContext,
	configuredEffort string,
	currentModelID string,
) (string, bool, error) {
	requestID := fmt.Sprintf("reasoning-%d", time.Now().UnixNano())
	currentSelection := strings.TrimSpace(configuredEffort)
	if currentSelection == "" {
		currentSelection = "default"
	}

	options := []ipc.ReasoningSelectionOptionPayload{
		{
			Value:       "default",
			Label:       "Default",
			Description: fmt.Sprintf("Use the model default for %s.", commandspkg.DescribeReasoningEffort("", currentModelID)),
			Active:      currentSelection == "default",
		},
		{
			Value:       api.ReasoningEffortLow,
			Label:       "Low",
			Description: "Lower latency and token use.",
			Active:      currentSelection == api.ReasoningEffortLow,
		},
		{
			Value:       api.ReasoningEffortMedium,
			Label:       "Medium",
			Description: "Balanced reasoning depth.",
			Active:      currentSelection == api.ReasoningEffortMedium,
		},
		{
			Value:       api.ReasoningEffortHigh,
			Label:       "High",
			Description: "More deliberate reasoning with higher cost.",
			Active:      currentSelection == api.ReasoningEffortHigh,
		},
		{
			Value:       api.ReasoningEffortXHigh,
			Label:       "XHigh",
			Description: "Maximum reasoning. Older models clamp this to high.",
			Active:      currentSelection == api.ReasoningEffortXHigh,
		},
	}

	if err := cmd.bridge.Emit(ipc.EventReasoningSelectionRequested, ipc.ReasoningSelectionRequestedPayload{
		RequestID:     requestID,
		CurrentEffort: currentSelection,
		Title:         "Select Reasoning",
		Description:   fmt.Sprintf("Current effective effort: %s", commandspkg.DescribeReasoningEffort(strings.TrimSpace(configuredEffort), currentModelID)),
		Options:       options,
	}); err != nil {
		return "", false, err
	}

	deferred := make([]ipc.ClientMessage, 0, 4)
	defer func() {
		cmd.router.Requeue(deferred...)
	}()

	for {
		msg, err := cmd.router.Next(cmd.ctx)
		if err != nil {
			return "", false, err
		}

		switch msg.Type {
		case ipc.MsgReasoningSelectionResponse:
			var payload ipc.ReasoningSelectionResponsePayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return "", false, fmt.Errorf("decode reasoning selection response: %w", err)
			}
			if payload.RequestID != requestID {
				deferred = append(deferred, msg)
				continue
			}
			if payload.Cancel {
				return "", true, nil
			}
			effort, clearSetting, err := commandspkg.ParseReasoningArgs(payload.Effort)
			if err != nil {
				return "", false, err
			}
			if clearSetting {
				return "", false, nil
			}
			return effort, false, nil
		case ipc.MsgShutdown:
			return "", false, context.Canceled
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
	if strings.TrimSpace(cmd.args) == "" {
		selected, cancelled, err := promptReasoningSelection(
			cmd,
			strings.TrimSpace(persisted.ReasoningEffort),
			currentModelID,
		)
		if err != nil {
			return err
		}
		if cancelled {
			return emitTextResponse(cmd.bridge, "Reasoning selection cancelled.")
		}
		persisted.ReasoningEffort = selected
	} else {
		nextEffort, clearSetting, err := commandspkg.ParseReasoningArgs(cmd.args)
		if err != nil {
			return emitTextResponse(cmd.bridge, err.Error())
		}

		if clearSetting {
			persisted.ReasoningEffort = ""
		} else {
			persisted.ReasoningEffort = nextEffort
		}
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
