package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/channyeintun/gocode/internal/agent"
	"github.com/channyeintun/gocode/internal/api"
	artifactspkg "github.com/channyeintun/gocode/internal/artifacts"
	"github.com/channyeintun/gocode/internal/compact"
	"github.com/channyeintun/gocode/internal/config"
	costpkg "github.com/channyeintun/gocode/internal/cost"
	"github.com/channyeintun/gocode/internal/ipc"
	"github.com/channyeintun/gocode/internal/session"
	"github.com/channyeintun/gocode/internal/timing"
)

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
) (bool, string, time.Time, agent.ExecutionMode, string, string, []api.Message, error) {
	command := strings.ToLower(strings.TrimSpace(payload.Command))
	args := strings.TrimSpace(payload.Args)

	switch command {
	case "plan", "plan-mode":
		mode = agent.ModePlan
		if err := persistSessionState(store, sessionStateParams{
			SessionID: sessionID,
			CreatedAt: startedAt,
			Mode:      mode,
			Model:     activeModelID,
			CWD:       cwd,
			Branch:    agent.LoadTurnContext().GitBranch,
			Tracker:   tracker,
			Messages:  messages,
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := bridge.Emit(ipc.EventModeChanged, ipc.ModeChangedPayload{Mode: string(mode)}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "fast":
		mode = agent.ModeFast
		if err := persistSessionState(store, sessionStateParams{
			SessionID: sessionID,
			CreatedAt: startedAt,
			Mode:      mode,
			Model:     activeModelID,
			CWD:       cwd,
			Branch:    agent.LoadTurnContext().GitBranch,
			Tracker:   tracker,
			Messages:  messages,
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := bridge.Emit(ipc.EventModeChanged, ipc.ModeChangedPayload{Mode: string(mode)}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "model":
		if args == "" {
			if err := emitTextResponse(bridge, fmt.Sprintf("Current model: %s", activeModelID)); err != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
			}
			return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
		}

		selectedModel := args
		if strings.EqualFold(strings.TrimSpace(args), "default") {
			selectedModel = cfg.Model
		}

		currentProvider, _ := config.ParseModel(activeModelID)
		provider, model := resolveModelSelection(selectedModel, currentProvider)
		nextClient, err := newLLMClient(provider, model, cfg)
		if err != nil {
			if emitErr := bridge.EmitError(fmt.Sprintf("switch model %q: %v", args, err), true); emitErr != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
			}
			return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
		}

		*client = nextClient
		activeModelID = modelRef(provider, nextClient.ModelID())
		if err := emitToolUseCapabilityNotice(bridge, activeModelID, *client, nil); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := persistSessionState(store, sessionStateParams{
			SessionID: sessionID,
			CreatedAt: startedAt,
			Mode:      mode,
			Model:     activeModelID,
			CWD:       cwd,
			Branch:    agent.LoadTurnContext().GitBranch,
			Tracker:   tracker,
			Messages:  messages,
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitModelChanged(bridge, activeModelID, *client); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitContextWindowUsage(bridge, *client, messages); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitTextResponse(bridge, fmt.Sprintf("Set model to %s", activeModelID)); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "cost", "usage":
		if err := emitTextResponse(bridge, formatCostSummary(tracker.Snapshot(), activeModelID)); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "compact":
		if len(messages) == 0 {
			if emitErr := bridge.EmitError("no messages to compact", true); emitErr != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
			}
			return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
		}

		resolvedClient, nextModelID, err := ensureClientForSelection(activeModelID, cfg, *client)
		if err != nil {
			if emitErr := bridge.EmitError(fmt.Sprintf("initialize model %q: %v", activeModelID, err), true); emitErr != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
			}
			return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
		}
		*client = resolvedClient
		activeModelID = nextModelID

		tokensBefore := compact.EstimateConversationTokens(messages)
		if err := bridge.Emit(ipc.EventCompactStart, ipc.CompactStartPayload{
			Strategy:     string(agent.CompactManual),
			TokensBefore: tokensBefore,
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}

		result, err := compactWithMetrics(ctx, bridge, tracker, *client, timingLogger, sessionID, 0, string(agent.CompactManual), messages)
		if err != nil {
			if emitErr := bridge.EmitError(fmt.Sprintf("compact conversation: %v", err), true); emitErr != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
			}
			return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
		}

		messages = result.Messages
		tokensAfter := compact.EstimateConversationTokens(messages)
		if err := persistSessionState(store, sessionStateParams{
			SessionID: sessionID,
			CreatedAt: startedAt,
			Mode:      mode,
			Model:     activeModelID,
			CWD:       cwd,
			Branch:    agent.LoadTurnContext().GitBranch,
			Tracker:   tracker,
			Messages:  messages,
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := bridge.Emit(ipc.EventCompactEnd, ipc.CompactEndPayload{TokensAfter: tokensAfter}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitContextWindowUsage(bridge, *client, messages); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitTextResponse(bridge, fmt.Sprintf("Compacted conversation with %s. Tokens %d -> %d.", result.Strategy, tokensBefore, tokensAfter)); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "resume":
		var targetID string
		if args != "" {
			targetID = args
		} else {
			meta, err := store.LatestResumeCandidate(sessionID)
			if err != nil {
				if emitErr := bridge.EmitError(err.Error(), true); emitErr != nil {
					return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
				}
				return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
			}
			targetID = meta.SessionID
		}

		restored, err := store.Restore(targetID)
		if err != nil {
			if emitErr := bridge.EmitError(fmt.Sprintf("restore session %q: %v", targetID, err), true); emitErr != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
			}
			return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
		}

		messages = append(messages[:0], restored.Messages...)
		sessionID = restored.Metadata.SessionID
		if !restored.Metadata.CreatedAt.IsZero() {
			startedAt = restored.Metadata.CreatedAt
		}
		mode = parseExecutionMode(restored.Metadata.Mode)

		if restored.Metadata.Model != "" {
			provider, model := config.ParseModel(restored.Metadata.Model)
			provider = normalizeProvider(provider)
			restoredClient, err := newLLMClient(provider, model, cfg)
			if err != nil {
				*client = nil
				activeModelID = modelRef(provider, model)
				if emitErr := bridge.EmitError(fmt.Sprintf("restore model %q: %v", restored.Metadata.Model, err), true); emitErr != nil {
					return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
				}
				return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
			}
			*client = restoredClient
			activeModelID = modelRef(provider, restoredClient.ModelID())
			if err := emitToolUseCapabilityNotice(bridge, activeModelID, *client, nil); err != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
			}
		}

		if restored.Metadata.CWD != "" {
			if err := os.Chdir(restored.Metadata.CWD); err == nil {
				cwd = restored.Metadata.CWD
			}
		}

		if err := persistSessionState(store, sessionStateParams{
			SessionID: sessionID,
			CreatedAt: startedAt,
			Mode:      mode,
			Model:     activeModelID,
			CWD:       cwd,
			Branch:    agent.LoadTurnContext().GitBranch,
			Tracker:   tracker,
			Messages:  messages,
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}

		if err := bridge.Emit(ipc.EventSessionRestored, ipc.SessionRestoredPayload{
			SessionID: sessionID,
			Mode:      string(mode),
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitSessionUpdated(bridge, sessionID, restored.Metadata.Title); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitModelChanged(bridge, activeModelID, *client); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitContextWindowUsage(bridge, *client, messages); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := bridge.Emit(ipc.EventModeChanged, ipc.ModeChangedPayload{Mode: string(mode)}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitSessionArtifacts(ctx, bridge, artifactManager, sessionID); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitTextResponse(bridge, fmt.Sprintf("Resumed session %s with %d messages.", sessionID, len(messages))); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "clear":
		messages = messages[:0]
		newID, err := newSessionID()
		if err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		sessionID = newID
		startedAt = time.Now()
		if err := persistSessionState(store, sessionStateParams{
			SessionID: sessionID,
			CreatedAt: startedAt,
			Mode:      mode,
			Model:     activeModelID,
			CWD:       cwd,
			Branch:    agent.LoadTurnContext().GitBranch,
			Tracker:   tracker,
			Messages:  messages,
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitSessionUpdated(bridge, sessionID, ""); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitContextWindowUsage(bridge, *client, messages); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitTextResponse(bridge, "Conversation cleared. New session started."); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "help":
		helpText := formatHelpText()
		if err := emitTextResponse(bridge, helpText); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "status":
		statusText := formatStatusText(sessionID, startedAt, mode, activeModelID, cwd, len(messages), tracker)
		if err := emitTextResponse(bridge, statusText); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "sessions":
		sessions, err := store.ListSessions()
		if err != nil {
			if emitErr := bridge.EmitError(fmt.Sprintf("list sessions: %v", err), true); emitErr != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
			}
			return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
		}
		if err := emitTextResponse(bridge, formatSessionList(sessions, sessionID)); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "diff":
		diffOutput := gitDiff(args)
		if strings.TrimSpace(diffOutput) == "" {
			diffOutput = "No changes detected."
		}
		if err := emitTextResponse(bridge, diffOutput); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	default:
		if err := bridge.EmitError(fmt.Sprintf("unknown slash command: %s", payload.Command), true); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	}
}

func emitTextResponse(bridge *ipc.Bridge, text string) error {
	if strings.TrimSpace(text) != "" {
		if err := bridge.Emit(ipc.EventTokenDelta, ipc.TokenDeltaPayload{Text: text}); err != nil {
			return err
		}
	}
	return bridge.Emit(ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: "end_turn"})
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
	return `Available slash commands:

  /plan          Switch to plan mode (read-only until approved)
  /fast          Switch to fast mode (direct execution)
  /model [name]  Show or switch the active model
  /cost          Show token usage and cost breakdown
  /usage         Alias for /cost
  /compact       Compact the conversation to save context
  /resume [id]   Resume a previous session
  /clear         Clear conversation and start a new session
  /status        Show current session status
  /sessions      List recent sessions
  /diff [args]   Show git diff (optionally with args like --staged)
  /help          Show this help message`
}

func formatStatusText(sessionID string, startedAt time.Time, mode agent.ExecutionMode, model string, cwd string, msgCount int, tracker *costpkg.Tracker) string {
	elapsed := time.Since(startedAt).Round(time.Second)
	snap := tracker.Snapshot()
	return fmt.Sprintf(
		"Session: %s\nStarted: %s (%s ago)\nMode: %s\nModel: %s\nCWD: %s\nMessages: %d\nCost: $%.4f\nTokens: %d in / %d out",
		sessionID,
		startedAt.Format(time.RFC3339),
		elapsed,
		string(mode),
		model,
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
