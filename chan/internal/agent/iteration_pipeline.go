package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/channyeintun/chan/internal/ipc"
	skillspkg "github.com/channyeintun/chan/internal/skills"
)

type iterationRuntime struct {
	currentUserPrompt    string
	pressure             ContextPressureDecision
	memoryRecalls        []MemoryRecallResult
	sessionMemory        SessionMemorySnapshot
	liveRetrievalSection string
	attemptEntries       []AttemptEntry
	attemptLogSection    string
	skillPrompt          string
}

type iterationStage func(context.Context, *QueryState, QueryDeps, *iterationRuntime, func(ipc.StreamEvent, error) bool) error

var defaultIterationStages = []iterationStage{
	applyResultBudgetStage,
	loadSessionMemoryStage,
	loadAttemptLogStage,
	runProactiveCompactionStage,
	evaluateContextPressureStage,
	recallMemoryStage,
	runLiveRetrievalStage,
	selectSkillsStage,
	composeSystemPromptStage,
	warnUnsupportedThinkingStage,
}

func loadSessionMemoryStage(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	runtime *iterationRuntime,
	yield func(ipc.StreamEvent, error) bool,
) error {
	runtime.sessionMemory = state.SessionMemory
	if deps.LoadSessionMemory == nil {
		return nil
	}
	if err := emitIterationProgress(yield, state, "session-memory", "Reviewing session memory"); err != nil {
		return err
	}
	snapshot, err := deps.LoadSessionMemory(ctx)
	if err != nil {
		if telemetryErr := emitNoticeTelemetry(deps.EmitTelemetry, fmt.Sprintf("session memory unavailable: %v", err)); telemetryErr != nil {
			return telemetryErr
		}
		return nil
	}
	state.SessionMemory = snapshot
	runtime.sessionMemory = snapshot
	return nil
}

func runIterationStages(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	runtime *iterationRuntime,
	yield func(ipc.StreamEvent, error) bool,
) error {
	for _, stage := range defaultIterationStages {
		if err := stage(ctx, state, deps, runtime, yield); err != nil {
			return err
		}
	}
	return nil
}

func applyResultBudgetStage(
	_ context.Context,
	state *QueryState,
	deps QueryDeps,
	_ *iterationRuntime,
	_ func(ipc.StreamEvent, error) bool,
) error {
	if deps.ApplyResultBudget != nil {
		state.Messages = deps.ApplyResultBudget(state.Messages)
	}
	return nil
}

func runProactiveCompactionStage(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	_ *iterationRuntime,
	yield func(ipc.StreamEvent, error) bool,
) error {
	return runProactiveCompaction(ctx, state, deps, yield)
}

func evaluateContextPressureStage(
	_ context.Context,
	state *QueryState,
	_ QueryDeps,
	runtime *iterationRuntime,
	_ func(ipc.StreamEvent, error) bool,
) error {
	runtime.currentUserPrompt = latestUserPrompt(state.Messages)
	runtime.pressure = EvaluateContextPressure(state.Messages, state.ContextWindow, state.MaxTokens, state.Continuation, ContextPressureSignals{
		SessionMemory:    runtime.sessionMemory,
		RetrievalTouched: state.RetrievalTouched,
		AttemptEntries:   runtime.attemptEntries,
	})
	return nil
}

func recallMemoryStage(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	runtime *iterationRuntime,
	yield func(ipc.StreamEvent, error) bool,
) error {
	if shouldEmitMemoryRecallProgress(state.SystemContext.MemoryFiles, runtime.currentUserPrompt, runtime.pressure) {
		if err := emitIterationProgress(yield, state, "memory-recall", "Recalling relevant memory"); err != nil {
			return err
		}
	}
	results, err := recallMemoryIndexes(ctx, deps.RecallMemory, state.SystemContext.MemoryFiles, runtime.currentUserPrompt, runtime.pressure)
	if err != nil {
		if telemetryErr := emitNoticeTelemetry(deps.EmitTelemetry, err.Error()); telemetryErr != nil {
			return telemetryErr
		}
		return nil
	}
	runtime.memoryRecalls = results
	return emitMemoryRecallTelemetry(deps.EmitTelemetry, state.SystemContext.MemoryFiles, runtime.memoryRecalls)
}

func runLiveRetrievalStage(
	_ context.Context,
	state *QueryState,
	deps QueryDeps,
	runtime *iterationRuntime,
	yield func(ipc.StreamEvent, error) bool,
) error {
	if !runtime.pressure.SkipLiveRetrieval {
		if err := emitIterationProgress(yield, state, "live-retrieval", "Scanning nearby code context"); err != nil {
			return err
		}
	}
	var retrievalMeta retrievalMeta
	runtime.liveRetrievalSection, retrievalMeta = runLiveRetrieval(state, runtime.currentUserPrompt, runtime.pressure)
	return emitRetrievalTelemetry(deps.EmitTelemetry, retrievalMeta)
}

