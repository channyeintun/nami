package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/channyeintun/gocode/internal/agent"
	"github.com/channyeintun/gocode/internal/api"
	artifactspkg "github.com/channyeintun/gocode/internal/artifacts"
	"github.com/channyeintun/gocode/internal/compact"
	"github.com/channyeintun/gocode/internal/config"
	costpkg "github.com/channyeintun/gocode/internal/cost"
	"github.com/channyeintun/gocode/internal/hooks"
	"github.com/channyeintun/gocode/internal/ipc"
	"github.com/channyeintun/gocode/internal/localmodel"
	"github.com/channyeintun/gocode/internal/permissions"
	"github.com/channyeintun/gocode/internal/session"
	skillspkg "github.com/channyeintun/gocode/internal/skills"
	toolpkg "github.com/channyeintun/gocode/internal/tools"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "gocode",
		Short:   "An agentic coding CLI powered by Go",
		Version: fmt.Sprintf("%s (%s)", version, commit),
	}

	// Flags
	var (
		flagModel string
		flagMode  string
		flagStdio bool
	)
	rootCmd.PersistentFlags().StringVar(&flagModel, "model", "", "Model to use (provider/model format, e.g. anthropic/claude-sonnet-4-20250514)")
	rootCmd.PersistentFlags().StringVar(&flagMode, "mode", "", "Execution mode: plan or fast")
	rootCmd.PersistentFlags().BoolVar(&flagStdio, "stdio", false, "Run in stdio mode (NDJSON engine only, no TUI)")

	// Run command (default)
	runCmd := &cobra.Command{
		Use:   "run",
		Short: "Start the agent (default command)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runEngine(flagModel, flagMode, flagStdio)
		},
	}
	rootCmd.AddCommand(runCmd)

	// Make "run" the default command
	rootCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return runEngine(flagModel, flagMode, flagStdio)
	}

	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func runEngine(modelFlag, modeFlag string, stdioMode bool) error {
	cfg := config.Load()

	// CLI flag overrides
	if modelFlag != "" {
		cfg.Model = modelFlag
	}
	if modeFlag != "" {
		cfg.DefaultMode = modeFlag
	}

	// Setup context with signal handling
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if stdioMode {
		return runStdioEngine(ctx, cfg)
	}

	return launchTUI(ctx, cfg)
}

func launchTUI(ctx context.Context, cfg config.Config) error {
	nodePath, err := exec.LookPath("node")
	if err != nil {
		return fmt.Errorf("node is required for TUI mode: %w", err)
	}

	enginePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve engine executable: %w", err)
	}
	if resolvedPath, resolveErr := filepath.EvalSymlinks(enginePath); resolveErr == nil {
		enginePath = resolvedPath
	}

	tuiEntry, err := resolveTUIEntry()
	if err != nil {
		return err
	}

	cmd := exec.CommandContext(ctx, nodePath, tuiEntry)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Env = append(os.Environ(),
		"GOCODE_ENGINE_PATH="+enginePath,
		"GOCODE_MODEL="+cfg.Model,
		"GOCODE_MODE="+cfg.DefaultMode,
		"GOCODE_COST_WARNING_THRESHOLD_USD="+strconv.FormatFloat(cfg.CostWarningThresholdUSD, 'f', -1, 64),
	)

	if err := cmd.Run(); err != nil {
		return fmt.Errorf("run ink tui: %w", err)
	}
	return nil
}

func resolveTUIEntry() (string, error) {
	if override := strings.TrimSpace(os.Getenv("GOCODE_TUI_ENTRY")); override != "" {
		if _, err := os.Stat(override); err != nil {
			return "", fmt.Errorf("stat GOCODE_TUI_ENTRY: %w", err)
		}
		return override, nil
	}

	_, sourceFile, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("resolve TUI entry: runtime caller unavailable")
	}

	moduleRoot := filepath.Clean(filepath.Join(filepath.Dir(sourceFile), "..", ".."))
	tuiEntry := filepath.Join(moduleRoot, "tui", "dist", "index.js")
	if _, err := os.Stat(tuiEntry); err != nil {
		return "", fmt.Errorf("TUI bundle not found at %s: %w", tuiEntry, err)
	}
	return tuiEntry, nil
}

