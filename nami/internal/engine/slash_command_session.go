package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/clientdebug"
	commandspkg "github.com/channyeintun/nami/internal/commands"
	"github.com/channyeintun/nami/internal/compact"
	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/debuglog"
	"github.com/channyeintun/nami/internal/ipc"
)

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

	cmd.state.Messages = cmd.state.Messages[:0]
	cmd.tracker.Reset()
	cmd.state.SessionID = newSessionID()
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

func handleSessionsSlashCommand(cmd *slashCommandContext) error {
	sessions, err := cmd.store.ListSessions()
	if err != nil {
		return cmd.bridge.EmitError(fmt.Sprintf("list sessions: %v", err), true)
	}
	return emitTextResponse(cmd.bridge, commandspkg.FormatSessionList(sessions, cmd.state.SessionID))
}
