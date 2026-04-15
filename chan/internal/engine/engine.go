package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/channyeintun/chan/internal/agent"
	"github.com/channyeintun/chan/internal/api"
	artifactspkg "github.com/channyeintun/chan/internal/artifacts"
	"github.com/channyeintun/chan/internal/clientdebug"
	"github.com/channyeintun/chan/internal/compact"
	"github.com/channyeintun/chan/internal/config"
	costpkg "github.com/channyeintun/chan/internal/cost"
	"github.com/channyeintun/chan/internal/debuglog"
	"github.com/channyeintun/chan/internal/hooks"
	"github.com/channyeintun/chan/internal/ipc"
	mcppkg "github.com/channyeintun/chan/internal/mcp"
	"github.com/channyeintun/chan/internal/session"
	"github.com/channyeintun/chan/internal/timing"
	toolpkg "github.com/channyeintun/chan/internal/tools"
)

func RunStdioEngine(ctx context.Context, cfg config.Config) error {
	engineStartedAt := time.Now()

	// Debug logging: activated by CHAN_DEBUG=1
	if os.Getenv("CHAN_DEBUG") != "" {
		debuglog.Enabled = true
	}

	var stdinR io.Reader = debuglog.NewIPCReader(os.Stdin)
	var stdoutW io.Writer = debuglog.NewIPCWriter(os.Stdout)

	bridge := ipc.NewBridge(stdinR, stdoutW)
	registry := toolpkg.NewRegistry()
	provider, model := config.ParseModel(cfg.Model)
	provider = normalizeProvider(provider)
	var (
		client          api.LLMClient
		startupModelErr error
	)
	toolUseNoticeModelID := ""
	activeModelID := modelRef(provider, model)
	client, err := newLLMClient(provider, model, cfg)
	if err != nil {
		startupModelErr = err
	} else {
		activeModelID = modelRef(provider, client.ModelID())
	}
	client = clientdebug.WrapClient(client)
	modelState := NewActiveModelState(client, activeModelID)
	subagentModelID := defaultSessionSubagentModel(cfg, activeModelID)
	subagentModelState := NewActiveSubagentModelState(subagentModelID)
	messages := make([]api.Message, 0, 32)
	mode := parseExecutionMode(cfg.DefaultMode)
	permissionCtx := newPermissionContext(cfg.PermissionMode)
	tracker := costpkg.NewTracker()
	hookRunner := hooks.NewRunner(hooks.DefaultHooksDir())
	sessionStore := session.NewStore(session.DefaultBaseDir())
	artifactStore := artifactspkg.NewLocalStore(filepath.Join(filepath.Dir(session.DefaultBaseDir()), "artifacts"))
	artifactManager := artifactspkg.NewManager(artifactStore)
	sessionID, err := newSessionID()
	if err != nil {
		return err
	}
	sessionDir := sessionStore.SessionDir(sessionID)
	if err := debuglog.ConfigureSession(sessionID, sessionDir); err != nil && debuglog.Enabled {
		fmt.Fprintf(os.Stderr, "debuglog: configure session %s: %v\n", sessionID, err)
	}
	timingLogger := timing.NewSessionLogger(sessionDir)

	// Init debug logging now that we have a session directory.
	if debuglog.Enabled {
		if _, err := debuglog.Enable(); err != nil {
			fmt.Fprintf(os.Stderr, "debuglog: enable: %v\n", err)
		}
		defer debuglog.Close()
		debuglog.LogGoroutineCount()
	}

	startupMetrics := timing.NewCheckpointRecorder(engineStartedAt)
	fileHistory := toolpkg.NewFileHistory(toolpkg.DefaultFileHistoryDir(sessionDir))
	toolpkg.SetGlobalFileHistory(fileHistory)
	toolpkg.SetGlobalSessionArtifacts(sessionID, artifactManager)
	if client != nil {
		startClientWarmup(ctx, timingLogger, startupMetrics, sessionID, activeModelID, client)
	}
	startedAt := time.Now()
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	mcpManager := mcppkg.NewManager(cwd, cfg.MCP)
	mcpManager.Start(ctx)
	defer func() {
		if err := mcpManager.Close(); err != nil && debuglog.Enabled {
			fmt.Fprintf(os.Stderr, "mcp: close: %v\n", err)
		}
	}()
	for _, discovered := range mcpManager.Tools() {
		registry.Register(toolpkg.NewMCPTool(mcpManager, discovered))
	}
	registry.Register(toolpkg.NewAgentTool(makeSubagentRunner(bridge, registry, permissionCtx, tracker, sessionStore, artifactManager, hookRunner, modelState, subagentModelState, cwd)))
	registry.Register(toolpkg.NewAgentStatusTool(lookupBackgroundAgentStatus))
	registry.Register(toolpkg.NewAgentStopTool(func(ctx context.Context, req toolpkg.AgentStopRequest) (toolpkg.AgentRunResult, error) {
		return stopBackgroundAgent(ctx, bridge, req)
	}))
	if err := persistSessionState(sessionStore, sessionStateParams{
		SessionID:     sessionID,
		CreatedAt:     startedAt,
		Mode:          mode,
		Model:         activeModelID,
		SubagentModel: subagentModelID,
		CWD:           cwd,
		Branch:        agent.LoadTurnContext().GitBranch,
		Tracker:       tracker,
		Messages:      messages,
	}); err != nil {
		return err
	}
	startupMetrics.Mark("session_persisted")

	// Emit ready event
	if err := bridge.EmitReady(slashCommandDescriptors()); err != nil {
		return fmt.Errorf("emit ready: %w", err)
	}
	startupMetrics.Mark("ready_emitted")
	_ = timingLogger.AppendSnapshot("session", "boot_to_ready", sessionID, 0, startupMetrics, map[string]any{
		"cwd":   cwd,
		"mode":  string(mode),
		"model": activeModelID,
	})
	if err := emitSessionUpdated(bridge, sessionID, ""); err != nil {
		return err
	}
	if client != nil {
		if err := emitModelChanged(bridge, activeModelID, client); err != nil {
			return err
		}
		if err := emitContextWindowUsage(bridge, client, messages); err != nil {
			return err
		}
	}
	if startupModelErr != nil {
		if err := bridge.EmitError(fmt.Sprintf("initialize model %q: %v", activeModelID, startupModelErr), true); err != nil {
			return err
		}
	}

	// Start the message router — single reader goroutine for the bridge.
	router := ipc.NewMessageRouter(ctx, bridge)
	defer toolpkg.ShutdownBackgroundCommandsForSession()

	// Fire session_start hooks (best-effort)
	_, _ = hookRunner.Run(ctx, hooks.Payload{
		Type:      hooks.HookSessionStart,
		SessionID: sessionID,
	})
	loopState := &engineLoopState{
		client:          client,
		sessionID:       sessionID,
		sessionDir:      sessionDir,
		startedAt:       startedAt,
		mode:            mode,
		activeModelID:   activeModelID,
		subagentModelID: subagentModelID,
		cwd:             cwd,
		messages:        messages,
		toolUseNoticeID: toolUseNoticeModelID,
		titleGenerated:  false,
	}
	loopDeps := engineLoopDeps{
		bridge:             bridge,
		router:             router,
		registry:           registry,
		permissionCtx:      permissionCtx,
		tracker:            tracker,
		hookRunner:         hookRunner,
		sessionStore:       sessionStore,
		artifactManager:    artifactManager,
		timingLogger:       timingLogger,
		modelState:         modelState,
		subagentModelState: subagentModelState,
		cfg:                cfg,
	}

	// Main event loop: read client messages and dispatch
	for {
		msg, err := router.Next(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil // clean shutdown
			}
			return fmt.Errorf("read message: %w", err)
		}

		switch msg.Type {
		case ipc.MsgShutdown:
			return nil
		case ipc.MsgUserInput:
			var payload ipc.UserInputPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return fmt.Errorf("decode user input: %w", err)
			}
			if err := handleUserInputMessage(ctx, payload, loopDeps, loopState); err != nil {
				return err
			}
		case ipc.MsgSlashCommand:
			var payload ipc.SlashCommandPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return fmt.Errorf("decode slash command: %w", err)
			}

			slashState, err := handleSlashCommand(
				ctx,
				bridge,
				router,
				sessionStore,
				timingLogger,
				cfg,
				artifactManager,
				mcpManager,
				tracker,
				payload,
				loopState.sessionID,
				loopState.startedAt,
				loopState.mode,
				loopState.activeModelID,
				loopState.subagentModelID,
				loopState.cwd,
				loopState.messages,
				&loopState.client,
			)
			if err != nil {
				return err
			}
			loopState.sessionID = slashState.SessionID
			loopState.sessionDir = sessionStore.SessionDir(slashState.SessionID)
			loopState.startedAt = slashState.StartedAt
			loopState.mode = slashState.Mode
			loopState.activeModelID = slashState.ActiveModelID
			loopState.subagentModelID = slashState.SubagentModelID
			loopState.cwd = slashState.CWD
			loopState.messages = slashState.Messages
			modelState.Set(loopState.client, loopState.activeModelID)
			subagentModelState.Set(loopState.subagentModelID)
			toolpkg.SetGlobalSessionArtifacts(loopState.sessionID, artifactManager)
			continue
		case ipc.MsgModeToggle:
			if loopState.mode == agent.ModePlan {
				loopState.mode = agent.ModeFast
			} else {
				loopState.mode = agent.ModePlan
			}
			if err := persistSessionState(sessionStore, sessionStateParams{
				SessionID:     loopState.sessionID,
				CreatedAt:     loopState.startedAt,
				Mode:          loopState.mode,
				Model:         loopState.activeModelID,
				SubagentModel: loopState.subagentModelID,
				CWD:           loopState.cwd,
				Branch:        agent.LoadTurnContext().GitBranch,
				Tracker:       tracker,
				Messages:      loopState.messages,
			}); err != nil {
				return err
			}
			if err := bridge.Emit(ipc.EventModeChanged, ipc.ModeChangedPayload{Mode: string(loopState.mode)}); err != nil {
				return err
			}
			continue
		case ipc.MsgPermissionResponse:
			continue // stale response outside query; ignore
		}
	}
}

