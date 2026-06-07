package engine

import (
	"context"
	"strings"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/hooks"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/timing"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

func emitToolArtifacts(bridge ipc.EventSink, updates []toolpkg.ArtifactMutation, turnMetrics *timing.CheckpointRecorder) error {
	for _, artifactUpdate := range updates {
		if artifactUpdate.Created {
			if err := emitArtifactCreated(bridge, artifactUpdate.Artifact); err != nil {
				return err
			}
		}
		if err := emitArtifactUpdated(bridge, artifactUpdate.Artifact, artifactUpdate.Content); err != nil {
			return err
		}
		if artifactUpdate.Focused {
			if err := emitArtifactFocusedForTurn(bridge, artifactUpdate.Artifact, turnMetrics); err != nil {
				return err
			}
		}
	}
	return nil
}

func markFirstToolResult(bridge ipc.EventSink, turnMetrics *timing.CheckpointRecorder) error {
	if turnMetrics == nil || !turnMetrics.Mark("first_tool_result") {
		return nil
	}
	return emitTurnTimingCheckpoint(bridge, turnMetrics, "first_tool_result")
}

func runPostToolUseHooks(ctx context.Context, hookRunner *hooks.Runner, sessionID string, call api.ToolCall, output string) {
	if hookRunner == nil {
		return
	}
	_, _ = hookRunner.Run(ctx, hooks.Payload{
		Type:      hooks.HookPostToolUse,
		SessionID: sessionID,
		ToolName:  call.Name,
		ToolInput: call.Input,
		Output:    output,
	})
}

func compactToolResults(results []api.ToolResult) []api.ToolResult {
	filtered := make([]api.ToolResult, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.ToolCallID) == "" {
			continue
		}
		filtered = append(filtered, result)
	}
	return filtered
}

func emitToolError(bridge ipc.EventSink, call api.ToolCall, message string, output toolpkg.ToolOutput, err error) error {
	payload := ipc.ToolErrorPayload{
		ToolID:    call.ID,
		Name:      call.Name,
		Input:     call.Input,
		Error:     message,
		FilePath:  output.FilePath,
		ErrorKind: output.ErrorKind,
		ErrorHint: output.ErrorHint,
	}
	if editFailure, ok := toolpkg.ExtractEditFailure(err); ok {
		if payload.FilePath == "" {
			payload.FilePath = editFailure.FilePath
		}
		if payload.ErrorKind == "" {
			payload.ErrorKind = string(editFailure.Kind)
		}
		if payload.ErrorHint == "" {
			payload.ErrorHint = editFailure.Hint
		}
	}
	return bridge.Emit(ipc.EventToolError, payload)
}
