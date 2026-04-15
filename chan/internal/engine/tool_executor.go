package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/channyeintun/chan/internal/agent"
	"github.com/channyeintun/chan/internal/api"
	artifactspkg "github.com/channyeintun/chan/internal/artifacts"
	costpkg "github.com/channyeintun/chan/internal/cost"
	"github.com/channyeintun/chan/internal/hooks"
	"github.com/channyeintun/chan/internal/ipc"
	"github.com/channyeintun/chan/internal/permissions"
	"github.com/channyeintun/chan/internal/timing"
	toolpkg "github.com/channyeintun/chan/internal/tools"
)

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
	turnMetrics *timing.CheckpointRecorder,
	turnStats *turnExecutionStats,
	calls []api.ToolCall,
) ([]api.ToolResult, error) {
	execState := newToolExecutionState(calls, maxOutputTokens, turnStats)
	if err := prepareToolCalls(ctx, bridge, router, registry, permissionCtx, planner, hookRunner, sessionID, calls, execState); err != nil {
		return nil, err
	}
	if err := executeApprovedToolBatches(ctx, bridge, tracker, artifactManager, hookRunner, sessionID, turnMetrics, turnStats, calls, execState); err != nil {
		return nil, err
	}
	if execState.pauseForPlanReview {
		return compactToolResults(execState.results), &agent.PauseForPlanReviewError{}
	}
	return execState.results, nil
}

type toolExecutionState struct {
	results            []api.ToolResult
	approved           []toolpkg.PendingCall
	approvalFeedback   map[int]string
	budget             toolpkg.ResultBudget
	aggregateBudget    *toolpkg.AggregateResultBudget
	pauseForPlanReview bool
	planSavedThisTurn  bool
}

func newToolExecutionState(calls []api.ToolCall, maxOutputTokens int, turnStats *turnExecutionStats) *toolExecutionState {
	budget := toolpkg.DefaultResultBudgetForModel(filepath.Join(os.TempDir(), "chan-session"), maxOutputTokens)
	aggregateBudget := toolpkg.NewAggregateResultBudget(budget)
	if turnStats != nil {
		turnStats.AggregateBudgetChars = aggregateBudget.MaxChars()
	}
	return &toolExecutionState{
		results:          make([]api.ToolResult, len(calls)),
		approved:         make([]toolpkg.PendingCall, 0, len(calls)),
		approvalFeedback: make(map[int]string, len(calls)),
		budget:           budget,
		aggregateBudget:  aggregateBudget,
	}
}

func prepareToolCalls(
	ctx context.Context,
	bridge *ipc.Bridge,
	router *ipc.MessageRouter,
	registry *toolpkg.Registry,
	permissionCtx *permissions.Context,
	planner *agent.Planner,
	hookRunner *hooks.Runner,
	sessionID string,
	calls []api.ToolCall,
	state *toolExecutionState,
) error {
	for index, call := range calls {
		shouldContinue, err := prepareToolCall(ctx, bridge, router, registry, permissionCtx, planner, hookRunner, sessionID, calls, index, call, state)
		if err != nil || !shouldContinue {
			return err
		}
	}
	return nil
}

func prepareToolCall(
	ctx context.Context,
	bridge *ipc.Bridge,
	router *ipc.MessageRouter,
	registry *toolpkg.Registry,
	permissionCtx *permissions.Context,
	planner *agent.Planner,
	hookRunner *hooks.Runner,
	sessionID string,
	calls []api.ToolCall,
	index int,
	call api.ToolCall,
	state *toolExecutionState,
) (bool, error) {
	call, tool, input, pendingCall, err := resolvePendingToolCall(registry, index, call)
	if err != nil {
		return true, recordToolPreparationError(bridge, state.results, index, calls[index], err)
	}
	if state.planSavedThisTurn && pendingCall.Tool.Permission() == toolpkg.PermissionWrite {
		state.pauseForPlanReview = true
		return false, nil
	}
	allowed, err := validatePlannedTool(ctx, planner, call, pendingCall, state.results, bridge)
	if err != nil {
		var reviewRequired *agent.PlanReviewRequiredError
		if errors.As(err, &reviewRequired) {
			state.pauseForPlanReview = true
			return false, nil
		}
		return true, nil
	}
	if !allowed {
		return true, nil
	}
	authorization, err := authorizeToolCall(ctx, bridge, router, permissionCtx, call.ID, pendingCall)
	if err != nil {
		return false, err
	}
	if !authorization.Allowed {
		state.results[index] = api.ToolResult{ToolCallID: call.ID, Output: authorization.DenyReason, IsError: true}
		return true, emitToolError(bridge, call, authorization.DenyReason, toolpkg.ToolOutput{}, nil)
	}
	if authorization.Feedback != "" {
		state.approvalFeedback[index] = authorization.Feedback
	}
	hookDenied, err := runPreToolUseHooks(ctx, hookRunner, sessionID, call, index, state.results, state.approvalFeedback[index], bridge)
	if err != nil {
		return false, err
	}
	if hookDenied {
		return true, nil
	}
	if err := emitToolStart(bridge, call); err != nil {
		return false, err
	}
	state.approved = append(state.approved, toolpkg.PendingCall{Index: index, Tool: tool, Input: input})
	if pendingCall.Tool.Name() == "save_implementation_plan" {
		state.planSavedThisTurn = true
	}
	return true, nil
}

