package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"iter"
	"slices"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	artifactspkg "github.com/channyeintun/nami/internal/artifacts"
	"github.com/channyeintun/nami/internal/clientdebug"
	"github.com/channyeintun/nami/internal/compact"
	"github.com/channyeintun/nami/internal/config"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/hooks"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/localmodel"
	memorypkg "github.com/channyeintun/nami/internal/memory"
	"github.com/channyeintun/nami/internal/permissions"
	"github.com/channyeintun/nami/internal/session"
	skillspkg "github.com/channyeintun/nami/internal/skills"
	"github.com/channyeintun/nami/internal/timing"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

type engineLoopDeps struct {
	bridge             *ipc.Bridge
	router             *ipc.MessageRouter
	registry           *toolpkg.Registry
	permissionCtx      *permissions.Context
	tracker            *costpkg.Tracker
	hookRunner         *hooks.Runner
	sessionStore       *session.Store
	artifactManager    *artifactspkg.Manager
	timingLogger       *timing.Logger
	modelState         *ActiveModelState
	subagentModelState *ActiveSubagentModelState
	cfg                config.Config
}

type engineLoopState struct {
	client          api.LLMClient
	sessionID       string
	sessionDir      string
	startedAt       time.Time
	mode            agent.ExecutionMode
	activeModelID   string
	subagentModelID string
	cwd             string
	messages        []api.Message
	timeline        *conversationTimeline
	titleGenerated  bool
	queryIndex      int
	toolUseNoticeID string
}

type userTurnContext struct {
	deps               engineLoopDeps
	state              *engineLoopState
	payload            ipc.UserInputPayload
	explicitSkills     []skillspkg.Skill
	plannerUserRequest string
	messageCountBefore int
	// queryStart is the index of the first message the running query owns.
	// Compaction rewrites the conversation mid-query, so it is kept in step by
	// rebaseAfterCompaction instead of being taken once up front.
	queryStart     int
	turnID         int
	turnMetrics    *timing.CheckpointRecorder
	turnStats      *turnExecutionStats
	turnStopReason string
}

func handleUserInputMessage(ctx context.Context, payload ipc.UserInputPayload, deps engineLoopDeps, state *engineLoopState) error {
	return handleUserInputMessageWithSkills(ctx, payload, deps, state, nil)
}

func handleUserInputMessageWithSkills(ctx context.Context, payload ipc.UserInputPayload, deps engineLoopDeps, state *engineLoopState, explicitSkills []skillspkg.Skill) error {
	if strings.TrimSpace(payload.Text) == "" && len(payload.Images) == 0 {
		return nil
	}
	turn := newUserTurnContext(deps, state, payload, explicitSkills)
	continueTurn, err := turn.prepareInput()
	if err != nil {
		return err
	}
	if !continueTurn {
		return nil
	}
	return turn.run(ctx)
}

func newUserTurnContext(deps engineLoopDeps, state *engineLoopState, payload ipc.UserInputPayload, explicitSkills []skillspkg.Skill) *userTurnContext {
	state.queryIndex++
	return &userTurnContext{
		deps:               deps,
		state:              state,
		payload:            payload,
		explicitSkills:     append([]skillspkg.Skill(nil), explicitSkills...),
		plannerUserRequest: payload.Text,
		messageCountBefore: len(state.messages),
		turnID:             state.queryIndex,
		turnMetrics:        timing.NewCheckpointRecorder(time.Now()),
		turnStats:          &turnExecutionStats{},
	}
}

func (t *userTurnContext) prepareInput() (bool, error) {
	continueTurn, err := t.prepareClient()
	if err != nil || !continueTurn {
		return continueTurn, err
	}
	return true, t.appendUserMessage()
}

