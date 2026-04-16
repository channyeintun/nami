package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/channyeintun/chan/internal/agent"
	"github.com/channyeintun/chan/internal/api"
	artifactspkg "github.com/channyeintun/chan/internal/artifacts"
	"github.com/channyeintun/chan/internal/compact"
	"github.com/channyeintun/chan/internal/config"
	costpkg "github.com/channyeintun/chan/internal/cost"
	"github.com/channyeintun/chan/internal/hooks"
	"github.com/channyeintun/chan/internal/ipc"
	memorypkg "github.com/channyeintun/chan/internal/memory"
	"github.com/channyeintun/chan/internal/permissions"
	"github.com/channyeintun/chan/internal/session"
	"github.com/channyeintun/chan/internal/timing"
	toolpkg "github.com/channyeintun/chan/internal/tools"
)

const exploreSubagentType = "Explore"
const generalPurposeSubagentType = "general-purpose"
const verificationSubagentType = "verification"

const (
	delegationPromptArchiveName  = "delegation-prompt.txt"
	delegationPromptArchiveLimit = 1800
	delegationPromptBriefLimit   = 1400
	delegationPromptLineLimit    = 6
	delegationPromptAnchorLimit  = 8
)

var delegatedPromptAnchorPattern = regexp.MustCompile(`(?:/[A-Za-z0-9._-]+)+(?:\.[A-Za-z0-9._-]+)?|(?:[A-Za-z0-9._-]+/)+[A-Za-z0-9._-]+(?:\.[A-Za-z0-9._-]+)?|` + "`[^`]+`")

var exploreSubagentTools = []string{
	"think",
	"list_dir",
	"read_file",
	"file_diff_preview",
	"file_search",
	"grep_search",
	"go_definition",
	"go_references",
	"read_project_structure",
	"project_overview",
	"dependency_overview",
	"symbol_search",
	"web_search",
	"web_fetch",
	"git",
}

var verificationSubagentTools = []string{
	"bash",
	"list_commands",
	"command_status",
	"send_command_input",
	"stop_command",
	"forget_command",
	"list_dir",
	"read_file",
	"file_diff_preview",
	"file_search",
	"grep_search",
	"go_definition",
	"go_references",
	"read_project_structure",
	"project_overview",
	"dependency_overview",
	"symbol_search",
	"web_fetch",
	"git",
	"think",
}

