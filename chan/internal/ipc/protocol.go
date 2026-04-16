package ipc

import "encoding/json"

// ProtocolVersion is the current IPC protocol version.
const ProtocolVersion = 2

// --- Go → Ink (stdout): StreamEvent ---

// EventType discriminates StreamEvent payloads.
type EventType string

const (
	// Model output
	EventTokenDelta    EventType = "token_delta"
	EventThinkingDelta EventType = "thinking_delta"
	EventProgress      EventType = "progress"
	EventTurnComplete  EventType = "turn_complete"

	// Tool lifecycle
	EventToolStart    EventType = "tool_start"
	EventToolProgress EventType = "tool_progress"
	EventToolResult   EventType = "tool_result"
	EventToolError    EventType = "tool_error"

	// Permission
	EventPermissionRequest EventType = "permission_request"

	// Session state
	EventModeChanged              EventType = "mode_changed"
	EventModelChanged             EventType = "model_changed"
	EventContextWindow            EventType = "context_window"
	EventCostUpdate               EventType = "cost_update"
	EventConversationHydrated     EventType = "conversation_hydrated"
	EventModelSelectionRequested  EventType = "model_selection_requested"
	EventRewindSelectionRequested EventType = "rewind_selection_requested"
	EventResumeSelectionRequested EventType = "resume_selection_requested"
	EventMemoryRecalled           EventType = "memory_recalled"
	EventRetrievalUsed            EventType = "retrieval_used"
	EventAttemptLogSurfaced       EventType = "attempt_log_surfaced"
	EventAttemptRepeated          EventType = "attempt_repeated"
	EventRateLimitUpdate          EventType = "rate_limit_update"
	EventTurnTiming               EventType = "turn_timing"
	EventCompactStart             EventType = "compact_start"
	EventCompactEnd               EventType = "compact_end"

	// Artifacts
	EventArtifactCreated         EventType = "artifact_created"
	EventArtifactUpdated         EventType = "artifact_updated"
	EventArtifactFocused         EventType = "artifact_focused"
	EventArtifactStatusChanged   EventType = "artifact_status_changed"
	EventArtifactReviewRequested EventType = "artifact_review_requested"
	EventArtifactReviewResolved  EventType = "artifact_review_resolved"
	EventBackgroundAgentUpdated  EventType = "background_agent_updated"

	// Engine status
	EventReady           EventType = "ready"
	EventError           EventType = "error"
	EventNotice          EventType = "notice"
	EventSessionRewound  EventType = "session_rewound"
	EventSessionUpdated  EventType = "session_updated"
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
	MsgUserInput               ClientMessageType = "user_input"
	MsgSlashCommand            ClientMessageType = "slash_command"
	MsgPermissionResponse      ClientMessageType = "permission_response"
	MsgModelSelectionResponse  ClientMessageType = "model_selection_response"
	MsgRewindSelectionResponse ClientMessageType = "rewind_selection_response"
	MsgResumeSelectionResponse ClientMessageType = "resume_selection_response"
	MsgCancel                  ClientMessageType = "cancel"
	MsgModeToggle              ClientMessageType = "mode_toggle"
	MsgShutdown                ClientMessageType = "shutdown"
	MsgArtifactReviewResponse  ClientMessageType = "artifact_review_response"
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

type ProgressPayload struct {
	ID      string `json:"id"`
	Message string `json:"message"`
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
	ToolID      string `json:"tool_id"`
	Output      string `json:"output"`
	Truncated   bool   `json:"truncated"`
	Name        string `json:"name,omitempty"`
	Input       string `json:"input,omitempty"`
	FilePath    string `json:"file_path,omitempty"`
	Preview     string `json:"preview,omitempty"`
	Insertions  int    `json:"insertions,omitempty"`
	Deletions   int    `json:"deletions,omitempty"`
	Diagnostics string `json:"diagnostics,omitempty"`
	ErrorKind   string `json:"error_kind,omitempty"`
	ErrorHint   string `json:"error_hint,omitempty"`
}

type ToolErrorPayload struct {
	ToolID    string `json:"tool_id"`
	Error     string `json:"error"`
	Name      string `json:"name,omitempty"`
	Input     string `json:"input,omitempty"`
	FilePath  string `json:"file_path,omitempty"`
	ErrorKind string `json:"error_kind,omitempty"`
	ErrorHint string `json:"error_hint,omitempty"`
}

type PermissionRequestPayload struct {
	RequestID       string `json:"request_id"`
	ToolID          string `json:"tool_id"`
	Tool            string `json:"tool"`
	Command         string `json:"command"`
	Risk            string `json:"risk"`
	RiskReason      string `json:"risk_reason,omitempty"`
	PermissionLevel string `json:"permission_level,omitempty"`
	TargetKind      string `json:"target_kind,omitempty"`
	TargetValue     string `json:"target_value,omitempty"`
	WorkingDir      string `json:"working_dir,omitempty"`
}

type ModeChangedPayload struct {
	Mode string `json:"mode"`
}

type ModelChangedPayload struct {
	Model            string `json:"model"`
	MaxContextWindow int    `json:"max_context_window,omitempty"`
	MaxOutputTokens  int    `json:"max_output_tokens,omitempty"`
}

type ContextWindowPayload struct {
	CurrentUsage int `json:"current_usage"`
}

type TurnTimingPayload struct {
	Checkpoint string `json:"checkpoint"`
	ElapsedMS  int64  `json:"elapsed_ms"`
}

type CostUpdatePayload struct {
	TotalUSD                 float64 `json:"total_usd"`
	InputTokens              int     `json:"input_tokens"`
	OutputTokens             int     `json:"output_tokens"`
	MemoryRecallUSD          float64 `json:"memory_recall_usd,omitempty"`
	MemoryRecallInputTokens  int     `json:"memory_recall_input_tokens,omitempty"`
	MemoryRecallOutputTokens int     `json:"memory_recall_output_tokens,omitempty"`
	ChildAgentUSD            float64 `json:"child_agent_usd,omitempty"`
	ChildAgentInputTokens    int     `json:"child_agent_input_tokens,omitempty"`
	ChildAgentOutputTokens   int     `json:"child_agent_output_tokens,omitempty"`
}

type ConversationHydratedMessageBlockPayload struct {
	Kind string `json:"kind"`
	Text string `json:"text"`
}

type ConversationHydratedMessagePayload struct {
	ID     string                                    `json:"id"`
	Role   string                                    `json:"role"`
	Text   string                                    `json:"text,omitempty"`
	Tone   string                                    `json:"tone,omitempty"`
	Blocks []ConversationHydratedMessageBlockPayload `json:"blocks,omitempty"`
	Model  string                                    `json:"model,omitempty"`
}

type ConversationHydratedToolCallPayload struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Input       string `json:"input"`
	Status      string `json:"status"`
	Output      string `json:"output,omitempty"`
	Truncated   bool   `json:"truncated,omitempty"`
	Error       string `json:"error,omitempty"`
	FilePath    string `json:"file_path,omitempty"`
	Preview     string `json:"preview,omitempty"`
	Insertions  int    `json:"insertions,omitempty"`
	Deletions   int    `json:"deletions,omitempty"`
	Diagnostics string `json:"diagnostics,omitempty"`
	ErrorKind   string `json:"error_kind,omitempty"`
	ErrorHint   string `json:"error_hint,omitempty"`
}