func (t *userTurnContext) prepareClient() (bool, error) {
	resolvedClient, nextModelID, err := ensureClientForSelection(t.state.activeModelID, t.deps.cfg, t.state.client)
	if err != nil {
		t.markTurnMetric("client_initialization_failed")
		t.flushTurnMetrics("client_initialization_failed")
		if emitErr := t.abandonTurn(fmt.Sprintf("initialize model %q: %v", t.state.activeModelID, err)); emitErr != nil {
			return false, emitErr
		}
		return false, nil
	}
	if resolvedClient != t.state.client {
		t.state.client = clientdebug.WrapClient(resolvedClient)
	}
	t.state.activeModelID = nextModelID
	t.deps.modelState.Set(t.state.client, t.state.activeModelID)
	if err := emitToolUseCapabilityNotice(t.deps.bridge, t.state.activeModelID, t.state.client, &t.state.toolUseNoticeID); err != nil {
		return false, err
	}
	if len(t.payload.Images) > 0 && !t.state.client.Capabilities().SupportsVision {
		t.markTurnMetric("vision_unsupported")
		t.flushTurnMetrics("vision_unsupported")
		if err := t.abandonTurn(fmt.Sprintf("model %q does not support image input", t.state.activeModelID)); err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
}

// abandonTurn reports why a turn could not start. The TUI entered its working
// state when it sent the input and leaves it only on turn_complete or an
// unrecoverable error; a recoverable one would leave it spinning, with its
// stop key reaching an engine that has no query to cancel.
func (t *userTurnContext) abandonTurn(message string) error {
	return t.deps.bridge.EmitError(message, false)
}

func (t *userTurnContext) appendUserMessage() error {
	if len(t.payload.Images) > 0 && !t.state.client.Capabilities().SupportsVision {
		return nil
	}
	images := make([]api.ImageAttachment, 0, len(t.payload.Images))
	for _, image := range t.payload.Images {
		images = append(images, api.ImageAttachment{
			ID:         image.ID,
			Data:       image.Data,
			MediaType:  image.MediaType,
			Filename:   image.Filename,
			SourcePath: image.SourcePath,
		})
	}
	t.state.messages = append(t.state.messages, api.Message{
		Role:    api.RoleUser,
		Content: t.payload.Text,
		Images:  images,
	})
	if t.state.timeline != nil {
		t.state.timeline.RecordUserMessage(len(t.state.messages) - 1)
	}
	return emitContextWindowUsage(t.deps.bridge, t.state.client, t.state.messages)
}

func (t *userTurnContext) run(ctx context.Context) error {
	availableSkills, err := loadAvailableSkills(t.deps.bridge, t.state.cwd)
	if err != nil {
		return err
	}
	for {
		continueTurn, err := t.runPlannerTurn(ctx, availableSkills)
		if err != nil || !continueTurn {
			return err
		}
	}
}

func (t *userTurnContext) runPlannerTurn(ctx context.Context, availableSkills []skillspkg.Skill) (bool, error) {
	t.queryStart = len(t.state.messages)
	planner := agent.NewPlanner(t.state.mode, t.state.sessionID, t.deps.artifactManager)
	if err := t.beginPlannerTurn(ctx, planner); err != nil {
		return false, err
	}
	queryResult, err := t.executeQuery(ctx, planner, availableSkills)
	if err != nil {
		return false, err
	}
	if queryResult.Stopped {
		return false, nil
	}
	if err := t.finalizePlannerTurn(ctx, planner, t.queryStart); err != nil {
		return false, err
	}
	if err := maybeRefreshSessionMemory(ctx, t.deps.bridge, t.deps.artifactManager, t.state.sessionID, t.turnID, t.state.messages, t.queryStart, newSessionMemoryRefiner(t.deps.bridge, t.deps.tracker, t.state.client)); err != nil {
		return false, err
	}
	continueTurn, err := t.handlePlanReviewDecision(ctx, t.queryStart)
	if err != nil || continueTurn {
		return continueTurn, err
	}
	t.maybeGenerateSessionTitle()
	t.markTurnMetric("completed")
	if t.turnStopReason == "" {
		t.turnStopReason = "completed"
	}
	t.flushTurnMetrics("completed")
	return false, nil
}

type queryRunResult struct {
	Stopped bool
}

func (t *userTurnContext) beginPlannerTurn(ctx context.Context, planner *agent.Planner) error {
	updates, err := planner.BeginTurn(ctx, t.plannerUserRequest)
	if err != nil {
		return t.deps.bridge.EmitError(fmt.Sprintf("create session artifact: %v", err), true)
	}
	return emitArtifactUpdates(t.deps.bridge, updates, nil, false)
}

func (t *userTurnContext) executeQuery(ctx context.Context, planner *agent.Planner, availableSkills []skillspkg.Skill) (queryRunResult, error) {
	queryCtx, queryCancel := context.WithCancel(ctx)
	defer queryCancel()

	queryDeps := t.newQueryDeps(planner)
	stopControl := agent.NewStopController()
	queryDeps.StopController = stopControl
	t.deps.router.SetCancelFunc(func() {
		stopControl.Request("cancelled")
		queryCancel()
	})
	defer t.deps.router.SetCancelFunc(nil)

	stream := agent.QueryStream(queryCtx, t.newQueryRequest(availableSkills), queryDeps)
	queryFailed := false
	queryCancelled := false
	for event, streamErr := range stream {
		if streamErr != nil {
			runSessionStopFailureHooks(queryCtx, t.deps.hookRunner, t.state.sessionID, t.turnStopReason, t.state.messages, streamErr)
			if queryCtx.Err() != nil {
				queryCancelled = true
				break
			}
			queryFailed = true
			if emitErr := t.deps.bridge.EmitError(streamErr.Error(), false); emitErr != nil {
				return queryRunResult{}, emitErr
			}
			break
		}
		if err := t.handleQueryEvent(event); err != nil {
			return queryRunResult{}, err
		}
	}
	t.closeUnfinishedToolCalls()

	if queryCancelled || t.turnStopReason == "cancelled" {
		if err := t.finishCancelledTurn(); err != nil {
			return queryRunResult{}, err
		}
		return queryRunResult{Stopped: true}, nil
	}
	if queryFailed {
		t.markTurnMetric("failed")
		t.flushTurnMetrics("failed")
		return queryRunResult{Stopped: true}, nil
	}
	return queryRunResult{}, nil
}

func (t *userTurnContext) handleQueryEvent(event ipc.StreamEvent) error {
	switch event.Type {
	case ipc.EventTokenDelta:
		if t.markTurnMetric("first_token") {
			if err := emitTurnTimingCheckpoint(t.deps.bridge, t.turnMetrics, "first_token"); err != nil {
				return err
			}
		}
		if t.state.timeline != nil {
			t.state.timeline.RecordAssistantMessage(len(t.state.messages))
		}
	case ipc.EventThinkingDelta:
		if t.state.timeline != nil {
			t.state.timeline.RecordAssistantMessage(len(t.state.messages))
		}
	case ipc.EventTurnComplete:
		if t.markTurnMetric("turn_complete") {
			if err := emitTurnTimingCheckpoint(t.deps.bridge, t.turnMetrics, "turn_complete"); err != nil {
				return err
			}
		}
		var completion ipc.TurnCompletePayload
		if err := json.Unmarshal(event.Payload, &completion); err == nil {
			t.turnStopReason = completion.StopReason
		}
	case ipc.EventProgress:
		var progress ipc.ProgressPayload
		if err := json.Unmarshal(event.Payload, &progress); err == nil && t.state.timeline != nil {
			t.state.timeline.RecordProgress(progress)
		}
	case ipc.EventToolStart:
		var toolStart ipc.ToolStartPayload
		if err := json.Unmarshal(event.Payload, &toolStart); err == nil && t.state.timeline != nil {
			t.state.timeline.RecordToolStart(toolStart)
		}
	}
	return t.deps.bridge.EmitEvent(event)
}

// closeUnfinishedToolCalls answers the tool calls a stopped query left
// without results and saves the conversation, so the next request the
// session sends is one providers accept.
func (t *userTurnContext) closeUnfinishedToolCalls() {
	messages, changed := answerUnfinishedToolCalls(t.state.messages)
	if !changed {
		return
	}
	t.state.messages = messages
	if t.state.timeline != nil {
		t.state.timeline.SyncMessages(messages)
	}
	t.persistCurrentMessages()
}

// unfinishedToolCallOutput is the result recorded for a tool call that never
// produced one.
const unfinishedToolCallOutput = "Tool call did not run to completion: the turn stopped before it returned a result."

// answerUnfinishedToolCalls gives every tool call in the last assistant
// message a result. A query that stops mid-batch — cancelled, failed, or
// paused for plan review — leaves the calls it never finished unanswered, and
// providers reject every later request that carries a tool call without its
// result. The added results go straight after the existing ones, because a
// call's results must come before anything else that follows it.
func answerUnfinishedToolCalls(messages []api.Message) ([]api.Message, bool) {
	callIndex := -1
	for index, message := range slices.Backward(messages) {
		if message.Role == api.RoleAssistant {
			callIndex = index
			break
		}
	}
	if callIndex < 0 {
		return messages, false
	}

	insertAt := callIndex + 1
	answered := make(map[string]bool)
	for insertAt < len(messages) && messages[insertAt].ToolResult != nil {
		answered[messages[insertAt].ToolResult.ToolCallID] = true
		insertAt++
	}
	var missing []api.Message
	for _, call := range messages[callIndex].ToolCalls {
		if answered[call.ID] {
			continue
		}
		missing = append(missing, api.Message{
			Role:    api.RoleTool,
			Content: unfinishedToolCallOutput,
			ToolResult: &api.ToolResult{
				ToolCallID: call.ID,
				Output:     unfinishedToolCallOutput,
				IsError:    true,
			},
		})
	}
	if len(missing) == 0 {
		return messages, false
	}
	return slices.Insert(messages, insertAt, missing...), true
}

func (t *userTurnContext) finishCancelledTurn() error {
	if t.markTurnMetric("cancelled") {
		if err := emitTurnTimingCheckpoint(t.deps.bridge, t.turnMetrics, "cancelled"); err != nil {
			return err
		}
	}
	if t.turnStopReason == "" {
		t.turnStopReason = "cancelled"
		if err := t.deps.bridge.Emit(ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: "cancelled"}); err != nil {
			return err
		}
	}
	t.flushTurnMetrics("cancelled")
	return nil
}