func resolvePendingToolCall(registry *toolpkg.Registry, index int, call api.ToolCall) (api.ToolCall, toolpkg.Tool, toolpkg.ToolInput, toolpkg.PendingCall, error) {
	normalizedCall, err := normalizeToolCall(call)
	if err != nil {
		return api.ToolCall{}, nil, toolpkg.ToolInput{}, toolpkg.PendingCall{}, err
	}
	tool, err := registry.Get(normalizedCall.Name)
	if err != nil {
		return normalizedCall, nil, toolpkg.ToolInput{}, toolpkg.PendingCall{}, err
	}
	input, err := decodeToolInput(normalizedCall)
	if err != nil {
		return normalizedCall, nil, toolpkg.ToolInput{}, toolpkg.PendingCall{}, err
	}
	if err := toolpkg.ValidateToolCall(tool, input); err != nil {
		return normalizedCall, nil, toolpkg.ToolInput{}, toolpkg.PendingCall{}, err
	}
	pendingCall := toolpkg.PendingCall{Index: index, Tool: tool, Input: input}
	return normalizedCall, tool, input, pendingCall, nil
}

func validatePlannedTool(
	ctx context.Context,
	planner *agent.Planner,
	call api.ToolCall,
	pendingCall toolpkg.PendingCall,
	results []api.ToolResult,
	bridge *ipc.Bridge,
) (bool, error) {
	if err := planner.ValidateTool(ctx, pendingCall.Tool.Name(), pendingCall.Tool.Permission()); err != nil {
		var reviewRequired *agent.PlanReviewRequiredError
		if errors.As(err, &reviewRequired) {
			return false, err
		}
		results[pendingCall.Index] = api.ToolResult{ToolCallID: call.ID, Output: err.Error(), IsError: true}
		if emitErr := emitToolError(bridge, call, err.Error(), toolpkg.ToolOutput{}, err); emitErr != nil {
			return false, emitErr
		}
		return false, nil
	}
	return true, nil
}

func runPreToolUseHooks(
	ctx context.Context,
	hookRunner *hooks.Runner,
	sessionID string,
	call api.ToolCall,
	index int,
	results []api.ToolResult,
	feedback string,
	bridge *ipc.Bridge,
) (bool, error) {
	if hookRunner == nil {
		return false, nil
	}
	responses, _ := hookRunner.Run(ctx, hooks.Payload{
		Type:      hooks.HookPreToolUse,
		SessionID: sessionID,
		ToolName:  call.Name,
		ToolInput: call.Input,
	})
	for _, resp := range responses {
		if resp.Action != "deny" {
			continue
		}
		reason := resp.Message
		if reason == "" {
			reason = "blocked by pre_tool_use hook"
		}
		reason = appendPermissionFeedback(reason, feedback)
		results[index] = api.ToolResult{ToolCallID: call.ID, Output: reason, IsError: true}
		return true, emitToolError(bridge, call, reason, toolpkg.ToolOutput{}, nil)
	}
	return false, nil
}

func emitToolStart(bridge *ipc.Bridge, call api.ToolCall) error {
	return bridge.Emit(ipc.EventToolStart, ipc.ToolStartPayload{ToolID: call.ID, Name: call.Name, Input: call.Input})
}

func recordToolPreparationError(bridge *ipc.Bridge, results []api.ToolResult, index int, call api.ToolCall, err error) error {
	results[index] = api.ToolResult{ToolCallID: call.ID, Output: err.Error(), IsError: true}
	return emitToolError(bridge, call, err.Error(), toolpkg.ToolOutput{}, err)
}

