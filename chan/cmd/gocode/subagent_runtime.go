package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"path/filepath"
	"strings"
	"time"

	"github.com/channyeintun/gocode/internal/agent"
	"github.com/channyeintun/gocode/internal/api"
	artifactspkg "github.com/channyeintun/gocode/internal/artifacts"
	"github.com/channyeintun/gocode/internal/config"
	costpkg "github.com/channyeintun/gocode/internal/cost"
	"github.com/channyeintun/gocode/internal/hooks"
	"github.com/channyeintun/gocode/internal/ipc"
	"github.com/channyeintun/gocode/internal/permissions"
	"github.com/channyeintun/gocode/internal/session"
	skillspkg "github.com/channyeintun/gocode/internal/skills"
	"github.com/channyeintun/gocode/internal/timing"
	toolpkg "github.com/channyeintun/gocode/internal/tools"
)

const exploreSubagentType = "explore"
const searchSubagentType = "search"
const executionSubagentType = "execution"
const generalPurposeSubagentType = "general-purpose"

var exploreSubagentTools = []string{
	"think",
	"list_dir",
	"file_read",
	"file_diff_preview",
	"glob",
	"grep",
	"go_definition",
	"go_references",
	"project_overview",
	"dependency_overview",
	"symbol_search",
	"web_search",
	"web_fetch",
	"git",
}

var searchSubagentTools = []string{
	"think",
	"list_dir",
	"file_read",
	"file_diff_preview",
	"glob",
	"grep",
	"go_definition",
	"go_references",
	"project_overview",
	"dependency_overview",
	"symbol_search",
	"git",
}

var executionSubagentTools = []string{
	"bash",
	"list_commands",
	"command_status",
	"send_command_input",
	"stop_command",
	"forget_command",
	"list_dir",
	"file_read",
	"file_diff_preview",
	"think",
}

var generalPurposeSubagentTools = []string{
	"bash",
	"think",
	"list_dir",
	"file_read",
	"file_diff_preview",
	"create_file",
	"file_write",
	"file_edit",
	"apply_patch",
	"multi_replace_file_content",
	"glob",
	"grep",
	"go_definition",
	"go_references",
	"project_overview",
	"dependency_overview",
	"symbol_search",
	"web_search",
	"web_fetch",
	"git",
	"file_history",
}

func makeSubagentRunner(
	bridge *ipc.Bridge,
	registry *toolpkg.Registry,
	permissionCtx *permissions.Context,
	parentTracker *costpkg.Tracker,
	sessionStore *session.Store,
	artifactManager *artifactspkg.Manager,
	hookRunner *hooks.Runner,
	modelState *activeModelState,
	cwd string,
) toolpkg.AgentRunner {
	return func(ctx context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		client, activeModelID := modelState.Get()
		if client == nil {
			return toolpkg.AgentRunResult{}, fmt.Errorf("agent tool is unavailable: model client is not initialized")
		}

		childClient, childActiveModelID, err := resolveSubagentClient(client, activeModelID)
		if err != nil {
			return toolpkg.AgentRunResult{}, err
		}

		subagentType := strings.TrimSpace(req.SubagentType)
		if subagentType == "" {
			subagentType = exploreSubagentType
		}
		if !toolpkg.IsSupportedSubagentType(subagentType) {
			return toolpkg.AgentRunResult{}, fmt.Errorf("agent subagent_type %q is not supported yet", subagentType)
		}

		invocationID, err := newSessionID()
		if err != nil {
			return toolpkg.AgentRunResult{}, err
		}

		execute := func(runCtx context.Context) (toolpkg.AgentRunResult, error) {
			return executeSubagent(runCtx, req, subagentType, invocationID, bridge, registry, permissionCtx, parentTracker, sessionStore, artifactManager, hookRunner, childClient, childActiveModelID, cwd, nil, nil)
		}
		if req.Background {
			launch := launchBackgroundAgent(ctx, bridge, strings.TrimSpace(req.Description), subagentType, invocationID, sessionStore, func(runCtx context.Context, stopControl *agent.StopController, reportStatus func(toolpkg.AgentRunResult)) (toolpkg.AgentRunResult, error) {
				return executeSubagent(runCtx, req, subagentType, invocationID, bridge, registry, permissionCtx, parentTracker, sessionStore, artifactManager, hookRunner, childClient, childActiveModelID, cwd, stopControl, reportStatus)
			})
			launch.SubagentType = subagentType
			launch.Tools = subagentToolNames(subagentType)
			return withChildMetadata(launch, strings.TrimSpace(req.Description)), nil
		}
		result, err := execute(ctx)
		if err != nil {
			return toolpkg.AgentRunResult{}, err
		}
		return withChildMetadata(result, strings.TrimSpace(req.Description)), nil
	}
}