func newLLMClient(provider, model string, cfg config.Config) (api.LLMClient, error) {
	provider = normalizeProvider(provider)
	return providerBehaviorFor(provider).NewClient(provider, model, cfg)
}

const clientWarmupTimeout = 3 * time.Second

func startClientWarmup(ctx context.Context, logger *timing.Logger, startupMetrics *timing.CheckpointRecorder, sessionID, activeModelID string, client api.LLMClient) {
	warmable, ok := client.(api.WarmupCapable)
	if !ok || warmable == nil {
		return
	}
	startupMetrics.Mark("api_preconnect_started")

	go func() {
		startedAt := time.Now()
		warmupCtx, cancel := context.WithTimeout(ctx, clientWarmupTimeout)
		defer cancel()

		err := warmable.Warmup(warmupCtx)
		endedAt := time.Now()
		if err == nil {
			startupMetrics.MarkAt("api_preconnect_completed", endedAt)
		}

		metadata := map[string]any{
			"model":   activeModelID,
			"outcome": "ok",
		}
		if err != nil {
			metadata["outcome"] = "error"
			metadata["error"] = err.Error()
		}

		_ = logger.Append(timing.Record{
			Kind:       "session",
			Metric:     "api_preconnect",
			SessionID:  sessionID,
			StartedAt:  startedAt.UTC(),
			EndedAt:    endedAt.UTC(),
			DurationMS: endedAt.Sub(startedAt).Milliseconds(),
			Metadata:   metadata,
		})
	}()
}

