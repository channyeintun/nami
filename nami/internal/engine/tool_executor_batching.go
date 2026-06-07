package engine

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/api"
	artifactspkg "github.com/channyeintun/nami/internal/artifacts"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/hooks"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/timing"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

func executeApprovedToolBatches(
	ctx context.Context,
	bridge ipc.EventSink,
	tracker *costpkg.Tracker,
	artifactManager *artifactspkg.Manager,
	hookRunner *hooks.Runner,
	sessionID string,
	turnMetrics *timing.CheckpointRecorder,
	turnStats *turnExecutionStats,
	calls []api.ToolCall,
	state *toolExecutionState,
) error {
	if len(state.approved) == 0 {
		return nil
	}

	executor := toolpkg.NewStreamingExecutor(ctx)
	defer executor.Cancel()
	startedAt := time.Now()
	for _, call := range state.approved {
		if err := executor.Add(call); err != nil {
			return err
		}
	}
	executor.Close()

	for !executor.Done() {
		ready, err := executor.Wait(ctx)
		if err != nil {
			return err
		}
		for _, result := range ready {
			if err := handleToolBatchResult(ctx, bridge, artifactManager, hookRunner, sessionID, turnMetrics, turnStats, calls[result.Index], result, state); err != nil {
				executor.Cancel()
				return err
			}
		}
	}
	tracker.RecordToolDuration(time.Since(startedAt))
	return nil
}

func handleToolBatchResult(
	ctx context.Context,
	bridge ipc.EventSink,
	artifactManager *artifactspkg.Manager,
	hookRunner *hooks.Runner,
	sessionID string,
	turnMetrics *timing.CheckpointRecorder,
	turnStats *turnExecutionStats,
	call api.ToolCall,
	result toolpkg.IndexedResult,
	state *toolExecutionState,
) error {
	toolResult := api.ToolResult{ToolCallID: call.ID, FilePath: result.Output.FilePath}
	feedback := state.approvalFeedback[result.Index]
	if result.Err != nil {
		toolResult.Output = appendPermissionFeedback(result.Err.Error(), feedback)
		toolResult.IsError = true
		state.results[result.Index] = toolResult
		return emitToolError(bridge, call, toolResult.Output, result.Output, result.Err)
	}
	output, truncated, spilled, err := finalizeToolOutput(ctx, bridge, artifactManager, sessionID, turnStats, call, result.Output, state, feedback)
	if err != nil {
		return err
	}
	toolResult.Output = output
	toolResult.IsError = result.Output.IsError
	state.results[result.Index] = toolResult
	if result.Output.IsError {
		return emitToolError(bridge, call, output, result.Output, nil)
	}
	if err := emitToolArtifacts(bridge, result.Output.Artifacts, turnMetrics); err != nil {
		return err
	}
	if err := markFirstToolResult(bridge, turnMetrics); err != nil {
		return err
	}
	if err := bridge.Emit(ipc.EventToolResult, ipc.ToolResultPayload{
		ToolID:      call.ID,
		Output:      output,
		Truncated:   truncated,
		Name:        call.Name,
		Input:       call.Input,
		FilePath:    result.Output.FilePath,
		Preview:     result.Output.Preview,
		Insertions:  result.Output.Insertions,
		Deletions:   result.Output.Deletions,
		Diagnostics: result.Output.Diagnostics,
		ErrorKind:   result.Output.ErrorKind,
		ErrorHint:   result.Output.ErrorHint,
	}); err != nil {
		return err
	}
	rememberInlineReadResult(result.Output, spilled)
	runPostToolUseHooks(ctx, hookRunner, sessionID, call, output)
	return nil
}

func finalizeToolOutput(
	ctx context.Context,
	bridge ipc.EventSink,
	artifactManager *artifactspkg.Manager,
	sessionID string,
	turnStats *turnExecutionStats,
	call api.ToolCall,
	output toolpkg.ToolOutput,
	state *toolExecutionState,
	feedback string,
) (string, bool, bool, error) {
	spilled := false
	finalOutput := output.Output
	spillPath := output.SpillPath
	truncated := output.Truncated
	if !output.IsError {
		budgetedOutput, artifact, budgetInfo, err := budgetToolOutput(ctx, artifactManager, sessionID, state.budget, state.aggregateBudget, call, finalOutput)
		finalOutput = budgetedOutput
		spilled = budgetInfo.Spilled
		if err != nil {
			if emitErr := bridge.EmitError(fmt.Sprintf("persist tool-log artifact: %v", err), true); emitErr != nil {
				return "", false, false, emitErr
			}
		}
		updateTurnToolStats(turnStats, budgetInfo)
		if artifact.ID != "" {
			spillPath = artifact.ContentPath
			truncated = true
			if err := emitArtifactCreated(bridge, artifact); err != nil {
				return "", false, false, err
			}
		}
	}
	finalOutput = appendPermissionFeedback(finalOutput, feedback)
	return finalOutput, truncated || spillPath != "", spilled, nil
}

func rememberInlineReadResult(output toolpkg.ToolOutput, spilled bool) {
	if spilled || strings.TrimSpace(output.FilePath) == "" || output.ReadLimit <= 0 {
		return
	}
	readState := toolpkg.GetGlobalFileReadState()
	if readState == nil {
		return
	}
	info, err := os.Stat(output.FilePath)
	if err != nil || info.IsDir() {
		return
	}
	readState.Remember(output.FilePath, max(1, output.ReadOffset), output.ReadLimit, info)
}

func updateTurnToolStats(turnStats *turnExecutionStats, budgetInfo toolBudgetInfo) {
	if turnStats == nil {
		return
	}
	turnStats.ToolResultCount++
	turnStats.ToolInlineChars += budgetInfo.InlineChars
	if budgetInfo.Spilled {
		turnStats.ToolSpillCount++
	}
	if budgetInfo.AggregateLimited {
		turnStats.AggregateBudgetSpills++
	}
}