func (t *userTurnContext) finalizePlannerTurn(ctx context.Context, planner *agent.Planner, messagesBeforeQuery int) error {
	updates, err := planner.FinalizeTurn(ctx, "", t.plannerUserRequest, t.state.messages, messagesBeforeQuery)
	if err != nil {
		return t.deps.bridge.EmitError(fmt.Sprintf("update session artifact: %v", err), true)
	}
	return emitArtifactUpdates(t.deps.bridge, updates, t.turnMetrics, true)
}

func (t *userTurnContext) handlePlanReviewDecision(ctx context.Context, messagesBeforeQuery int) (bool, error) {
	if t.state.mode != agent.ModePlan {
		return false, nil
	}
	reviewResult, reviewErr := handlePlanReviewGate(ctx, t.deps.bridge, t.deps.router, &t.state.mode, t.deps.artifactManager, t.state.sessionID, t.state.messages, messagesBeforeQuery, t.turnStopReason)
	if reviewErr != nil && reviewErr != context.Canceled {
		if emitErr := t.deps.bridge.EmitError(fmt.Sprintf("plan review gate: %v", reviewErr), true); emitErr != nil {
			return false, emitErr
		}
	}
	switch reviewResult.Decision {
	case "approved":
		return t.appendReviewFollowUp("plan_approved", "plan_approved", "Plan approved. Implement it now.")
	case "revised":
		return t.appendReviewFollowUp("plan_review_revised", "plan_revised", planRevisionFeedbackMessage(reviewResult.Feedback))
	case "cancelled":
		t.markTurnMetric("plan_review_cancelled")
		t.turnStopReason = "plan_cancelled"
		t.flushTurnMetrics("plan_review_cancelled")
		return false, nil
	default:
		return false, nil
	}
}