func ensureClientForSelection(modelSelection string, cfg config.Config, current api.LLMClient) (api.LLMClient, string, error) {
	if current != nil {
		return current, modelSelection, nil
	}

	provider, model := config.ParseModel(strings.TrimSpace(modelSelection))
	provider, model = resolveModelSelection(modelSelection, provider)
	client, err := newLLMClient(provider, model, cfg)
	if err != nil {
		return nil, modelRef(provider, model), err
	}
	return client, modelRef(provider, client.ModelID()), nil
}

func normalizeProvider(provider string) string {
	if strings.TrimSpace(provider) == "" {
		return "anthropic"
	}
	return provider
}

func modelRef(provider, model string) string {
	provider = normalizeProvider(provider)
	model = strings.TrimSpace(model)
	if model == "" {
		return provider
	}
	if provider == "" {
		return model
	}
	return provider + "/" + model
}

func resolveModelSelection(input string, fallbackProvider string) (string, string) {
	return providerBehaviorFor(fallbackProvider).ResolveSelection(input, fallbackProvider)
}

func parseExecutionMode(mode string) agent.ExecutionMode {
	if strings.EqualFold(mode, string(agent.ModeFast)) {
		return agent.ModeFast
	}
	return agent.ModePlan
}

func defaultSystemPrompt() string {
	return strings.TrimSpace(`You are Go CLI, a pragmatic coding assistant. Be concise, prefer inspecting files before changing them, and use tools when needed.
Be extremely concise. Sacrifice grammar for the sake of concision.
Communicate efficiently. Keep user-visible updates short, factual, and action-oriented.
Do not front-load long reasoning, speculative plans, or repeated recaps in the transcript. Inspect with tools, act once you have enough context, and summarize only the essential next step.
Prefer brief progress updates over long explanations. After a meaningful read-only batch or roughly 3-5 tool calls, give a short status update and what you will do next.
Unless the user explicitly asks for planning or deep explanation, avoid long setup prose. For simple implementation requests, make the obvious local changes directly and verify them.

IMPORTANT: Always use absolute paths with file tools. The working directory is provided in the environment context below — use it to construct absolute paths. For example, if the working directory is /home/user/project, use /home/user/project/file.txt instead of file.txt.
Always use tools to answer questions — do NOT just make a plan without acting. Call tools immediately when you need information.
For simple, self-contained implementation requests, do not browse the web or ask routine clarifying questions. Make the obvious file changes directly with local file tools.
Use the exact runtime tool names when calling tools, including agent, agent_status, agent_stop, bash, think, list_dir, create_file, read_file, file_write, replace_string_in_file, multi_replace_string_in_file, apply_patch, file_diff_preview, file_search, grep_search, go_definition, go_references, read_project_structure, project_overview, dependency_overview, symbol_search, web_search, web_fetch, git, list_commands, command_status, send_command_input, stop_command, forget_command, file_history, file_history_rewind, save_implementation_plan, upsert_task_list, and save_walkthrough.
Use read_project_structure when you need the actual file tree or directory layout. Use project_overview when you need a compact semantic summary of the repository.
For bounded delegated work, prefer agent with subagent_type=search for code discovery and file/line references, subagent_type=execution for terminal-heavy tasks, subagent_type=explore for broad read-only research, and subagent_type=general-purpose only when the task does not fit a specialized mode.
Work like a choreographer, not an orchestrator: delegate bounded work to specialized child agents with a clear objective, constraints, and expected output, let them finish, then synthesize the result in the parent context.
Use child agents proactively for non-trivial exploration, broad codebase discovery, or terminal-heavy execution instead of manually chaining many parent-level tool calls when the work can be isolated cleanly.
Only use run_in_background=true when the user explicitly wants asynchronous progress or the task genuinely benefits from later monitoring. Otherwise prefer the default bounded foreground child-agent flow.
Call agent_status or agent_stop only for agents that were launched in background. Do not poll normal foreground child agents; their returned result is the status signal.

Use the file-edit ladder deliberately:
- replace_string_in_file: first choice for one exact literal replacement in one existing file.
- multi_replace_string_in_file: first choice for several exact literal replacements in one file or a small set of files.
- apply_patch: use only when exact replacements are awkward or impossible, or when the edit is truly multi-file, multi-hunk, create/delete, or broadly structural.
- file_write: full overwrite of one existing file only.
- create_file: create a brand-new file only.

For complex, multi-step tasks, follow a structured workflow:
1. Research: Use read tools or focused child agents to understand the codebase and gather context before making changes. Prefer child agents early when the task spans multiple directories, needs pattern discovery, or can be parallelized.
2. Plan: For non-trivial implementation work, create or update an implementation plan with save_implementation_plan before editing. Treat it as a durable review artifact, not disposable transcript text. The user can review, request revisions, or approve it before you proceed.
3. Track: Use upsert_task_list to break work into concrete checklist items once the task is substantial enough to benefit from tracking. Mark items in-progress when starting and completed when done — keep the list current as a living document.
4. Implement: Work through the task list deliberately. If unexpected complexity arises, pause and revise the plan before continuing.
5. Verify: After implementation, run builds and tests. Save a walkthrough with save_walkthrough summarizing what changed and how it was validated.
For simple tasks (single-file edits, quick questions, small fixes), skip straight to implementation — do not create unnecessary artifacts.

Artifacts are first-class outputs in this runtime — durable, reviewable work products, not just overflow containers for long text. Use them intentionally:
- save_implementation_plan: real implementation plans that the user will review before execution begins. Update the existing plan in place as the design changes.
- upsert_task_list: live multi-step progress tracking for ongoing work; update it as tasks complete.
- save_walkthrough: completed-work summaries after finishing a task.
- search-report and diff-preview artifacts are produced automatically for large web_fetch and git diff results.
- Oversized tool outputs are saved automatically as tool-log artifacts.
Do NOT save an artifact merely because a response is long. Save it when the content should persist for review, revision, or resumption across turns.

Write artifact content in clean GitHub-flavored markdown optimized for the artifact panel: clear headings, short lists, tables, fenced code blocks with language tags, diff blocks, and GitHub alert blocks (> [!NOTE], > [!WARNING], > [!CAUTION]) for important review items. Keep artifact bodies self-contained and revision-friendly. After saving a substantial artifact, write a short transcript summary of the key outcome — do not repeat the full artifact body in the transcript.`)
}

