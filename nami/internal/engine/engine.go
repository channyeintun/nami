package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	artifactspkg "github.com/channyeintun/nami/internal/artifacts"
	"github.com/channyeintun/nami/internal/clientdebug"
	commandspkg "github.com/channyeintun/nami/internal/commands"
	"github.com/channyeintun/nami/internal/compact"
	"github.com/channyeintun/nami/internal/config"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/debuglog"
	"github.com/channyeintun/nami/internal/hooks"
	"github.com/channyeintun/nami/internal/ipc"
	mcppkg "github.com/channyeintun/nami/internal/mcp"
	"github.com/channyeintun/nami/internal/session"
	skillspkg "github.com/channyeintun/nami/internal/skills"
	"github.com/channyeintun/nami/internal/timing"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

func RunStdioEngine(ctx context.Context, cfg config.Config) error {
	engineStartedAt := time.Now()

	// Debug logging: activated by NAMI_DEBUG=1
	if os.Getenv("NAMI_DEBUG") != "" {
		debuglog.Enabled = true
	}

	var stdinR io.Reader = debuglog.NewIPCReader(os.Stdin)
	var stdoutW io.Writer = debuglog.NewIPCWriter(os.Stdout)

	bridge := ipc.NewBridge(stdinR, stdoutW)
	registry := toolpkg.NewRegistry()
	startupSelection := resolveStartupProviderSelection(cfg)
	provider := normalizeProvider(startupSelection.Provider)
	model := strings.TrimSpace(startupSelection.Model)
	var (
		client          api.LLMClient
		startupModelErr error
	)
	startupNotice := strings.TrimSpace(startupSelection.Notice)
	toolUseNoticeModelID := ""
	activeModelID := modelRef(provider, model)
	client, err := newLLMClient(provider, model, cfg)
	if err != nil {
		startupModelErr = err
	} else {
		activeModelID = modelRef(provider, client.ModelID())
		rememberSuccessfulModelSelection(activeModelID)
	}
	client = clientdebug.WrapClient(client)
	modelState := NewActiveModelState(client, activeModelID)
	subagentModelID := defaultSessionSubagentModel(cfg, activeModelID)
	subagentModelState := NewActiveSubagentModelState(subagentModelID)
	messages := make([]api.Message, 0, 32)
	mode := parseExecutionMode(cfg.DefaultMode)
	permissionCtx := newPermissionContext(cfg.PermissionMode, cfg.AutoMode)
	tracker := costpkg.NewTracker()
	hooksDir := strings.TrimSpace(cfg.HooksDir)
	if hooksDir == "" {
		hooksDir = hooks.DefaultHooksDir()
	}
	hookRunner := hooks.NewRunner(hooksDir)
	sessionStore := session.NewStore(session.DefaultBaseDir())
	artifactStore := artifactspkg.NewLocalStore(config.ArtifactsDir())
	artifactManager := artifactspkg.NewManager(artifactStore)
	sessionID := newSessionID()
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
	fileReadState := toolpkg.NewFileReadState()
	if client != nil {
		startClientWarmup(ctx, timingLogger, startupMetrics, sessionID, activeModelID, client)
	}
	startedAt := time.Now()
	cwd, err := os.Getwd()
	if err != nil {
		return err
	}
	sessionRuntime := toolpkg.SessionRuntimeConfig{
		FileHistory:   fileHistory,
		FileReadState: fileReadState,
		SessionArtifacts: toolpkg.SessionArtifactRuntimeConfig{
			SessionID: sessionID,
			Manager:   artifactManager,
		},
		Swarm: toolpkg.SwarmRuntimeConfig{
			SessionID: sessionID,
			Manager:   artifactManager,
			Store:     sessionStore,
			CWD:       cwd,
		},
	}
	toolpkg.InstallSessionRuntime(sessionRuntime)
	defer toolpkg.ClearSessionRuntime()
	interactionRuntime := toolpkg.InteractionRuntimeConfig{
		BackgroundCommandNotifier: func(update toolpkg.BackgroundCommandUpdate) {
			_ = bridge.Emit(ipc.EventBackgroundCommandUpdated, ipc.BackgroundCommandUpdatedPayload{
				CommandID:       update.CommandID,
				Command:         update.Command,
				Cwd:             update.Cwd,
				Status:          update.Status,
				Running:         update.Running,
				StartedAt:       update.StartedAt,
				UpdatedAt:       update.UpdatedAt,
				OutputPreview:   update.OutputPreview,
				HasUnreadOutput: update.HasUnreadOutput,
				UnreadBytes:     update.UnreadBytes,
				ExitCode:        update.ExitCode,
				Error:           update.Error,
			})
		},
	}
	defer toolpkg.ClearInteractionRuntime()
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
		timeline:        newConversationTimeline(),
		titleGenerated:  false,
	}
	mcpManager := mcppkg.NewManager(cwd, cfg.MCP)
	interactionRuntime.SessionControlRuntime = newSessionControlRuntime(bridge, sessionStore, tracker, loopState)
	interactionRuntime.ToolSearchRuntime = registry
	toolpkg.InstallInteractionRuntime(interactionRuntime)
	defer func() {
		if err := mcpManager.Close(); err != nil && debuglog.Enabled {
			fmt.Fprintf(os.Stderr, "mcp: close: %v\n", err)
		}
	}()
	registry.Register(toolpkg.NewAgentTool(makeSubagentRunner(bridge, registry, permissionCtx, tracker, sessionStore, artifactManager, hookRunner, modelState, subagentModelState, loopState, cwd)))
	registry.Register(toolpkg.NewAgentStatusTool(lookupBackgroundAgentStatus))
	registry.Register(toolpkg.NewAgentStopTool(func(ctx context.Context, req toolpkg.AgentStopRequest) (toolpkg.AgentRunResult, error) {
		return stopBackgroundAgent(ctx, bridge, req)
	}))
	registry.Register(toolpkg.NewAgentTeamTool(func(ctx context.Context, req toolpkg.AgentTeamLaunchRequest) (toolpkg.AgentTeamLaunchResult, error) {
		runner := makeSubagentRunner(bridge, registry, permissionCtx, tracker, sessionStore, artifactManager, hookRunner, modelState, subagentModelState, loopState, cwd)
		return launchBackgroundTeam(ctx, runner, req)
	}))
	registry.Register(toolpkg.NewAgentTeamStatusTool(lookupBackgroundTeamStatus))
	registry.Register(toolpkg.NewWorkflowTool(func(ctx context.Context, req toolpkg.WorkflowLaunchRequest) (toolpkg.WorkflowRunResult, error) {
		runner := makeSubagentRunner(bridge, registry, permissionCtx, tracker, sessionStore, artifactManager, hookRunner, modelState, subagentModelState, loopState, cwd)
		return launchWorkflow(ctx, runner, bridge, sessionStore.SessionDir(loopState.sessionID), req)
	}))
	registry.Register(toolpkg.NewWorkflowStatusTool(lookupWorkflowStatus))
	registry.Register(toolpkg.NewListMCPResourcesToolWithManager(mcpManager))
	registry.Register(toolpkg.NewReadMCPResourceToolWithManager(mcpManager))
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
	if err := persistConversationHydratedPayload(sessionStore, sessionID, loopState.timeline, messages, activeModelID); err != nil {
		return err
	}
	startupMetrics.Mark("session_persisted")

	// Emit ready event
	slashDescriptors, slashDescriptorErr := slashCommandDescriptors(cwd)
	if err := bridge.EmitReady(slashDescriptors); err != nil {
		return fmt.Errorf("emit ready: %w", err)
	}
	startupMetrics.Mark("ready_emitted")
	_ = timingLogger.AppendSnapshot("session", "boot_to_ready", sessionID, 0, startupMetrics, map[string]any{
		"cwd":   cwd,
		"mode":  string(mode),
		"model": activeModelID,
	})
	if slashDescriptorErr != nil {
		if err := bridge.EmitNotice(fmt.Sprintf("load slash skills: %v", slashDescriptorErr)); err != nil {
			return err
		}
	}
	startMCPDiscovery(ctx, bridge, registry, mcpManager)

	// Start the message router as soon as the UI is ready so any immediate user
	// input is buffered while startup capability refresh completes.
	router := ipc.NewMessageRouter(ctx, bridge)
	interactionRuntime.AskUserQuestionRuntime = newAskUserQuestionRuntime(bridge, router)
	toolpkg.InstallInteractionRuntime(interactionRuntime)
	defer toolpkg.ShutdownBackgroundCommandsForSession()

	if startupNotice != "" {
		if err := bridge.EmitNotice(startupNotice); err != nil {
			return err
		}
	}
	if err := emitSessionUpdated(bridge, sessionID, ""); err != nil {
		return err
	}
	if err := maybeEmitSwarmSpecStartup(ctx, bridge, artifactManager, sessionID, cwd); err != nil {
		return err
	}
	if client != nil {
		refreshedClient, refreshErr := refreshStartupModelCapabilities(ctx, activeModelID, client, cfg)
		if refreshErr == nil {
			client = refreshedClient
			loopState.client = refreshedClient
			modelState.Set(refreshedClient, activeModelID)
		}
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

	// Fire session_start hooks (best-effort)
	_, _ = hookRunner.Run(ctx, hooks.Payload{
		Type:      hooks.HookSessionStart,
		SessionID: sessionID,
	})
	loopState.toolUseNoticeID = toolUseNoticeModelID
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

			slashState, handled, err := handleSlashCommand(
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
				loopState.timeline,
				registry.Definitions(),
				&loopState.client,
			)
			if err != nil {
				return err
			}
			if handled {
				loopState.sessionID = slashState.SessionID
				loopState.sessionDir = sessionStore.SessionDir(slashState.SessionID)
				loopState.startedAt = slashState.StartedAt
				loopState.mode = slashState.Mode
				loopState.activeModelID = slashState.ActiveModelID
				loopState.subagentModelID = slashState.SubagentModelID
				loopState.cwd = slashState.CWD
				loopState.messages = slashState.Messages
				loopState.timeline = slashState.Timeline
				modelState.Set(loopState.client, loopState.activeModelID)
				subagentModelState.Set(loopState.subagentModelID)
				sessionRuntime.SessionArtifacts.SessionID = loopState.sessionID
				sessionRuntime.Swarm.SessionID = loopState.sessionID
				sessionRuntime.Swarm.CWD = loopState.cwd
				toolpkg.InstallSessionRuntime(sessionRuntime)
				continue
			}

			skill, ok, skillErr := lookupSlashSkill(loopState.cwd, payload.Command)
			if skillErr != nil {
				if err := bridge.EmitNotice(fmt.Sprintf("load skills: %v", skillErr)); err != nil {
					return err
				}
			}
			if ok {
				promptText := "/" + strings.TrimSpace(payload.Command)
				if args := strings.TrimSpace(payload.Args); args != "" {
					promptText += " " + args
				}
				if err := handleUserInputMessageWithSkills(ctx, ipc.UserInputPayload{Text: promptText}, loopDeps, loopState, []skillspkg.Skill{skill}); err != nil {
					return err
				}
				continue
			}

			if err := bridge.EmitError(fmt.Sprintf("unknown slash command: %s", payload.Command), true); err != nil {
				return err
			}
			if err := bridge.Emit(ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: "end_turn"}); err != nil {
				return err
			}
			continue
		case ipc.MsgBackgroundCommandInspect:
			var payload ipc.BackgroundCommandInspectPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return fmt.Errorf("decode background command inspect: %w", err)
			}
			if err := handleBackgroundCommandInspectMessage(ctx, bridge, payload); err != nil {
				return err
			}
			continue
		case ipc.MsgBackgroundCommandStop:
			var payload ipc.BackgroundCommandStopPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return fmt.Errorf("decode background command stop: %w", err)
			}
			if err := handleBackgroundCommandStopMessage(bridge, payload); err != nil {
				return err
			}
			continue
		case ipc.MsgBackgroundAgentInspect:
			var payload ipc.BackgroundAgentInspectPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return fmt.Errorf("decode background agent inspect: %w", err)
			}
			if err := handleBackgroundAgentInspectMessage(ctx, bridge, payload); err != nil {
				return err
			}
			continue
		case ipc.MsgBackgroundAgentStop:
			var payload ipc.BackgroundAgentStopPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return fmt.Errorf("decode background agent stop: %w", err)
			}
			if err := handleBackgroundAgentStopMessage(ctx, bridge, payload); err != nil {
				return err
			}
			continue
		case ipc.MsgSwarmDashboardInspect:
			if err := handleSwarmDashboardInspectMessage(ctx, bridge, sessionStore, loopState.sessionID); err != nil {
				return err
			}
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
			if err := persistConversationHydratedPayload(sessionStore, loopState.sessionID, loopState.timeline, loopState.messages, loopState.activeModelID); err != nil {
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
	client, err := providerBehaviorFor(provider).NewClient(provider, model, cfg)
	if api.IsNilLLMClient(client) {
		return nil, err
	}
	return client, err
}