type ConversationHydratedTranscriptEntryPayload struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	RefID string `json:"ref_id,omitempty"`
}

type ConversationHydratedPayload struct {
	Messages   []ConversationHydratedMessagePayload         `json:"messages,omitempty"`
	ToolCalls  []ConversationHydratedToolCallPayload        `json:"tool_calls,omitempty"`
	Transcript []ConversationHydratedTranscriptEntryPayload `json:"transcript,omitempty"`
}

type ResumeSelectionSessionPayload struct {
	SessionID    string  `json:"session_id"`
	Title        string  `json:"title,omitempty"`
	UpdatedAt    string  `json:"updated_at,omitempty"`
	Model        string  `json:"model,omitempty"`
	TotalCostUSD float64 `json:"total_cost_usd,omitempty"`
}

type ResumeSelectionRequestedPayload struct {
	RequestID string                          `json:"request_id"`
	Sessions  []ResumeSelectionSessionPayload `json:"sessions"`
}

type RewindSelectionTurnPayload struct {
	MessageIndex int    `json:"message_index"`
	TurnNumber   int    `json:"turn_number"`
	Preview      string `json:"preview,omitempty"`
}

type RewindSelectionRequestedPayload struct {
	RequestID string                       `json:"request_id"`
	Turns     []RewindSelectionTurnPayload `json:"turns"`
}

type ModelSelectionOptionPayload struct {
	Label       string `json:"label"`
	Model       string `json:"model,omitempty"`
	Provider    string `json:"provider,omitempty"`
	Description string `json:"description,omitempty"`
	IsCustom    bool   `json:"is_custom,omitempty"`
	Active      bool   `json:"active,omitempty"`
}