var generalPurposeSubagentTools = []string{
	"bash",
	"think",
	"list_dir",
	"read_file",
	"file_diff_preview",
	"create_file",
	"file_write",
	"replace_string_in_file",
	"multi_replace_string_in_file",
	"apply_patch",
	"file_search",
	"grep_search",
	"go_definition",
	"go_references",
	"read_project_structure",
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
	modelState *ActiveModelState,
	subagentModelState *ActiveSubagentModelState,
	cwd string,
) toolpkg.AgentRunner {
	return func(ctx context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		client, activeModelID := modelState.Get()
		if client == nil {
			return toolpkg.AgentRunResult{}, fmt.Errorf("agent tool is unavailable: model client is not initialized")
		}

		childClient, childActiveModelID, err := resolveSubagentClient(client, activeModelID, subagentModelState)
		if err != nil {
			return toolpkg.AgentRunResult{}, err
		}

		subagentType := toolpkg.NormalizeSubagentType(req.SubagentType)
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

func resolveSubagentClient(parent api.LLMClient, activeModelID string, subagentModelState *ActiveSubagentModelState) (api.LLMClient, string, error) {
	provider, activeModel := config.ParseModel(strings.TrimSpace(activeModelID))
	provider = normalizeProvider(provider)
	if strings.TrimSpace(activeModel) == "" {
		activeModel = strings.TrimSpace(provider)
		provider = ""
	}

	cfg := config.Load()
	selection := strings.TrimSpace(cfg.SubagentModel)
	if subagentModelState != nil {
		if current := strings.TrimSpace(subagentModelState.Get()); current != "" {
			selection = current
		}
	}
	if selection == "" {
		selection = defaultSessionSubagentModel(cfg, activeModelID)
	}

	childProvider, childModel := resolveModelSelection(selection, provider)
	if childProvider == provider && strings.EqualFold(strings.TrimSpace(childModel), strings.TrimSpace(activeModel)) {
		return parent, modelRef(provider, activeModel), nil
	}
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
	childModelID string,
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
	childSkills, err := loadAvailableSkills(bridge, cwd)
	if err != nil {
		return toolpkg.AgentRunResult{}, err
	}
	childMode := agent.ModeFast
	startHookMessages := runChildStartHooks(ctx, hookRunner, childSessionID, invocationID, req, subagentType)
	promptArchivePath, archiveErr := archiveDelegatedPrompt(sessionStore.SessionDir(childSessionID), req.Description, req.Prompt)
	if archiveErr != nil && bridge != nil {
		_ = bridge.EmitNotice(fmt.Sprintf("archive child prompt: %v", archiveErr))
	}
	childHandoff := buildDelegatedPromptBrief(req.Description, req.Prompt, subagentType, promptArchivePath)
	childMessages := []api.Message{{Role: api.RoleUser, Content: injectChildHookContext(childHandoff, startHookMessages)}}
	allowedDefs := childRegistry.Definitions()
	childPrompt := subagentSystemPrompt(subagentType, allowedDefs)
	queryTools := allowedDefs
	executionRegistry := childRegistry
	transcriptPath := filepath.Join(sessionStore.SessionDir(childSessionID), "transcript.ndjson")
	resultFile := filepath.Join(sessionStore.SessionDir(childSessionID), "agent-result.json")
	lifecycle := &childLifecycleTracker{}

	if err := persistSessionState(sessionStore, sessionStateParams{
		SessionID:     childSessionID,
		CreatedAt:     childStartedAt,
		Mode:          childMode,
		Model:         childModelID,
		SubagentModel: "",
		CWD:           cwd,
		Branch:        agent.LoadTurnContext().GitBranch,
		Tracker:       childTracker,
		Messages:      childMessages,
	}); err != nil {
		return toolpkg.AgentRunResult{}, err
	}
	_ = sessionStore.SaveMetadata(session.Metadata{
		SessionID:     childSessionID,
		CreatedAt:     childStartedAt,
		UpdatedAt:     childStartedAt,
		Mode:          string(childMode),
		Model:         childModelID,
		SubagentModel: "",
		CWD:           cwd,
		Branch:        agent.LoadTurnContext().GitBranch,
		TotalCostUSD:  0,
		Title:         req.Description,
	})

	childDeps := agent.QueryDeps{
		CallModel: func(callCtx context.Context, modelReq api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
			return trackModelStream(callCtx, childBridge, childTracker, client, modelReq)
		},
		ExecuteToolBatch: func(callCtx context.Context, calls []api.ToolCall) ([]api.ToolResult, error) {
			return executeToolCallsForSubagent(callCtx, subagentType, executionRegistry, childPermissionCtx, artifactManager, childSessionID, sessionStore.SessionDir(childSessionID), childTracker, client.Capabilities().MaxOutputTokens, calls)
		},
		CompactMessages: func(callCtx context.Context, current []api.Message, reason agent.CompactReason) (compact.CompactResult, error) {
			sessionMemory, _ := loadSessionMemorySnapshot(callCtx, artifactManager, childSessionID)
			return compactWithMetrics(callCtx, childBridge, childTracker, client, childTimingLogger, childSessionID, 0, string(reason), sessionMemory, childPrompt, queryTools, current)
		},
		RecallMemory: func(callCtx context.Context, files []agent.MemoryFile, userPrompt string) ([]agent.MemoryRecallResult, error) {
			selector := memorypkg.RecallSelector{}
			return selector.Select(callCtx, files, userPrompt)
		},
		LoadSessionMemory: func(callCtx context.Context) (agent.SessionMemorySnapshot, error) {
			return loadSessionMemorySnapshot(callCtx, artifactManager, childSessionID)
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
				SessionID:     childSessionID,
				CreatedAt:     childStartedAt,
				Mode:          childMode,
				Model:         childModelID,
				SubagentModel: "",
				CWD:           cwd,
				Branch:        agent.LoadTurnContext().GitBranch,
				Tracker:       childTracker,
				Messages:      childMessages,
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
		Tools:           queryTools,
		Capabilities:    client.Capabilities(),
		ContextWindow:   client.Capabilities().MaxContextWindow,
		MaxTokens:       client.Capabilities().MaxOutputTokens,
		SessionMemory:   agent.SessionMemorySnapshot{},
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
		SessionID:     childSessionID,
		CreatedAt:     childStartedAt,
		Mode:          childMode,
		Model:         childModelID,
		SubagentModel: "",
		CWD:           cwd,
		Branch:        agent.LoadTurnContext().GitBranch,
		Tracker:       childTracker,
		Messages:      childMessages,
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

	result := toolpkg.AgentRunResult{
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
	}
	writeBackgroundAgentResultFile(result)
	return result, nil
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
	subagentType = toolpkg.NormalizeSubagentType(subagentType)
	names := toolDefinitionNames(defs)
	toolList := strings.Join(names, ", ")
	common := fmt.Sprintf(`You are Go CLI %s, bounded subagent in fresh context.
Use tools early. Keep transcript terse. Final answer concise, concrete, evidence-first.
Always absolute paths. Working directory in environment context below.
No follow-up questions to the parent/user. If context is thin, inspect workspace, make best reasonable assumptions, continue. State assumptions briefly in final answer.
Available tools: %s.
If additional tool schemas appear for cache compatibility, treat any tool not listed above as unavailable; those calls will be rejected.`, subagentDisplayName(subagentType), toolList)

	switch subagentType {
	case exploreSubagentType:
		return strings.TrimSpace(fmt.Sprintf(`%s

Read-only codebase explorer.
Search broad first. Narrow after evidence.
If first pass is weak, run another pass with different anchors.
Prefer file_search, grep_search, read_file, project_overview, read_project_structure, go_definition, go_references.
No file writes. No artifact writes. No background control.

Return ONLY <final_answer>.

Format:
Scope: <one sentence>
Findings:
- <fact>
Evidence:
- /absolute/path/to/file:10-40
Open questions:
- <only if needed>

Example:
<final_answer>
Scope: trace auth token refresh path
Findings:
- Token refresh starts in /absolute/path/to/auth.go:44-91 and retries once in /absolute/path/to/client.go:120-168.
Evidence:
- /absolute/path/to/auth.go:44-91
- /absolute/path/to/client.go:120-168
</final_answer>`, common))
	case verificationSubagentType:
		return strings.TrimSpace(fmt.Sprintf(`%s

Verification specialist. Try to break the work.
Reading code is not verification. Run commands. Check output.
Do not modify project files. Do not install deps. Do not run git write commands.
No artifact writes. No background control beyond provided command tools.
If environment blocks verification, say exactly why.

Return ONLY <final_answer>.

Format:
Checks:
- <command> -> <result>
Findings:
- <fact>
VERDICT: PASS|FAIL|PARTIAL

Example:
<final_answer>
Checks:
- go test ./... -> FAIL: ./internal/api timeout in TestStream
Findings:
- Reproduced failure. No evidence of fix.
VERDICT: FAIL
</final_answer>`, common))
	case generalPurposeSubagentType:
		return strings.TrimSpace(fmt.Sprintf(`%s

General-purpose subagent.
Complete delegated task fully. Research first when needed. Edit only when task requires it.
No artifact writes. No interactive approval. If denied, explain constraint, then continue with safe inspection.

Return ONLY <final_answer>.

Format:
Scope: <one sentence>
Done:
- <completed work>
Evidence:
- /absolute/path/to/file:10-40
Next:
- <only if needed>`, common))
	default:
		return strings.TrimSpace(fmt.Sprintf(`%s

Read-only, artifact-safe: no file modifications, no artifact writes, no background process control.
Delegated task only. Parallel read-only exploration when helpful. Concise findings with paths and next steps.`, common))
	}
}

func subagentToolNames(subagentType string) []string {
	switch toolpkg.NormalizeSubagentType(subagentType) {
	case generalPurposeSubagentType:
		return generalPurposeSubagentTools
	case verificationSubagentType:
		return verificationSubagentTools
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
		if !subagentAllowsToolName(subagentType, tool.Name()) {
			results[index] = api.ToolResult{ToolCallID: normalized.ID, Output: fmt.Sprintf("tool %q is not allowed in the %s subagent", tool.Name(), subagentType), IsError: true}
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
			toolResult := api.ToolResult{ToolCallID: call.ID, FilePath: result.Output.FilePath}
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
	switch toolpkg.NormalizeSubagentType(subagentType) {
	case generalPurposeSubagentType:
		return "General Purpose"
	case verificationSubagentType:
		return "Verification"
	default:
		return "Explore"
	}
}

func subagentAllowsTool(subagentType string, permission toolpkg.PermissionLevel) bool {
	switch toolpkg.NormalizeSubagentType(subagentType) {
	case exploreSubagentType:
		return permission == toolpkg.PermissionReadOnly
	case verificationSubagentType:
		return permission != toolpkg.PermissionWrite
	default:
		return true
	}
}

func subagentAllowsToolName(subagentType string, toolName string) bool {
	allowed := subagentToolNames(subagentType)
	for _, name := range allowed {
		if name == toolName {
			return true
		}
	}
	return false
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

func archiveDelegatedPrompt(sessionDir string, description string, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" || utf8.RuneCountInString(prompt) <= delegationPromptArchiveLimit {
		return "", nil
	}
	if err := os.MkdirAll(sessionDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(sessionDir, delegationPromptArchiveName)
	var builder strings.Builder
	if strings.TrimSpace(description) != "" {
		builder.WriteString("Description: ")
		builder.WriteString(strings.TrimSpace(description))
		builder.WriteString("\n\n")
	}
	builder.WriteString(prompt)
	builder.WriteString("\n")
	if err := os.WriteFile(path, []byte(builder.String()), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

func buildDelegatedPromptBrief(description string, prompt string, subagentType string, archivePath string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	if utf8.RuneCountInString(prompt) <= delegationPromptArchiveLimit {
		return prompt
	}

	briefLines := collectDelegatedPromptLines(prompt, delegationPromptLineLimit, delegationPromptBriefLimit)
	anchors := extractDelegatedPromptAnchors(prompt, delegationPromptAnchorLimit)

	var builder strings.Builder
	builder.WriteString("Delegated task brief:\n")
	if strings.TrimSpace(description) != "" {
		builder.WriteString("- Summary: ")
		builder.WriteString(strings.TrimSpace(description))
		builder.WriteString("\n")
	}
	builder.WriteString("- Subagent: ")
	builder.WriteString(subagentDisplayName(subagentType))
	builder.WriteString("\n")
	if len(briefLines) > 0 {
		builder.WriteString("- Key instructions:\n")
		for _, line := range briefLines {
			builder.WriteString("  - ")
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}
	if len(anchors) > 0 {
		builder.WriteString("- Key anchors:\n")
		for _, anchor := range anchors {
			builder.WriteString("  - ")
			builder.WriteString(anchor)
			builder.WriteString("\n")
		}
	}
	if archivePath != "" {
		builder.WriteString("- Full delegated prompt saved at ")
		builder.WriteString(archivePath)
		builder.WriteString(". Read it only if the brief is insufficient.\n")
	}
	return strings.TrimSpace(builder.String())
}

func collectDelegatedPromptLines(prompt string, maxLines int, maxChars int) []string {
	lines := strings.Split(prompt, "\n")
	selected := make([]string, 0, maxLines)
	seen := make(map[string]struct{}, maxLines)
	totalChars := 0
	for _, raw := range lines {
		line := normalizeDelegatedPromptLine(raw)
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		if totalChars+len(line) > maxChars && len(selected) > 0 {
			break
		}
		selected = append(selected, line)
		seen[line] = struct{}{}
		totalChars += len(line)
		if len(selected) >= maxLines {
			break
		}
	}
	return selected
}

func normalizeDelegatedPromptLine(value string) string {
	line := strings.TrimSpace(value)
	if line == "" {
		return ""
	}
	line = strings.TrimPrefix(line, "- ")
	line = strings.TrimPrefix(line, "* ")
	line = strings.TrimSpace(line)
	if line == "" || line == "```" {
		return ""
	}
	line = strings.Join(strings.Fields(line), " ")
	if len(line) > 220 {
		line = strings.TrimSpace(line[:220])
	}
	return line
}

func extractDelegatedPromptAnchors(prompt string, limit int) []string {
	matches := delegatedPromptAnchorPattern.FindAllString(prompt, -1)
	anchors := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, match := range matches {
		anchor := strings.TrimSpace(strings.Trim(match, "`"))
		if anchor == "" {
			continue
		}
		if _, ok := seen[anchor]; ok {
			continue
		}
		seen[anchor] = struct{}{}
		anchors = append(anchors, anchor)
		if len(anchors) >= limit {
			break
		}
	}
	return anchors
}