func resolveSubagentClient(parent api.LLMClient, activeModelID string) (api.LLMClient, string, error) {
	provider, _ := config.ParseModel(strings.TrimSpace(activeModelID))
	provider = normalizeProvider(provider)
	if provider != "github-copilot" {
		return parent, activeModelID, nil
	}

	cfg := config.Load()
	selection := strings.TrimSpace(cfg.SubagentModel)
	if selection == "" {
		selection = modelRef(provider, api.GitHubCopilotDefaultSubagentModel)
	}

	childProvider, childModel := resolveModelSelection(selection, provider)
	childClient, err := newLLMClient(childProvider, childModel, cfg)
	if err != nil {
		return nil, "", fmt.Errorf("initialize subagent model %q: %w", selection, err)
	}

	return childClient, modelRef(childProvider, childClient.ModelID()), nil
}

func executeSubagent(
	ctx context.Context,
	req toolpkg.AgentRunRequest,
	subagentType string,
	invocationID string,
	bridge *ipc.Bridge,
	registry *toolpkg.Registry,
	permissionCtx *permissions.Context,
	parentTracker *costpkg.Tracker,
	sessionStore *session.Store,
	artifactManager *artifactspkg.Manager,
	hookRunner *hooks.Runner,
	client api.LLMClient,
	activeModelID string,
	cwd string,
	stopControl *agent.StopController,
	reportStatus func(toolpkg.AgentRunResult),
) (toolpkg.AgentRunResult, error) {
	childSessionID := invocationID
	childStartedAt := time.Now()
	childTracker := costpkg.NewTracker()
	childRegistry := registry.CloneFiltered(subagentToolNames(subagentType))
	childPermissionCtx := permissions.CloneContext(permissionCtx)
	childBridge := ipc.NewBridge(strings.NewReader(""), io.Discard)
	childTimingLogger := timing.NewSessionLogger(sessionStore.SessionDir(childSessionID))
	childSkills, _ := skillspkg.LoadAll(cwd)
	childMode := agent.ModeFast
	startHookMessages := runChildStartHooks(ctx, hookRunner, childSessionID, invocationID, req, subagentType)
	childMessages := []api.Message{{Role: api.RoleUser, Content: injectChildHookContext(req.Prompt, startHookMessages)}}
	childPrompt := subagentSystemPrompt(subagentType, childRegistry.Definitions())
	transcriptPath := filepath.Join(sessionStore.SessionDir(childSessionID), "transcript.ndjson")
	resultFile := filepath.Join(sessionStore.SessionDir(childSessionID), "agent-result.json")
	lifecycle := &childLifecycleTracker{}

	if err := persistSessionState(sessionStore, sessionStateParams{
		SessionID: childSessionID,
		CreatedAt: childStartedAt,
		Mode:      childMode,
		Model:     activeModelID,
		CWD:       cwd,
		Branch:    agent.LoadTurnContext().GitBranch,
		Tracker:   childTracker,
		Messages:  childMessages,
	}); err != nil {
		return toolpkg.AgentRunResult{}, err
	}
	_ = sessionStore.SaveMetadata(session.Metadata{
		SessionID:    childSessionID,
		CreatedAt:    childStartedAt,
		UpdatedAt:    childStartedAt,
		Mode:         string(childMode),
		Model:        activeModelID,
		CWD:          cwd,
		Branch:       agent.LoadTurnContext().GitBranch,
		TotalCostUSD: 0,
		Title:        req.Description,
	})

	childDeps := agent.QueryDeps{
		CallModel: func(callCtx context.Context, modelReq api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
			return trackModelStream(callCtx, childBridge, childTracker, client, modelReq)
		},
		ExecuteToolBatch: func(callCtx context.Context, calls []api.ToolCall) ([]api.ToolResult, error) {
			return executeToolCallsForSubagent(callCtx, subagentType, childRegistry, childPermissionCtx, artifactManager, childSessionID, sessionStore.SessionDir(childSessionID), childTracker, client.Capabilities().MaxOutputTokens, calls)
		},
		CompactMessages: func(callCtx context.Context, current []api.Message, reason agent.CompactReason) ([]api.Message, error) {
			result, err := compactWithMetrics(callCtx, childBridge, childTracker, client, childTimingLogger, childSessionID, 0, string(reason), current)
			if err != nil {
				return nil, err
			}
			return result.Messages, nil
		},
		RecallMemory: func(callCtx context.Context, files []agent.MemoryFile, userPrompt string) ([]agent.MemoryRecallResult, error) {
			selector := memoryRecallSelector{bridge: childBridge, tracker: childTracker, client: client}
			return selector.Select(callCtx, files, userPrompt)
		},
		BeforeStop: func(callCtx context.Context, stopReq agent.StopRequest) (agent.StopDecision, error) {
			return evaluateChildStopHooks(callCtx, hookRunner, childSessionID, invocationID, req, subagentType, stopReq, lifecycle, transcriptPath, resultFile, reportStatus)
		},
		StopController: stopControl,
		ApplyResultBudget: func(current []api.Message) []api.Message {
			return current
		},
		EmitTelemetry: childBridge.EmitEvent,
		PersistMessages: func(updated []api.Message) {
			childMessages = updated
			_ = persistSessionState(sessionStore, sessionStateParams{
				SessionID: childSessionID,
				CreatedAt: childStartedAt,
				Mode:      childMode,
				Model:     activeModelID,
				CWD:       cwd,
				Branch:    agent.LoadTurnContext().GitBranch,
				Tracker:   childTracker,
				Messages:  childMessages,
			})
		},
		Clock: time.Now,
	}

	stream := agent.QueryStream(ctx, agent.QueryRequest{
		Messages:        childMessages,
		SystemPrompt:    childPrompt,
		ModelID:         client.ModelID(),
		ReasoningEffort: config.Load().ReasoningEffort,
		Mode:            childMode,
		SessionID:       childSessionID,
		Skills:          childSkills,
		Tools:           childRegistry.Definitions(),
		Capabilities:    client.Capabilities(),
		ContextWindow:   client.Capabilities().MaxContextWindow,
		MaxTokens:       client.Capabilities().MaxOutputTokens,
	}, childDeps)

	turnStopReason := ""
	for event, streamErr := range stream {
		if streamErr != nil {
			runChildStopFailureHooks(ctx, hookRunner, childSessionID, invocationID, req, subagentType, childMessages, streamErr)
			return toolpkg.AgentRunResult{}, streamErr
		}
		if event.Type == ipc.EventTurnComplete {
			var payload ipc.TurnCompletePayload
			if err := json.Unmarshal(event.Payload, &payload); err == nil {
				turnStopReason = payload.StopReason
			}
		}
	}

	if err := persistSessionState(sessionStore, sessionStateParams{
		SessionID: childSessionID,
		CreatedAt: childStartedAt,
		Mode:      childMode,
		Model:     activeModelID,
		CWD:       cwd,
		Branch:    agent.LoadTurnContext().GitBranch,
		Tracker:   childTracker,
		Messages:  childMessages,
	}); err != nil {
		return toolpkg.AgentRunResult{}, err
	}

	status := "completed"
	errorMessage := ""
	if turnStopReason == "cancelled" {
		status = "cancelled"
		errorMessage = "background child agent cancelled"
	}

	childSnapshot := childTracker.Snapshot()
	parentTracker.RecordChildAgentSnapshot(childSnapshot)
	_ = emitCostUpdate(bridge, parentTracker)

	return toolpkg.AgentRunResult{
		Status:         status,
		InvocationID:   invocationID,
		SubagentType:   subagentType,
		SessionID:      childSessionID,
		TranscriptPath: transcriptPath,
		OutputFile:     resultFile,
		Summary:        latestAssistantContent(childMessages),
		Error:          errorMessage,
		TotalCostUSD:   childSnapshot.TotalCostUSD,
		InputTokens:    childSnapshot.TotalInputTokens,
		OutputTokens:   childSnapshot.TotalOutputTokens,
		Tools:          toolDefinitionNames(childRegistry.Definitions()),
		Metadata:       lifecycle.metadata(),
	}, nil
}