type ModelSelectionRequestedPayload struct {
	RequestID    string                        `json:"request_id"`
	CurrentModel string                        `json:"current_model,omitempty"`
	Options      []ModelSelectionOptionPayload `json:"options"`
}

type MemoryRecallEntryPayload struct {
	Title     string `json:"title"`
	NoteType  string `json:"note_type,omitempty"`
	Source    string `json:"source,omitempty"`
	IndexPath string `json:"index_path,omitempty"`
	NotePath  string `json:"note_path,omitempty"`
	Line      string `json:"line,omitempty"`
}

type MemoryRecalledPayload struct {
	Count   int                        `json:"count"`
	Source  string                     `json:"source,omitempty"`
	Entries []MemoryRecallEntryPayload `json:"entries,omitempty"`
}

type RateLimitWindowPayload struct {
	UsedPercentage float64 `json:"used_percentage"`
	ResetsAt       int64   `json:"resets_at"`
}

type RateLimitUpdatePayload struct {
	FiveHour *RateLimitWindowPayload `json:"five_hour,omitempty"`
	SevenDay *RateLimitWindowPayload `json:"seven_day,omitempty"`
}

type CompactStartPayload struct {
	Strategy         string `json:"strategy"`
	TokensBefore     int    `json:"tokens_before"`
	HasSessionMemory bool   `json:"has_session_memory,omitempty"`
}

type CompactEndPayload struct {
	Strategy                string `json:"strategy,omitempty"`
	TokensBefore            int    `json:"tokens_before,omitempty"`
	TokensAfter             int    `json:"tokens_after"`
	TokensSaved             int    `json:"tokens_saved,omitempty"`
	MicrocompactApplied     bool   `json:"microcompact_applied,omitempty"`
	MicrocompactTokensSaved int    `json:"microcompact_tokens_saved,omitempty"`
	HasSessionMemory        bool   `json:"has_session_memory,omitempty"`
}

// RetrievalUsedPayload is emitted after the live retrieval step runs each turn.
type RetrievalUsedPayload struct {
	SnippetCount  int  `json:"snippet_count"`
	TokensUsed    int  `json:"tokens_used"`
	AnchorCount   int  `json:"anchor_count"`
	EdgesExpanded int  `json:"edges_expanded"`
	Skipped       bool `json:"skipped"`
}

// AttemptLogSurfacedPayload is emitted when session attempt-log entries are
// injected into the prompt to prevent repeated failures.
type AttemptLogSurfacedPayload struct {
	EntryCount int  `json:"entry_count"`
	TokensUsed int  `json:"tokens_used"`
	Injected   bool `json:"injected"`
}

// AttemptRepeatedPayload is emitted when a new tool failure matches a
// previously logged attempt-log signature.
type AttemptRepeatedPayload struct {
	RepeatedCount int `json:"repeated_count"`
}

type ArtifactCreatedPayload struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Scope   string `json:"scope,omitempty"`
	Title   string `json:"title"`
	Version int    `json:"version,omitempty"`
	Source  string `json:"source,omitempty"`
	Status  string `json:"status,omitempty"`
}

type ArtifactUpdatedPayload struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Version int    `json:"version,omitempty"`
	Status  string `json:"status,omitempty"`
}

type SlashCommandDescriptorPayload struct {
	Name           string `json:"name"`
	Description    string `json:"description"`
	Usage          string `json:"usage,omitempty"`
	TakesArguments bool   `json:"takes_arguments,omitempty"`
}

type ReadyPayload struct {
	ProtocolVersion int                             `json:"protocol_version"`
	SlashCommands   []SlashCommandDescriptorPayload `json:"slash_commands,omitempty"`
}

type ErrorPayload struct {
	Message     string `json:"message"`
	Recoverable bool   `json:"recoverable"`
}

type NoticePayload struct {
	Message string `json:"message"`
}

type SessionRestoredPayload struct {
	SessionID string `json:"session_id"`
	Mode      string `json:"mode"`
}

type SessionRewoundPayload struct {
	SessionID    string `json:"session_id"`
	MessageCount int    `json:"message_count,omitempty"`
}

type SessionUpdatedPayload struct {
	SessionID string `json:"session_id"`
	Title     string `json:"title,omitempty"`
}

// Client message payloads