func (t *userTurnContext) appendReviewFollowUp(metric, outcome, content string) (bool, error) {
	t.markTurnMetric(metric)
	t.turnStopReason = outcome
	t.flushTurnMetrics(metric)
	t.state.messages = append(t.state.messages, api.Message{Role: api.RoleUser, Content: content})
	t.persistCurrentMessages()
	if err := emitContextWindowUsage(t.deps.bridge, t.state.client, t.state.messages); err != nil {
		return false, err
	}
	return true, nil
}

func (t *userTurnContext) maybeGenerateSessionTitle() {
	if t.state.titleGenerated || len(t.state.messages) == 0 {
		return
	}
	t.state.titleGenerated = true
	titleClient := t.state.client
	titleSessionID := t.state.sessionID
	titleMessages := api.DeepCopyMessages(t.state.messages)
	go func() {
		modelRouter := localmodel.NewRouter(titleClient)
		title := session.GenerateTitle(modelRouter, newAccountedClient(titleClient, t.deps.bridge, t.deps.tracker), titleMessages)
		if title != "" {
			_ = t.deps.sessionStore.UpdateMetadata(titleSessionID, func(existing session.Metadata) session.Metadata {
				existing.Title = title
				existing.UpdatedAt = time.Now()
				return existing
			})
			_ = emitSessionUpdated(t.deps.bridge, titleSessionID, title)
		}
	}()
}