func systemPromptForMode(mode agent.ExecutionMode) string {
	prompt := defaultSystemPrompt()
	if mode == agent.ModePlan {
		return prompt + "\n\n" + strings.TrimSpace(`When plan mode is active, Ultrathink. Prefer specialized child agents early for bounded research instead of manually orchestrating long chains of exploratory tool calls in the parent context. Plan mode does not make the runtime read-only: if the user explicitly asks you to create or modify something, do it. For non-trivial implementation tasks, produce a concrete markdown implementation plan and save it with save_implementation_plan so the plan remains the explicit reviewable artifact for the task. The system may surface a review gate after you save a final plan; they can approve it, request revisions, or cancel. If the user sends revision feedback, update the same plan artifact in place rather than creating a new one.

For research, explanation, review, or other non-implementation requests, answer directly and do not create a plan artifact. When a real implementation plan is warranted, do not leave it only in the transcript; save or update the artifact. When you produce a real implementation plan, prefer this structure: Goal Description, Proposed Changes (grouped by component with [NEW]/[MODIFY]/[DELETE] markers), User Review Required, Open Questions, and Verification Plan. Use > [!CAUTION] or > [!WARNING] alert blocks for risky or irreversible changes that need explicit attention before approval.`) + " " + agent.PlanModePromptHint()
	}
	return prompt
}