func executeApprovedToolBatches(
	ctx context.Context,
	bridge *ipc.Bridge,
	tracker *costpkg.Tracker,
	artifactManager *artifactspkg.Manager,
	hookRunner *hooks.Runner,
	sessionID string,
	turnMetrics *timing.CheckpointRecorder,
	turnStats *turnExecutionStats,
	calls []api.ToolCall,
	state *toolExecutionState,
) error {
	for _, batch := range toolpkg.PartitionBatches(state.approved) {
		batchStart := time.Now()
		batchResults := toolpkg.ExecuteBatch(ctx, batch)
		tracker.RecordToolDuration(time.Since(batchStart))
		for _, result := range batchResults {
			if err := handleToolBatchResult(ctx, bridge, artifactManager, hookRunner, sessionID, turnMetrics, turnStats, calls[result.Index], result, state); err != nil {
				return err
			}
		}
	}
	return nil
}

func handleToolBatchResult(
	ctx context.Context,
	bridge *ipc.Bridge,
	artifactManager *artifactspkg.Manager,
	hookRunner *hooks.Runner,
	sessionID string,
	turnMetrics *timing.CheckpointRecorder,
	turnStats *turnExecutionStats,
	call api.ToolCall,
	result toolpkg.IndexedResult,
	state *toolExecutionState,
) error {
	toolResult := api.ToolResult{ToolCallID: call.ID, FilePath: result.Output.FilePath}
	feedback := state.approvalFeedback[result.Index]
	if result.Err != nil {
		toolResult.Output = appendPermissionFeedback(result.Err.Error(), feedback)
		toolResult.IsError = true
		state.results[result.Index] = toolResult
		return emitToolError(bridge, call, toolResult.Output, result.Output, result.Err)
	}
	output, truncated, err := finalizeToolOutput(ctx, bridge, artifactManager, sessionID, turnStats, call, result.Output, state, feedback)
	if err != nil {
		return err
	}
	toolResult.Output = output
	toolResult.IsError = result.Output.IsError
	state.results[result.Index] = toolResult
	if result.Output.IsError {
		return emitToolError(bridge, call, output, result.Output, nil)
	}
	if err := emitToolArtifacts(bridge, result.Output.Artifacts, turnMetrics); err != nil {
		return err
	}
	if err := markFirstToolResult(bridge, turnMetrics); err != nil {
		return err
	}
	if err := bridge.Emit(ipc.EventToolResult, ipc.ToolResultPayload{
		ToolID:      call.ID,
		Output:      output,
		Truncated:   truncated,
		Name:        call.Name,
		Input:       call.Input,
		FilePath:    result.Output.FilePath,
		Preview:     result.Output.Preview,
		Insertions:  result.Output.Insertions,
		Deletions:   result.Output.Deletions,
		Diagnostics: result.Output.Diagnostics,
		ErrorKind:   result.Output.ErrorKind,
		ErrorHint:   result.Output.ErrorHint,
	}); err != nil {
		return err
	}
	runPostToolUseHooks(ctx, hookRunner, sessionID, call, output)
	return nil
}

func finalizeToolOutput(
	ctx context.Context,
	bridge *ipc.Bridge,
	artifactManager *artifactspkg.Manager,
	sessionID string,
	turnStats *turnExecutionStats,
	call api.ToolCall,
	output toolpkg.ToolOutput,
	state *toolExecutionState,
	feedback string,
) (string, bool, error) {
	finalOutput := output.Output
	spillPath := output.SpillPath
	truncated := output.Truncated
	if !output.IsError {
		budgetedOutput, artifact, budgetInfo, err := budgetToolOutput(ctx, artifactManager, sessionID, state.budget, state.aggregateBudget, call, finalOutput)
		finalOutput = budgetedOutput
		if err != nil {
			if emitErr := bridge.EmitError(fmt.Sprintf("persist tool-log artifact: %v", err), true); emitErr != nil {
				return "", false, emitErr
			}
		}
		updateTurnToolStats(turnStats, budgetInfo)
		if artifact.ID != "" {
			spillPath = artifact.ContentPath
			truncated = true
			if err := emitArtifactCreated(bridge, artifact); err != nil {
				return "", false, err
			}
		}
	}
	finalOutput = appendPermissionFeedback(finalOutput, feedback)
	return finalOutput, truncated || spillPath != "", nil
}

func updateTurnToolStats(turnStats *turnExecutionStats, budgetInfo toolBudgetInfo) {
	if turnStats == nil {
		return
	}
	turnStats.ToolResultCount++
	turnStats.ToolInlineChars += budgetInfo.InlineChars
	if budgetInfo.Spilled {
		turnStats.ToolSpillCount++
	}
	if budgetInfo.AggregateLimited {
		turnStats.AggregateBudgetSpills++
	}
}

