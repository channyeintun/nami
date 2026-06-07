package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/permissions"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

type authorizationResult struct {
	Allowed    bool
	DenyReason string
	Feedback   string
}

type permissionResponse struct {
	Decision string
	Feedback string
}

func newPermissionContext(mode string, autoMode bool) *permissions.Context {
	ctx := permissions.NewContext()
	ctx.SessionAllowAll = autoMode
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
	bridge ipc.EventSink,
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
	bridge ipc.EventSink,
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
		RawInput:        strings.TrimSpace(pending.Input.Raw),
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
			return "files"
		}
	}
	if fileTargets, _ := permissionFileTargets(call.Input.Params); len(fileTargets) > 0 {
		if len(fileTargets) == 1 {
			return "file"
		}
		return "files"
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
	if _, summary := permissionFileTargets(call.Input.Params); summary != "" {
		return summary
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
	if summary := summarizePermissionParams(call.Input.Params); summary != "" {
		return summary
	}
	if call.Tool.Permission() == toolpkg.PermissionWrite {
		return call.Tool.Name()
	}
	if raw := strings.TrimSpace(call.Input.Raw); raw != "" {
		return raw
	}
	return call.Tool.Name()
}

func permissionFileTargets(params map[string]any) ([]string, string) {
	if filePath, ok := firstStringParamFromMap(params, "filePath", "file_path"); ok && strings.TrimSpace(filePath) != "" {
		target := strings.TrimSpace(filePath)
		return []string{target}, target
	}

	replacements, ok := params["replacements"].([]any)
	if !ok || len(replacements) == 0 {
		return nil, ""
	}

	seen := make(map[string]struct{}, len(replacements))
	targets := make([]string, 0, len(replacements))
	for _, raw := range replacements {
		replacement, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		filePath, ok := firstStringParamFromMap(replacement, "filePath", "file_path")
		if !ok || strings.TrimSpace(filePath) == "" {
			continue
		}
		target := strings.TrimSpace(filePath)
		if _, exists := seen[target]; exists {
			continue
		}
		seen[target] = struct{}{}
		targets = append(targets, target)
	}
	if len(targets) == 0 {
		return nil, ""
	}
	if len(targets) == 1 {
		return targets, targets[0]
	}
	previewTargets := permissionFileTargetPreview(targets, 3)
	return targets, fmt.Sprintf("%d files: %s", len(targets), strings.Join(previewTargets, ", "))
}

func permissionFileTargetPreview(targets []string, limit int) []string {
	if len(targets) == 0 {
		return nil
	}
	previewTargets := targets
	if limit > 0 && len(previewTargets) > limit {
		previewTargets = previewTargets[:limit]
	}
	baseNames := make([]string, 0, len(previewTargets))
	seen := make(map[string]struct{}, len(previewTargets))
	for _, target := range previewTargets {
		baseName := strings.TrimSpace(filepath.Base(target))
		if baseName == "" || baseName == "." || baseName == string(filepath.Separator) {
			return previewTargets
		}
		if _, exists := seen[baseName]; exists {
			return previewTargets
		}
		seen[baseName] = struct{}{}
		baseNames = append(baseNames, baseName)
	}
	return baseNames
}

func summarizePermissionParams(params map[string]any) string {
	if len(params) == 0 {
		return ""
	}
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	parts := make([]string, 0, 3)
	for _, key := range keys {
		if shouldSkipPermissionParamSummary(key) {
			continue
		}
		part, ok := summarizePermissionParamValue(key, params[key])
		if !ok {
			continue
		}
		parts = append(parts, part)
		if len(parts) == 3 {
			break
		}
	}
	return strings.Join(parts, " | ")
}

func shouldSkipPermissionParamSummary(key string) bool {
	switch key {
	case "command", "content", "cwd", "explanation", "filePath", "file_path", "input", "newString", "new_string", "oldString", "old_string", "patch", "query", "replacements", "url", "pattern":
		return true
	default:
		return false
	}
}

func summarizePermissionParamValue(key string, value any) (string, bool) {
	switch typed := value.(type) {
	case string:
		normalized := normalizePermissionSummaryValue(typed)
		if normalized == "" {
			return "", false
		}
		return key + "=" + normalized, true
	case bool:
		return fmt.Sprintf("%s=%t", key, typed), true
	case float64:
		return fmt.Sprintf("%s=%g", key, typed), true
	case int:
		return fmt.Sprintf("%s=%d", key, typed), true
	case int64:
		return fmt.Sprintf("%s=%d", key, typed), true
	default:
		return "", false
	}
}

func normalizePermissionSummaryValue(value string) string {
	trimmed := strings.TrimSpace(strings.Join(strings.Fields(value), " "))
	if trimmed == "" {
		return ""
	}
	const limit = 80
	if len(trimmed) <= limit {
		return trimmed
	}
	return trimmed[:limit-3] + "..."
}

func applyPatchPermissionTargets(call toolpkg.PendingCall) ([]string, string) {
	patchText, ok := stringParamFromMap(call.Input.Params, "input")
	if !ok || strings.TrimSpace(patchText) == "" {
		patchText, ok = stringParamFromMap(call.Input.Params, "patch")
	}
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
