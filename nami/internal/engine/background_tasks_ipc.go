package engine

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/ipc"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

const backgroundCommandDetailTailBytes = 8 * 1024

func handleBackgroundCommandInspectMessage(
	ctx context.Context,
	bridge *ipc.Bridge,
	payload ipc.BackgroundCommandInspectPayload,
) error {
	commandID := strings.TrimSpace(payload.CommandID)
	if commandID == "" {
		return bridge.EmitNotice("Background command inspect requires command_id.")
	}
	if payload.WaitMs < 0 {
		return bridge.EmitNotice("Background command inspect wait_ms must be >= 0.")
	}

	detail, err := toolpkg.InspectBackgroundCommand(
		ctx,
		commandID,
		time.Duration(payload.WaitMs)*time.Millisecond,
		backgroundCommandDetailTailBytes,
	)
	if err != nil {
		message := fmt.Sprintf("Inspect background command failed: %v", err)
		if emitErr := bridge.Emit(ipc.EventBackgroundCommandDetail, ipc.BackgroundCommandDetailPayload{
			CommandID: commandID,
			Status:    "failed",
			Running:   false,
			Error:     message,
		}); emitErr != nil {
			return emitErr
		}
		return bridge.EmitNotice(message)
	}

	return bridge.Emit(ipc.EventBackgroundCommandDetail, ipc.BackgroundCommandDetailPayload{
		CommandID:       detail.CommandID,
		Command:         detail.Command,
		Cwd:             detail.Cwd,
		Status:          detail.Status,
		Running:         detail.Running,
		StartedAt:       detail.StartedAt,
		UpdatedAt:       detail.UpdatedAt,
		Output:          detail.Output,
		HasUnreadOutput: detail.HasUnreadOutput,
		UnreadBytes:     detail.UnreadBytes,
		ExitCode:        detail.ExitCode,
		Error:           detail.Error,
	})
}

func handleBackgroundCommandStopMessage(
	bridge *ipc.Bridge,
	payload ipc.BackgroundCommandStopPayload,
) error {
	commandID := strings.TrimSpace(payload.CommandID)
	if commandID == "" {
		return bridge.EmitNotice("Background command stop requires command_id.")
	}
	if payload.WaitMs < 0 {
		return bridge.EmitNotice("Background command stop wait_ms must be >= 0.")
	}

	result, err := toolpkg.StopBackgroundCommand(
		commandID,
		time.Duration(payload.WaitMs)*time.Millisecond,
	)
	if err != nil {
		return bridge.EmitNotice(fmt.Sprintf("Stop background command failed: %v", err))
	}

	if update, updateErr := toolpkg.BackgroundCommandUpdateSnapshot(commandID); updateErr == nil {
		if err := bridge.Emit(ipc.EventBackgroundCommandUpdated, ipc.BackgroundCommandUpdatedPayload{
			CommandID:       update.CommandID,
			Command:         update.Command,
			Cwd:             update.Cwd,
			Status:          "stopped",
			Running:         update.Running,
			StartedAt:       update.StartedAt,
			UpdatedAt:       update.UpdatedAt,
			OutputPreview:   update.OutputPreview,
			HasUnreadOutput: update.HasUnreadOutput,
			UnreadBytes:     update.UnreadBytes,
			ExitCode:        update.ExitCode,
			Error:           update.Error,
		}); err != nil {
			return err
		}
	}

	return bridge.Emit(ipc.EventBackgroundCommandDetail, ipc.BackgroundCommandDetailPayload{
		CommandID:       result.CommandID,
		Command:         result.Command,
		Cwd:             result.Cwd,
		Status:          "stopped",
		Running:         result.Running,
		StartedAt:       result.StartedAt,
		UpdatedAt:       result.UpdatedAt,
		Output:          result.Output,
		HasUnreadOutput: strings.TrimSpace(result.Output) != "",
		UnreadBytes:     len(result.Output),
		ExitCode:        result.ExitCode,
		Error:           result.Error,
	})
}

