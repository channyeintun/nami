package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/channyeintun/gocode/internal/agent"
	"github.com/channyeintun/gocode/internal/api"
	artifactspkg "github.com/channyeintun/gocode/internal/artifacts"
	"github.com/channyeintun/gocode/internal/compact"
	"github.com/channyeintun/gocode/internal/config"
	costpkg "github.com/channyeintun/gocode/internal/cost"
	"github.com/channyeintun/gocode/internal/debuglog"
	"github.com/channyeintun/gocode/internal/hooks"
	"github.com/channyeintun/gocode/internal/ipc"
	"github.com/channyeintun/gocode/internal/localmodel"
	"github.com/channyeintun/gocode/internal/session"
	skillspkg "github.com/channyeintun/gocode/internal/skills"
	"github.com/channyeintun/gocode/internal/timing"
	toolpkg "github.com/channyeintun/gocode/internal/tools"
)

func runStdioEngine(ctx context.Context, cfg config.Config) error {
	engineStartedAt := time.Now()

	// Debug logging: activated by GOCODE_DEBUG=1
	if os.Getenv("GOCODE_DEBUG") != "" {
		debuglog.Enabled = true
	}

	var stdinR io.Reader = os.Stdin
	var stdoutW io.Writer = os.Stdout
	if debuglog.Enabled {
		stdinR = debuglog.NewIPCReader(os.Stdin)
		stdoutW = debuglog.NewIPCWriter(os.Stdout)
	}

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
	if debuglog.Enabled && client != nil {
		client = newDebugClientProxy(client)
	}
	modelState := newActiveModelState(client, activeModelID)
	messages := make([]api.Message, 0, 32)
	mode := parseExecutionMode(cfg.DefaultMode)
	permissionCtx := newPermissionContext(cfg.PermissionMode)
	tracker := costpkg.NewTracker()
	hookRunner := hooks.NewRunner(hooks.DefaultHooksDir())
	sessionStore := session.NewStore(session.DefaultBaseDir())
	artifactStore := artifactspkg.NewLocalStore(filepath.Join(filepath.Dir(session.DefaultBaseDir()), "artifacts"))
	artifactManager := artifactspkg.NewManager(artifactStore)
	sessionTitleGenerated := false
	sessionID, err := newSessionID()
	if err != nil {
		return err
	}
	timingLogger := timing.NewSessionLogger(sessionStore.SessionDir(sessionID))

	// Init debug logging now that we have a session directory.
	if debuglog.Enabled {
		debuglog.Init(sessionStore.SessionDir(sessionID))
		defer debuglog.Close()
		debuglog.LogGoroutineCount()
	}

	startupMetrics := timing.NewCheckpointRecorder(engineStartedAt)
	fileHistory := toolpkg.NewFileHistory(toolpkg.DefaultFileHistoryDir(sessionStore.SessionDir(sessionID)))
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
	registry.Register(toolpkg.NewAgentTool(makeSubagentRunner(bridge, registry, permissionCtx, tracker, sessionStore, artifactManager, hookRunner, modelState, cwd)))
	registry.Register(toolpkg.NewAgentStatusTool(lookupBackgroundAgentStatus))
	registry.Register(toolpkg.NewAgentStopTool(func(ctx context.Context, req toolpkg.AgentStopRequest) (toolpkg.AgentRunResult, error) {
		return stopBackgroundAgent(ctx, bridge, req)
	}))
	if err := persistSessionState(sessionStore, sessionStateParams{
		SessionID: sessionID,
		CreatedAt: startedAt,
		Mode:      mode,
		Model:     activeModelID,
		CWD:       cwd,
		Branch:    agent.LoadTurnContext().GitBranch,
		Tracker:   tracker,
		Messages:  messages,
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
	queryIndex := 0

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
			if strings.TrimSpace(payload.Text) == "" && len(payload.Images) == 0 {
				continue
			}
			messageCountBefore := len(messages)
			queryIndex++
			turnID := queryIndex
			turnMetrics := timing.NewCheckpointRecorder(time.Now())
			turnStats := &turnExecutionStats{}
			turnStopReason := ""
			flushTurnMetrics := func(outcome string) {
				if turnMetrics == nil {
					return
				}
				_ = timingLogger.AppendSnapshot("turn", "query_latency", sessionID, turnID, turnMetrics, map[string]any{
					"aggregate_tool_budget_chars": turnStats.AggregateBudgetChars,
					"aggregate_budget_spills":     turnStats.AggregateBudgetSpills,
					"continuation_budget_tokens":  turnStats.ContinuationBudgetTokens,
					"continuation_count":          turnStats.ContinuationCount,
					"continuation_stop_reason":    turnStats.ContinuationStopReason,
					"continuation_used_tokens":    turnStats.ContinuationUsedTokens,
					"image_count":                 len(payload.Images),
					"message_count_after":         len(messages),
					"message_count_before":        messageCountBefore,
					"mode":                        string(mode),
					"model":                       activeModelID,
					"outcome":                     outcome,
					"stop_reason":                 turnStopReason,
					"tool_inline_chars":           turnStats.ToolInlineChars,
					"tool_result_count":           turnStats.ToolResultCount,
					"tool_spill_count":            turnStats.ToolSpillCount,
					"user_input_characters":       len(payload.Text),
				})
				turnMetrics = nil
			}

			resolvedClient, nextModelID, err := ensureClientForSelection(activeModelID, cfg, client)
			if err != nil {
				turnMetrics.Mark("client_initialization_failed")
				flushTurnMetrics("client_initialization_failed")
				if emitErr := bridge.EmitError(fmt.Sprintf("initialize model %q: %v", activeModelID, err), true); emitErr != nil {
					return emitErr
				}
				continue
			}
			if resolvedClient != client {
				client = resolvedClient
				if debuglog.Enabled {
					client = newDebugClientProxy(client)
				}
			}
			activeModelID = nextModelID
			modelState.Set(client, activeModelID)
			if err := emitToolUseCapabilityNotice(bridge, activeModelID, client, &toolUseNoticeModelID); err != nil {
				return err
			}

			if len(payload.Images) > 0 && !client.Capabilities().SupportsVision {
				turnMetrics.Mark("vision_unsupported")
				flushTurnMetrics("vision_unsupported")
				if err := bridge.EmitError(fmt.Sprintf("model %q does not support image input", activeModelID), true); err != nil {
					return err
				}
				continue
			}

			images := make([]api.ImageAttachment, 0, len(payload.Images))
			for _, image := range payload.Images {
				images = append(images, api.ImageAttachment{
					ID:         image.ID,
					Data:       image.Data,
					MediaType:  image.MediaType,
					Filename:   image.Filename,
					SourcePath: image.SourcePath,
				})
			}

			messages = append(messages, api.Message{
				Role:    api.RoleUser,
				Content: payload.Text,
				Images:  images,
			})
			if err := emitContextWindowUsage(bridge, client, messages); err != nil {
				return err
			}
			availableSkills, _ := skillspkg.LoadAll(cwd)
			plannerUserRequest := payload.Text
			persistCurrentMessages := func() {
				_ = persistSessionState(sessionStore, sessionStateParams{
					SessionID: sessionID,
					CreatedAt: startedAt,
					Mode:      mode,
					Model:     activeModelID,
					CWD:       cwd,
					Branch:    agent.LoadTurnContext().GitBranch,
					Tracker:   tracker,
					Messages:  messages,
				})
			}

			for {
				messagesBeforeQuery := len(messages)
				planner := agent.NewPlanner(mode, sessionID, artifactManager)
				if updates, beginErr := planner.BeginTurn(ctx, plannerUserRequest); beginErr != nil {
					if emitErr := bridge.EmitError(fmt.Sprintf("create session artifact: %v", beginErr), true); emitErr != nil {
						return emitErr
					}
				} else {
					for _, update := range updates {
						if update.Created {
							if err := emitArtifactCreated(bridge, update.Artifact); err != nil {
								return err
							}
						}
						if err := emitArtifactUpdated(bridge, update.Artifact, update.Content); err != nil {
							return err
						}
					}
				}

				deps := agent.QueryDeps{
					CallModel: func(callCtx context.Context, req api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
						return trackModelStream(callCtx, bridge, tracker, client, req)
					},
					ExecuteToolBatch: func(callCtx context.Context, calls []api.ToolCall) ([]api.ToolResult, error) {
						return executeToolCalls(callCtx, bridge, router, registry, permissionCtx, tracker, planner, artifactManager, hookRunner, sessionID, client.Capabilities().MaxOutputTokens, turnMetrics, turnStats, calls)
					},
					CompactMessages: func(callCtx context.Context, current []api.Message, reason agent.CompactReason) ([]api.Message, error) {
						result, err := compactWithMetrics(callCtx, bridge, tracker, client, timingLogger, sessionID, turnID, string(reason), current)
						if err != nil {
							return nil, err
						}
						return result.Messages, nil
					},
					RecallMemory: func(callCtx context.Context, files []agent.MemoryFile, userPrompt string) ([]agent.MemoryRecallResult, error) {
						selector := memoryRecallSelector{bridge: bridge, tracker: tracker, client: client}
						return selector.Select(callCtx, files, userPrompt)
					},
					BeforeStop: func(callCtx context.Context, stopReq agent.StopRequest) (agent.StopDecision, error) {
						return evaluateSessionStopHooks(callCtx, hookRunner, sessionID, stopReq)
					},
					ApplyResultBudget: func(current []api.Message) []api.Message {
						return current
					},
					ObserveContinuation: func(tracker agent.ContinuationTracker, reason string) {
						turnStats.ContinuationBudgetTokens = tracker.MaxBudgetTokens
						turnStats.ContinuationCount = tracker.ContinuationCount
						turnStats.ContinuationStopReason = reason
						turnStats.ContinuationUsedTokens = tracker.BudgetUsedTokens
					},
					EmitTelemetry: bridge.EmitEvent,
					PersistMessages: func(updated []api.Message) {
						messages = updated
						persistCurrentMessages()
						_ = emitContextWindowUsage(bridge, client, messages)
					},
					Clock: time.Now,
				}

				queryCtx, queryCancel := context.WithCancel(ctx)
				stopControl := agent.NewStopController()
				deps.StopController = stopControl
				router.SetCancelFunc(func() {
					stopControl.Request("cancelled")
					queryCancel()
				})

				stream := agent.QueryStream(queryCtx, agent.QueryRequest{
					Messages:        messages,
					SystemPrompt:    systemPromptForMode(mode),
					ModelID:         client.ModelID(),
					ReasoningEffort: config.Load().ReasoningEffort,
					Mode:            mode,
					SessionID:       sessionID,
					Skills:          availableSkills,
					Tools:           registry.Definitions(),
					Capabilities:    client.Capabilities(),
					ContextWindow:   client.Capabilities().MaxContextWindow,
					MaxTokens:       client.Capabilities().MaxOutputTokens,
				}, deps)

				queryFailed := false
				queryCancelled := false
				for event, streamErr := range stream {
					if streamErr != nil {
						runSessionStopFailureHooks(queryCtx, hookRunner, sessionID, turnStopReason, messages, streamErr)
						if queryCtx.Err() != nil {
							queryCancelled = true
							break
						}
						queryFailed = true
						if emitErr := bridge.EmitError(streamErr.Error(), false); emitErr != nil {
							return emitErr
						}
						break
					}
					switch event.Type {
					case ipc.EventTokenDelta:
						if turnMetrics.Mark("first_token") {
							if err := emitTurnTimingCheckpoint(bridge, turnMetrics, "first_token"); err != nil {
								return err
							}
						}
					case ipc.EventTurnComplete:
						if turnMetrics.Mark("turn_complete") {
							if err := emitTurnTimingCheckpoint(bridge, turnMetrics, "turn_complete"); err != nil {
								return err
							}
						}
						var payload ipc.TurnCompletePayload
						if err := json.Unmarshal(event.Payload, &payload); err == nil {
							turnStopReason = payload.StopReason
						}
					}
					if err := bridge.EmitEvent(event); err != nil {
						return err
					}
				}

				queryCancel()
				router.SetCancelFunc(nil)

				if queryCancelled || turnStopReason == "cancelled" {
					if turnMetrics.Mark("cancelled") {
						if err := emitTurnTimingCheckpoint(bridge, turnMetrics, "cancelled"); err != nil {
							return err
						}
					}
					if turnStopReason == "" {
						turnStopReason = "cancelled"
						if err := bridge.Emit(ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: "cancelled"}); err != nil {
							return err
						}
					}
					flushTurnMetrics("cancelled")
					break
				}

				if queryFailed {
					turnMetrics.Mark("failed")
					flushTurnMetrics("failed")
					break
				}

				if updates, finalizeErr := planner.FinalizeTurn(ctx, "", plannerUserRequest, messages, messagesBeforeQuery); finalizeErr != nil {
					if emitErr := bridge.EmitError(fmt.Sprintf("update session artifact: %v", finalizeErr), true); emitErr != nil {
						return emitErr
					}
				} else {
					for _, update := range updates {
						if update.Created {
							if err := emitArtifactCreated(bridge, update.Artifact); err != nil {
								return err
							}
						}
						if err := emitArtifactUpdated(bridge, update.Artifact, update.Content); err != nil {
							return err
						}
						if update.Artifact.Kind == artifactspkg.KindImplementationPlan && strings.TrimSpace(update.Content) != "" {
							if err := emitArtifactFocusedForTurn(bridge, update.Artifact, turnMetrics); err != nil {
								return err
							}
						}
					}
				}

				// Plan review gate: after a successful plan-mode query that saved a final
				// implementation plan, pause for explicit user review before execution.
				if mode == agent.ModePlan {
					reviewResult, reviewErr := handlePlanReviewGate(ctx, bridge, router, &mode, artifactManager, sessionID, messages, messagesBeforeQuery)
					if reviewErr != nil && reviewErr != context.Canceled {
						if emitErr := bridge.EmitError(fmt.Sprintf("plan review gate: %v", reviewErr), true); emitErr != nil {
							return emitErr
						}
					}
					if reviewResult.Decision == "approved" {
						// Mode already switched to fast inside handlePlanReviewGate; persist it.
						// Inject a user message so the model knows to begin implementation, then
						// continue the inner loop to run an immediate fast-mode execution turn.
						turnMetrics.Mark("plan_approved")
						turnStopReason = "plan_approved"
						flushTurnMetrics("plan_approved")
						messages = append(messages, api.Message{
							Role:    api.RoleUser,
							Content: "Plan approved. Implement it now.",
						})
						persistCurrentMessages()
						if err := emitContextWindowUsage(bridge, client, messages); err != nil {
							return err
						}
						continue
					}
					if reviewResult.Decision == "revised" {
						turnMetrics.Mark("plan_review_revised")
						turnStopReason = "plan_revised"
						flushTurnMetrics("plan_review_revised")
						messages = append(messages, api.Message{
							Role:    api.RoleUser,
							Content: planRevisionFeedbackMessage(reviewResult.Feedback),
						})
						persistCurrentMessages()
						if err := emitContextWindowUsage(bridge, client, messages); err != nil {
							return err
						}
						continue
					}
					if reviewResult.Decision == "cancelled" {
						turnMetrics.Mark("plan_review_cancelled")
						turnStopReason = "plan_cancelled"
						flushTurnMetrics("plan_review_cancelled")
						break
					}
				}

				// Generate session title after the first successful query
				if !sessionTitleGenerated && len(messages) > 0 {
					sessionTitleGenerated = true
					titleClient := client
					titleSessionID := sessionID
					titleStartedAt := startedAt
					titleMode := mode
					titleModelID := activeModelID
					titleCWD := cwd
					titleBranch := agent.LoadTurnContext().GitBranch
					titleMessages := api.DeepCopyMessages(messages)
					go func() {
						modelRouter := localmodel.NewRouter(titleClient)
						title := session.GenerateTitle(modelRouter, titleMessages)
						if title != "" {
							_ = sessionStore.SaveMetadata(session.Metadata{
								SessionID:    titleSessionID,
								CreatedAt:    titleStartedAt,
								UpdatedAt:    time.Now(),
								Mode:         string(titleMode),
								Model:        titleModelID,
								CWD:          titleCWD,
								Branch:       titleBranch,
								TotalCostUSD: tracker.Snapshot().TotalCostUSD,
								Title:        title,
							})
							_ = emitSessionUpdated(bridge, titleSessionID, title)
						}
					}()
				}

				turnMetrics.Mark("completed")
				if turnStopReason == "" {
					turnStopReason = "completed"
				}
				flushTurnMetrics("completed")

				break
			}
		case ipc.MsgSlashCommand:
			var payload ipc.SlashCommandPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return fmt.Errorf("decode slash command: %w", err)
			}

			slashState, err := handleSlashCommand(
				ctx,
				bridge,
				sessionStore,
				timingLogger,
				cfg,
				artifactManager,
				tracker,
				payload,
				sessionID,
				startedAt,
				mode,
				activeModelID,
				cwd,
				messages,
				&client,
			)
			if err != nil {
				return err
			}
			sessionID = slashState.SessionID
			startedAt = slashState.StartedAt
			mode = slashState.Mode
			activeModelID = slashState.ActiveModelID
			cwd = slashState.CWD
			messages = slashState.Messages
			modelState.Set(client, activeModelID)
			toolpkg.SetGlobalSessionArtifacts(sessionID, artifactManager)
			continue
		case ipc.MsgModeToggle:
			if mode == agent.ModePlan {
				mode = agent.ModeFast
			} else {
				mode = agent.ModePlan
			}
			if err := persistSessionState(sessionStore, sessionStateParams{
				SessionID: sessionID,
				CreatedAt: startedAt,
				Mode:      mode,
				Model:     activeModelID,
				CWD:       cwd,
				Branch:    agent.LoadTurnContext().GitBranch,
				Tracker:   tracker,
				Messages:  messages,
			}); err != nil {
				return err
			}
			if err := bridge.Emit(ipc.EventModeChanged, ipc.ModeChangedPayload{Mode: string(mode)}); err != nil {
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
	if provider == "github-copilot" {
		resolved, err := resolveGitHubCopilotConfig(cfg)
		if err != nil {
			return nil, err
		}
		cfg = resolved
	}

	baseURL := cfg.BaseURL
	apiKey := cfg.APIKey
	var capabilitiesOverride *api.ModelCapabilities
	if provider == "github-copilot" {
		if capabilities, ok := resolveGitHubCopilotCapabilities(cfg, model); ok {
			capabilitiesOverride = &capabilities
		}
	}

	var client api.LLMClient
	var err error
	if provider == "github-copilot" {
		switch {
		case api.GitHubCopilotUsesAnthropicMessages(model):
			client, err = api.NewAnthropicClientForProvider(provider, model, apiKey, baseURL)
		case api.GitHubCopilotUsesOpenAIResponses(model):
			client, err = api.NewOpenAIResponsesClient(provider, model, apiKey, baseURL)
		}
		if err != nil {
			return nil, err
		}
		if client != nil {
			refresher := newCopilotTokenRefresher(cfg.GitHubCopilot)
			if capabilitiesOverride != nil {
				client = api.WithCapabilities(client, *capabilitiesOverride)
			}
			api.SetAPIKeyFunc(client, refresher.resolve)
			return client, nil
		}
	}

	client, err = api.NewClientForProvider(provider, model, apiKey, baseURL)
	if err != nil {
		return nil, err
	}
	if capabilitiesOverride != nil {
		client = api.WithCapabilities(client, *capabilitiesOverride)
	}
	return client, nil
}

func resolveGitHubCopilotConfig(cfg config.Config) (config.Config, error) {
	loaded := config.Load()
	if strings.TrimSpace(loaded.GitHubCopilot.GitHubToken) != "" {
		cfg.GitHubCopilot = loaded.GitHubCopilot
	}

	if strings.TrimSpace(cfg.APIKey) == "" {
		creds := cfg.GitHubCopilot
		if strings.TrimSpace(creds.GitHubToken) == "" {
			return cfg, &api.APIError{Type: api.ErrAuth, Message: "GitHub Copilot is not connected. Run /connect first."}
		}

		expiresAt := time.UnixMilli(creds.ExpiresAtUnixMS)
		if strings.TrimSpace(creds.AccessToken) == "" || time.Now().After(expiresAt) {
			refreshCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()

			refreshed, err := api.RefreshGitHubCopilotToken(refreshCtx, creds.GitHubToken, creds.EnterpriseDomain)
			if err != nil {
				return cfg, err
			}

			creds.AccessToken = refreshed.AccessToken
			creds.ExpiresAtUnixMS = refreshed.ExpiresAt.UnixMilli()
			cfg.GitHubCopilot = creds

			loaded.GitHubCopilot = creds
			if err := config.Save(loaded); err != nil {
				return cfg, fmt.Errorf("save refreshed GitHub Copilot credentials: %w", err)
			}
		}

		cfg.APIKey = creds.AccessToken
	}

	if strings.TrimSpace(cfg.BaseURL) == "" {
		cfg.BaseURL = api.GetGitHubCopilotBaseURL(cfg.GitHubCopilot.AccessToken, cfg.GitHubCopilot.EnterpriseDomain)
	}

	return cfg, nil
}

func resolveGitHubCopilotCapabilities(cfg config.Config, model string) (api.ModelCapabilities, bool) {
	accessToken := strings.TrimSpace(cfg.GitHubCopilot.AccessToken)
	if accessToken == "" {
		return api.ModelCapabilities{}, false
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	capabilities, ok, err := api.ResolveGitHubCopilotModelCapabilities(ctx, accessToken, cfg.GitHubCopilot.EnterpriseDomain, model)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to fetch GitHub Copilot model metadata for %q: %v\n", model, err)
		return api.ModelCapabilities{}, false
	}
	return capabilities, ok
}

// copilotTokenRefresher caches a GitHub Copilot access token and refreshes it
// only when expired. The fast path is a single timestamp comparison.
type copilotTokenRefresher struct {
	mu               sync.Mutex
	githubToken      string
	enterpriseDomain string
	accessToken      string
	expiresAt        time.Time
}

func newCopilotTokenRefresher(creds config.GitHubCopilotAuth) *copilotTokenRefresher {
	return &copilotTokenRefresher{
		githubToken:      creds.GitHubToken,
		enterpriseDomain: creds.EnterpriseDomain,
		accessToken:      creds.AccessToken,
		expiresAt:        time.UnixMilli(creds.ExpiresAtUnixMS),
	}
}

func (r *copilotTokenRefresher) resolve() (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.accessToken != "" && time.Now().Before(r.expiresAt) {
		return r.accessToken, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	refreshed, err := api.RefreshGitHubCopilotToken(ctx, r.githubToken, r.enterpriseDomain)
	if err != nil {
		return "", fmt.Errorf("refresh GitHub Copilot token: %w", err)
	}

	r.accessToken = refreshed.AccessToken
	r.expiresAt = refreshed.ExpiresAt

	// Persist to config so other processes and restarts pick up the fresh token.
	loaded := config.Load()
	loaded.GitHubCopilot.AccessToken = refreshed.AccessToken
	loaded.GitHubCopilot.ExpiresAtUnixMS = refreshed.ExpiresAt.UnixMilli()
	_ = config.Save(loaded)

	return r.accessToken, nil
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
	return provider + "/" + model
}

func resolveModelSelection(input string, fallbackProvider string) (string, string) {
	provider, model := config.ParseModel(strings.TrimSpace(input))
	if model == "" && provider != "" {
		model = provider
		provider = ""
	}
	if provider != "" {
		return normalizeProvider(provider), model
	}
	if normalizeProvider(fallbackProvider) == "github-copilot" {
		return "github-copilot", model
	}

	lower := strings.ToLower(model)
	switch {
	case strings.Contains(lower, "gemini"):
		provider = "gemini"
	case strings.Contains(lower, "gpt"), strings.HasPrefix(lower, "o1"), strings.HasPrefix(lower, "o3"), strings.HasPrefix(lower, "o4"):
		provider = "openai"
	case strings.Contains(lower, "deepseek"):
		provider = "deepseek"
	case strings.Contains(lower, "qwen"):
		provider = "qwen"
	case strings.Contains(lower, "glm"):
		provider = "glm"
	case strings.Contains(lower, "mistral"):
		provider = "mistral"
	case strings.Contains(lower, "llama"), strings.Contains(lower, "maverick"):
		provider = "groq"
	case strings.Contains(lower, "gemma"), strings.Contains(lower, "ollama"):
		provider = "ollama"
	case strings.Contains(lower, "claude"), strings.Contains(lower, "sonnet"), strings.Contains(lower, "opus"), strings.Contains(lower, "haiku"):
		provider = "anthropic"
	default:
		provider = normalizeProvider(fallbackProvider)
	}

	return provider, model
}

func parseExecutionMode(mode string) agent.ExecutionMode {
	if strings.EqualFold(mode, string(agent.ModeFast)) {
		return agent.ModeFast
	}
	return agent.ModePlan
}

func defaultSystemPrompt() string {
	return strings.TrimSpace(`You are Go CLI, a pragmatic coding assistant. Be concise, prefer inspecting files before changing them, and use tools when needed.

IMPORTANT: Always use absolute paths with file tools. The working directory is provided in the environment context below — use it to construct absolute paths. For example, if the working directory is /home/user/project, use /home/user/project/file.txt instead of file.txt.
Always use tools to answer questions — do NOT just make a plan without acting. Call tools immediately when you need information.
For simple, self-contained implementation requests, do not browse the web or ask routine clarifying questions. Make the obvious file changes directly with local file tools.
Use the exact runtime tool names when calling tools, including agent, agent_status, agent_stop, bash, think, list_dir, create_file, file_read, file_write, file_edit, apply_patch, multi_replace_file_content, file_diff_preview, glob, grep, go_definition, go_references, project_overview, dependency_overview, symbol_search, web_search, web_fetch, git, list_commands, command_status, send_command_input, stop_command, forget_command, file_history, file_history_rewind, save_implementation_plan, upsert_task_list, and save_walkthrough. Do not invent alternate names like file_search or read_file.
For bounded delegated work, prefer agent with subagent_type=search for code discovery and file/line references, subagent_type=execution for terminal-heavy tasks, subagent_type=explore for broad read-only research, and subagent_type=general-purpose only when the task does not fit a specialized mode.

Use the file-edit ladder deliberately:
- file_edit: one exact snippet replacement in one existing file.
- multi_replace_file_content: several exact, non-overlapping replacements in one existing file when you know the current line ranges and target text.
- apply_patch: multi-file, multi-hunk, create/delete, or broader structural edits.
- file_write: full overwrite of one existing file only.
- create_file: create a brand-new file only.

For complex, multi-step tasks, follow a structured workflow:
1. Research: Use read tools (file_read, glob, grep, bash with read-only commands) to understand the codebase and gather context before making changes.
2. Plan: For non-trivial implementation work, save an implementation plan with save_implementation_plan. The user can review, request revisions, or approve it before you proceed.
3. Track: Use upsert_task_list to break work into concrete checklist items. Mark items in-progress when starting and completed when done — keep the list current as a living document.
4. Implement: Work through the task list deliberately. If unexpected complexity arises, pause and revise the plan before continuing.
5. Verify: After implementation, run builds and tests. Save a walkthrough with save_walkthrough summarizing what changed and how it was validated.
For simple tasks (single-file edits, quick questions, small fixes), skip straight to implementation — do not create unnecessary artifacts.

Artifacts are first-class outputs in this runtime — durable, reviewable work products, not just overflow containers for long text. Use them intentionally:
- save_implementation_plan: real implementation plans that the user will review before execution begins.
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
		return prompt + "\n\n" + strings.TrimSpace(`When plan mode is active, use read tools to explore before any writes. For implementation tasks, produce a concrete markdown implementation plan and save it with save_implementation_plan — this makes the plan the explicit reviewable artifact for the task. The system will surface a review gate to the user after you save a final plan; they can approve it (which switches to fast mode for you), request revisions, or cancel. If the user sends revision feedback, update the same plan artifact in place rather than creating a new one.

For research, explanation, review, or other non-implementation requests, answer directly and do not create a plan artifact. When you produce a real implementation plan, prefer this structure: Goal Description, Proposed Changes (grouped by component with [NEW]/[MODIFY]/[DELETE] markers), User Review Required, Open Questions, and Verification Plan. Use > [!CAUTION] or > [!WARNING] alert blocks for risky or irreversible changes that need explicit attention before approval.`) + " " + agent.PlanModePromptHint()
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
) (planReviewGateResult, error) {
	if *mode != agent.ModePlan {
		return planReviewGateResult{}, nil
	}
	if !turnUsedToolName(messages, fromIndex, "save_implementation_plan") {
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