func evaluateSessionStopHooks(
	ctx context.Context,
	hookRunner *hooks.Runner,
	sessionID string,
	stopReq agent.StopRequest,
) (agent.StopDecision, error) {
	if hookRunner == nil {
		return agent.StopDecision{}, nil
	}
	responses, err := hookRunner.Run(ctx, hooks.Payload{
		Type:      hooks.HookStop,
		SessionID: sessionID,
		Output:    strings.TrimSpace(stopReq.AssistantMessage.Content),
		Extra: map[string]any{
			"stop_reason": stopReq.StopReason,
			"turn_count":  stopReq.TurnCount,
		},
	})
	if err != nil {
		return agent.StopDecision{}, err
	}
	for _, resp := range responses {
		action := strings.ToLower(strings.TrimSpace(resp.Action))
		if action != "deny" && action != "stop" {
			continue
		}
		reason := strings.TrimSpace(resp.Message)
		if reason == "" {
			reason = "blocked by stop hook"
		}
		return agent.StopDecision{
			Continue:        true,
			Reason:          reason,
			FollowUpMessage: sessionStopBlockedFollowUp(reason),
		}, nil
	}
	return agent.StopDecision{}, nil
}

func runSessionStopFailureHooks(
	ctx context.Context,
	hookRunner *hooks.Runner,
	sessionID string,
	stopReason string,
	messages []api.Message,
	err error,
) {
	if hookRunner == nil || err == nil {
		return
	}
	_, _ = hookRunner.Run(ctx, hooks.Payload{
		Type:      hooks.HookStopFailure,
		SessionID: sessionID,
		Output:    latestSessionAssistantContent(messages),
		Error:     err.Error(),
		Extra: map[string]any{
			"stop_reason": strings.TrimSpace(stopReason),
		},
	})
}

