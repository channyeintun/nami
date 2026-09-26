package engine

import (
	"context"
	"fmt"
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
	bridge *ipc.Bridge,
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
	bridge *ipc.Bridge,
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
	rememberInlineReadResult(toolpkg.FileReadStateFor(ctx), result.Output, spilled)
	runPostToolUseHooks(ctx, hookRunner, sessionID, call, output)
	return nil
}

func finalizeToolOutput(
	ctx context.Context,
	bridge *ipc.Bridge,
	artifactManager *artifactspkg.Manager,
	sessionID string,
	turnStats *turnExecutionStats,
	call api.ToolCall,
	output toolpkg.ToolOutput,
	state *toolExecutionState,
	feedback string,
) (string, bool, bool, error) {
	spillPath := output.SpillPath
	truncated := output.Truncated
	// A failed call's output is budgeted like any other: a failing command
	// can return megabytes, and an MCP error has no limit at all.
	finalOutput, artifact, budgetInfo, err := budgetToolOutput(ctx, artifactManager, sessionID, state.budget, state.aggregateBudget, call, output.Output)
	spilled := budgetInfo.Spilled
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
	finalOutput = appendPermissionFeedback(finalOutput, feedback)
	return finalOutput, truncated || spillPath != "", spilled, nil
}

// forgetFileReads makes read_file return content again for every file. Its
// "unchanged since last read" answer points the model at an earlier result,
// which a compacted, rewound, cleared or resumed conversation no longer holds.
func forgetFileReads() {
	toolpkg.GetGlobalFileReadState().Reset()
}

// rememberInlineReadResult records a read whose content the conversation now
// holds in full, so reading the file again unchanged can be answered with a
// stub. readState is the conversation's; see toolpkg.FileReadStateFor.
func rememberInlineReadResult(readState *toolpkg.FileReadState, output toolpkg.ToolOutput, spilled bool) {
	if spilled || strings.TrimSpace(output.FilePath) == "" || output.ReadLimit <= 0 || output.ReadInfo == nil {
		return
	}
	if readState == nil {
		return
	}
	// The file as read_file saw it. A stat taken now would record a change
	// made since the read as already seen, and the next read of the file
	// would get the "unchanged" stub for content the model never saw.
	readState.Remember(output.FilePath, max(1, output.ReadOffset), output.ReadLimit, output.ReadInfo)
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