func (t *userTurnContext) newQueryRequest(availableSkills []skillspkg.Skill) agent.QueryRequest {
	sessionMemory, _ := loadSessionMemorySnapshot(context.Background(), t.deps.artifactManager, t.state.sessionID)
	capabilities := t.state.client.Capabilities()
	return agent.QueryRequest{
		Messages:        t.state.messages,
		SystemPrompt:    systemPromptForMode(t.state.mode),
		ModelID:         t.state.client.ModelID(),
		ReasoningEffort: config.Load().ReasoningEffort,
		Mode:            t.state.mode,
		SessionID:       t.state.sessionID,
		Skills:          availableSkills,
		ExplicitSkills:  t.explicitSkills,
		Tools:           t.deps.registry.Definitions(),
		Capabilities:    capabilities,
		ContextWindow:   capabilities.PromptTokenBudget(),
		MaxTokens:       capabilities.MaxOutputTokens,
		SessionMemory:   sessionMemory,
	}
}

func (t *userTurnContext) newQueryDeps(planner *agent.Planner) agent.QueryDeps {
	return agent.QueryDeps{
		CallModel: func(callCtx context.Context, req api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
			return trackModelStream(callCtx, t.deps.bridge, t.deps.tracker, t.state.client, req)
		},
		ExecuteToolBatch: func(callCtx context.Context, calls []api.ToolCall) ([]api.ToolResult, error) {
			return executeToolCalls(callCtx, t.deps.bridge, t.deps.router, t.deps.registry, t.deps.permissionCtx, t.deps.tracker, planner, t.deps.artifactManager, t.deps.hookRunner, t.state.sessionID, t.state.sessionDir, t.state.client.Capabilities().MaxOutputTokens, t.turnMetrics, t.turnStats, calls)
		},
		CompactMessages: func(callCtx context.Context, current []api.Message, reason agent.CompactReason) (compact.CompactResult, error) {
			sessionMemory, _ := loadSessionMemorySnapshot(callCtx, t.deps.artifactManager, t.state.sessionID)
			result, err := compactWithMetrics(callCtx, t.deps.bridge, t.deps.tracker, t.state.client, t.deps.timingLogger, t.state.sessionID, t.turnID, string(reason), sessionMemory, systemPromptForMode(t.state.mode), t.deps.registry.Definitions(), current)
			if err != nil {
				return compact.CompactResult{}, err
			}
			t.rebaseAfterCompaction(result)
			return result, nil
		},
		RecallMemory: func(callCtx context.Context, files []agent.MemoryFile, userPrompt string) ([]agent.MemoryRecallResult, error) {
			selector := memorypkg.RecallSelector{}
			return selector.Select(callCtx, files, userPrompt)
		},
		LoadSessionMemory: func(callCtx context.Context) (agent.SessionMemorySnapshot, error) {
			return loadSessionMemorySnapshot(callCtx, t.deps.artifactManager, t.state.sessionID)
		},
		BeforeStop: func(callCtx context.Context, stopReq agent.StopRequest) (agent.StopDecision, error) {
			// File stop hooks run first: they are user-authored and cost
			// nothing. Goal evaluation costs a model call, so it only runs when
			// no hook has already decided to keep the turn open.
			decision, err := evaluateSessionStopHooks(callCtx, t.deps.hookRunner, t.state.sessionID, stopReq)
			if err != nil || decision.Continue {
				return decision, err
			}
			judge := newAccountedClient(t.state.client, t.deps.bridge, t.deps.tracker)
			return evaluateSessionGoal(callCtx, t.deps.bridge, goalStoreFor(t.state.sessionDir), judge, stopReq)
		},
		ApplyResultBudget: func(current []api.Message) []api.Message {
			return current
		},
		ObserveContinuation: func(tracker agent.ContinuationTracker, reason string) {
			t.turnStats.ContinuationBudgetTokens = tracker.MaxBudgetTokens
			t.turnStats.ContinuationCount = tracker.ContinuationCount
			t.turnStats.ContinuationStopReason = reason
			t.turnStats.ContinuationUsedTokens = tracker.BudgetUsedTokens
		},
		EmitTelemetry: t.deps.bridge.EmitEvent,
		PersistMessages: func(updated []api.Message) {
			t.state.messages = updated
			if t.state.timeline != nil {
				t.state.timeline.SyncMessages(updated)
			}
			t.persistCurrentMessages()
			_ = emitContextWindowUsage(t.deps.bridge, t.state.client, t.state.messages)
		},
		Clock:      time.Now,
		AttemptLog: agent.NewAttemptLog(t.state.sessionDir),
	}
}