func sessionStopBlockedFollowUp(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "A local stop hook blocked completion. Continue working until the stop condition is satisfied."
	}
	return fmt.Sprintf("A local stop hook blocked completion: %s\n\nContinue working until the stop condition is satisfied.", reason)
}

func latestSessionAssistantContent(messages []api.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		msg := messages[index]
		if msg.Role != api.RoleAssistant {
			continue
		}
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		return strings.TrimSpace(msg.Content)
	}
	return ""
}

func trackModelStream(
	ctx context.Context,
	bridge *ipc.Bridge,
	tracker *costpkg.Tracker,
	client api.LLMClient,
	req api.ModelRequest,
) (iter.Seq2[api.ModelEvent, error], error) {
	stream, err := client.Stream(ctx, req)
	if err != nil {
		return nil, err
	}

	return func(yield func(api.ModelEvent, error) bool) {
		startedAt := time.Now()
		var usage api.Usage

		for event, streamErr := range stream {
			if streamErr != nil {
				yield(api.ModelEvent{}, streamErr)
				return
			}
			if event.Type == api.ModelEventRateLimits && event.RateLimits != nil {
				if err := emitRateLimitUpdate(bridge, event.RateLimits); err != nil {
					yield(api.ModelEvent{}, err)
					return
				}
			}
			if event.Type == api.ModelEventUsage && event.Usage != nil {
				usage = mergeUsage(usage, *event.Usage)
			}
			if !yield(event, nil) {
				return
			}
		}

		tracker.RecordAPICall(
			client.ModelID(),
			usage.InputTokens,
			usage.OutputTokens,
			usage.CacheReadTokens,
			usage.CacheCreationTokens,
			time.Since(startedAt),
			costpkg.CalculateUSDCost(client.ModelID(), usage),
		)
		if err := emitCostUpdate(bridge, tracker); err != nil {
			yield(api.ModelEvent{}, err)
		}
	}, nil
}

func mergeUsage(current api.Usage, next api.Usage) api.Usage {
	current.InputTokens += next.InputTokens
	current.OutputTokens += next.OutputTokens
	current.CacheReadTokens += next.CacheReadTokens
	current.CacheCreationTokens += next.CacheCreationTokens
	return current
}

