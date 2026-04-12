package hooks

// HookType identifies lifecycle hooks.
type HookType string

const (
	HookSessionStart        HookType = "session_start"
	HookSubagentStart       HookType = "subagent_start"
	HookSessionEnd          HookType = "session_end"
	HookPreToolUse          HookType = "pre_tool_use"
	HookPostToolUse         HookType = "post_tool_use"
	HookPermissionRequest   HookType = "permission_request"
	HookPreCompact          HookType = "pre_compact"
	HookPostCompact         HookType = "post_compact"
	HookStop                HookType = "stop"
	HookSubagentStop        HookType = "subagent_stop"
	HookStopFailure         HookType = "stop_failure"
	HookSubagentStopFailure HookType = "subagent_stop_failure"
)

// Payload is the data passed to a hook.
type Payload struct {
	Type      HookType       `json:"type"`
	SessionID string         `json:"session_id,omitempty"`
	ToolName  string         `json:"tool_name,omitempty"`
	ToolInput string         `json:"tool_input,omitempty"`
	Output    string         `json:"output,omitempty"`
	Error     string         `json:"error,omitempty"`
	Extra     map[string]any `json:"extra,omitempty"`
}

// Response is the result from a hook execution.
type Response struct {
	Action  string `json:"action,omitempty"`  // "allow", "deny", "ask", "stop", ""; child stop hooks treat "deny" or "stop" as a block
	Message string `json:"message,omitempty"` // feedback message for next iteration
}
