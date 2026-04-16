// StreamEvent types (Go → Ink)
export type EventType =
  | "token_delta"
  | "thinking_delta"
  | "progress"
  | "turn_complete"
  | "tool_start"
  | "tool_progress"
  | "tool_result"
  | "tool_error"
  | "permission_request"
  | "conversation_hydrated"
  | "model_selection_requested"
  | "rewind_selection_requested"
  | "resume_selection_requested"
  | "mode_changed"
  | "model_changed"
  | "context_window"
  | "cost_update"
  | "memory_recalled"
  | "retrieval_used"
  | "attempt_log_surfaced"
  | "attempt_repeated"
  | "turn_timing"
  | "rate_limit_update"
  | "compact_start"
  | "compact_end"
  | "artifact_created"
  | "artifact_updated"
  | "artifact_focused"
  | "artifact_status_changed"
  | "artifact_review_requested"
  | "artifact_review_resolved"
  | "background_agent_updated"
  | "ready"
  | "error"
  | "notice"
  | "session_rewound"
  | "session_updated"
  | "session_restored";

export interface StreamEvent {
  type: EventType;
  payload?: unknown;
}

// ClientMessage types (Ink → Go)
export type ClientMessageType =
  | "user_input"
  | "slash_command"
  | "permission_response"
  | "model_selection_response"
  | "rewind_selection_response"
  | "resume_selection_response"
  | "cancel"
  | "mode_toggle"
  | "shutdown"
  | "artifact_review_response";

export interface ClientMessage {
  type: ClientMessageType;
  payload?: unknown;
}

export interface UserInputImagePayload {
  id: number;
  data: string;
  media_type: string;
  filename?: string;
  source_path?: string;
}

export interface UserInputPayload {
  text: string;
  images?: UserInputImagePayload[];
}

// Typed payloads
export interface TokenDeltaPayload {
  text: string;
}

export interface ProgressPayload {
  id: string;
  message: string;
}

export interface TurnCompletePayload {
  stop_reason: string;
}

export interface ToolStartPayload {
  tool_id: string;
  name: string;
  input: string;
}

export interface ToolProgressPayload {
  tool_id: string;
  bytes_read: number;
}

export interface ToolResultPayload {
  tool_id: string;
  output: string;
  truncated: boolean;
  name?: string;
  input?: string;
  file_path?: string;
  preview?: string;
  insertions?: number;
  deletions?: number;
  diagnostics?: string;
  error_kind?: string;
  error_hint?: string;
}

export interface ToolErrorPayload {
  tool_id: string;
  error: string;
  name?: string;
  input?: string;
  file_path?: string;
  error_kind?: string;
  error_hint?: string;
}

export interface PermissionRequestPayload {
  request_id: string;
  tool_id: string;
  tool: string;
  command: string;
  risk: string;
  risk_reason?: string;
  permission_level?: string;
  target_kind?: string;
  target_value?: string;
  working_dir?: string;
}

export type PermissionResponseDecision =
  | "allow"
  | "deny"
  | "always_allow"
  | "allow_all_session";

export interface PermissionResponsePayload {
  request_id: string;
  decision: PermissionResponseDecision;
  feedback?: string;
}

export interface ResumeSelectionSessionPayload {
  session_id: string;
  title?: string;
  updated_at?: string;
  model?: string;
  total_cost_usd?: number;
}

export interface ResumeSelectionRequestedPayload {
  request_id: string;
  sessions: ResumeSelectionSessionPayload[];
}

export interface RewindSelectionTurnPayload {
  message_index: number;
  turn_number: number;
  preview?: string;
}

export interface RewindSelectionRequestedPayload {
  request_id: string;
  turns: RewindSelectionTurnPayload[];
}

export interface ModelSelectionOptionPayload {
  label: string;
  model?: string;
  provider?: string;
  description?: string;
  is_custom?: boolean;
  active?: boolean;
}

export interface ModelSelectionRequestedPayload {
  request_id: string;
  current_model?: string;
  options: ModelSelectionOptionPayload[];
}

export interface ModelSelectionResponsePayload {
  request_id: string;
  model?: string;
  provider?: string;
  cancel?: boolean;
}

export interface RewindSelectionResponsePayload {
  request_id: string;
  message_index?: number;
  cancel?: boolean;
}

export interface ResumeSelectionResponsePayload {
  request_id: string;
  session_id?: string;
  cancel?: boolean;
}

export interface ModeChangedPayload {
  mode: string;
}

export interface ModelChangedPayload {
  model: string;
  max_context_window?: number;
  max_output_tokens?: number;
}

export interface ContextWindowPayload {
  current_usage: number;
}

export interface TurnTimingPayload {
  checkpoint: string;
  elapsed_ms: number;
}