func handleBackgroundAgentInspectMessage(
	ctx context.Context,
	bridge *ipc.Bridge,
	payload ipc.BackgroundAgentInspectPayload,
) error {
	agentID := strings.TrimSpace(payload.AgentID)
	if agentID == "" {
		return bridge.EmitNotice("Background agent inspect requires agent_id.")
	}
	if payload.WaitMs < 0 {
		return bridge.EmitNotice("Background agent inspect wait_ms must be >= 0.")
	}

	result, err := lookupBackgroundAgentStatus(ctx, toolpkg.AgentStatusRequest{
		AgentID: agentID,
		WaitMs:  payload.WaitMs,
	})
	if err != nil {
		message := fmt.Sprintf("Inspect background agent failed: %v", err)
		if emitErr := bridge.Emit(ipc.EventBackgroundAgentDetail, ipc.BackgroundAgentDetailPayload{
			AgentID: agentID,
			Status:  "failed",
			Error:   message,
		}); emitErr != nil {
			return emitErr
		}
		return bridge.EmitNotice(message)
	}

	return bridge.Emit(ipc.EventBackgroundAgentDetail, backgroundAgentDetailPayloadFromResult(result))
}

func handleBackgroundAgentStopMessage(
	ctx context.Context,
	bridge *ipc.Bridge,
	payload ipc.BackgroundAgentStopPayload,
) error {
	agentID := strings.TrimSpace(payload.AgentID)
	if agentID == "" {
		return bridge.EmitNotice("Background agent stop requires agent_id.")
	}
	if payload.WaitMs < 0 {
		return bridge.EmitNotice("Background agent stop wait_ms must be >= 0.")
	}

	result, err := stopBackgroundAgent(ctx, bridge, toolpkg.AgentStopRequest{
		AgentID: agentID,
		WaitMs:  payload.WaitMs,
	})
	if err != nil {
		return bridge.EmitNotice(fmt.Sprintf("Stop background agent failed: %v", err))
	}

	return bridge.Emit(ipc.EventBackgroundAgentDetail, backgroundAgentDetailPayloadFromResult(result))
}

func backgroundAgentDetailPayloadFromResult(
	result toolpkg.AgentRunResult,
) ipc.BackgroundAgentDetailPayload {
	var metadata *ipc.ChildAgentMetadataPayload
	if result.Metadata != nil {
		metadata = &ipc.ChildAgentMetadataPayload{
			InvocationID:      result.Metadata.InvocationID,
			AgentID:           result.Metadata.AgentID,
			Description:       result.Metadata.Description,
			Role:              result.Metadata.Role,
			SubagentType:      result.Metadata.SubagentType,
			WorkspaceStrategy: result.Metadata.WorkspaceStrategy,
			WorkspacePath:     result.Metadata.WorkspacePath,
			RepositoryRoot:    result.Metadata.RepositoryRoot,
			WorktreeBranch:    result.Metadata.WorktreeBranch,
			WorktreeCreated:   result.Metadata.WorktreeCreated,
			LifecycleState:    result.Metadata.LifecycleState,
			StatusMessage:     result.Metadata.StatusMessage,
			StopBlockReason:   result.Metadata.StopBlockReason,
			StopBlockCount:    result.Metadata.StopBlockCount,
			SessionID:         result.Metadata.SessionID,
			TranscriptPath:    result.Metadata.TranscriptPath,
			ResultPath:        result.Metadata.ResultPath,
			Tools:             append([]string(nil), result.Metadata.Tools...),
		}
	}

	description := ""
	if metadata != nil {
		description = metadata.Description
	}

	return ipc.BackgroundAgentDetailPayload{
		AgentID:        result.AgentID,
		InvocationID:   result.InvocationID,
		Description:    description,
		SubagentType:   result.SubagentType,
		Status:         result.Status,
		Summary:        result.Summary,
		SessionID:      result.SessionID,
		TranscriptPath: result.TranscriptPath,
		OutputFile:     result.OutputFile,
		Error:          result.Error,
		TotalCostUSD:   result.TotalCostUSD,
		InputTokens:    result.InputTokens,
		OutputTokens:   result.OutputTokens,
		Metadata:       metadata,
	}
}