type childLifecycleTracker struct {
	stopBlockReason string
	stopBlockCount  int
}

func (s *childLifecycleTracker) noteStopBlock(reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "blocked by child stop hook"
	}
	s.stopBlockReason = reason
	s.stopBlockCount++
}

func (s *childLifecycleTracker) metadata() *toolpkg.ChildAgentMetadata {
	if s == nil || s.stopBlockCount == 0 {
		return nil
	}
	return &toolpkg.ChildAgentMetadata{
		StopBlockReason: s.stopBlockReason,
		StopBlockCount:  s.stopBlockCount,
	}
}

func runChildStartHooks(
	ctx context.Context,
	hookRunner *hooks.Runner,
	childSessionID string,
	invocationID string,
	req toolpkg.AgentRunRequest,
	subagentType string,
) []string {
	if hookRunner == nil {
		return nil
	}
	responses, _ := hookRunner.Run(ctx, hooks.Payload{
		Type:      hooks.HookSubagentStart,
		SessionID: childSessionID,
		Extra: map[string]any{
			"child_agent":       true,
			"agent_id":          invocationID,
			"agent_type":        subagentType,
			"invocation_id":     invocationID,
			"description":       req.Description,
			"prompt":            req.Prompt,
			"subagent_type":     subagentType,
			"run_in_background": req.Background,
		},
	})
	messages := make([]string, 0, len(responses))
	for _, resp := range responses {
		if message := strings.TrimSpace(resp.Message); message != "" {
			messages = append(messages, message)
		}
	}
	return messages
}

