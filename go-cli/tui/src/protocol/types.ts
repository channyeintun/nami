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
  | "cost_update"
  | "compact_start"
  | "compact_end"
  | "artifact_created"
  | "artifact_updated"
  | "ready"
  | "error"
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
  | "shutdown";

export interface ClientMessage {
  type: ClientMessageType;
  payload?: unknown;
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

export interface ToolResultPayload {
  tool_id: string;
  output: string;
  truncated: boolean;
}

export interface PermissionRequestPayload {
  request_id: string;
  tool: string;
  command: string;
  risk: string;
}

export interface ModeChangedPayload {
  mode: string;
}

export interface CostUpdatePayload {
  total_usd: number;
  input_tokens: number;
  output_tokens: number;
}

export interface ReadyPayload {
  protocol_version: number;
}

export interface ErrorPayload {
  message: string;
  recoverable: boolean;
}
