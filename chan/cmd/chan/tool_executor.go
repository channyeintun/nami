package main

import (
	"context"
	"encoding/json"
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
	results := make([]api.ToolResult, len(calls))
	approved := make([]toolpkg.PendingCall, 0, len(calls))
	approvalFeedback := make(map[int]string, len(calls))
	budget := toolpkg.DefaultResultBudgetForModel(filepath.Join(os.TempDir(), "chan-session"), maxOutputTokens)
	aggregateBudget := toolpkg.NewAggregateResultBudget(budget)
	if turnStats != nil {
		turnStats.AggregateBudgetChars = aggregateBudget.MaxChars()
	}

	for index, call := range calls {
		call, err := normalizeToolCall(call)
		if err != nil {
			results[index] = api.ToolResult{ToolCallID: calls[index].ID, Output: err.Error(), IsError: true}
			if emitErr := emitToolError(bridge, calls[index], err.Error(), toolpkg.ToolOutput{}, err); emitErr != nil {
				return nil, emitErr
			}
			continue
		}

		tool, err := registry.Get(call.Name)
		if err != nil {
			results[index] = api.ToolResult{ToolCallID: call.ID, Output: err.Error(), IsError: true}
			if emitErr := emitToolError(bridge, call, err.Error(), toolpkg.ToolOutput{}, err); emitErr != nil {
				return nil, emitErr
			}
			continue
		}

		input, err := decodeToolInput(call)
		if err != nil {
			results[index] = api.ToolResult{ToolCallID: call.ID, Output: err.Error(), IsError: true}
			if emitErr := emitToolError(bridge, call, err.Error(), toolpkg.ToolOutput{}, err); emitErr != nil {
				return nil, emitErr
			}
			continue
		}

		if err := toolpkg.ValidateToolCall(tool, input); err != nil {
			results[index] = api.ToolResult{ToolCallID: call.ID, Output: err.Error(), IsError: true}
			if emitErr := emitToolError(bridge, call, err.Error(), toolpkg.ToolOutput{}, err); emitErr != nil {
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
			if emitErr := emitToolError(bridge, call, err.Error(), toolpkg.ToolOutput{}, err); emitErr != nil {
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
			if emitErr := emitToolError(bridge, call, authorization.DenyReason, toolpkg.ToolOutput{}, nil); emitErr != nil {
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
					_ = emitToolError(bridge, call, reason, toolpkg.ToolOutput{}, nil)
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
				if err := emitToolError(bridge, call, toolResult.Output, result.Output, result.Err); err != nil {
					return nil, err
				}
				results[result.Index] = toolResult
				continue
			}

			output := result.Output.Output
			spillPath := result.Output.SpillPath
			truncated := result.Output.Truncated
			if !result.Output.IsError {
				budgetedOutput, artifact, budgetInfo, err := budgetToolOutput(ctx, artifactManager, sessionID, budget, aggregateBudget, call, output)
				output = budgetedOutput
				if err != nil {
					if emitErr := bridge.EmitError(fmt.Sprintf("persist tool-log artifact: %v", err), true); emitErr != nil {
						return nil, emitErr
					}
				}
				if turnStats != nil {
					turnStats.ToolResultCount++
					turnStats.ToolInlineChars += budgetInfo.InlineChars
					if budgetInfo.Spilled {
						turnStats.ToolSpillCount++
					}
					if budgetInfo.AggregateLimited {
						turnStats.AggregateBudgetSpills++
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
				if err := emitToolError(bridge, call, output, result.Output, nil); err != nil {
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
				if artifactUpdate.Focused {
					if err := emitArtifactFocusedForTurn(bridge, artifactUpdate.Artifact, turnMetrics); err != nil {
						return nil, err
					}
				}
			}

			if turnMetrics != nil {
				if turnMetrics.Mark("first_tool_result") {
					if err := emitTurnTimingCheckpoint(bridge, turnMetrics, "first_tool_result"); err != nil {
						return nil, err
					}
				}
			}
			if err := bridge.Emit(ipc.EventToolResult, ipc.ToolResultPayload{
				ToolID:      call.ID,
				Output:      output,
				Truncated:   truncated || spillPath != "",
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
	case "file_search", "grep_search", "read_file", "google:search", "google_search", "google.search":
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