func emitCostUpdate(bridge *ipc.Bridge, tracker *costpkg.Tracker) error {
	snapshot := tracker.Snapshot()
	return bridge.Emit(ipc.EventCostUpdate, ipc.CostUpdatePayload{
		TotalUSD:                 snapshot.TotalCostUSD,
		InputTokens:              snapshot.TotalInputTokens,
		OutputTokens:             snapshot.TotalOutputTokens,
		MemoryRecallUSD:          snapshot.MemoryRecallCostUSD,
		MemoryRecallInputTokens:  snapshot.MemoryRecallInputTokens,
		MemoryRecallOutputTokens: snapshot.MemoryRecallOutputTokens,
		ChildAgentUSD:            snapshot.ChildAgentCostUSD,
		ChildAgentInputTokens:    snapshot.ChildAgentInputTokens,
		ChildAgentOutputTokens:   snapshot.ChildAgentOutputTokens,
	})
}

func emitRateLimitUpdate(bridge *ipc.Bridge, rateLimits *api.RateLimits) error {
	if rateLimits == nil {
		return nil
	}

	payload := ipc.RateLimitUpdatePayload{
		FiveHour: toRateLimitWindowPayload(rateLimits.FiveHour),
		SevenDay: toRateLimitWindowPayload(rateLimits.SevenDay),
	}
	if payload.FiveHour == nil && payload.SevenDay == nil {
		return nil
	}
	return bridge.Emit(ipc.EventRateLimitUpdate, payload)
}

func toRateLimitWindowPayload(window *api.RateLimitWindow) *ipc.RateLimitWindowPayload {
	if window == nil {
		return nil
	}
	return &ipc.RateLimitWindowPayload{
		UsedPercentage: window.Utilization * 100,
		ResetsAt:       window.ResetsAt,
	}
}

func emitModelChanged(bridge *ipc.Bridge, activeModelID string, client api.LLMClient) error {
	payload := ipc.ModelChangedPayload{Model: activeModelID}
	if client != nil {
		capabilities := client.Capabilities()
		payload.MaxContextWindow = capabilities.MaxContextWindow
		payload.MaxOutputTokens = capabilities.MaxOutputTokens
	}
	return bridge.Emit(ipc.EventModelChanged, payload)
}

func emitContextWindowUsage(bridge *ipc.Bridge, client api.LLMClient, messages []api.Message) error {
	if client == nil {
		return nil
	}

	return bridge.Emit(ipc.EventContextWindow, ipc.ContextWindowPayload{
		CurrentUsage: compact.EstimateConversationTokens(messages),
	})
}

func emitSessionUpdated(bridge *ipc.Bridge, sessionID, title string) error {
	return bridge.Emit(ipc.EventSessionUpdated, ipc.SessionUpdatedPayload{
		SessionID: sessionID,
		Title:     title,
	})
}

func emitToolUseCapabilityNotice(
	bridge *ipc.Bridge,
	activeModelID string,
	client api.LLMClient,
	lastNoticeModelID *string,
) error {
	if client == nil || client.Capabilities().SupportsToolUse {
		return nil
	}
	if lastNoticeModelID != nil && *lastNoticeModelID == activeModelID {
		return nil
	}
	if lastNoticeModelID != nil {
		*lastNoticeModelID = activeModelID
	}
	return bridge.Emit(ipc.EventError, ipc.ErrorPayload{
		Message:     fmt.Sprintf("Model %s does not support native tool use; continuing in text-only mode.", activeModelID),
		Recoverable: true,
	})
}

func planRevisionFeedbackMessage(feedback string) string {
	feedback = strings.TrimSpace(feedback)
	if feedback == "" {
		return "Please revise the implementation plan, update the existing implementation-plan artifact, and resubmit it for review."
	}

	return "Please revise the implementation plan, update the existing implementation-plan artifact, and address this feedback:\n\n" + feedback
}

