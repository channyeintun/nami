package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	artifactspkg "github.com/channyeintun/nami/internal/artifacts"
	commandspkg "github.com/channyeintun/nami/internal/commands"
	"github.com/channyeintun/nami/internal/config"
	costpkg "github.com/channyeintun/nami/internal/cost"
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
	bridge          ipc.EventSink
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

type modelSelectionChoice struct {
	Model    string
	Provider string
}

func appendCuratedModelSelectionOptions(options []ipc.ModelSelectionOptionPayload, snapshot commandspkg.ProviderSnapshot, currentSelection string) []ipc.ModelSelectionOptionPayload {
	if len(api.CuratedModelCatalog) == 0 {
		return options
	}

	currentProvider, currentModel := commandspkg.ResolveModelSelection(currentSelection)
	currentRef := modelSelectionOptionRef(currentProvider, currentModel)
	merged := make([]ipc.ModelSelectionOptionPayload, 0, len(options)+len(api.CuratedModelCatalog))
	seen := make(map[string]struct{}, len(options)+len(api.CuratedModelCatalog))

	appendMatchingPresets := func(match func(commandspkg.ProviderStatus) bool) {
		for _, preset := range api.CuratedModelCatalog {
			displayProvider := normalizeProvider(strings.TrimSpace(preset.ProviderID))
			providerID, status, ok := curatedModelAccessProvider(snapshot, displayProvider, preset.ModelID, currentProvider, match)
			if !ok {
				continue
			}

			ref := modelSelectionOptionRef(providerID, preset.ModelID)
			if _, exists := seen[ref]; exists {
				continue
			}

			merged = append(merged, ipc.ModelSelectionOptionPayload{
				Label:           fmt.Sprintf("%s via %s · %s", preset.Label, status.Label, commandspkg.ProviderStateLabel(status)),
				Model:           strings.TrimSpace(preset.ModelID),
				Provider:        providerID,
				DisplayProvider: displayProvider,
				Description:     formatCuratedModelSelectionDescription(preset.Description, status),
				Active:          strings.EqualFold(ref, currentRef),
			})
			seen[ref] = struct{}{}
		}
	}

	appendMatchingPresets(func(status commandspkg.ProviderStatus) bool { return status.Usable })
	appendMatchingPresets(func(status commandspkg.ProviderStatus) bool { return !status.Usable })
	for _, option := range options {
		ref := modelSelectionOptionRef(option.Provider, option.Model)
		if ref == "" {
			continue
		}
		if _, exists := seen[ref]; exists {
			continue
		}
		merged = append(merged, option)
		seen[ref] = struct{}{}
	}
	return merged
}

func curatedModelAccessProvider(
	snapshot commandspkg.ProviderSnapshot,
	displayProvider string,
	model string,
	currentProvider string,
	match func(commandspkg.ProviderStatus) bool,
) (string, commandspkg.ProviderStatus, bool) {
	candidates := make([]string, 0, len(snapshot.Providers)+2)
	addCandidate := func(providerID string) {
		providerID = normalizeProvider(strings.TrimSpace(providerID))
		if providerID == "" {
			return
		}
		if slices.Contains(candidates, providerID) {
			return
		}
		candidates = append(candidates, providerID)
	}

	addCandidate(currentProvider)
	addCandidate(displayProvider)
	for _, status := range snapshot.Providers {
		addCandidate(status.ID)
	}

	for _, providerID := range candidates {
		status, ok := snapshot.LookupProvider(providerID)
		if !ok || !match(status) {
			continue
		}
		return providerID, status, true
	}
	return "", commandspkg.ProviderStatus{}, false
}

func formatCuratedModelSelectionDescription(summary string, status commandspkg.ProviderStatus) string {
	parts := make([]string, 0, 4)
	if trimmed := strings.TrimSpace(summary); trimmed != "" {
		parts = append(parts, trimmed)
	}
	providerInfo := status.Label
	if state := commandspkg.ProviderStateLabel(status); state != "" {
		providerInfo += " (" + state + ")"
	}
	parts = append(parts, providerInfo)
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
	bridge ipc.EventSink,
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