type ImageInputPayload struct {
	ID         int    `json:"id"`
	Data       string `json:"data"`
	MediaType  string `json:"media_type"`
	Filename   string `json:"filename,omitempty"`
	SourcePath string `json:"source_path,omitempty"`
}

type UserInputPayload struct {
	Text   string              `json:"text"`
	Images []ImageInputPayload `json:"images,omitempty"`
}

type SlashCommandPayload struct {
	Command string `json:"command"`
	Args    string `json:"args"`
}

type PermissionResponsePayload struct {
	RequestID string `json:"request_id"`
	Decision  string `json:"decision"` // "allow", "deny", "always_allow", "allow_all_session"
	Feedback  string `json:"feedback,omitempty"`
}

type ResumeSelectionResponsePayload struct {
	RequestID string `json:"request_id"`
	SessionID string `json:"session_id,omitempty"`
	Cancel    bool   `json:"cancel,omitempty"`
}

type ModelSelectionResponsePayload struct {
	RequestID string `json:"request_id"`
	Model     string `json:"model,omitempty"`
	Provider  string `json:"provider,omitempty"`
	Cancel    bool   `json:"cancel,omitempty"`
}

type RewindSelectionResponsePayload struct {
	RequestID    string `json:"request_id"`
	MessageIndex int    `json:"message_index,omitempty"`
	Cancel       bool   `json:"cancel,omitempty"`
}

// ArtifactFocusedPayload is emitted when the primary artifact for the active turn changes.
type ArtifactFocusedPayload struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Title   string `json:"title"`
	Version int    `json:"version,omitempty"`
	Status  string `json:"status,omitempty"`
}

// ArtifactStatusChangedPayload is emitted when an artifact transitions between lifecycle states.
type ArtifactStatusChangedPayload struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

// BackgroundAgentUpdatedPayload is emitted when a background child agent changes state.
type BackgroundAgentUpdatedPayload struct {
	AgentID        string                     `json:"agent_id"`
	InvocationID   string                     `json:"invocation_id,omitempty"`
	Description    string                     `json:"description,omitempty"`
	SubagentType   string                     `json:"subagent_type,omitempty"`
	Status         string                     `json:"status"`
	Summary        string                     `json:"summary,omitempty"`
	SessionID      string                     `json:"session_id,omitempty"`
	TranscriptPath string                     `json:"transcript_path,omitempty"`
	OutputFile     string                     `json:"output_file,omitempty"`
	Error          string                     `json:"error,omitempty"`
	TotalCostUSD   float64                    `json:"total_cost_usd,omitempty"`
	InputTokens    int                        `json:"input_tokens,omitempty"`
	OutputTokens   int                        `json:"output_tokens,omitempty"`
	Metadata       *ChildAgentMetadataPayload `json:"metadata,omitempty"`
}

type ChildAgentMetadataPayload struct {
	InvocationID    string   `json:"invocation_id,omitempty"`
	AgentID         string   `json:"agent_id,omitempty"`
	Description     string   `json:"description,omitempty"`
	SubagentType    string   `json:"subagent_type,omitempty"`
	LifecycleState  string   `json:"lifecycle_state,omitempty"`
	StatusMessage   string   `json:"status_message,omitempty"`
	StopBlockReason string   `json:"stop_block_reason,omitempty"`
	StopBlockCount  int      `json:"stop_block_count,omitempty"`
	SessionID       string   `json:"session_id,omitempty"`
	TranscriptPath  string   `json:"transcript_path,omitempty"`
	ResultPath      string   `json:"result_path,omitempty"`
	Tools           []string `json:"tools,omitempty"`
}

// ArtifactReviewRequestedPayload is emitted when an implementation-plan artifact
// requires explicit user review before execution can proceed.
type ArtifactReviewRequestedPayload struct {
	RequestID string `json:"request_id"`
	ID        string `json:"id"`
	Kind      string `json:"kind"`
	Title     string `json:"title"`
	Version   int    `json:"version,omitempty"`
}

// ArtifactReviewResolvedPayload is emitted once the review gate is lifted.
type ArtifactReviewResolvedPayload struct {
	RequestID string `json:"request_id"`
	Decision  string `json:"decision"` // "approved", "revised", "cancelled"
}

// ArtifactReviewResponsePayload is sent by the TUI to respond to a review gate.
type ArtifactReviewResponsePayload struct {
	RequestID string `json:"request_id"`
	Decision  string `json:"decision"` // "approve", "revise", "cancel"
	Feedback  string `json:"feedback,omitempty"`
}