export interface CostUpdatePayload {
  total_usd: number;
  input_tokens: number;
  output_tokens: number;
  memory_recall_usd?: number;
  memory_recall_input_tokens?: number;
  memory_recall_output_tokens?: number;
  child_agent_usd?: number;
  child_agent_input_tokens?: number;
  child_agent_output_tokens?: number;
}

export interface ConversationHydratedMessageBlockPayload {
  kind: string;
  text: string;
}

export interface ConversationHydratedMessagePayload {
  id: string;
  role: string;
  text?: string;
  tone?: string;
  blocks?: ConversationHydratedMessageBlockPayload[];
  model?: string;
}

export interface ConversationHydratedProgressPayload {
  id: string;
  message: string;
}

export interface ConversationHydratedToolCallPayload {
  id: string;
  name: string;
  input: string;
  status: string;
  output?: string;
  truncated?: boolean;
  error?: string;
  file_path?: string;
  preview?: string;
  insertions?: number;
  deletions?: number;
  diagnostics?: string;
  error_kind?: string;
  error_hint?: string;
}

export interface ConversationHydratedTranscriptEntryPayload {
  id: string;
  kind: string;
  ref_id?: string;
}

export interface ConversationHydratedPayload {
  messages?: ConversationHydratedMessagePayload[];
  progress?: ConversationHydratedProgressPayload[];
  tool_calls?: ConversationHydratedToolCallPayload[];
  transcript?: ConversationHydratedTranscriptEntryPayload[];
}

export interface MemoryRecallEntryPayload {
  title: string;
  note_type?: string;
  source?: string;
  index_path?: string;
  note_path?: string;
  line?: string;
}

export interface MemoryRecalledPayload {
  count: number;
  source?: string;
  entries?: MemoryRecallEntryPayload[];
}

export interface RetrievalUsedPayload {
  snippet_count: number;
  tokens_used: number;
  anchor_count: number;
  edges_expanded: number;
  skipped: boolean;
}

export interface AttemptLogSurfacedPayload {
  entry_count: number;
  tokens_used: number;
  injected: boolean;
}

export interface AttemptRepeatedPayload {
  repeated_count: number;
}

export interface RateLimitWindowPayload {
  used_percentage: number;
  resets_at: number;
}

export interface RateLimitUpdatePayload {
  five_hour?: RateLimitWindowPayload;
  seven_day?: RateLimitWindowPayload;
}

export interface CompactStartPayload {
  strategy: string;
  tokens_before: number;
}

export interface CompactEndPayload {
  tokens_after: number;
}

export interface ArtifactCreatedPayload {
  id: string;
  kind: string;
  scope?: string;
  title: string;
  version?: number;
  source?: string;
  status?: string;
}

export interface ArtifactUpdatedPayload {
  id: string;
  content: string;
  version?: number;
  status?: string;
}

export interface SessionRestoredPayload {
  session_id: string;
  mode: string;
}

export interface SessionRewoundPayload {
  session_id: string;
  message_count?: number;
}

export interface SessionUpdatedPayload {
  session_id: string;
  title?: string;
}

export interface SlashCommandDescriptorPayload {
  name: string;
  description: string;
  usage?: string;
  takes_arguments?: boolean;
}

export interface ReadyPayload {
  protocol_version: number;
  slash_commands?: SlashCommandDescriptorPayload[];
}

export interface ErrorPayload {
  message: string;
  recoverable: boolean;
}

export interface NoticePayload {
  message: string;
}

export interface ArtifactFocusedPayload {
  id: string;
  kind: string;
  title: string;
  version?: number;
  status?: string;
}

export interface ArtifactStatusChangedPayload {
  id: string;
  status: string;
}

export interface ArtifactReviewRequestedPayload {
  request_id: string;
  id: string;
  kind: string;
  title: string;
  version?: number;
}

export interface ArtifactReviewResolvedPayload {
  request_id: string;
  decision: string; // "approved" | "revised" | "cancelled"
}

export interface BackgroundAgentUpdatedPayload {
  agent_id: string;
  invocation_id?: string;
  description?: string;
  subagent_type?: string;
  status: string;
  summary?: string;
  session_id?: string;
  transcript_path?: string;
  output_file?: string;
  error?: string;
  total_cost_usd?: number;
  input_tokens?: number;
  output_tokens?: number;
  metadata?: ChildAgentMetadata;
}

export interface ChildAgentMetadata {
  invocation_id?: string;
  agent_id?: string;
  description?: string;
  subagent_type?: string;
  lifecycle_state?: string;
  status_message?: string;
  stop_block_reason?: string;
  stop_block_count?: number;
  session_id?: string;
  transcript_path?: string;
  result_path?: string;
  tools?: string[];
}

export interface ArtifactReviewResponsePayload {
  request_id: string;
  decision: string; // "approve" | "revise" | "cancel"
  feedback?: string;
}
