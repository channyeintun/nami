package main

import (
	"context"
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
	"github.com/channyeintun/go-cli/internal/compact"
	"github.com/channyeintun/go-cli/internal/config"
	"github.com/channyeintun/go-cli/internal/ipc"
	"github.com/channyeintun/go-cli/internal/permissions"
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
	client, err := newLLMClient(provider, model, cfg)
	if err != nil {
		return err
	}
	messages := make([]api.Message, 0, 32)
	mode := parseExecutionMode(cfg.DefaultMode)
	permissionCtx := newPermissionContext(cfg.PermissionMode)

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

			deps := agent.QueryDeps{
				CallModel: func(callCtx context.Context, req api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
					return client.Stream(callCtx, req)
				},
				ExecuteToolBatch: func(callCtx context.Context, calls []api.ToolCall) ([]api.ToolResult, error) {
					return executeToolCalls(callCtx, bridge, registry, permissionCtx, calls)
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
				},
				Clock: time.Now,
			}

			stream := agent.QueryStream(ctx, agent.QueryRequest{
				Messages:     messages,
				SystemPrompt: defaultSystemPrompt(),
				Mode:         mode,
				Tools:        registry.Definitions(),
				MaxTokens:    client.Capabilities().MaxOutputTokens,
			}, deps)

			for event, streamErr := range stream {
				if streamErr != nil {
					if emitErr := bridge.EmitError(streamErr.Error(), false); emitErr != nil {
						return emitErr
					}
					break
				}
				if err := bridge.EmitEvent(event); err != nil {
					return err
				}
			}
		case ipc.MsgSlashCommand:
			// TODO: dispatch slash commands
			continue
		case ipc.MsgModeToggle:
			if mode == agent.ModePlan {
				mode = agent.ModeFast
			} else {
				mode = agent.ModePlan
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
	if provider == "" {
		provider = "anthropic"
	}

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

func parseExecutionMode(mode string) agent.ExecutionMode {
	if strings.EqualFold(mode, string(agent.ModeFast)) {
		return agent.ModeFast
	}
	return agent.ModePlan
}

func defaultSystemPrompt() string {
	return "You are Go CLI, a pragmatic coding assistant. Be concise, prefer inspecting files before changing them, and use tools when needed."
}

func executeToolCalls(
	ctx context.Context,
	bridge *ipc.Bridge,
	registry *toolpkg.Registry,
	permissionCtx *permissions.Context,
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
		for _, result := range toolpkg.ExecuteBatch(ctx, batch) {
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
				budgetedOutput, spill, err := toolpkg.ApplyBudget(budget, call.ID, output)
				if err == nil {
					output = budgetedOutput
					spillPath = spill
					truncated = truncated || spill != ""
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

func stringParamFromMap(params map[string]any, key string) (string, bool) {
	value, ok := params[key]
	if !ok {
		return "", false
	}
	stringValue, ok := value.(string)
	return stringValue, ok
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