func emitToolArtifacts(bridge *ipc.Bridge, updates []toolpkg.ArtifactMutation, turnMetrics *timing.CheckpointRecorder) error {
	for _, artifactUpdate := range updates {
		if artifactUpdate.Created {
			if err := emitArtifactCreated(bridge, artifactUpdate.Artifact); err != nil {
				return err
			}
		}
		if err := emitArtifactUpdated(bridge, artifactUpdate.Artifact, artifactUpdate.Content); err != nil {
			return err
		}
		if artifactUpdate.Focused {
			if err := emitArtifactFocusedForTurn(bridge, artifactUpdate.Artifact, turnMetrics); err != nil {
				return err
			}
		}
	}
	return nil
}

func markFirstToolResult(bridge *ipc.Bridge, turnMetrics *timing.CheckpointRecorder) error {
	if turnMetrics == nil || !turnMetrics.Mark("first_tool_result") {
		return nil
	}
	return emitTurnTimingCheckpoint(bridge, turnMetrics, "first_tool_result")
}

func runPostToolUseHooks(ctx context.Context, hookRunner *hooks.Runner, sessionID string, call api.ToolCall, output string) {
	if hookRunner == nil {
		return
	}
	_, _ = hookRunner.Run(ctx, hooks.Payload{
		Type:      hooks.HookPostToolUse,
		SessionID: sessionID,
		ToolName:  call.Name,
		ToolInput: call.Input,
		Output:    output,
	})
}

func compactToolResults(results []api.ToolResult) []api.ToolResult {
	filtered := make([]api.ToolResult, 0, len(results))
	for _, result := range results {
		if strings.TrimSpace(result.ToolCallID) == "" {
			continue
		}
		filtered = append(filtered, result)
	}
	return filtered
}

