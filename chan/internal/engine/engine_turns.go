package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"iter"
	"strings"
	"time"

	"github.com/channyeintun/chan/internal/agent"
	"github.com/channyeintun/chan/internal/api"
	artifactspkg "github.com/channyeintun/chan/internal/artifacts"
	"github.com/channyeintun/chan/internal/clientdebug"
	"github.com/channyeintun/chan/internal/compact"
	"github.com/channyeintun/chan/internal/config"
	costpkg "github.com/channyeintun/chan/internal/cost"
	"github.com/channyeintun/chan/internal/hooks"
	"github.com/channyeintun/chan/internal/ipc"
	"github.com/channyeintun/chan/internal/localmodel"
	memorypkg "github.com/channyeintun/chan/internal/memory"
	"github.com/channyeintun/chan/internal/permissions"
	"github.com/channyeintun/chan/internal/session"
	skillspkg "github.com/channyeintun/chan/internal/skills"
	"github.com/channyeintun/chan/internal/timing"
	toolpkg "github.com/channyeintun/chan/internal/tools"
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
	turnID             int
	turnMetrics        *timing.CheckpointRecorder
	turnStats          *turnExecutionStats
	turnStopReason     string
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
		if emitErr := t.deps.bridge.EmitError(fmt.Sprintf("initialize model %q: %v", t.state.activeModelID, err), true); emitErr != nil {
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
		if err := t.deps.bridge.EmitError(fmt.Sprintf("model %q does not support image input", t.state.activeModelID), true); err != nil {
			return false, err
		}
		return false, nil
	}
	return true, nil
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
	messagesBeforeQuery := len(t.state.messages)
	planner := agent.NewPlanner(t.state.mode, t.state.sessionID, t.deps.artifactManager)
	if err := t.beginPlannerTurn(ctx, planner); err != nil {
		return false, err
	}
	queryResult, err := t.executeQuery(ctx, planner, availableSkills)
	if err != nil {
		return false, err
	}
	if queryResult.Completed {
		return false, nil
	}
	if queryResult.Stopped {
		return false, nil
	}
	if err := t.finalizePlannerTurn(ctx, planner, messagesBeforeQuery); err != nil {
		return false, err
	}
	if err := maybeRefreshSessionMemory(ctx, t.deps.bridge, t.deps.artifactManager, t.state.sessionID, t.turnID, t.state.messages, messagesBeforeQuery, newSessionMemoryRefiner(t.deps.bridge, t.deps.tracker, t.state.client)); err != nil {
		return false, err
	}
	continueTurn, err := t.handlePlanReviewDecision(ctx, messagesBeforeQuery)
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
	Completed bool
	Stopped   bool
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
	titleStartedAt := t.state.startedAt
	titleMode := t.state.mode
	titleModelID := t.state.activeModelID
	titleCWD := t.state.cwd
	titleBranch := agent.LoadTurnContext().GitBranch
	titleMessages := api.DeepCopyMessages(t.state.messages)
	go func() {
		modelRouter := localmodel.NewRouter(titleClient)
		title := session.GenerateTitle(modelRouter, titleClient, titleMessages)
		if title != "" {
			_ = t.deps.sessionStore.SaveMetadata(session.Metadata{
				SessionID:     titleSessionID,
				CreatedAt:     titleStartedAt,
				UpdatedAt:     time.Now(),
				Mode:          string(titleMode),
				Model:         titleModelID,
				SubagentModel: t.deps.subagentModelState.Get(),
				CWD:           titleCWD,
				Branch:        titleBranch,
				TotalCostUSD:  t.deps.tracker.Snapshot().TotalCostUSD,
				Title:         title,
			})
			_ = emitSessionUpdated(t.deps.bridge, titleSessionID, title)
		}
	}()
}

func (t *userTurnContext) newQueryRequest(availableSkills []skillspkg.Skill) agent.QueryRequest {
	sessionMemory, _ := loadSessionMemorySnapshot(context.Background(), t.deps.artifactManager, t.state.sessionID)
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
		Capabilities:    t.state.client.Capabilities(),
		ContextWindow:   t.state.client.Capabilities().MaxContextWindow,
		MaxTokens:       t.state.client.Capabilities().MaxOutputTokens,
		SessionMemory:   sessionMemory,
	}
}

func (t *userTurnContext) newQueryDeps(planner *agent.Planner) agent.QueryDeps {
	return agent.QueryDeps{
		CallModel: func(callCtx context.Context, req api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
			return trackModelStream(callCtx, t.deps.bridge, t.deps.tracker, t.state.client, req)
		},
		ExecuteToolBatch: func(callCtx context.Context, calls []api.ToolCall) ([]api.ToolResult, error) {
			return executeToolCalls(callCtx, t.deps.bridge, t.deps.router, t.deps.registry, t.deps.permissionCtx, t.deps.tracker, planner, t.deps.artifactManager, t.deps.hookRunner, t.state.sessionID, t.state.client.Capabilities().MaxOutputTokens, t.turnMetrics, t.turnStats, calls)
		},
		CompactMessages: func(callCtx context.Context, current []api.Message, reason agent.CompactReason) (compact.CompactResult, error) {
			sessionMemory, _ := loadSessionMemorySnapshot(callCtx, t.deps.artifactManager, t.state.sessionID)
			return compactWithMetrics(callCtx, t.deps.bridge, t.deps.tracker, t.state.client, t.deps.timingLogger, t.state.sessionID, t.turnID, string(reason), sessionMemory, systemPromptForMode(t.state.mode), t.deps.registry.Definitions(), current)
		},
		RecallMemory: func(callCtx context.Context, files []agent.MemoryFile, userPrompt string) ([]agent.MemoryRecallResult, error) {
			selector := memorypkg.RecallSelector{}
			return selector.Select(callCtx, files, userPrompt)
		},
		LoadSessionMemory: func(callCtx context.Context) (agent.SessionMemorySnapshot, error) {
			return loadSessionMemorySnapshot(callCtx, t.deps.artifactManager, t.state.sessionID)
		},
		BeforeStop: func(callCtx context.Context, stopReq agent.StopRequest) (agent.StopDecision, error) {
			return evaluateSessionStopHooks(callCtx, t.deps.hookRunner, t.state.sessionID, stopReq)
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
				if t.turnStopReason != "" {
					t.state.timeline.FlushPendingAssistantMessages()
				}
			}
			t.persistCurrentMessages()
			_ = emitContextWindowUsage(t.deps.bridge, t.state.client, t.state.messages)
		},
		Clock:      time.Now,
		AttemptLog: agent.NewAttemptLog(t.state.sessionDir),
	}
}

func (t *userTurnContext) persistCurrentMessages() {
	_ = persistSessionState(t.deps.sessionStore, sessionStateParams{
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
	_ = persistConversationHydratedPayload(t.deps.sessionStore, t.state.sessionID, t.state.timeline, t.state.messages, t.state.activeModelID)
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
	skills, err := skillspkg.LoadAll(cwd)
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