func injectChildHookContext(prompt string, hookMessages []string) string {
	prompt = strings.TrimSpace(prompt)
	if len(hookMessages) == 0 {
		return prompt
	}
	lines := make([]string, 0, len(hookMessages)+3)
	lines = append(lines, "Additional local child-agent context:")
	for _, message := range hookMessages {
		message = strings.TrimSpace(message)
		if message == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s", message))
	}
	if prompt != "" {
		lines = append(lines, "", "Delegated task:", prompt)
	}
	return strings.Join(lines, "\n")
}

func evaluateChildStopHooks(
	ctx context.Context,
	hookRunner *hooks.Runner,
	childSessionID string,
	invocationID string,
	req toolpkg.AgentRunRequest,
	subagentType string,
	stopReq agent.StopRequest,
	lifecycle *childLifecycleTracker,
	transcriptPath string,
	resultFile string,
	reportStatus func(toolpkg.AgentRunResult),
) (agent.StopDecision, error) {
	if hookRunner == nil {
		return agent.StopDecision{}, nil
	}
	responses, err := hookRunner.Run(ctx, hooks.Payload{
		Type:      hooks.HookSubagentStop,
		SessionID: childSessionID,
		Output:    stopReq.AssistantMessage.Content,
		Extra: map[string]any{
			"child_agent":       true,
			"agent_id":          invocationID,
			"agent_type":        subagentType,
			"invocation_id":     invocationID,
			"description":       req.Description,
			"subagent_type":     subagentType,
			"run_in_background": req.Background,
			"stop_reason":       stopReq.StopReason,
			"turn_count":        stopReq.TurnCount,
		},
	})
	if err != nil {
		return agent.StopDecision{}, err
	}
	blocked, reason := blockedChildStop(responses)
	if !blocked {
		return agent.StopDecision{}, nil
	}
	lifecycle.noteStopBlock(reason)
	if reportStatus != nil {
		reportStatus(toolpkg.AgentRunResult{
			Status:         "running",
			InvocationID:   invocationID,
			SubagentType:   subagentType,
			SessionID:      childSessionID,
			TranscriptPath: transcriptPath,
			OutputFile:     resultFile,
			Metadata: &toolpkg.ChildAgentMetadata{
				LifecycleState:  "stop_blocked",
				StatusMessage:   reason,
				StopBlockReason: lifecycle.stopBlockReason,
				StopBlockCount:  lifecycle.stopBlockCount,
			},
		})
	}
	return agent.StopDecision{
		Continue:        true,
		Reason:          reason,
		FollowUpMessage: childStopBlockedFollowUp(reason),
	}, nil
}

