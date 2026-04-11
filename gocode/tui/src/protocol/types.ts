// StreamEvent types (Go → Ink)
export type EventType =
  | "token_delta"
  | "thinking_delta"
  | "turn_complete"
  | "tool_start"
  | "tool_progress"
  | "tool_result"
  | "tool_error"
  | "permission_request"
  | "mode_changed"
  | "model_changed"
  | "context_window"
  | "cost_update"
  | "memory_recalled"
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

export interface SessionUpdatedPayload {
  session_id: string;
  title?: string;
}

export interface ReadyPayload {
  protocol_version: number;
}

export interface ErrorPayload {
  message: string;
  recoverable: boolean;
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
}

export interface ArtifactReviewResponsePayload {
  request_id: string;
  decision: string; // "approve" | "revise" | "cancel"
  feedback?: string;
}