func loadAttemptLogStage(
	_ context.Context,
	state *QueryState,
	deps QueryDeps,
	runtime *iterationRuntime,
	yield func(ipc.StreamEvent, error) bool,
) error {
	if deps.AttemptLog != nil {
		if err := emitIterationProgress(yield, state, "attempt-log", "Reviewing recent failed attempts"); err != nil {
			return err
		}
		entries, err := deps.AttemptLog.Load()
		if err != nil {
			if telemetryErr := emitNoticeTelemetry(deps.EmitTelemetry, fmt.Sprintf("session attempt log unavailable: %v", err)); telemetryErr != nil {
				return telemetryErr
			}
		} else {
			runtime.attemptEntries = entries
			state.AttemptEntries = entries
			runtime.attemptLogSection = FormatAttemptLogSection(runtime.attemptEntries)
		}
	}
	return emitAttemptLogTelemetry(deps.EmitTelemetry, runtime.attemptEntries, runtime.attemptLogSection)
}

func selectSkillsStage(
	_ context.Context,
	state *QueryState,
	_ QueryDeps,
	runtime *iterationRuntime,
	_ func(ipc.StreamEvent, error) bool,
) error {
	selectedSkills := skillspkg.SelectForPrompt(state.Skills, runtime.currentUserPrompt, state.ExplicitSkills)
	runtime.skillPrompt = skillspkg.FormatPromptSection(selectedSkills)
	return nil
}

func composeSystemPromptStage(
	_ context.Context,
	state *QueryState,
	_ QueryDeps,
	runtime *iterationRuntime,
	_ func(ipc.StreamEvent, error) bool,
) error {
	basePrompt := state.BasePrompt
	if state.PromptCache != nil {
		state.SystemPrompt = state.PromptCache.Compose(
			basePrompt,
			state.SystemContext,
			state.TurnContext,
			runtime.currentUserPrompt,
			runtime.memoryRecalls,
			runtime.sessionMemory,
			state.Capabilities,
			runtime.skillPrompt,
			runtime.liveRetrievalSection,
			runtime.attemptLogSection,
		)
		state.PromptInjection = composePromptInjection(
			state.SystemContext,
			state.TurnContext,
			runtime.currentUserPrompt,
			runtime.memoryRecalls,
			runtime.sessionMemory,
			state.Capabilities,
			runtime.skillPrompt,
			runtime.liveRetrievalSection,
			runtime.attemptLogSection,
		)
		return nil
	}
	state.SystemPrompt = composeSystemPrompt(
		basePrompt,
		state.SystemContext,
		state.TurnContext,
		runtime.currentUserPrompt,
		runtime.memoryRecalls,
		runtime.sessionMemory,
		state.Capabilities,
		runtime.skillPrompt,
		runtime.liveRetrievalSection,
		runtime.attemptLogSection,
	)
	state.PromptInjection = composePromptInjection(
		state.SystemContext,
		state.TurnContext,
		runtime.currentUserPrompt,
		runtime.memoryRecalls,
		runtime.sessionMemory,
		state.Capabilities,
		runtime.skillPrompt,
		runtime.liveRetrievalSection,
		runtime.attemptLogSection,
	)
	return nil
}

func emitIterationProgress(
	yield func(ipc.StreamEvent, error) bool,
	state *QueryState,
	stageID string,
	message string,
) error {
	if yield == nil {
		return nil
	}
	trimmedStageID := strings.TrimSpace(stageID)
	trimmedMessage := strings.TrimSpace(message)
	if trimmedStageID == "" || trimmedMessage == "" {
		return nil
	}
	if !yield(newEvent(ipc.EventProgress, ipc.ProgressPayload{
		ID:      fmt.Sprintf("turn-%d-progress-%s", state.TurnCount, trimmedStageID),
		Message: trimmedMessage,
	}), nil) {
		return context.Canceled
	}
	return nil
}

func shouldEmitMemoryRecallProgress(
	files []MemoryFile,
	currentUserPrompt string,
	pressure ContextPressureDecision,
) bool {
	if pressure.SkipMemoryRecall || strings.TrimSpace(currentUserPrompt) == "" {
		return false
	}
	for _, file := range files {
		if file.Type == memoryTypeProjectIndex || file.Type == memoryTypeUserIndex {
			return true
		}
	}
	return false
}

func warnUnsupportedThinkingStage(
	_ context.Context,
	state *QueryState,
	_ QueryDeps,
	runtime *iterationRuntime,
	yield func(ipc.StreamEvent, error) bool,
) error {
	return warnUnsupportedThinking(runtime.currentUserPrompt, state.Capabilities, yield)
}