func runChildStopFailureHooks(
	ctx context.Context,
	hookRunner *hooks.Runner,
	childSessionID string,
	invocationID string,
	req toolpkg.AgentRunRequest,
	subagentType string,
	messages []api.Message,
	err error,
) {
	if hookRunner == nil || err == nil {
		return
	}
	_, _ = hookRunner.Run(ctx, hooks.Payload{
		Type:      hooks.HookSubagentStopFailure,
		SessionID: childSessionID,
		Output:    latestAssistantContent(messages),
		Error:     err.Error(),
		Extra: map[string]any{
			"child_agent":       true,
			"agent_id":          invocationID,
			"agent_type":        subagentType,
			"invocation_id":     invocationID,
			"description":       req.Description,
			"subagent_type":     subagentType,
			"run_in_background": req.Background,
		},
	})
}

func blockedChildStop(responses []hooks.Response) (bool, string) {
	for _, resp := range responses {
		action := strings.ToLower(strings.TrimSpace(resp.Action))
		if action != "deny" && action != "stop" {
			continue
		}
		reason := strings.TrimSpace(resp.Message)
		if reason == "" {
			reason = "blocked by child stop hook"
		}
		return true, reason
	}
	return false, ""
}

func childStopBlockedFollowUp(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "A local stop hook blocked completion. Continue working until the stop condition is satisfied."
	}
	return fmt.Sprintf("A local stop hook blocked completion: %s\n\nContinue working until the stop condition is satisfied.", reason)
}

func withChildMetadata(result toolpkg.AgentRunResult, description string) toolpkg.AgentRunResult {
	result.Metadata = buildChildMetadata(result, description)
	return result
}

func buildChildMetadata(result toolpkg.AgentRunResult, description string) *toolpkg.ChildAgentMetadata {
	invocationID := firstNonEmpty(result.InvocationID, result.SessionID)
	if invocationID == "" && result.AgentID == "" {
		return nil
	}
	metadata := &toolpkg.ChildAgentMetadata{}
	if result.Metadata != nil {
		*metadata = *result.Metadata
		metadata.Tools = append([]string(nil), result.Metadata.Tools...)
	}
	metadata.InvocationID = firstNonEmpty(invocationID, metadata.InvocationID)
	metadata.AgentID = firstNonEmpty(result.AgentID, metadata.AgentID)
	metadata.Description = firstNonEmpty(strings.TrimSpace(description), metadata.Description)
	metadata.SubagentType = firstNonEmpty(result.SubagentType, metadata.SubagentType)
	metadata.LifecycleState = childLifecycleState(result.Status, metadata.LifecycleState)
	if strings.TrimSpace(result.Summary) != "" || strings.TrimSpace(result.Error) != "" || strings.TrimSpace(metadata.StatusMessage) == "" {
		metadata.StatusMessage = childStatusMessage(result)
	}
	metadata.SessionID = firstNonEmpty(result.SessionID, metadata.SessionID)
	metadata.TranscriptPath = firstNonEmpty(result.TranscriptPath, metadata.TranscriptPath)
	metadata.ResultPath = firstNonEmpty(result.OutputFile, metadata.ResultPath)
	if len(result.Tools) > 0 {
		metadata.Tools = append([]string(nil), result.Tools...)
	}
	return metadata
}

func childLifecycleState(status string, existing string) string {
	switch strings.TrimSpace(status) {
	case "", "async_launched":
		return "launching"
	case "running":
		if strings.TrimSpace(existing) == "stop_blocked" {
			return existing
		}
		return "running"
	default:
		return strings.TrimSpace(status)
	}
}