// rebaseAfterCompaction brings the turn's view of the conversation in line
// with a compaction that just rewrote it mid-query. Summarizing replaces the
// earlier messages with a single summary message, so every position recorded
// before it is stale: the query start can point past the end of the shorter
// list, and the timeline names messages by their index. The query's messages
// now begin at the new summary, which also lets the post-turn steps see that
// this turn compacted.
func (t *userTurnContext) rebaseAfterCompaction(result compact.CompactResult) {
	if result.Strategy != compact.StrategySummarize && result.Strategy != compact.StrategyPartial {
		// Truncating tool output edits messages in place; positions still hold.
		return
	}
	t.queryStart = 0
	for index, message := range slices.Backward(result.Messages) {
		if compact.IsSummaryMessage(message) {
			t.queryStart = index
			break
		}
	}
	// The agent keeps appending to its own slice, so hold a copy.
	t.state.messages = append([]api.Message(nil), result.Messages...)
	if t.state.timeline != nil {
		t.state.timeline = rebuildConversationTimeline(t.state.messages)
	}
}

// persistCurrentMessages saves the conversation mid-turn. A failed save must
// not end the turn, since the work is still in memory, but the user has to
// learn that the session on disk stopped keeping up before relying on it.
func (t *userTurnContext) persistCurrentMessages() {
	stateErr := persistSessionState(t.deps.sessionStore, sessionStateParams{
		SessionID:     t.state.sessionID,
		CreatedAt:     t.state.startedAt,
		Mode:          t.state.mode,
		Model:         t.state.activeModelID,
		SubagentModel: t.deps.subagentModelState.Get(),
		CWD:           t.state.cwd,
		Branch:        agent.LoadTurnContext().GitBranch,
		Tracker:       t.deps.tracker,
		Messages:      t.state.messages,
	})
	timelineErr := persistConversationHydratedPayload(t.deps.sessionStore, t.state.sessionID, t.state.timeline, t.state.messages, t.state.activeModelID)
	if err := errors.Join(stateErr, timelineErr); err != nil {
		// The callers have no error to return, and a bridge that cannot
		// carry this notice fails the turn on its next event anyway.
		_ = t.deps.bridge.EmitNotice(fmt.Sprintf("Session not saved: %v", err))
	}
}