const clientWarmupTimeout = 3 * time.Second
const startupCapabilityRefreshTimeout = 5 * time.Second

func startClientWarmup(ctx context.Context, logger *timing.Logger, startupMetrics *timing.CheckpointRecorder, sessionID, activeModelID string, client api.LLMClient) {
	if api.IsNilLLMClient(client) {
		return
	}
	warmable, ok := client.(api.WarmupCapable)
	if !ok || api.IsNilValue(warmable) {
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

func refreshStartupModelCapabilities(ctx context.Context, activeModelID string, client api.LLMClient, cfg config.Config) (api.LLMClient, error) {
	if api.IsNilLLMClient(client) {
		return client, nil
	}

	provider, model := config.ParseModel(strings.TrimSpace(activeModelID))
	if normalizeProvider(provider) != "github-copilot" || strings.TrimSpace(model) == "" {
		return client, nil
	}

	resolved, err := resolveGitHubCopilotConfig(cfg)
	if err != nil {
		return client, nil
	}

	refresher := newCopilotTokenRefresher(resolved.GitHubCopilot)
	api.SetGitHubCopilotEnterpriseDomain(client, resolved.GitHubCopilot.EnterpriseDomain)
	api.SetAPIKeyFunc(client, refresher.resolve)

	refreshCtx, cancel := context.WithTimeout(ctx, startupCapabilityRefreshTimeout)
	defer cancel()

	apiKey, err := refresher.resolve()
	if err != nil {
		return client, err
	}

	capabilities, ok, err := api.ResolveGitHubCopilotModelCapabilities(
		refreshCtx,
		apiKey,
		resolved.GitHubCopilot.EnterpriseDomain,
		model,
	)
	if err != nil {
		if !ok {
			return client, err
		}
	}
	if !ok {
		return client, nil
	}

	updatedClient := api.WithCapabilities(client, capabilities)
	api.SetGitHubCopilotEnterpriseDomain(updatedClient, resolved.GitHubCopilot.EnterpriseDomain)
	api.SetAPIKeyFunc(updatedClient, refresher.resolve)
	return updatedClient, nil
}

func ensureClientForSelection(modelSelection string, cfg config.Config, current api.LLMClient) (api.LLMClient, string, error) {
	if current != nil {
		return current, modelSelection, nil
	}

	provider, _ := config.ParseModel(strings.TrimSpace(modelSelection))
	provider, model := resolveModelSelection(modelSelection, provider)
	client, err := newLLMClient(provider, model, cfg)
	if err != nil {
		return nil, modelRef(provider, model), err
	}
	return client, modelRef(provider, client.ModelID()), nil
}

func startMCPDiscovery(ctx context.Context, bridge *ipc.Bridge, registry *toolpkg.Registry, manager *mcppkg.Manager) {
	if manager == nil {
		return
	}
	statuses := manager.Statuses()
	enabledCount := 0
	for _, status := range statuses {
		if status.Enabled {
			enabledCount++
		}
	}
	if enabledCount == 0 {
		return
	}

	_ = bridge.EmitNotice(fmt.Sprintf("MCP discovery started for %d server(s).", enabledCount))
	go func() {
		manager.StartWithCallback(ctx, func(status mcppkg.ServerStatus) {
			if status.Connected {
				registered := registerMCPToolsForServer(registry, manager, status.Name)
				_ = bridge.EmitNotice(fmt.Sprintf("MCP server %q connected with %d tool(s).", status.Name, registered))
				return
			}
			if strings.TrimSpace(status.Error) != "" {
				_ = bridge.EmitNotice(fmt.Sprintf("MCP server %q failed: %s", status.Name, status.Error))
			}
		})
	}()
}

func registerMCPToolsForServer(registry *toolpkg.Registry, manager *mcppkg.Manager, serverName string) int {
	registered := 0
	for _, discovered := range manager.Tools() {
		if discovered.ServerName != serverName {
			continue
		}
		registry.Register(toolpkg.NewMCPTool(manager, discovered))
		registered++
	}
	return registered
}

func normalizeProvider(provider string) string {
	if strings.TrimSpace(provider) == "" {
		return "anthropic"
	}
	return provider
}

func modelRef(provider, model string) string {
	return config.NewModelSelection(normalizeProvider(provider), model, "", provider != "").Ref()
}

func resolveModelSelection(input string, fallbackProvider string) (string, string) {
	return providerBehaviorFor(fallbackProvider).ResolveSelection(input, fallbackProvider)
}

func parseExecutionMode(mode string) agent.ExecutionMode {
	switch strings.ToLower(strings.TrimSpace(mode)) {
	case string(agent.ModePlan):
		return agent.ModePlan
	default:
		return agent.ModeFast
	}
}

func defaultSystemPrompt() string {
	return strings.TrimSpace(`You are Nami CLI, a pragmatic coding agent. You share a workspace with the user, and your job is to handle their request end to end with a senior engineer's judgment. Be concise, direct, and action-oriented: if the request is clear, start using tools immediately instead of front-loading plans or recaps.

# Reading the request

Match your behavior to the kind of request the user actually made:
- Answer, explain, review, or report status: inspect the relevant code and give an evidence-backed response. These requests do not authorize file edits or other mutations; reversible read-only checks are fine.
- Diagnose: find the cause and explain it. Do not implement the fix unless the user asks for one or the request clearly includes it.
- Change or build: implement the change, verify it in proportion to risk, and report the outcome. Do not stop at analysis, a proposal, or a half-finished fix.
- Monitor or wait: use background commands and task notifications. Unchanged external state is expected and is not by itself a blocker.

Words like "finish" or "don't stop" call for persistence toward the outcome, not broader authority to act. Ask one short clarifying question only when missing information blocks a safe change; otherwise make a reasonable assumption, state it briefly, and keep moving.

# Engineering judgment

- Gather context before acting. Read the relevant code first, make no assumptions about it, and keep iterating tool calls until the task is complete or provably impossible. You are responsible for collecting sufficient context.
- Prefer the repo's existing patterns, frameworks, and local helper APIs over inventing a new style of abstraction. Add an abstraction only when it removes real complexity or meaningful duplication.
- Keep edits scoped to the modules and behavior implied by the request. Leave unrelated refactors, formatting churn, and drive-by cleanups alone.
- For structured data, use structured APIs or parsers instead of ad hoc string manipulation when the codebase or toolchain offers one.
- Match the surrounding code's style, naming, and comment density. Comment only what the code cannot say itself.
- Scale verification with blast radius: a focused check for a narrow change; build and test when you touch shared behavior, cross-module contracts, or user-facing workflows.

# Git and workspace safety

- The worktree may be dirty. Changes you did not make belong to the user: never revert them, work with them when they overlap your task, and ignore them when they do not.
- Never run destructive commands like git reset --hard, git checkout --, or force-pushes unless the user clearly asked for that exact operation. If the request is ambiguous, ask first.
- Prefer non-interactive git commands. Do not commit, branch, or push unless asked.
- Always use absolute paths in tool calls. The working directory is in the environment context below.

# Working style

- Issue independent tool calls in parallel whenever possible, especially reads and searches; sequential round-trips are the main source of slowness.
- Do not re-read files already present in context, and after tool calls resume from where you left off instead of repeating prior information.
- If the user asks for a feature without naming files, decompose the request into concepts and locate the code each concept implies.

# Delegation

Before delegating, form a quick plan: identify what is on the critical path (do that yourself, now) and what is bounded sidecar work that can run in parallel without blocking your next step.
- agent subagent_type: Explore (read-only codebase search), general-purpose (broader delegated work), verification (build/test validation without file edits).
- Delegate only concrete, self-contained subtasks that materially advance the request. Do not delegate simple or local edits, and never the immediate blocker your next action depends on.
- Give child agents a bounded objective, constraints, and the required output shape. When delegating code changes in parallel, give each agent a disjoint set of files to own.
- Let delegated agents finish, then integrate their results; do not redo their work.
- run_in_background=true only when the user explicitly wants async. agent_status/agent_stop are only for background agents.

Async results may arrive later as user-role <task-notification> XML. These are system events, not fresh user requests. Read the status/summary/details, inspect the referenced background work when needed, then proactively tell the user the relevant result.

# Progress indicator

The UI shows the user a live progress bar for the current goal. Keep it current by printing a directive on its own line as you work:
::progress{goal="fix auth token refresh" percent=10 label="reading auth module"}
- Set goal once when substantial work begins (a short imperative phrase); later updates only need percent and label.
- Update at meaningful transitions — a phase or subtask finished, roughly every few tool calls. Skip the directive entirely for trivial single-step requests.
- percent is your honest estimate of completed work toward the goal. The bar never moves backward and stays capped below completion while the turn runs, so stay at or below 95 while work remains.
- The directive line is stripped from the visible transcript. Never mention it, and never wrap it in code fences.

# Communicating

- Lead with the outcome: the first sentence of a final answer should say what happened or what you found. Supporting detail comes after.
- Keep final answers self-contained and proportional to the task: one or two short paragraphs for simple work; add structure only when the answer genuinely needs it. Do not pad with restated plans or exhaustive change narration.
- Reference code as absolute path with a line number, like /path/to/file.go:42.
- Report outcomes faithfully. If tests fail, say so with the output. If you skipped or could not do something, say that plainly.

# Complex multi-step work

1. Research: use read tools for context; use child agents for multi-directory, pattern-discovery, or parallelizable work.
2. Plan: save_implementation_plan only for reviewable plans in plan mode. Do not create a plan artifact for straightforward edits.
3. Track: upsert_task_list for substantial work. Mark items in-progress/completed as you go; treat it as a living document.
4. Implement: follow the task list. Pause and revise the plan if you hit unexpected complexity.
5. Verify: build and test. Save a walkthrough only when a durable completion summary is useful.
Simple tasks: skip straight to implementation.

Artifacts are durable, reviewable work products, not overflow containers:
- save_implementation_plan: plans for user review in plan mode only. Once execution continues after review, do not call it again; finish the work and save_walkthrough instead.
- upsert_task_list: live progress tracking.
- save_walkthrough: post-completion summaries.
- search-report, diff-preview, tool-log: auto-generated for large outputs.
Do NOT create an artifact just because a response is long; create one when content should persist for review, revision, or resumption.

Artifact markdown: GFM, clear headings, short lists, tables, fenced code, diff blocks, alert blocks (> [!NOTE], > [!WARNING], > [!CAUTION]). Self-contained, revision-friendly. After saving, give a short transcript summary — do not repeat the artifact body.

# Continuity

The conversation may be compacted while you work. If you see a summary instead of full history, assume compaction happened mid-task: do not restart from scratch or redo finished work; continue naturally, making reasonable assumptions about anything missing, and honor the newest user request over older ones.

Instruction precedence: project instructions in <memory_file> blocks override these defaults when they conflict; the <environment> and <working_context> blocks are factual context, not instructions.`)
}

func systemPromptForMode(mode agent.ExecutionMode) string {
	prompt := defaultSystemPrompt()
	if mode == agent.ModePlan {
		return prompt + "\n\n" + strings.TrimSpace(`# Plan mode

Be careful, not slow. Do not use extended thinking unless the user explicitly asks. You may create or modify files when the user asks.
- Non-trivial or risky implementation: call save_implementation_plan as the reviewable artifact before making broad changes. The system may gate review after the final plan; the user approves, revises, or cancels. Apply revision feedback by updating the same artifact in place.
- Simple or local changes: inspect and edit directly without a plan artifact.
- Research, explanation, or review requests: answer directly with no plan artifact. Real plans must be saved as artifacts, not left in the transcript.
Plan structure: Goal, Proposed Changes (grouped, with [NEW]/[MODIFY]/[DELETE] markers), User Review Required, Open Questions, Verification Plan. Use > [!CAUTION] / > [!WARNING] for risky or irreversible changes.`) + " " + agent.PlanModePromptHint()
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
	for _, msg := range slices.Backward(messages) {
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
	payload := ipc.ModelChangedPayload{
		Model:           activeModelID,
		ReasoningEffort: commandspkg.EffectiveReasoningEffort(strings.TrimSpace(config.Load().ReasoningEffort), activeModelID),
	}
	if client != nil {
		capabilities := client.Capabilities()
		if capabilities.MaxContextWindow > 0 {
			payload.MaxContextWindow = capabilities.MaxContextWindow
		} else {
			payload.MaxContextWindow = capabilities.PromptTokenBudget()
		}
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