// planReviewGateResult describes the outcome of handlePlanReviewGate.
type planReviewGateResult struct {
	Decision string // "approved", "revised", "cancelled", or "" (no gate triggered)
	Feedback string // non-empty for "revised"
}

// handlePlanReviewGate emits artifact_review_requested after a successful plan
// query, waits for the TUI response, and emits artifact_review_resolved.
// On "approved" it auto-switches the engine to fast mode.
func handlePlanReviewGate(
	ctx context.Context,
	bridge *ipc.Bridge,
	router *ipc.MessageRouter,
	mode *agent.ExecutionMode,
	artifactManager *artifactspkg.Manager,
	sessionID string,
	messages []api.Message,
	fromIndex int,
	stopReason string,
) (planReviewGateResult, error) {
	if *mode != agent.ModePlan {
		return planReviewGateResult{}, nil
	}
	if !turnUsedToolName(messages, fromIndex, "save_implementation_plan") && stopReason != "plan_review_required" {
		return planReviewGateResult{}, nil
	}

	artifact, found, err := artifactManager.FindSessionArtifact(ctx,
		artifactspkg.KindImplementationPlan, artifactspkg.ScopeSession, sessionID, "active")
	if err != nil || !found {
		return planReviewGateResult{}, err
	}
	if artifactMetadataString(artifact, "status") != "final" {
		return planReviewGateResult{}, nil
	}

	requestID := fmt.Sprintf("review-%d", time.Now().UnixNano())
	if err := bridge.Emit(ipc.EventArtifactReviewRequested, ipc.ArtifactReviewRequestedPayload{
		RequestID: requestID,
		ID:        artifact.ID,
		Kind:      string(artifact.Kind),
		Title:     artifact.Title,
		Version:   artifact.Version,
	}); err != nil {
		return planReviewGateResult{}, err
	}

	deferred := make([]ipc.ClientMessage, 0, 4)
	defer func() {
		router.Requeue(deferred...)
	}()

	for {
		msg, err := router.Next(ctx)
		if err != nil {
			return planReviewGateResult{}, err
		}

		switch msg.Type {
		case ipc.MsgArtifactReviewResponse:
			var payload ipc.ArtifactReviewResponsePayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return planReviewGateResult{}, fmt.Errorf("decode artifact review response: %w", err)
			}
			if payload.RequestID != requestID {
				deferred = append(deferred, msg)
				continue
			}

			decision := strings.TrimSpace(payload.Decision)
			feedback := strings.TrimSpace(payload.Feedback)

			resolvedDecision := "cancelled"
			switch decision {
			case "approve":
				resolvedDecision = "approved"
			case "revise":
				resolvedDecision = "revised"
			}

			if err := bridge.Emit(ipc.EventArtifactReviewResolved, ipc.ArtifactReviewResolvedPayload{
				RequestID: requestID,
				Decision:  resolvedDecision,
			}); err != nil {
				return planReviewGateResult{}, err
			}

			if resolvedDecision == "approved" {
				*mode = agent.ModeFast
				if err := bridge.Emit(ipc.EventModeChanged, ipc.ModeChangedPayload{Mode: string(agent.ModeFast)}); err != nil {
					return planReviewGateResult{}, err
				}
			}

			return planReviewGateResult{Decision: resolvedDecision, Feedback: feedback}, nil

		case ipc.MsgShutdown:
			return planReviewGateResult{}, context.Canceled
		default:
			deferred = append(deferred, msg)
		}
	}
}

// turnUsedToolName returns true if any assistant tool call in messages[fromIndex:]
// has the given tool name.
func turnUsedToolName(messages []api.Message, fromIndex int, toolName string) bool {
	for _, msg := range messages[fromIndex:] {
		if msg.Role != api.RoleAssistant {
			continue
		}
		for _, call := range msg.ToolCalls {
			if call.Name == toolName {
				return true
			}
		}
	}
	return false
}
