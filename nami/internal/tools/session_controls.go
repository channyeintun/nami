package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

type SessionControlRuntime interface {
	SwitchMode(mode string) (string, error)
	EnterWorktree(context.Context, WorktreeControlRequest) (WorktreeControlResult, error)
	ExitWorktree(context.Context) (WorktreeControlResult, error)
}

type WorktreeControlRequest struct {
	Path         string
	Branch       string
	CreateBranch bool
}

type WorktreeControlResult struct {
	Path       string `json:"path"`
	Previous   string `json:"previous,omitempty"`
	Branch     string `json:"branch,omitempty"`
	Created    bool   `json:"created,omitempty"`
	Repository string `json:"repository,omitempty"`
}

type sessionControlRuntimeState struct {
	mu      sync.RWMutex
	runtime SessionControlRuntime
}

var globalSessionControlRuntime sessionControlRuntimeState

type EnterPlanModeTool struct{}
type ExitPlanModeTool struct{}
type EnterWorktreeTool struct{}
type ExitWorktreeTool struct{}

func SetSessionControlRuntime(runtime SessionControlRuntime) {
	globalSessionControlRuntime.mu.Lock()
	defer globalSessionControlRuntime.mu.Unlock()
	globalSessionControlRuntime.runtime = runtime
}

func getSessionControlRuntime() (SessionControlRuntime, error) {
	globalSessionControlRuntime.mu.RLock()
	defer globalSessionControlRuntime.mu.RUnlock()
	if globalSessionControlRuntime.runtime == nil {
		return nil, fmt.Errorf("session controls are unavailable")
	}
	return globalSessionControlRuntime.runtime, nil
}

func NewEnterPlanModeTool() *EnterPlanModeTool { return &EnterPlanModeTool{} }
func NewExitPlanModeTool() *ExitPlanModeTool   { return &ExitPlanModeTool{} }
func NewEnterWorktreeTool() *EnterWorktreeTool { return &EnterWorktreeTool{} }
func NewExitWorktreeTool() *ExitWorktreeTool   { return &ExitWorktreeTool{} }

func (t *EnterPlanModeTool) Name() string { return "enter_plan_mode" }

func (t *EnterPlanModeTool) Description() string {
	return "Switch the current session into explicit plan mode using the existing persisted runtime state."
}

func (t *EnterPlanModeTool) InputSchema() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *EnterPlanModeTool) Permission() PermissionLevel { return PermissionReadOnly }

func (t *EnterPlanModeTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *EnterPlanModeTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	runtime, err := getSessionControlRuntime()
	if err != nil {
		return ToolOutput{}, err
	}
	mode, err := runtime.SwitchMode("plan")
	if err != nil {
		return ToolOutput{}, err
	}
	return ToolOutput{Output: fmt.Sprintf("Session mode set to %s", mode)}, nil
}

func (t *ExitPlanModeTool) Name() string { return "exit_plan_mode" }

func (t *ExitPlanModeTool) Description() string {
	return "Exit plan mode by switching the current session into fast mode."
}

func (t *ExitPlanModeTool) InputSchema() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *ExitPlanModeTool) Permission() PermissionLevel { return PermissionReadOnly }

func (t *ExitPlanModeTool) Concurrency(input ToolInput) ConcurrencyDecision { return ConcurrencySerial }

func (t *ExitPlanModeTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	runtime, err := getSessionControlRuntime()
	if err != nil {
		return ToolOutput{}, err
	}
	mode, err := runtime.SwitchMode("fast")
	if err != nil {
		return ToolOutput{}, err
	}
	return ToolOutput{Output: fmt.Sprintf("Session mode set to %s", mode)}, nil
}

func (t *EnterWorktreeTool) Name() string { return "enter_worktree" }

func (t *EnterWorktreeTool) Description() string {
	return "Switch the session into an existing git worktree or create one and switch into it."
}

func (t *EnterWorktreeTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path":          map[string]any{"type": "string", "description": "Existing or target worktree path."},
			"branch":        map[string]any{"type": "string", "description": "Branch to switch to or create for a new worktree."},
			"createBranch":  map[string]any{"type": "boolean", "description": "Create the branch when adding a new worktree."},
			"create_branch": map[string]any{"type": "boolean", "description": "Snake_case alias for createBranch."},
		},
	}
}

func (t *EnterWorktreeTool) Permission() PermissionLevel { return PermissionExecute }

func (t *EnterWorktreeTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *EnterWorktreeTool) Validate(input ToolInput) error {
	if firstStringOrEmpty(input.Params, "path", "branch") == "" {
		return fmt.Errorf("enter_worktree requires path or branch")
	}
	return nil
}

func (t *EnterWorktreeTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	runtime, err := getSessionControlRuntime()
	if err != nil {
		return ToolOutput{}, err
	}
	result, err := runtime.EnterWorktree(ctx, WorktreeControlRequest{
		Path:         firstStringOrEmpty(input.Params, "path"),
		Branch:       firstStringOrEmpty(input.Params, "branch"),
		CreateBranch: firstBoolParam(input.Params, "createBranch", "create_branch"),
	})
	if err != nil {
		return ToolOutput{}, err
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal enter_worktree result: %w", err)
	}
	return ToolOutput{Output: string(encoded)}, nil
}

func (t *ExitWorktreeTool) Name() string { return "exit_worktree" }

func (t *ExitWorktreeTool) Description() string {
	return "Switch the session back to the primary git worktree for the current repository."
}

func (t *ExitWorktreeTool) InputSchema() any {
	return map[string]any{"type": "object", "properties": map[string]any{}}
}

func (t *ExitWorktreeTool) Permission() PermissionLevel { return PermissionExecute }

func (t *ExitWorktreeTool) Concurrency(input ToolInput) ConcurrencyDecision { return ConcurrencySerial }

func (t *ExitWorktreeTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	runtime, err := getSessionControlRuntime()
	if err != nil {
		return ToolOutput{}, err
	}
	result, err := runtime.ExitWorktree(ctx)
	if err != nil {
		return ToolOutput{}, err
	}
	encoded, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal exit_worktree result: %w", err)
	}
	return ToolOutput{Output: string(encoded)}, nil
}

func firstStringOrEmpty(params map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := stringParam(params, key); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