func childStatusMessage(result toolpkg.AgentRunResult) string {
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		return summary
	}
	if errText := strings.TrimSpace(result.Error); errText != "" {
		return errText
	}
	switch strings.TrimSpace(result.Status) {
	case "async_launched":
		return "Background child agent launched."
	case "running":
		return "Background child agent is still running."
	case "cancelling":
		return "Cancellation requested for background child agent."
	case "completed":
		return "Background child agent completed."
	case "cancelled":
		return "Background child agent cancelled."
	case "failed":
		return "Background child agent failed."
	default:
		return "Child agent updated."
	}
}

func subagentSystemPrompt(subagentType string, defs []api.ToolDefinition) string {
	names := toolDefinitionNames(defs)
	toolList := strings.Join(names, ", ")
	common := fmt.Sprintf(`You are Go CLI %s, a bounded subagent running in a fresh context.

IMPORTANT: Always use absolute paths with file tools. The working directory is provided in the environment context below.
Use only the tools exposed to you in this session. The exact runtime tool names available are: %s.`, subagentDisplayName(subagentType), toolList)

	switch subagentType {
	case searchSubagentType:
		return strings.TrimSpace(fmt.Sprintf(`%s

You are an AI coding research assistant that uses search tools to gather information. Stay workspace-focused: search the repository, inspect files, and return compact references instead of long prose.
This subagent is read-only and artifact-safe: do not modify files, do not create or update session artifacts, and do not attempt background process control.
Search iteratively until you have enough evidence. Prefer concise findings tied to concrete file paths.

Once you have thoroughly searched the repository, return a message with ONLY the <final_answer> tag containing relevant absolute file paths and line ranges.

Example:
<final_answer>
/absolute/path/to/file.go:10-40
/absolute/path/to/other.go:88-130
</final_answer>`, common))
	case executionSubagentType:
		return strings.TrimSpace(fmt.Sprintf(`%s

You are a terminal-focused execution assistant. You may run commands and adapt them as needed to complete the delegated task efficiently.
This subagent is artifact-safe and non-writing by default: do not modify files, do not create or update session artifacts, and do not attempt background process control beyond the command-management tools already provided.
There is no interactive approval path inside the child session. Any execute action that the cloned permission policy would not auto-approve will be denied. If a command is denied, report that clearly and continue with any safe read-only inspection that still helps.

Once you have finished, return a message with ONLY the <final_answer> tag containing a compact summary of each important command that was run.

Example:
<final_answer>
Command: go test ./...
Summary: 2 packages failed. Key error excerpt: ...

Command: go test ./internal/api
Summary: Package passes after isolating the failure.
</final_answer>`, common))
	case generalPurposeSubagentType:
		return strings.TrimSpace(fmt.Sprintf(`%s

This subagent is artifact-safe: do not create or update session artifacts. You may use broader tools, but there is no interactive approval path inside the child session. Any write or execute action that the cloned permission policy would not auto-approve will be denied. Avoid background process control tools.
Work only on the delegated task. Keep the final response concise and report concrete outcomes, files, and next steps when useful.`, common))
	default:
		return strings.TrimSpace(fmt.Sprintf(`%s

This subagent is read-only and artifact-safe: do not modify files, do not create or update session artifacts, and do not attempt background process control.
Work only on the delegated task. Prefer parallel read-only exploration where it helps. Keep the final response concise and report concrete findings with file paths and next steps when useful.`, common))
	}
}

func subagentToolNames(subagentType string) []string {
	switch subagentType {
	case searchSubagentType:
		return searchSubagentTools
	case executionSubagentType:
		return executionSubagentTools
	case generalPurposeSubagentType:
		return generalPurposeSubagentTools
	default:
		return exploreSubagentTools
	}
}

func toolDefinitionNames(defs []api.ToolDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}

func latestAssistantContent(messages []api.Message) string {
	for index := len(messages) - 1; index >= 0; index-- {
		msg := messages[index]
		if msg.Role != api.RoleAssistant {
			continue
		}
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		return normalizeSubagentFinalAnswer(msg.Content)
	}
	return "Subagent completed without a final text response. See the child transcript for details."
}