func emitToolError(bridge *ipc.Bridge, call api.ToolCall, message string, output toolpkg.ToolOutput, err error) error {
	payload := ipc.ToolErrorPayload{
		ToolID:    call.ID,
		Name:      call.Name,
		Input:     call.Input,
		Error:     message,
		FilePath:  output.FilePath,
		ErrorKind: output.ErrorKind,
		ErrorHint: output.ErrorHint,
	}
	if editFailure, ok := toolpkg.ExtractEditFailure(err); ok {
		if payload.FilePath == "" {
			payload.FilePath = editFailure.FilePath
		}
		if payload.ErrorKind == "" {
			payload.ErrorKind = string(editFailure.Kind)
		}
		if payload.ErrorHint == "" {
			payload.ErrorHint = editFailure.Hint
		}
	}
	return bridge.Emit(ipc.EventToolError, payload)
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
	risk := permissions.AssessRisk(pending.Tool.Name(), pending.Input, pending.Tool.Permission())
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
			if risk.DisallowPersistentAllow {
				return authorizationResult{Allowed: true, Feedback: response.Feedback}, nil
			}
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
	risk := permissions.AssessRisk(pending.Tool.Name(), pending.Input, pending.Tool.Permission())
	if err := bridge.Emit(ipc.EventPermissionRequest, ipc.PermissionRequestPayload{
		RequestID:       requestID,
		ToolID:          toolCallID,
		Tool:            pending.Tool.Name(),
		Command:         summarizePermissionTarget(pending),
		Risk:            permissionRisk(pending),
		RiskReason:      risk.Reason,
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
	return permissions.AssessRisk(call.Tool.Name(), call.Input, call.Tool.Permission()).Level
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
	if provider, ok := call.Tool.(toolpkg.PermissionTargetProvider); ok {
		target := provider.PermissionTarget(call.Input)
		if strings.TrimSpace(target.Kind) != "" {
			return target.Kind
		}
	}
	if command, ok := stringParamFromMap(call.Input.Params, "command"); ok && strings.TrimSpace(command) != "" {
		return "command"
	}
	if call.Tool.Name() == "apply_patch" {
		targets, _ := applyPatchPermissionTargets(call)
		if len(targets) == 1 {
			return "file"
		}
		if len(targets) > 1 {
			return "target"
		}
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
	if provider, ok := call.Tool.(toolpkg.PermissionTargetProvider); ok {
		target := provider.PermissionTarget(call.Input)
		if strings.TrimSpace(target.WorkingDir) != "" {
			return strings.TrimSpace(target.WorkingDir)
		}
	}
	workingDir, ok := stringParamFromMap(call.Input.Params, "cwd")
	if !ok {
		return ""
	}
	return strings.TrimSpace(workingDir)
}

func summarizePermissionTarget(call toolpkg.PendingCall) string {
	if provider, ok := call.Tool.(toolpkg.PermissionTargetProvider); ok {
		target := provider.PermissionTarget(call.Input)
		if strings.TrimSpace(target.Value) != "" {
			return target.Value
		}
	}
	if command, ok := stringParamFromMap(call.Input.Params, "command"); ok && strings.TrimSpace(command) != "" {
		return command
	}
	if call.Tool.Name() == "apply_patch" {
		targets, summary := applyPatchPermissionTargets(call)
		if len(targets) == 1 {
			return targets[0]
		}
		if summary != "" {
			return summary
		}
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

func applyPatchPermissionTargets(call toolpkg.PendingCall) ([]string, string) {
	patchText, ok := stringParamFromMap(call.Input.Params, "patch")
	if !ok || strings.TrimSpace(patchText) == "" {
		return nil, ""
	}
	targets, err := toolpkg.ExtractApplyPatchTargets(patchText)
	if err != nil || len(targets) == 0 {
		return nil, ""
	}
	if len(targets) == 1 {
		return targets, targets[0]
	}
	previewTargets := targets
	if len(previewTargets) > 3 {
		previewTargets = previewTargets[:3]
	}
	return targets, fmt.Sprintf("%d files: %s", len(targets), strings.Join(previewTargets, ", "))
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

func normalizeToolCall(call api.ToolCall) (api.ToolCall, error) {
	alias := strings.TrimSpace(call.Name)
	switch alias {
	case "file_search", "grep_search", "read_file", "replace_string_in_file", "glob", "grep", "file_read", "file_edit", "google:search", "google_search", "google.search":
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
	case "file_search", "glob":
		normalized.Name = "file_search"
		if pattern, ok := stringParamFromMap(normalizedParams, "pattern"); !ok || strings.TrimSpace(pattern) == "" {
			if query, ok := stringParamFromMap(normalizedParams, "query"); ok && strings.TrimSpace(query) != "" {
				normalizedParams["pattern"] = normalizeFileSearchPattern(query)
			}
		}
		renameToolParam(normalizedParams, "pattern", "query")
		if _, ok := stringParamFromMap(normalizedParams, "path"); !ok {
			if includePattern, ok := stringParamFromMap(normalizedParams, "includePattern"); ok && strings.TrimSpace(includePattern) != "" && !looksLikeGlob(includePattern) {
				normalizedParams["path"] = includePattern
			}
		}
	case "grep_search", "grep":
		normalized.Name = "grep_search"
		if pattern, ok := stringParamFromMap(normalizedParams, "pattern"); !ok || strings.TrimSpace(pattern) == "" {
			if query, ok := stringParamFromMap(normalizedParams, "query"); ok && strings.TrimSpace(query) != "" {
				if isRegexp, ok := normalizedParams["isRegexp"].(bool); ok && !isRegexp {
					normalizedParams["pattern"] = regexp.QuoteMeta(query)
				} else {
					normalizedParams["pattern"] = query
				}
			}
		}
		renameToolParam(normalizedParams, "pattern", "query")
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
	case "read_file", "file_read":
		normalized.Name = "read_file"
		renameToolParam(normalizedParams, "filePath", "file_path")
		renameToolParam(normalizedParams, "startLine", "start_line")
		renameToolParam(normalizedParams, "endLine", "end_line")
	case "replace_string_in_file", "file_edit":
		normalized.Name = "replace_string_in_file"
		renameToolParam(normalizedParams, "filePath", "file_path")
		renameToolParam(normalizedParams, "oldString", "old_string")
		renameToolParam(normalizedParams, "newString", "new_string")
		renameToolParam(normalizedParams, "replaceAll", "replace_all")
	case "google:search", "google_search", "google.search":
		normalized.Name = "web_search"
		if query, ok := stringParamFromMap(normalizedParams, "query"); !ok || strings.TrimSpace(query) == "" {
			if firstQuery, ok := firstStringInArrayParamFromMap(normalizedParams, "queries"); ok {
				normalizedParams["query"] = firstQuery
			}
		}
		renameToolParam(normalizedParams, "max_results", "limit")
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

func firstStringInArrayParamFromMap(params map[string]any, key string) (string, bool) {
	value, ok := params[key]
	if !ok {
		return "", false
	}
	items, ok := value.([]any)
	if !ok {
		return "", false
	}
	for _, item := range items {
		text, ok := item.(string)
		if ok && strings.TrimSpace(text) != "" {
			return text, true
		}
	}
	return "", false
}