func runStdioEngine(ctx context.Context, cfg config.Config) error {
	bridge := ipc.NewBridge(os.Stdin, os.Stdout)
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
	fileHistory := toolpkg.NewFileHistory(toolpkg.DefaultFileHistoryDir(sessionStore.SessionDir(sessionID)))
	toolpkg.SetGlobalFileHistory(fileHistory)
	toolpkg.SetGlobalSessionArtifacts(sessionID, artifactManager)
	startedAt := time.Now()
	cwd, err := os.Getwd()
	if err != nil {
		return err
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

	// Emit ready event
	if err := bridge.EmitReady(); err != nil {
		return fmt.Errorf("emit ready: %w", err)
	}
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

	// Fire session_start hooks (best-effort)
	_, _ = hookRunner.Run(ctx, hooks.Payload{
		Type:      hooks.HookSessionStart,
		SessionID: sessionID,
	})

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

			resolvedClient, nextModelID, err := ensureClientForSelection(activeModelID, cfg, client)
			if err != nil {
				if emitErr := bridge.EmitError(fmt.Sprintf("initialize model %q: %v", activeModelID, err), true); emitErr != nil {
					return emitErr
				}
				continue
			}
			client = resolvedClient
			activeModelID = nextModelID
			if err := emitToolUseCapabilityNotice(bridge, activeModelID, client, &toolUseNoticeModelID); err != nil {
				return err
			}

			if len(payload.Images) > 0 && !client.Capabilities().SupportsVision {
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
			messagesBeforeQuery := len(messages)
			planner := agent.NewPlanner(mode, sessionID, artifactManager)
			if updates, beginErr := planner.BeginTurn(ctx, payload.Text); beginErr != nil {
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
					return executeToolCalls(callCtx, bridge, router, registry, permissionCtx, tracker, planner, artifactManager, hookRunner, sessionID, client.Capabilities().MaxOutputTokens, calls)
				},
				CompactMessages: func(callCtx context.Context, current []api.Message, reason agent.CompactReason) ([]api.Message, error) {
					pipeline := newCompactionPipeline(bridge, tracker, client)
					result, err := pipeline.Compact(callCtx, current, string(reason))
					if err != nil {
						return nil, err
					}
					return result.Messages, nil
				},
				ApplyResultBudget: func(current []api.Message) []api.Message {
					return current
				},
				PersistMessages: func(updated []api.Message) {
					messages = updated
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
					_ = emitContextWindowUsage(bridge, client, messages)
				},
				Clock: time.Now,
			}

			queryCtx, queryCancel := context.WithCancel(ctx)
			router.SetCancelFunc(queryCancel)

			stream := agent.QueryStream(queryCtx, agent.QueryRequest{
				Messages:      messages,
				SystemPrompt:  systemPromptForMode(mode),
				Mode:          mode,
				SessionID:     sessionID,
				Skills:        availableSkills,
				Tools:         registry.Definitions(),
				Capabilities:  client.Capabilities(),
				ContextWindow: client.Capabilities().MaxContextWindow,
				MaxTokens:     client.Capabilities().MaxOutputTokens,
			}, deps)

			queryFailed := false
			queryCancelled := false
			for event, streamErr := range stream {
				if streamErr != nil {
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
				if err := bridge.EmitEvent(event); err != nil {
					return err
				}
			}

			queryCancel()
			router.SetCancelFunc(nil)

			if queryCancelled {
				if err := bridge.Emit(ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: "cancelled"}); err != nil {
					return err
				}
			}

			if !queryFailed && !queryCancelled {
				if updates, finalizeErr := planner.FinalizeTurn(ctx, "", payload.Text, messages, messagesBeforeQuery); finalizeErr != nil {
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
					titleMessages := append([]api.Message(nil), messages...)
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
			}
		case ipc.MsgSlashCommand:
			var payload ipc.SlashCommandPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return fmt.Errorf("decode slash command: %w", err)
			}

			var handled bool
			handled, sessionID, startedAt, mode, activeModelID, cwd, messages, err = handleSlashCommand(
				ctx,
				bridge,
				sessionStore,
				cfg,
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
			if handled {
				toolpkg.SetGlobalSessionArtifacts(sessionID, artifactManager)
				continue
			}
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

	baseURL := cfg.BaseURL
	switch api.Presets[provider].ClientType {
	case api.AnthropicAPI:
		return api.NewAnthropicClient(model, cfg.APIKey, baseURL)
	case api.GeminiAPI:
		return api.NewGeminiClient(model, cfg.APIKey, baseURL)
	case api.OpenAICompatAPI:
		return api.NewOpenAICompatClient(provider, model, cfg.APIKey, baseURL)
	case api.OllamaAPI:
		return api.NewOllamaClient(model, cfg.APIKey, baseURL)
	default:
		return nil, fmt.Errorf("unsupported provider %q", provider)
	}
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
	return "You are Go CLI, a pragmatic coding assistant. Be concise, prefer inspecting files before changing them, and use tools when needed.\n\nIMPORTANT: Always use absolute paths with file tools. The working directory is provided in the environment context below — use it to construct absolute paths. For example, if the working directory is /home/user/project, use /home/user/project/file.txt instead of file.txt.\nAlways use tools to answer questions — do NOT just make a plan without acting. Call tools immediately when you need information.\nUse the exact runtime tool names when calling tools, including bash, list_dir, file_read, file_write, file_edit, multi_replace_file_content, glob, grep, web_search, web_fetch, git, command_status, send_command_input, save_implementation_plan, upsert_task_list, and save_walkthrough. Do not invent alternate names like file_search or read_file.\n\nArtifacts: the runtime can surface markdown artifacts in a dedicated panel. Implementation plans, task lists, and walkthroughs should be created intentionally with save_implementation_plan, upsert_task_list, and save_walkthrough. Oversized tool outputs may still be saved automatically as tool-log artifacts. Write artifact content in clean GitHub-flavored markdown so it renders well in that panel. Use upsert_task_list for live multi-step progress, use save_implementation_plan only for a real implementation plan, and use save_walkthrough after a meaningful implementation slice so the user can catch up quickly."
}

func systemPromptForMode(mode agent.ExecutionMode) string {
	prompt := defaultSystemPrompt()
	if mode == agent.ModePlan {
		return prompt + "\n\nWhen plan mode is active, use read tools to explore before any writes. For implementation tasks, respond with a concrete markdown implementation plan before proposing file mutations, then save that plan with save_implementation_plan so it appears in the plan panel. For research, explanation, review, or other non-implementation requests, answer directly and do not create a plan artifact. Keep real implementation plans actionable and review-friendly. When you produce a real implementation plan, prefer this structure when it fits the request: Goal Description, User Review Required, Proposed Changes, Open Questions, and Verification Plan. In Proposed Changes, group work by component or file and use [NEW], [MODIFY], and [DELETE] markers where they add clarity. Use GitHub alert blocks for critical review items or risky changes. " + agent.PlanModePromptHint()
	}
	return prompt
}

func executeToolCalls(
	ctx context.Context,
	bridge *ipc.Bridge,
	router *ipc.MessageRouter,
	registry *toolpkg.Registry,
	permissionCtx *permissions.Context,
	tracker *costpkg.Tracker,
	planner *agent.Planner,
	artifactManager *artifactspkg.Manager,
	hookRunner *hooks.Runner,
	sessionID string,
	maxOutputTokens int,
	calls []api.ToolCall,
) ([]api.ToolResult, error) {
	results := make([]api.ToolResult, len(calls))
	approved := make([]toolpkg.PendingCall, 0, len(calls))
	approvalFeedback := make(map[int]string, len(calls))
	budget := toolpkg.DefaultResultBudgetForModel(filepath.Join(os.TempDir(), "gocode-session"), maxOutputTokens)

	for index, call := range calls {
		call, err := normalizeToolCall(call)
		if err != nil {
			results[index] = api.ToolResult{ToolCallID: calls[index].ID, Output: err.Error(), IsError: true}
			if emitErr := bridge.Emit(ipc.EventToolError, ipc.ToolErrorPayload{ToolID: calls[index].ID, Name: calls[index].Name, Input: calls[index].Input, Error: err.Error()}); emitErr != nil {
				return nil, emitErr
			}
			continue
		}

		tool, err := registry.Get(call.Name)
		if err != nil {
			results[index] = api.ToolResult{ToolCallID: call.ID, Output: err.Error(), IsError: true}
			if emitErr := bridge.Emit(ipc.EventToolError, ipc.ToolErrorPayload{ToolID: call.ID, Name: call.Name, Input: call.Input, Error: err.Error()}); emitErr != nil {
				return nil, emitErr
			}
			continue
		}

		input, err := decodeToolInput(call)
		if err != nil {
			results[index] = api.ToolResult{ToolCallID: call.ID, Output: err.Error(), IsError: true}
			if emitErr := bridge.Emit(ipc.EventToolError, ipc.ToolErrorPayload{ToolID: call.ID, Name: call.Name, Input: call.Input, Error: err.Error()}); emitErr != nil {
				return nil, emitErr
			}
			continue
		}

		if err := bridge.Emit(ipc.EventToolStart, ipc.ToolStartPayload{
			ToolID: call.ID,
			Name:   call.Name,
			Input:  call.Input,
		}); err != nil {
			return nil, err
		}

		pendingCall := toolpkg.PendingCall{Index: index, Tool: tool, Input: input}
		if err := planner.ValidateTool(ctx, pendingCall.Tool.Name(), pendingCall.Tool.Permission()); err != nil {
			results[index] = api.ToolResult{ToolCallID: call.ID, Output: err.Error(), IsError: true}
			if emitErr := bridge.Emit(ipc.EventToolError, ipc.ToolErrorPayload{ToolID: call.ID, Name: call.Name, Input: call.Input, Error: err.Error()}); emitErr != nil {
				return nil, emitErr
			}
			continue
		}
		authorization, err := authorizeToolCall(ctx, bridge, router, permissionCtx, call.ID, pendingCall)
		if err != nil {
			return nil, err
		}
		if !authorization.Allowed {
			results[index] = api.ToolResult{ToolCallID: call.ID, Output: authorization.DenyReason, IsError: true}
			if emitErr := bridge.Emit(ipc.EventToolError, ipc.ToolErrorPayload{ToolID: call.ID, Name: call.Name, Input: call.Input, Error: authorization.DenyReason}); emitErr != nil {
				return nil, emitErr
			}
			continue
		}
		if authorization.Feedback != "" {
			approvalFeedback[index] = authorization.Feedback
		}

		// Fire pre_tool_use hook
		hookDenied := false
		if hookRunner != nil {
			responses, _ := hookRunner.Run(ctx, hooks.Payload{
				Type:      hooks.HookPreToolUse,
				SessionID: sessionID,
				ToolName:  call.Name,
				ToolInput: call.Input,
			})
			for _, resp := range responses {
				if resp.Action == "deny" {
					reason := resp.Message
					if reason == "" {
						reason = "blocked by pre_tool_use hook"
					}
					reason = appendPermissionFeedback(reason, approvalFeedback[index])
					results[index] = api.ToolResult{ToolCallID: call.ID, Output: reason, IsError: true}
					_ = bridge.Emit(ipc.EventToolError, ipc.ToolErrorPayload{ToolID: call.ID, Name: call.Name, Input: call.Input, Error: reason})
					hookDenied = true
					break
				}
			}
		}
		if hookDenied {
			continue
		}

		approved = append(approved, pendingCall)
	}

	for _, batch := range toolpkg.PartitionBatches(approved) {
		batchStart := time.Now()
		batchResults := toolpkg.ExecuteBatch(ctx, batch)
		tracker.RecordToolDuration(time.Since(batchStart))
		for _, result := range batchResults {
			call := calls[result.Index]
			toolResult := api.ToolResult{ToolCallID: call.ID}
			feedback := approvalFeedback[result.Index]

			if result.Err != nil {
				toolResult.Output = appendPermissionFeedback(result.Err.Error(), feedback)
				toolResult.IsError = true
				if err := bridge.Emit(ipc.EventToolError, ipc.ToolErrorPayload{ToolID: call.ID, Name: call.Name, Input: call.Input, Error: toolResult.Output}); err != nil {
					return nil, err
				}
				results[result.Index] = toolResult
				continue
			}

			output := result.Output.Output
			spillPath := result.Output.SpillPath
			truncated := result.Output.Truncated
			if !result.Output.IsError {
				budgetedOutput, artifact, err := budgetToolOutput(ctx, artifactManager, sessionID, budget, call, output)
				output = budgetedOutput
				if err != nil {
					if emitErr := bridge.EmitError(fmt.Sprintf("persist tool-log artifact: %v", err), true); emitErr != nil {
						return nil, emitErr
					}
				}
				if artifact.ID != "" {
					spillPath = artifact.ContentPath
					truncated = true
					if err := emitArtifactCreated(bridge, artifact); err != nil {
						return nil, err
					}
				}
			}
			output = appendPermissionFeedback(output, feedback)

			toolResult.Output = output
			toolResult.IsError = result.Output.IsError
			results[result.Index] = toolResult

			if result.Output.IsError {
				if err := bridge.Emit(ipc.EventToolError, ipc.ToolErrorPayload{ToolID: call.ID, Name: call.Name, Input: call.Input, Error: output}); err != nil {
					return nil, err
				}
				continue
			}

			for _, artifactUpdate := range result.Output.Artifacts {
				if artifactUpdate.Created {
					if err := emitArtifactCreated(bridge, artifactUpdate.Artifact); err != nil {
						return nil, err
					}
				}
				if err := emitArtifactUpdated(bridge, artifactUpdate.Artifact, artifactUpdate.Content); err != nil {
					return nil, err
				}
			}

			if err := bridge.Emit(ipc.EventToolResult, ipc.ToolResultPayload{
				ToolID:     call.ID,
				Output:     output,
				Truncated:  truncated || spillPath != "",
				Name:       call.Name,
				Input:      call.Input,
				FilePath:   result.Output.FilePath,
				Preview:    result.Output.Preview,
				Insertions: result.Output.Insertions,
				Deletions:  result.Output.Deletions,
			}); err != nil {
				return nil, err
			}

			// Fire post_tool_use hook
			if hookRunner != nil {
				_, _ = hookRunner.Run(ctx, hooks.Payload{
					Type:      hooks.HookPostToolUse,
					SessionID: sessionID,
					ToolName:  call.Name,
					ToolInput: call.Input,
					Output:    output,
				})
			}
		}
	}

	return results, nil
}

type authorizationResult struct {
	Allowed    bool
	DenyReason string
	Feedback   string
}

type permissionResponse struct {
	Decision string
	Feedback string
}

func newPermissionContext(mode string) *permissions.Context {
	ctx := permissions.NewContext()
	switch permissions.Mode(mode) {
	case permissions.ModeBypassPermissions:
		ctx.Mode = permissions.ModeBypassPermissions
	case permissions.ModeAutoApprove:
		ctx.Mode = permissions.ModeAutoApprove
	default:
		ctx.Mode = permissions.ModeDefault
	}
	return ctx
}

func authorizeToolCall(
	ctx context.Context,
	bridge *ipc.Bridge,
	router *ipc.MessageRouter,
	permissionCtx *permissions.Context,
	toolCallID string,
	pending toolpkg.PendingCall,
) (authorizationResult, error) {
	decision := permissionCtx.Check(pending.Tool.Name(), pending.Input, pending.Tool.Permission())
	switch decision {
	case permissions.DecisionAllow:
		return authorizationResult{Allowed: true}, nil
	case permissions.DecisionDeny:
		return authorizationResult{DenyReason: toolPermissionMessage("denied", pending, "permission policy denied this tool call")}, nil
	case permissions.DecisionAsk:
		response, err := waitForPermissionDecision(ctx, bridge, router, toolCallID, pending)
		if err != nil {
			return authorizationResult{}, err
		}
		switch response.Decision {
		case "allow":
			return authorizationResult{Allowed: true, Feedback: response.Feedback}, nil
		case "always_allow":
			if raw := strings.TrimSpace(pending.Input.Raw); raw != "" {
				if err := permissionCtx.AddAlwaysAllow(pending.Tool.Name(), "^"+regexp.QuoteMeta(raw)+"$"); err != nil {
					return authorizationResult{}, err
				}
			}
			return authorizationResult{Allowed: true, Feedback: response.Feedback}, nil
		case "allow_all_session":
			permissionCtx.SessionAllowAll = true
			return authorizationResult{Allowed: true, Feedback: response.Feedback}, nil
		default:
			return authorizationResult{
				DenyReason: appendPermissionFeedback(
					toolPermissionMessage("denied", pending, "user denied permission request"),
					response.Feedback,
				),
			}, nil
		}
	default:
		return authorizationResult{DenyReason: toolPermissionMessage("denied", pending, "permission policy denied this tool call")}, nil
	}
}

func waitForPermissionDecision(
	ctx context.Context,
	bridge *ipc.Bridge,
	router *ipc.MessageRouter,
	toolCallID string,
	pending toolpkg.PendingCall,
) (permissionResponse, error) {
	requestID := fmt.Sprintf("perm-%d", time.Now().UnixNano())
	if err := bridge.Emit(ipc.EventPermissionRequest, ipc.PermissionRequestPayload{
		RequestID:       requestID,
		ToolID:          toolCallID,
		Tool:            pending.Tool.Name(),
		Command:         summarizePermissionTarget(pending),
		Risk:            permissionRisk(pending),
		PermissionLevel: permissionLevelLabel(pending),
		TargetKind:      permissionTargetKind(pending),
		TargetValue:     summarizePermissionTarget(pending),
		WorkingDir:      permissionWorkingDir(pending),
	}); err != nil {
		return permissionResponse{}, err
	}

	for {
		msg, err := router.Next(ctx)
		if err != nil {
			return permissionResponse{}, err
		}

		switch msg.Type {
		case ipc.MsgPermissionResponse:
			var payload ipc.PermissionResponsePayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return permissionResponse{}, fmt.Errorf("decode permission response: %w", err)
			}
			if payload.RequestID != requestID {
				continue
			}
			return permissionResponse{
				Decision: strings.TrimSpace(payload.Decision),
				Feedback: strings.TrimSpace(payload.Feedback),
			}, nil
		case ipc.MsgShutdown:
			return permissionResponse{}, context.Canceled
		default:
			continue
		}
	}
}

func permissionRisk(call toolpkg.PendingCall) string {
	if call.Tool.Name() == "bash" {
		command, _ := stringParamFromMap(call.Input.Params, "command")
		if warning := permissions.CheckDestructive(command); warning != "" {
			return "destructive"
		}
		return "execute"
	}

	switch call.Tool.Permission() {
	case toolpkg.PermissionWrite:
		return "write"
	case toolpkg.PermissionExecute:
		return "execute"
	default:
		return "read"
	}
}

func permissionLevelLabel(call toolpkg.PendingCall) string {
	switch call.Tool.Permission() {
	case toolpkg.PermissionWrite:
		return "write"
	case toolpkg.PermissionExecute:
		return "execute"
	default:
		return "read"
	}
}

func permissionTargetKind(call toolpkg.PendingCall) string {
	if command, ok := stringParamFromMap(call.Input.Params, "command"); ok && strings.TrimSpace(command) != "" {
		return "command"
	}
	if filePath, ok := stringParamFromMap(call.Input.Params, "file_path"); ok && strings.TrimSpace(filePath) != "" {
		return "file"
	}
	if url, ok := stringParamFromMap(call.Input.Params, "url"); ok && strings.TrimSpace(url) != "" {
		return "url"
	}
	if pattern, ok := stringParamFromMap(call.Input.Params, "pattern"); ok && strings.TrimSpace(pattern) != "" {
		return "pattern"
	}
	if query, ok := stringParamFromMap(call.Input.Params, "query"); ok && strings.TrimSpace(query) != "" {
		return "query"
	}
	return "target"
}

func permissionWorkingDir(call toolpkg.PendingCall) string {
	workingDir, ok := stringParamFromMap(call.Input.Params, "cwd")
	if !ok {
		return ""
	}
	return strings.TrimSpace(workingDir)
}

func summarizePermissionTarget(call toolpkg.PendingCall) string {
	if command, ok := stringParamFromMap(call.Input.Params, "command"); ok && strings.TrimSpace(command) != "" {
		return command
	}
	if filePath, ok := stringParamFromMap(call.Input.Params, "file_path"); ok && strings.TrimSpace(filePath) != "" {
		return filePath
	}
	if url, ok := stringParamFromMap(call.Input.Params, "url"); ok && strings.TrimSpace(url) != "" {
		return url
	}
	if pattern, ok := stringParamFromMap(call.Input.Params, "pattern"); ok && strings.TrimSpace(pattern) != "" {
		return pattern
	}
	if query, ok := stringParamFromMap(call.Input.Params, "query"); ok && strings.TrimSpace(query) != "" {
		return query
	}
	if raw := strings.TrimSpace(call.Input.Raw); raw != "" {
		return raw
	}
	return call.Tool.Name()
}

func toolPermissionMessage(action string, call toolpkg.PendingCall, reason string) string {
	if reason == "" {
		reason = "permission policy requires user approval"
	}
	return fmt.Sprintf("tool %q %s: %s", call.Tool.Name(), action, reason)
}

func appendPermissionFeedback(message, feedback string) string {
	trimmedFeedback := strings.TrimSpace(feedback)
	if trimmedFeedback == "" {
		return message
	}
	if strings.TrimSpace(message) == "" {
		return "User feedback: " + trimmedFeedback
	}
	return message + "\n\nUser feedback: " + trimmedFeedback
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
		TotalUSD:     snapshot.TotalCostUSD,
		InputTokens:  snapshot.TotalInputTokens,
		OutputTokens: snapshot.TotalOutputTokens,
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

func handleSlashCommand(
	ctx context.Context,
	bridge *ipc.Bridge,
	store *session.Store,
	cfg config.Config,
	tracker *costpkg.Tracker,
	payload ipc.SlashCommandPayload,
	sessionID string,
	startedAt time.Time,
	mode agent.ExecutionMode,
	activeModelID string,
	cwd string,
	messages []api.Message,
	client *api.LLMClient,
) (bool, string, time.Time, agent.ExecutionMode, string, string, []api.Message, error) {
	command := strings.ToLower(strings.TrimSpace(payload.Command))
	args := strings.TrimSpace(payload.Args)

	switch command {
	case "plan", "plan-mode":
		mode = agent.ModePlan
		if err := persistSessionState(store, sessionStateParams{
			SessionID: sessionID,
			CreatedAt: startedAt,
			Mode:      mode,
			Model:     activeModelID,
			CWD:       cwd,
			Branch:    agent.LoadTurnContext().GitBranch,
			Tracker:   tracker,
			Messages:  messages,
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := bridge.Emit(ipc.EventModeChanged, ipc.ModeChangedPayload{Mode: string(mode)}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "fast":
		mode = agent.ModeFast
		if err := persistSessionState(store, sessionStateParams{
			SessionID: sessionID,
			CreatedAt: startedAt,
			Mode:      mode,
			Model:     activeModelID,
			CWD:       cwd,
			Branch:    agent.LoadTurnContext().GitBranch,
			Tracker:   tracker,
			Messages:  messages,
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := bridge.Emit(ipc.EventModeChanged, ipc.ModeChangedPayload{Mode: string(mode)}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "model":
		if args == "" {
			if err := emitTextResponse(bridge, fmt.Sprintf("Current model: %s", activeModelID)); err != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
			}
			return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
		}

		selectedModel := args
		if strings.EqualFold(strings.TrimSpace(args), "default") {
			selectedModel = cfg.Model
		}

		currentProvider, _ := config.ParseModel(activeModelID)
		provider, model := resolveModelSelection(selectedModel, currentProvider)
		nextClient, err := newLLMClient(provider, model, cfg)
		if err != nil {
			if emitErr := bridge.EmitError(fmt.Sprintf("switch model %q: %v", args, err), true); emitErr != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
			}
			return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
		}

		*client = nextClient
		activeModelID = modelRef(provider, nextClient.ModelID())
		if err := emitToolUseCapabilityNotice(bridge, activeModelID, *client, nil); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := persistSessionState(store, sessionStateParams{
			SessionID: sessionID,
			CreatedAt: startedAt,
			Mode:      mode,
			Model:     activeModelID,
			CWD:       cwd,
			Branch:    agent.LoadTurnContext().GitBranch,
			Tracker:   tracker,
			Messages:  messages,
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitModelChanged(bridge, activeModelID, *client); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitContextWindowUsage(bridge, *client, messages); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitTextResponse(bridge, fmt.Sprintf("Set model to %s", activeModelID)); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "cost", "usage":
		if err := emitTextResponse(bridge, formatCostSummary(tracker.Snapshot(), activeModelID)); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "compact":
		if len(messages) == 0 {
			if emitErr := bridge.EmitError("no messages to compact", true); emitErr != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
			}
			return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
		}

		resolvedClient, nextModelID, err := ensureClientForSelection(activeModelID, cfg, *client)
		if err != nil {
			if emitErr := bridge.EmitError(fmt.Sprintf("initialize model %q: %v", activeModelID, err), true); emitErr != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
			}
			return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
		}
		*client = resolvedClient
		activeModelID = nextModelID

		tokensBefore := compact.EstimateConversationTokens(messages)
		if err := bridge.Emit(ipc.EventCompactStart, ipc.CompactStartPayload{
			Strategy:     string(agent.CompactManual),
			TokensBefore: tokensBefore,
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}

		pipeline := newCompactionPipeline(bridge, tracker, *client)
		result, err := pipeline.Compact(ctx, messages, string(agent.CompactManual))
		if err != nil {
			if emitErr := bridge.EmitError(fmt.Sprintf("compact conversation: %v", err), true); emitErr != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
			}
			return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
		}

		messages = result.Messages
		tokensAfter := compact.EstimateConversationTokens(messages)
		if err := persistSessionState(store, sessionStateParams{
			SessionID: sessionID,
			CreatedAt: startedAt,
			Mode:      mode,
			Model:     activeModelID,
			CWD:       cwd,
			Branch:    agent.LoadTurnContext().GitBranch,
			Tracker:   tracker,
			Messages:  messages,
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := bridge.Emit(ipc.EventCompactEnd, ipc.CompactEndPayload{TokensAfter: tokensAfter}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitContextWindowUsage(bridge, *client, messages); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitTextResponse(bridge, fmt.Sprintf("Compacted conversation with %s. Tokens %d -> %d.", result.Strategy, tokensBefore, tokensAfter)); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "resume":
		var targetID string
		if args != "" {
			targetID = args
		} else {
			meta, err := store.LatestResumeCandidate(sessionID)
			if err != nil {
				if emitErr := bridge.EmitError(err.Error(), true); emitErr != nil {
					return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
				}
				return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
			}
			targetID = meta.SessionID
		}

		restored, err := store.Restore(targetID)
		if err != nil {
			if emitErr := bridge.EmitError(fmt.Sprintf("restore session %q: %v", targetID, err), true); emitErr != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
			}
			return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
		}

		messages = append(messages[:0], restored.Messages...)
		sessionID = restored.Metadata.SessionID
		if !restored.Metadata.CreatedAt.IsZero() {
			startedAt = restored.Metadata.CreatedAt
		}
		mode = parseExecutionMode(restored.Metadata.Mode)

		if restored.Metadata.Model != "" {
			provider, model := config.ParseModel(restored.Metadata.Model)
			provider = normalizeProvider(provider)
			restoredClient, err := newLLMClient(provider, model, cfg)
			if err != nil {
				*client = nil
				activeModelID = modelRef(provider, model)
				if emitErr := bridge.EmitError(fmt.Sprintf("restore model %q: %v", restored.Metadata.Model, err), true); emitErr != nil {
					return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
				}
				return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
			}
			*client = restoredClient
			activeModelID = modelRef(provider, restoredClient.ModelID())
			if err := emitToolUseCapabilityNotice(bridge, activeModelID, *client, nil); err != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
			}
		}

		if restored.Metadata.CWD != "" {
			if err := os.Chdir(restored.Metadata.CWD); err == nil {
				cwd = restored.Metadata.CWD
			}
		}

		if err := persistSessionState(store, sessionStateParams{
			SessionID: sessionID,
			CreatedAt: startedAt,
			Mode:      mode,
			Model:     activeModelID,
			CWD:       cwd,
			Branch:    agent.LoadTurnContext().GitBranch,
			Tracker:   tracker,
			Messages:  messages,
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}

		if err := bridge.Emit(ipc.EventSessionRestored, ipc.SessionRestoredPayload{
			SessionID: sessionID,
			Mode:      string(mode),
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitSessionUpdated(bridge, sessionID, restored.Metadata.Title); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitModelChanged(bridge, activeModelID, *client); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitContextWindowUsage(bridge, *client, messages); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := bridge.Emit(ipc.EventModeChanged, ipc.ModeChangedPayload{Mode: string(mode)}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitTextResponse(bridge, fmt.Sprintf("Resumed session %s with %d messages.", sessionID, len(messages))); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "clear":
		messages = messages[:0]
		newID, err := newSessionID()
		if err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		sessionID = newID
		startedAt = time.Now()
		if err := persistSessionState(store, sessionStateParams{
			SessionID: sessionID,
			CreatedAt: startedAt,
			Mode:      mode,
			Model:     activeModelID,
			CWD:       cwd,
			Branch:    agent.LoadTurnContext().GitBranch,
			Tracker:   tracker,
			Messages:  messages,
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitSessionUpdated(bridge, sessionID, ""); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitContextWindowUsage(bridge, *client, messages); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitTextResponse(bridge, "Conversation cleared. New session started."); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "help":
		helpText := formatHelpText()
		if err := emitTextResponse(bridge, helpText); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "status":
		statusText := formatStatusText(sessionID, startedAt, mode, activeModelID, cwd, len(messages), tracker)
		if err := emitTextResponse(bridge, statusText); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "sessions":
		sessions, err := store.ListSessions()
		if err != nil {
			if emitErr := bridge.EmitError(fmt.Sprintf("list sessions: %v", err), true); emitErr != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
			}
			return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
		}
		if err := emitTextResponse(bridge, formatSessionList(sessions, sessionID)); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	case "diff":
		diffOutput := gitDiff(args)
		if strings.TrimSpace(diffOutput) == "" {
			diffOutput = "No changes detected."
		}
		if err := emitTextResponse(bridge, diffOutput); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	default:
		if err := bridge.EmitError(fmt.Sprintf("unknown slash command: %s", payload.Command), true); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
	}
}

func emitTextResponse(bridge *ipc.Bridge, text string) error {
	if strings.TrimSpace(text) != "" {
		if err := bridge.Emit(ipc.EventTokenDelta, ipc.TokenDeltaPayload{Text: text}); err != nil {
			return err
		}
	}
	return bridge.Emit(ipc.EventTurnComplete, ipc.TurnCompletePayload{StopReason: "end_turn"})
}

func emitArtifactCreated(bridge *ipc.Bridge, artifact artifactspkg.Artifact) error {
	return bridge.Emit(ipc.EventArtifactCreated, ipc.ArtifactCreatedPayload{
		ID:    artifact.ID,
		Kind:  string(artifact.Kind),
		Title: artifact.Title,
	})
}

func emitArtifactUpdated(bridge *ipc.Bridge, artifact artifactspkg.Artifact, content string) error {
	return bridge.Emit(ipc.EventArtifactUpdated, ipc.ArtifactUpdatedPayload{
		ID:      artifact.ID,
		Content: content,
	})
}

func budgetToolOutput(
	ctx context.Context,
	artifactManager *artifactspkg.Manager,
	sessionID string,
	budget toolpkg.ResultBudget,
	call api.ToolCall,
	output string,
) (string, artifactspkg.Artifact, error) {
	if len(output) <= budget.MaxChars {
		return output, artifactspkg.Artifact{}, nil
	}
	if artifactManager == nil {
		return truncateOutputPreview(output, budget.PreviewLen, "", len(output)), artifactspkg.Artifact{}, nil
	}

	artifact, _, _, err := artifactManager.SaveMarkdown(ctx, artifactspkg.MarkdownRequest{
		Kind:    artifactspkg.KindToolLog,
		Scope:   artifactspkg.ScopeSession,
		Title:   fmt.Sprintf("Tool Log: %s", call.Name),
		Source:  call.Name,
		Content: artifactspkg.RenderToolLogMarkdown(sessionID, call.Name, call.ID, call.Input, output),
		Metadata: map[string]any{
			"session_id":   sessionID,
			"tool_call_id": call.ID,
			"tool_name":    call.Name,
		},
	})
	if err != nil {
		return truncateOutputPreview(output, budget.PreviewLen, "", len(output)), artifactspkg.Artifact{}, err
	}

	return truncateOutputPreview(output, budget.PreviewLen, artifact.ContentPath, len(output)), artifact, nil
}

func truncateOutputPreview(output string, previewLen int, artifactPath string, totalChars int) string {
	if previewLen <= 0 || previewLen > len(output) {
		previewLen = len(output)
	}
	preview := output[:previewLen]
	if artifactPath == "" {
		return fmt.Sprintf("%s\n\n[Output truncated (%d chars).]", preview, totalChars)
	}
	return fmt.Sprintf("%s\n\n[Output truncated. Full markdown artifact saved to %s (%d chars)]", preview, artifactPath, totalChars)
}

func formatCostSummary(snapshot costpkg.TrackerSnapshot, activeModelID string) string {
	return fmt.Sprintf(
		"Model: %s\nTotal cost: $%.4f\nInput tokens: %d\nOutput tokens: %d\nAPI duration: %s\nTool duration: %s",
		activeModelID,
		snapshot.TotalCostUSD,
		snapshot.TotalInputTokens,
		snapshot.TotalOutputTokens,
		snapshot.TotalAPIDuration.Round(time.Millisecond),
		snapshot.TotalToolDuration.Round(time.Millisecond),
	)
}

func stringParamFromMap(params map[string]any, key string) (string, bool) {
	value, ok := params[key]
	if !ok {
		return "", false
	}
	stringValue, ok := value.(string)
	return stringValue, ok
}

func formatHelpText() string {
	return `Available slash commands:

  /plan          Switch to plan mode (read-only until approved)
  /fast          Switch to fast mode (direct execution)
  /model [name]  Show or switch the active model
  /cost          Show token usage and cost breakdown
  /usage         Alias for /cost
  /compact       Compact the conversation to save context
  /resume [id]   Resume a previous session
  /clear         Clear conversation and start a new session
  /status        Show current session status
  /sessions      List recent sessions
  /diff [args]   Show git diff (optionally with args like --staged)
  /help          Show this help message`
}

func formatStatusText(sessionID string, startedAt time.Time, mode agent.ExecutionMode, model string, cwd string, msgCount int, tracker *costpkg.Tracker) string {
	elapsed := time.Since(startedAt).Round(time.Second)
	snap := tracker.Snapshot()
	return fmt.Sprintf(
		"Session: %s\nStarted: %s (%s ago)\nMode: %s\nModel: %s\nCWD: %s\nMessages: %d\nCost: $%.4f\nTokens: %d in / %d out",
		sessionID,
		startedAt.Format(time.RFC3339),
		elapsed,
		string(mode),
		model,
		cwd,
		msgCount,
		snap.TotalCostUSD,
		snap.TotalInputTokens,
		snap.TotalOutputTokens,
	)
}

func formatSessionList(sessions []session.Metadata, currentID string) string {
	if len(sessions) == 0 {
		return "No sessions found."
	}
	var b strings.Builder
	b.WriteString("Recent sessions:\n\n")
	shown := 0
	for _, meta := range sessions {
		if shown >= 20 {
			break
		}
		marker := "  "
		if meta.SessionID == currentID {
			marker = "* "
		}
		title := meta.Title
		if title == "" {
			title = "(untitled)"
		}
		b.WriteString(fmt.Sprintf("%s%s  %s  %s  %s  $%.4f\n",
			marker,
			meta.SessionID[:8],
			meta.UpdatedAt.Format("2006-01-02 15:04"),
			meta.Model,
			title,
			meta.TotalCostUSD,
		))
		shown++
	}
	return strings.TrimSpace(b.String())
}

func gitDiff(args string) string {
	parts := []string{"diff", "--stat"}
	if strings.TrimSpace(args) != "" {
		parts = strings.Fields("diff " + args)
	}
	cmd := exec.Command("git", parts...)
	out, err := cmd.Output()
	if err != nil {
		return fmt.Sprintf("git diff error: %v", err)
	}
	result := strings.TrimSpace(string(out))
	if len(result) > 5000 {
		result = result[:5000] + "\n[truncated]"
	}
	return result
}

type sessionStateParams struct {
	SessionID string
	CreatedAt time.Time
	Mode      agent.ExecutionMode
	Model     string
	CWD       string
	Branch    string
	Tracker   *costpkg.Tracker
	Messages  []api.Message
}

type compactionSummarizer struct {
	bridge  *ipc.Bridge
	tracker *costpkg.Tracker
	client  api.LLMClient
	router  *localmodel.Router
}

func newCompactionPipeline(bridge *ipc.Bridge, tracker *costpkg.Tracker, client api.LLMClient) *compact.Pipeline {
	return compact.NewPipeline(client.Capabilities().MaxContextWindow, compactionSummarizer{
		bridge:  bridge,
		tracker: tracker,
		client:  client,
		router:  localmodel.NewRouter(client),
	})
}

func (s compactionSummarizer) Summarize(ctx context.Context, messages []api.Message) (string, error) {
	return s.SummarizeWithPrompt(ctx, messages, compact.CompactionPromptTemplate)
}

func (s compactionSummarizer) SummarizeWithPrompt(ctx context.Context, messages []api.Message, prompt string) (string, error) {
	if summary, usedLocal, err := s.summarizeWithLocal(prompt, messages); usedLocal {
		if err == nil && strings.TrimSpace(summary) != "" {
			return compact.NormalizeSummary(summary), nil
		}
	}

	stream, err := s.client.Stream(ctx, api.ModelRequest{
		Messages:     messages,
		SystemPrompt: prompt,
		MaxTokens:    2048,
	})
	if err != nil {
		return "", err
	}

	startedAt := time.Now()
	var usage api.Usage
	var builder strings.Builder

	for event, streamErr := range stream {
		if streamErr != nil {
			return "", streamErr
		}
		switch event.Type {
		case api.ModelEventToken:
			builder.WriteString(event.Text)
		case api.ModelEventUsage:
			if event.Usage != nil {
				usage = mergeUsage(usage, *event.Usage)
			}
		}
	}

	s.tracker.RecordAPICall(
		s.client.ModelID(),
		usage.InputTokens,
		usage.OutputTokens,
		usage.CacheReadTokens,
		usage.CacheCreationTokens,
		time.Since(startedAt),
		costpkg.CalculateUSDCost(s.client.ModelID(), usage),
	)
	if err := emitCostUpdate(s.bridge, s.tracker); err != nil {
		return "", err
	}

	return compact.NormalizeSummary(builder.String()), nil
}

func (s compactionSummarizer) summarizeWithLocal(prompt string, messages []api.Message) (string, bool, error) {
	if s.router == nil {
		return "", false, nil
	}

	prompt = renderCompactionPrompt(prompt, messages)
	if strings.TrimSpace(prompt) == "" {
		return "", false, nil
	}

	return s.router.TryLocal(localmodel.TaskCompaction, prompt, 2048)
}

func renderCompactionPrompt(promptTemplate string, messages []api.Message) string {
	var builder strings.Builder
	builder.WriteString(promptTemplate)
	builder.WriteString("\n\nConversation:\n")

	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if content == "" && len(message.ToolCalls) == 0 && message.ToolResult == nil {
			continue
		}

		builder.WriteString("\n[")
		builder.WriteString(strings.ToUpper(string(message.Role)))
		builder.WriteString("]\n")

		if content != "" {
			builder.WriteString(content)
			builder.WriteString("\n")
		}
		for _, call := range message.ToolCalls {
			builder.WriteString("Tool call ")
			builder.WriteString(call.Name)
			builder.WriteString(": ")
			builder.WriteString(call.Input)
			builder.WriteString("\n")
		}
		if message.ToolResult != nil && strings.TrimSpace(message.ToolResult.Output) != "" {
			builder.WriteString("Tool result: ")
			builder.WriteString(strings.TrimSpace(message.ToolResult.Output))
			builder.WriteString("\n")
		}
	}

	builder.WriteString("\nSummary:\n")
	return builder.String()
}

func persistSessionState(store *session.Store, params sessionStateParams) error {
	if err := store.SaveTranscript(params.SessionID, params.Messages); err != nil {
		return err
	}

	totalCost := 0.0
	if params.Tracker != nil {
		totalCost = params.Tracker.Snapshot().TotalCostUSD
	}

	title := ""
	if existing, err := store.LoadMetadata(params.SessionID); err == nil {
		title = existing.Title
	}

	return store.SaveMetadata(session.Metadata{
		SessionID:    params.SessionID,
		CreatedAt:    params.CreatedAt,
		UpdatedAt:    time.Now(),
		Mode:         string(params.Mode),
		Model:        params.Model,
		CWD:          params.CWD,
		Branch:       params.Branch,
		TotalCostUSD: totalCost,
		Title:        title,
	})
}

func newSessionID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	encoded := hex.EncodeToString(buf)
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[0:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:32]), nil
}

func decodeToolInput(call api.ToolCall) (toolpkg.ToolInput, error) {
	params := make(map[string]any)
	raw := strings.TrimSpace(call.Input)
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &params); err != nil {
			return toolpkg.ToolInput{}, fmt.Errorf("decode tool input for %q: %w", call.Name, err)
		}
	}
	return toolpkg.ToolInput{
		Name:   call.Name,
		Params: params,
		Raw:    call.Input,
	}, nil
}

func normalizeToolCall(call api.ToolCall) (api.ToolCall, error) {
	alias := strings.TrimSpace(call.Name)
	switch alias {
	case "file_search", "grep_search", "read_file":
	default:
		return call, nil
	}

	params := make(map[string]any)
	raw := strings.TrimSpace(call.Input)
	if raw != "" {
		if err := json.Unmarshal([]byte(raw), &params); err != nil {
			return api.ToolCall{}, fmt.Errorf("decode tool input for %q: %w", call.Name, err)
		}
	}

	normalized := call
	normalizedParams := cloneToolParams(params)

	switch alias {
	case "file_search":
		normalized.Name = "glob"
		if pattern, ok := stringParamFromMap(normalizedParams, "pattern"); !ok || strings.TrimSpace(pattern) == "" {
			if query, ok := stringParamFromMap(normalizedParams, "query"); ok && strings.TrimSpace(query) != "" {
				normalizedParams["pattern"] = normalizeFileSearchPattern(query)
			}
		}
		if _, ok := stringParamFromMap(normalizedParams, "path"); !ok {
			if includePattern, ok := stringParamFromMap(normalizedParams, "includePattern"); ok && strings.TrimSpace(includePattern) != "" && !looksLikeGlob(includePattern) {
				normalizedParams["path"] = includePattern
			}
		}
	case "grep_search":
		normalized.Name = "grep"
		if pattern, ok := stringParamFromMap(normalizedParams, "pattern"); !ok || strings.TrimSpace(pattern) == "" {
			if query, ok := stringParamFromMap(normalizedParams, "query"); ok && strings.TrimSpace(query) != "" {
				if isRegexp, ok := normalizedParams["isRegexp"].(bool); ok && !isRegexp {
					normalizedParams["pattern"] = regexp.QuoteMeta(query)
				} else {
					normalizedParams["pattern"] = query
				}
			}
		}
		if _, ok := stringParamFromMap(normalizedParams, "path"); !ok {
			if includePattern, ok := stringParamFromMap(normalizedParams, "includePattern"); ok && strings.TrimSpace(includePattern) != "" {
				if looksLikeGlob(includePattern) {
					normalizedParams["glob"] = includePattern
				} else {
					normalizedParams["path"] = includePattern
				}
			}
		}
		if _, ok := normalizedParams["head_limit"]; !ok {
			if maxResults, ok := intParamFromMap(normalizedParams, "maxResults"); ok && maxResults > 0 {
				normalizedParams["head_limit"] = maxResults
			}
		}
	case "read_file":
		normalized.Name = "file_read"
		renameToolParam(normalizedParams, "filePath", "file_path")
		renameToolParam(normalizedParams, "startLine", "start_line")
		renameToolParam(normalizedParams, "endLine", "end_line")
	}

	encoded, err := json.Marshal(normalizedParams)
	if err != nil {
		return api.ToolCall{}, fmt.Errorf("encode normalized tool input for %q: %w", call.Name, err)
	}
	normalized.Input = string(encoded)
	return normalized, nil
}

func cloneToolParams(params map[string]any) map[string]any {
	cloned := make(map[string]any, len(params))
	for key, value := range params {
		cloned[key] = value
	}
	return cloned
}

func renameToolParam(params map[string]any, from, to string) {
	if _, exists := params[to]; exists {
		return
	}
	value, ok := params[from]
	if !ok {
		return
	}
	params[to] = value
}

func normalizeFileSearchPattern(query string) string {
	trimmed := strings.TrimSpace(query)
	if trimmed == "" || filepath.IsAbs(trimmed) || looksLikeGlob(trimmed) {
		return trimmed
	}

	normalized := strings.TrimPrefix(filepath.ToSlash(trimmed), "./")
	if normalized == "" {
		return trimmed
	}
	if strings.HasSuffix(normalized, "/") {
		return "**/" + strings.TrimSuffix(normalized, "/") + "/**"
	}
	if strings.Contains(normalized, "/") {
		return "**/" + normalized + "*"
	}
	return "**/*" + normalized + "*"
}

func looksLikeGlob(value string) bool {
	return strings.ContainsAny(value, "*?[]{}")
}

func intParamFromMap(params map[string]any, key string) (int, bool) {
	value, ok := params[key]
	if !ok {
		return 0, false
	}
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	default:
		return 0, false
	}
}
