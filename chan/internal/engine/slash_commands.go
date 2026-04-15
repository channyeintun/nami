package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/channyeintun/chan/internal/agent"
	"github.com/channyeintun/chan/internal/api"
	artifactspkg "github.com/channyeintun/chan/internal/artifacts"
	"github.com/channyeintun/chan/internal/config"
	costpkg "github.com/channyeintun/chan/internal/cost"
	"github.com/channyeintun/chan/internal/ipc"
	mcppkg "github.com/channyeintun/chan/internal/mcp"
	"github.com/channyeintun/chan/internal/session"
	"github.com/channyeintun/chan/internal/timing"
)

func handleSlashCommand(
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
	client *api.LLMClient,
) (slashCommandState, error) {
	cmd := newSlashCommandContext(
		ctx,
		bridge,
		router,
		store,
		timingLogger,
		cfg,
		artifactManager,
		mcpManager,
		tracker,
		payload,
		sessionID,
		startedAt,
		mode,
		activeModelID,
		subagentModelID,
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
	return providerBehaviorFor("github-copilot").PolicyModels(cfg)
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

func defaultSessionSubagentModel(cfg config.Config, activeModelID string) string {
	activeProvider, _ := config.ParseModel(strings.TrimSpace(activeModelID))
	return providerBehaviorFor(activeProvider).DefaultSubagentModel(cfg, activeModelID)
}