func (t *userTurnContext) markTurnMetric(checkpoint string) bool {
	if t.turnMetrics == nil {
		return false
	}
	return t.turnMetrics.Mark(checkpoint)
}

func (t *userTurnContext) flushTurnMetrics(outcome string) {
	if t.turnMetrics == nil {
		return
	}
	_ = t.deps.timingLogger.AppendSnapshot("turn", "query_latency", t.state.sessionID, t.turnID, t.turnMetrics, map[string]any{
		"aggregate_tool_budget_chars": t.turnStats.AggregateBudgetChars,
		"aggregate_budget_spills":     t.turnStats.AggregateBudgetSpills,
		"continuation_budget_tokens":  t.turnStats.ContinuationBudgetTokens,
		"continuation_count":          t.turnStats.ContinuationCount,
		"continuation_stop_reason":    t.turnStats.ContinuationStopReason,
		"continuation_used_tokens":    t.turnStats.ContinuationUsedTokens,
		"image_count":                 len(t.payload.Images),
		"message_count_after":         len(t.state.messages),
		"message_count_before":        t.messageCountBefore,
		"mode":                        string(t.state.mode),
		"model":                       t.state.activeModelID,
		"outcome":                     outcome,
		"stop_reason":                 t.turnStopReason,
		"tool_inline_chars":           t.turnStats.ToolInlineChars,
		"tool_result_count":           t.turnStats.ToolResultCount,
		"tool_spill_count":            t.turnStats.ToolSpillCount,
		"user_input_characters":       len(t.payload.Text),
	})
	t.turnMetrics = nil
}

func emitArtifactUpdates(bridge *ipc.Bridge, updates []agent.ArtifactUpdate, turnMetrics *timing.CheckpointRecorder, focusPlans bool) error {
	for _, update := range updates {
		if update.Created {
			if err := emitArtifactCreated(bridge, update.Artifact); err != nil {
				return err
			}
		}
		if err := emitArtifactUpdated(bridge, update.Artifact, update.Content); err != nil {
			return err
		}
		if focusPlans && update.Artifact.Kind == artifactspkg.KindImplementationPlan && strings.TrimSpace(update.Content) != "" {
			if err := emitArtifactFocusedForTurn(bridge, update.Artifact, turnMetrics); err != nil {
				return err
			}
		}
	}
	return nil
}

func loadAvailableSkills(bridge *ipc.Bridge, cwd string) ([]skillspkg.Skill, error) {
	cfg := config.LoadForWorkingDir(cwd)
	skills, err := skillspkg.LoadAll(cwd, cfg.SkillDir)
	if err == nil {
		return skills, nil
	}
	if bridge == nil {
		return skills, err
	}
	if emitErr := bridge.EmitNotice(fmt.Sprintf("load skills: %v", err)); emitErr != nil {
		return skills, emitErr
	}
	return skills, nil
}