func executeToolCallsForSubagent(
	ctx context.Context,
	subagentType string,
	registry *toolpkg.Registry,
	permissionCtx *permissions.Context,
	artifactManager *artifactspkg.Manager,
	sessionID string,
	sessionDir string,
	tracker *costpkg.Tracker,
	maxOutputTokens int,
	calls []api.ToolCall,
) ([]api.ToolResult, error) {
	results := make([]api.ToolResult, len(calls))
	pending := make([]toolpkg.PendingCall, 0, len(calls))
	budget := toolpkg.DefaultResultBudgetForModel(sessionDir, maxOutputTokens)
	aggregateBudget := toolpkg.NewAggregateResultBudget(budget)
	permissionGate := permissions.ExecutorGate{Context: permissionCtx}

	for index, call := range calls {
		normalized, err := normalizeToolCall(call)
		if err != nil {
			results[index] = api.ToolResult{ToolCallID: call.ID, Output: err.Error(), IsError: true}
			continue
		}
		tool, err := registry.Get(normalized.Name)
		if err != nil {
			results[index] = api.ToolResult{ToolCallID: normalized.ID, Output: err.Error(), IsError: true}
			continue
		}
		input, err := decodeToolInput(normalized)
		if err != nil {
			results[index] = api.ToolResult{ToolCallID: normalized.ID, Output: err.Error(), IsError: true}
			continue
		}
		if err := toolpkg.ValidateToolCall(tool, input); err != nil {
			results[index] = api.ToolResult{ToolCallID: normalized.ID, Output: err.Error(), IsError: true}
			continue
		}
		if !subagentAllowsTool(subagentType, tool.Permission()) {
			results[index] = api.ToolResult{ToolCallID: normalized.ID, Output: fmt.Sprintf("tool %q is not allowed in the %s subagent", tool.Name(), subagentType), IsError: true}
			continue
		}
		pending = append(pending, toolpkg.PendingCall{Index: index, Tool: tool, Input: input})
	}

	for _, batch := range toolpkg.PartitionBatches(pending) {
		batchStart := time.Now()
		batchResults := toolpkg.ExecuteBatchWithOptions(ctx, batch, toolpkg.ExecuteOptions{
			PermissionGate: permissionGate.Check,
		})
		if tracker != nil {
			tracker.RecordToolDuration(time.Since(batchStart))
		}
		for _, result := range batchResults {
			call := calls[result.Index]
			toolResult := api.ToolResult{ToolCallID: call.ID}
			if result.Err != nil {
				toolResult.Output = result.Err.Error()
				toolResult.IsError = true
				results[result.Index] = toolResult
				continue
			}

			output := result.Output.Output
			if !result.Output.IsError {
				budgetedOutput, _, _, err := budgetToolOutput(ctx, artifactManager, sessionID, budget, aggregateBudget, call, output)
				if err == nil {
					output = budgetedOutput
				}
			}
			toolResult.Output = output
			toolResult.IsError = result.Output.IsError
			results[result.Index] = toolResult
		}
	}

	return results, nil
}

func subagentDisplayName(subagentType string) string {
	switch strings.TrimSpace(subagentType) {
	case searchSubagentType:
		return "Search"
	case executionSubagentType:
		return "Execution"
	case generalPurposeSubagentType:
		return "General Purpose"
	default:
		return "Explore"
	}
}

func subagentAllowsTool(subagentType string, permission toolpkg.PermissionLevel) bool {
	switch strings.TrimSpace(subagentType) {
	case exploreSubagentType, searchSubagentType:
		return permission == toolpkg.PermissionReadOnly
	case executionSubagentType:
		return permission != toolpkg.PermissionWrite
	default:
		return true
	}
}

func normalizeSubagentFinalAnswer(content string) string {
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	start := strings.Index(lower, "<final_answer>")
	end := strings.LastIndex(lower, "</final_answer>")
	if start == -1 || end == -1 || end <= start {
		return trimmed
	}
	inner := strings.TrimSpace(trimmed[start+len("<final_answer>") : end])
	if inner == "" {
		return trimmed
	}
	return inner
}
