package ipc

import "encoding/json"

// ProtocolVersion is the current IPC protocol version.
const ProtocolVersion = 1

// --- Go → Ink (stdout): StreamEvent ---

// EventType discriminates StreamEvent payloads.
type EventType string

const (
	// Model output
	EventTokenDelta    EventType = "token_delta"
	EventThinkingDelta EventType = "thinking_delta"
	EventTurnComplete  EventType = "turn_complete"

	// Tool lifecycle
	EventToolStart    EventType = "tool_start"
	EventToolProgress EventType = "tool_progress"
	EventToolResult   EventType = "tool_result"
	EventToolError    EventType = "tool_error"

	// Permission
	EventPermissionRequest EventType = "permission_request"

	// Session state
	EventModeChanged  EventType = "mode_changed"
	EventCostUpdate   EventType = "cost_update"
	EventCompactStart EventType = "compact_start"
	EventCompactEnd   EventType = "compact_end"

	// Artifacts
	EventArtifactCreated EventType = "artifact_created"
	EventArtifactUpdated EventType = "artifact_updated"

	// Engine status
	EventReady           EventType = "ready"
	EventError           EventType = "error"
	EventSessionRestored EventType = "session_restored"
)

// StreamEvent is one NDJSON line from Go engine → Ink frontend.
type StreamEvent struct {
	Type    EventType       `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

// --- Ink → Go (stdin): ClientMessage ---

// ClientMessageType discriminates ClientMessage payloads.
type ClientMessageType string

const (
	MsgUserInput          ClientMessageType = "user_input"
	MsgSlashCommand       ClientMessageType = "slash_command"
	MsgPermissionResponse ClientMessageType = "permission_response"
	MsgCancel             ClientMessageType = "cancel"
	MsgModeToggle         ClientMessageType = "mode_toggle"
	MsgShutdown           ClientMessageType = "shutdown"
)

// ClientMessage is one NDJSON line from Ink frontend → Go engine.
type ClientMessage struct {
	Type    ClientMessageType `json:"type"`
	Payload json.RawMessage   `json:"payload,omitempty"`
}

// --- Typed payloads ---

type TokenDeltaPayload struct {
	Text string `json:"text"`
}

type TurnCompletePayload struct {
	StopReason string `json:"stop_reason"`
}

type ToolStartPayload struct {
	ToolID string `json:"tool_id"`
	Name   string `json:"name"`
	Input  string `json:"input"`
}

type ToolProgressPayload struct {
	ToolID    string `json:"tool_id"`
	BytesRead int    `json:"bytes_read"`
}

type ToolResultPayload struct {
	ToolID    string `json:"tool_id"`
	Output    string `json:"output"`
	Truncated bool   `json:"truncated"`
}

type ToolErrorPayload struct {
	ToolID string `json:"tool_id"`
	Error  string `json:"error"`
}

type PermissionRequestPayload struct {
	RequestID string `json:"request_id"`
	Tool      string `json:"tool"`
	Command   string `json:"command"`
	Risk      string `json:"risk"`
}

type ModeChangedPayload struct {
	Mode string `json:"mode"`
}

type CostUpdatePayload struct {
	TotalUSD     float64 `json:"total_usd"`
	InputTokens  int     `json:"input_tokens"`
	OutputTokens int     `json:"output_tokens"`
}

type CompactStartPayload struct {
	Strategy     string `json:"strategy"`
	TokensBefore int    `json:"tokens_before"`
}

type CompactEndPayload struct {
	TokensAfter int `json:"tokens_after"`
}

type ArtifactCreatedPayload struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Title string `json:"title"`
}

type ArtifactUpdatedPayload struct {
	ID      string `json:"id"`
	Content string `json:"content"`
}

type ReadyPayload struct {
	ProtocolVersion int `json:"protocol_version"`
}

type ErrorPayload struct {
	Message     string `json:"message"`
	Recoverable bool   `json:"recoverable"`
}

type SessionRestoredPayload struct {
	SessionID string `json:"session_id"`
	Mode      string `json:"mode"`
}

// Client message payloads

type UserInputPayload struct {
	Text string `json:"text"`
}

type SlashCommandPayload struct {
	Command string `json:"command"`
	Args    string `json:"args"`
}

type PermissionResponsePayload struct {
	RequestID string `json:"request_id"`
	Decision  string `json:"decision"` // "allow", "deny", "always_allow"
}
