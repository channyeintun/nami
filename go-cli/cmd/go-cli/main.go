package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"iter"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/channyeintun/go-cli/internal/agent"
	"github.com/channyeintun/go-cli/internal/api"
	artifactspkg "github.com/channyeintun/go-cli/internal/artifacts"
	"github.com/channyeintun/go-cli/internal/compact"
	"github.com/channyeintun/go-cli/internal/config"
	costpkg "github.com/channyeintun/go-cli/internal/cost"
	"github.com/channyeintun/go-cli/internal/ipc"
	"github.com/channyeintun/go-cli/internal/permissions"
	"github.com/channyeintun/go-cli/internal/session"
	skillspkg "github.com/channyeintun/go-cli/internal/skills"
	toolpkg "github.com/channyeintun/go-cli/internal/tools"
)

var (
	version = "dev"
	commit  = "none"
)

func main() {
	rootCmd := &cobra.Command{
		Use:     "go-cli",
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

	// TUI mode: check for node, then spawn Ink frontend
	// (for now, fall back to stdio mode)
	fmt.Fprintln(os.Stderr, "TUI mode not yet implemented. Use --stdio for engine-only mode.")
	return runStdioEngine(ctx, cfg)
}

func runStdioEngine(ctx context.Context, cfg config.Config) error {
	bridge := ipc.NewBridge(os.Stdin, os.Stdout)
	registry := toolpkg.NewRegistry()
	provider, model := config.ParseModel(cfg.Model)
	provider = normalizeProvider(provider)
	client, err := newLLMClient(provider, model, cfg)
	if err != nil {
		return err
	}
	activeModelID := modelRef(provider, client.ModelID())
	messages := make([]api.Message, 0, 32)
	mode := parseExecutionMode(cfg.DefaultMode)
	permissionCtx := newPermissionContext(cfg.PermissionMode)
	tracker := costpkg.NewTracker()
	sessionStore := session.NewStore(session.DefaultBaseDir())
	artifactStore := artifactspkg.NewLocalStore(filepath.Join(filepath.Dir(session.DefaultBaseDir()), "artifacts"))
	artifactManager := artifactspkg.NewManager(artifactStore)
	sessionID, err := newSessionID()
	if err != nil {
		return err
	}
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

	// Main event loop: read client messages and dispatch
	for {
		msg, err := bridge.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil // clean shutdown
			}
			return fmt.Errorf("read message: %w", err)
		}

		switch msg.Type {
		case ipc.MsgShutdown:
			return nil
		case ipc.MsgCancel:
			// TODO: cancel in-flight query
			continue
		case ipc.MsgUserInput:
			var payload ipc.UserInputPayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return fmt.Errorf("decode user input: %w", err)
			}
			if strings.TrimSpace(payload.Text) == "" {
				continue
			}

			messages = append(messages, api.Message{
				Role:    api.RoleUser,
				Content: payload.Text,
			})
			availableSkills, _ := skillspkg.LoadAll(cwd)
			messagesBeforeQuery := len(messages)
			planArtifactID := ""
			planner := agent.NewPlanner(mode, sessionID, artifactManager)
			if update, beginErr := planner.BeginTurn(ctx, payload.Text); beginErr != nil {
				if emitErr := bridge.EmitError(fmt.Sprintf("persist implementation plan artifact: %v", beginErr), true); emitErr != nil {
					return emitErr
				}
			} else if update != nil {
				planArtifactID = update.Artifact.ID
				if update.Created {
					if err := emitArtifactCreated(bridge, update.Artifact); err != nil {
						return err
					}
				}
				if err := emitArtifactUpdated(bridge, update.Artifact, update.Content); err != nil {
					return err
				}
			}

			deps := agent.QueryDeps{
				CallModel: func(callCtx context.Context, req api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
					return trackModelStream(callCtx, bridge, tracker, client, req)
				},
				ExecuteToolBatch: func(callCtx context.Context, calls []api.ToolCall) ([]api.ToolResult, error) {
					return executeToolCalls(callCtx, bridge, registry, permissionCtx, tracker, planner, artifactManager, sessionID, calls)
				},
				CompactMessages: func(callCtx context.Context, current []api.Message, reason agent.CompactReason) ([]api.Message, error) {
					pipeline := compact.NewPipeline(client.Capabilities().MaxContextWindow, nil)
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
				},
				Clock: time.Now,
			}

			stream := agent.QueryStream(ctx, agent.QueryRequest{
				Messages:     messages,
				SystemPrompt: systemPromptForMode(mode),
				Mode:         mode,
				SessionID:    sessionID,
				Skills:       availableSkills,
				Tools:        registry.Definitions(),
				MaxTokens:    client.Capabilities().MaxOutputTokens,
			}, deps)

			queryFailed := false
			for event, streamErr := range stream {
				if streamErr != nil {
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

			if !queryFailed {
				if update, finalizeErr := planner.FinalizeTurn(ctx, planArtifactID, payload.Text, messages, messagesBeforeQuery); finalizeErr != nil {
					if emitErr := bridge.EmitError(fmt.Sprintf("update implementation plan artifact: %v", finalizeErr), true); emitErr != nil {
						return emitErr
					}
				} else if update != nil {
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
			// TODO: resolve pending permission request
			continue
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
	return "You are Go CLI, a pragmatic coding assistant. Be concise, prefer inspecting files before changing them, and use tools when needed."
}

func systemPromptForMode(mode agent.ExecutionMode) string {
	prompt := defaultSystemPrompt()
	if mode == agent.ModePlan {
		return prompt + "\n\nWhen plan mode is active, respond with a concrete markdown implementation plan before proposing file mutations. Keep the plan actionable and review-friendly. " + agent.PlanModePromptHint()
	}
	return prompt
}

func executeToolCalls(
	ctx context.Context,
	bridge *ipc.Bridge,
	registry *toolpkg.Registry,
	permissionCtx *permissions.Context,
	tracker *costpkg.Tracker,
	planner *agent.Planner,
	artifactManager *artifactspkg.Manager,
	sessionID string,
	calls []api.ToolCall,
) ([]api.ToolResult, error) {
	results := make([]api.ToolResult, len(calls))
	approved := make([]toolpkg.PendingCall, 0, len(calls))
	budget := toolpkg.DefaultResultBudget(filepath.Join(os.TempDir(), "go-cli-session"))

	for index, call := range calls {
		tool, err := registry.Get(call.Name)
		if err != nil {
			results[index] = api.ToolResult{ToolCallID: call.ID, Output: err.Error(), IsError: true}
			if emitErr := bridge.Emit(ipc.EventToolError, ipc.ToolErrorPayload{ToolID: call.ID, Error: err.Error()}); emitErr != nil {
				return nil, emitErr
			}
			continue
		}

		input, err := decodeToolInput(call)
		if err != nil {
			results[index] = api.ToolResult{ToolCallID: call.ID, Output: err.Error(), IsError: true}
			if emitErr := bridge.Emit(ipc.EventToolError, ipc.ToolErrorPayload{ToolID: call.ID, Error: err.Error()}); emitErr != nil {
				return nil, emitErr
			}
			continue
		}

		pendingCall := toolpkg.PendingCall{Index: index, Tool: tool, Input: input}
		if err := planner.ValidateTool(ctx, pendingCall.Tool.Name(), pendingCall.Tool.Permission()); err != nil {
			results[index] = api.ToolResult{ToolCallID: call.ID, Output: err.Error(), IsError: true}
			if emitErr := bridge.Emit(ipc.EventToolError, ipc.ToolErrorPayload{ToolID: call.ID, Error: err.Error()}); emitErr != nil {
				return nil, emitErr
			}
			continue
		}
		allowed, denyReason, err := authorizeToolCall(ctx, bridge, permissionCtx, pendingCall)
		if err != nil {
			return nil, err
		}
		if !allowed {
			results[index] = api.ToolResult{ToolCallID: call.ID, Output: denyReason, IsError: true}
			if emitErr := bridge.Emit(ipc.EventToolError, ipc.ToolErrorPayload{ToolID: call.ID, Error: denyReason}); emitErr != nil {
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
		approved = append(approved, pendingCall)
	}

	for _, batch := range toolpkg.PartitionBatches(approved) {
		batchStart := time.Now()
		batchResults := toolpkg.ExecuteBatch(ctx, batch)
		tracker.RecordToolDuration(time.Since(batchStart))
		for _, result := range batchResults {
			call := calls[result.Index]
			toolResult := api.ToolResult{ToolCallID: call.ID}

			if result.Err != nil {
				toolResult.Output = result.Err.Error()
				toolResult.IsError = true
				if err := bridge.Emit(ipc.EventToolError, ipc.ToolErrorPayload{ToolID: call.ID, Error: result.Err.Error()}); err != nil {
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

			toolResult.Output = output
			toolResult.IsError = result.Output.IsError
			results[result.Index] = toolResult

			if result.Output.IsError {
				if err := bridge.Emit(ipc.EventToolError, ipc.ToolErrorPayload{ToolID: call.ID, Error: output}); err != nil {
					return nil, err
				}
				continue
			}

			if err := bridge.Emit(ipc.EventToolResult, ipc.ToolResultPayload{
				ToolID:    call.ID,
				Output:    output,
				Truncated: truncated || spillPath != "",
			}); err != nil {
				return nil, err
			}
		}
	}

	return results, nil
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
	permissionCtx *permissions.Context,
	pending toolpkg.PendingCall,
) (bool, string, error) {
	decision := permissionCtx.Check(pending.Tool.Name(), pending.Input, pending.Tool.Permission())
	switch decision {
	case permissions.DecisionAllow:
		return true, "", nil
	case permissions.DecisionDeny:
		return false, toolPermissionMessage("denied", pending, "permission policy denied this tool call"), nil
	case permissions.DecisionAsk:
		response, err := waitForPermissionDecision(ctx, bridge, pending)
		if err != nil {
			return false, "", err
		}
		switch response {
		case "allow":
			return true, "", nil
		case "always_allow":
			if raw := strings.TrimSpace(pending.Input.Raw); raw != "" {
				if err := permissionCtx.AddAlwaysAllow(pending.Tool.Name(), "^"+regexp.QuoteMeta(raw)+"$"); err != nil {
					return false, "", err
				}
			}
			return true, "", nil
		default:
			return false, toolPermissionMessage("denied", pending, "user denied permission request"), nil
		}
	default:
		return false, toolPermissionMessage("denied", pending, "permission policy denied this tool call"), nil
	}
}

func waitForPermissionDecision(
	ctx context.Context,
	bridge *ipc.Bridge,
	pending toolpkg.PendingCall,
) (string, error) {
	requestID := fmt.Sprintf("perm-%d", time.Now().UnixNano())
	if err := bridge.Emit(ipc.EventPermissionRequest, ipc.PermissionRequestPayload{
		RequestID: requestID,
		Tool:      pending.Tool.Name(),
		Command:   summarizePermissionTarget(pending),
		Risk:      permissionRisk(pending),
	}); err != nil {
		return "", err
	}

	for {
		msg, err := bridge.ReadMessage(ctx)
		if err != nil {
			return "", err
		}

		switch msg.Type {
		case ipc.MsgPermissionResponse:
			var payload ipc.PermissionResponsePayload
			if err := json.Unmarshal(msg.Payload, &payload); err != nil {
				return "", fmt.Errorf("decode permission response: %w", err)
			}
			if payload.RequestID != requestID {
				continue
			}
			return payload.Decision, nil
		case ipc.MsgCancel, ipc.MsgShutdown:
			return "", context.Canceled
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
		if err := bridge.Emit(ipc.EventModelChanged, ipc.ModelChangedPayload{Model: activeModelID}); err != nil {
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

		tokensBefore := estimateConversationTokens(messages)
		if err := bridge.Emit(ipc.EventCompactStart, ipc.CompactStartPayload{
			Strategy:     string(agent.CompactManual),
			TokensBefore: tokensBefore,
		}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}

		pipeline := compact.NewPipeline((*client).Capabilities().MaxContextWindow, nil)
		result, err := pipeline.Compact(ctx, messages, string(agent.CompactManual))
		if err != nil {
			if emitErr := bridge.EmitError(fmt.Sprintf("compact conversation: %v", err), true); emitErr != nil {
				return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
			}
			return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
		}

		messages = result.Messages
		tokensAfter := estimateConversationTokens(messages)
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
				if emitErr := bridge.EmitError(fmt.Sprintf("restore model %q: %v", restored.Metadata.Model, err), true); emitErr != nil {
					return false, sessionID, startedAt, mode, activeModelID, cwd, messages, emitErr
				}
				return true, sessionID, startedAt, mode, activeModelID, cwd, messages, nil
			}
			*client = restoredClient
			activeModelID = modelRef(provider, restoredClient.ModelID())
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
		if err := bridge.Emit(ipc.EventModelChanged, ipc.ModelChangedPayload{Model: activeModelID}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := bridge.Emit(ipc.EventModeChanged, ipc.ModeChangedPayload{Mode: string(mode)}); err != nil {
			return false, sessionID, startedAt, mode, activeModelID, cwd, messages, err
		}
		if err := emitTextResponse(bridge, fmt.Sprintf("Resumed session %s with %d messages.", sessionID, len(messages))); err != nil {
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

func estimateConversationTokens(messages []api.Message) int {
	total := 0
	for _, message := range messages {
		total += compact.EstimateTokens(message.Content)
		for _, call := range message.ToolCalls {
			total += compact.EstimateTokens(call.Name)
			total += compact.EstimateTokens(call.Input)
		}
		if message.ToolResult != nil {
			total += compact.EstimateTokens(message.ToolResult.Output)
		}
	}
	return total
}

func stringParamFromMap(params map[string]any, key string) (string, bool) {
	value, ok := params[key]
	if !ok {
		return "", false
	}
	stringValue, ok := value.(string)
	return stringValue, ok
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

func persistSessionState(store *session.Store, params sessionStateParams) error {
	if err := store.SaveTranscript(params.SessionID, params.Messages); err != nil {
		return err
	}

	totalCost := 0.0
	if params.Tracker != nil {
		totalCost = params.Tracker.Snapshot().TotalCostUSD
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
