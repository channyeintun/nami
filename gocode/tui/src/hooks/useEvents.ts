import { useState, useCallback } from "react";
import type {
  ArtifactCreatedPayload,
  ArtifactFocusedPayload,
  ArtifactReviewRequestedPayload,
  ArtifactReviewResolvedPayload,
  BackgroundAgentUpdatedPayload,
  ArtifactStatusChangedPayload,
  ArtifactUpdatedPayload,
  CompactEndPayload,
  CompactStartPayload,
  ContextWindowPayload,
  CostUpdatePayload,
  ErrorPayload,
  ModeChangedPayload,
  ModelChangedPayload,
  PermissionRequestPayload,
  RateLimitUpdatePayload,
  ReadyPayload,
  SessionRestoredPayload,
  SessionUpdatedPayload,
  StreamEvent,
  TurnCompletePayload,
  TokenDeltaPayload,
  ToolErrorPayload,
  ToolProgressPayload,
  ToolResultPayload,
  ToolStartPayload,
} from "../protocol/types.js";

export interface UIArtifactReview {
  requestId: string;
  id: string;
  kind: string;
  title: string;
  version: number;
}

export interface UIArtifact {
  id: string;
  kind: string;
  scope: string;
  title: string;
  version: number;
  source: string;
  status: string;
  content: string;
}

export interface UIAssistantBlock {
  kind: "text" | "thinking";
  text: string;
}

interface UIMessageBase {
  id: string;
  timestamp: string;
  model?: string;
}

export interface UIUserMessage extends UIMessageBase {
  role: "user";
  text: string;
}

export interface UIAssistantMessage extends UIMessageBase {
  role: "assistant";
  blocks: UIAssistantBlock[];
}

export type UIMessage = UIUserMessage | UIAssistantMessage;

export interface UITranscriptEntry {
  id: string;
  kind: "message" | "tool_call";
}

export type UIToolStatus =
  | "running"
  | "waiting_permission"
  | "completed"
  | "error";

export interface UIToolCall {
  id: string;
  name: string;
  input: string;
  status: UIToolStatus;
  output?: string;
  error?: string;
  truncated?: boolean;
  progressBytes?: number;
  permissionRequestId?: string;
  filePath?: string;
  preview?: string;
  insertions?: number;
  deletions?: number;
}

export interface UIBackgroundAgent {
  agentId: string;
  description: string;
  subagentType: string;
  status: string;
  summary: string;
  sessionId?: string;
  transcriptPath?: string;
  outputFile?: string;
  error?: string;
  updatedAt: string;
}

export interface UIRateLimitWindow {
  usedPercentage: number;
  resetsAt: number;
}

export interface UIRateLimits {
  fiveHour: UIRateLimitWindow | null;
  sevenDay: UIRateLimitWindow | null;
}

export type UIActiveTurnStatus =
  | "idle"
  | "working"
  | "thinking"
  | "responding"
  | "running_tools"
  | "waiting_permission"
  | "cancelling";

export interface EngineUIState {
  ready: boolean;
  messages: UIMessage[];
  transcript: UITranscriptEntry[];
  liveAssistantBlocks: UIAssistantBlock[];
  activeTurnStatus: UIActiveTurnStatus;
  showPlanPanel: boolean;
  mode: string;
  model: string;
  sessionId: string | null;
  sessionTitle: string | null;
  maxContextWindow: number | null;
  maxOutputTokens: number | null;
  currentContextUsage: number | null;
  cost: { totalUsd: number; inputTokens: number; outputTokens: number };
  rateLimits: UIRateLimits;
  artifacts: UIArtifact[];
  focusedArtifactId: string | null;
  pendingArtifactReview: UIArtifactReview | null;
  toolCalls: UIToolCall[];
  backgroundAgents: UIBackgroundAgent[];
  compact: {
    active: boolean;
    strategy: string;
    tokensBefore: number;
    tokensAfter?: number;
  } | null;
  statusLine: string | null;
  pendingPermission: PermissionRequestPayload | null;
  error: string | null;
  isStreaming: boolean;
}

const initialState = (model: string, mode: string): EngineUIState => ({
  ready: false,
  messages: [],
  transcript: [],
  liveAssistantBlocks: [],
  activeTurnStatus: "idle",
  showPlanPanel: false,
  mode,
  model,
  sessionId: null,
  sessionTitle: null,
  maxContextWindow: null,
  maxOutputTokens: null,
  currentContextUsage: null,
  cost: { totalUsd: 0, inputTokens: 0, outputTokens: 0 },
  rateLimits: { fiveHour: null, sevenDay: null },
  artifacts: [],
  focusedArtifactId: null,
  pendingArtifactReview: null,
  toolCalls: [],
  backgroundAgents: [],
  compact: null,
  statusLine: null,
  pendingPermission: null,
  error: null,
  isStreaming: false,
});

let nextMessageId = 0;

function createUserMessage(text: string): UIUserMessage {
  nextMessageId += 1;
  return {
    id: `msg-${nextMessageId}`,
    role: "user",
    text,
    timestamp: new Date().toISOString(),
  };
}

function createAssistantMessage(
  blocks: UIAssistantBlock[],
  options?: { model?: string },
): UIAssistantMessage {
  nextMessageId += 1;
  return {
    id: `msg-${nextMessageId}`,
    role: "assistant",
    blocks,
    timestamp: new Date().toISOString(),
    model: options?.model,
  };
}

export function useEvents(initialModel: string, initialMode: string) {
  const [uiState, setUIState] = useState<EngineUIState>(() =>
    initialState(initialModel, initialMode),
  );

  const handleEvent = useCallback((event: StreamEvent) => {
    switch (event.type) {
      case "ready": {
        const p = event.payload as ReadyPayload;
        setUIState((s) => ({
          ...s,
          ready: p.protocol_version > 0,
          statusLine: `Engine ready (protocol v${p.protocol_version})`,
        }));
        break;
      }
      case "token_delta": {
        const p = event.payload as TokenDeltaPayload;
        setUIState((s) => ({
          ...s,
          liveAssistantBlocks: appendAssistantBlock(
            s.liveAssistantBlocks,
            "text",
            p.text,
          ),
          activeTurnStatus: "responding",
          isStreaming: true,
          statusLine: null,
          error: null,
        }));
        break;
      }
      case "thinking_delta": {
        const p = event.payload as TokenDeltaPayload;
        setUIState((s) => ({
          ...s,
          liveAssistantBlocks: appendAssistantBlock(
            s.liveAssistantBlocks,
            "thinking",
            p.text,
          ),
          activeTurnStatus: "thinking",
          isStreaming: true,
          statusLine: null,
          error: null,
        }));
        break;
      }
      case "turn_complete": {
        const p = event.payload as TurnCompletePayload;
        setUIState((s) => {
          if (p.stop_reason === "cancelled") {
            const hasPartialResponse = assistantBlocksHaveContent(
              s.liveAssistantBlocks,
            );
            const partialMessage = hasPartialResponse
              ? createAssistantMessage(s.liveAssistantBlocks, {
                  model: s.model,
                })
              : null;

            return {
              ...s,
              messages: partialMessage
                ? [...s.messages, partialMessage]
                : s.messages,
              transcript: partialMessage
                ? appendTranscriptEntry(s.transcript, {
                    id: partialMessage.id,
                    kind: "message",
                  })
                : s.transcript,
              liveAssistantBlocks: [],
              activeTurnStatus: "idle",
              pendingPermission: null,
              isStreaming: false,
              compact: null,
              statusLine: "Turn cancelled",
            };
          }

          const blocks: UIAssistantBlock[] = assistantBlocksHaveContent(
            s.liveAssistantBlocks,
          )
            ? s.liveAssistantBlocks
            : [{ kind: "text", text: "(Model returned an empty response)" }];
          const message = createAssistantMessage(blocks, { model: s.model });
          return {
            ...s,
            messages: [...s.messages, message],
            transcript: appendTranscriptEntry(s.transcript, {
              id: message.id,
              kind: "message",
            }),
            liveAssistantBlocks: [],
            activeTurnStatus: "idle",
            isStreaming: false,
            compact: null,
            statusLine: `Turn complete (${p.stop_reason})`,
          };
        });
        break;
      }
      case "tool_start": {
        const p = event.payload as ToolStartPayload;
        setUIState((s) => ({
          ...s,
          activeTurnStatus: "running_tools",
          isStreaming: true,
          statusLine: null,
          error: null,
          transcript: appendTranscriptEntry(s.transcript, {
            id: p.tool_id,
            kind: "tool_call",
          }),
          toolCalls: upsertToolCall(s.toolCalls, {
            id: p.tool_id,
            name: p.name,
            input: p.input,
            status: "running",
            output: undefined,
            error: undefined,
            truncated: false,
            progressBytes: undefined,
            permissionRequestId: undefined,
          }),
        }));
        break;
      }
      case "tool_progress": {
        const p = event.payload as ToolProgressPayload;
        setUIState((s) => ({
          ...s,
          activeTurnStatus: "running_tools",
          isStreaming: true,
          transcript: appendTranscriptEntry(s.transcript, {
            id: p.tool_id,
            kind: "tool_call",
          }),
          toolCalls: upsertToolCall(s.toolCalls, {
            id: p.tool_id,
            name: toolCallName(s.toolCalls, p.tool_id),
            input: toolCallInput(s.toolCalls, p.tool_id),
            status: "running",
            progressBytes: p.bytes_read,
          }),
        }));
        break;
      }
      case "tool_result": {
        const p = event.payload as ToolResultPayload;
        setUIState((s) => ({
          ...s,
          activeTurnStatus: "working",
          isStreaming: true,
          transcript: appendTranscriptEntry(s.transcript, {
            id: p.tool_id,
            kind: "tool_call",
          }),
          toolCalls: upsertToolCall(s.toolCalls, {
            id: p.tool_id,
            name: p.name ?? toolCallName(s.toolCalls, p.tool_id),
            input: p.input ?? toolCallInput(s.toolCalls, p.tool_id),
            status: "completed",
            output: p.output,
            truncated: p.truncated,
            error: undefined,
            permissionRequestId: undefined,
            filePath: p.file_path,
            preview: p.preview,
            insertions: p.insertions,
            deletions: p.deletions,
          }),
          backgroundAgents: applyBackgroundAgentResult(s.backgroundAgents, p),
        }));
        break;
      }
      case "tool_error": {
        const p = event.payload as ToolErrorPayload;
        setUIState((s) => ({
          ...s,
          activeTurnStatus: "working",
          isStreaming: true,
          transcript: appendTranscriptEntry(s.transcript, {
            id: p.tool_id,
            kind: "tool_call",
          }),
          toolCalls: upsertToolCall(s.toolCalls, {
            id: p.tool_id,
            name: p.name ?? toolCallName(s.toolCalls, p.tool_id),
            input: p.input ?? toolCallInput(s.toolCalls, p.tool_id),
            status: "error",
            error: p.error,
            permissionRequestId: undefined,
          }),
        }));
        break;
      }
      case "compact_start": {
        const p = event.payload as CompactStartPayload;
        setUIState((s) => ({
          ...s,
          compact: {
            active: true,
            strategy: p.strategy,
            tokensBefore: p.tokens_before,
          },
          statusLine: null,
        }));
        break;
      }
      case "compact_end": {
        const p = event.payload as CompactEndPayload;
        setUIState((s) => ({
          ...s,
          compact: s.compact
            ? {
                ...s.compact,
                active: false,
                tokensAfter: p.tokens_after,
              }
            : {
                active: false,
                strategy: "compact",
                tokensBefore: 0,
                tokensAfter: p.tokens_after,
              },
          statusLine: `Compaction complete (${p.tokens_after} tokens)`,
        }));
        break;
      }
      case "permission_request": {
        const p = event.payload as PermissionRequestPayload;
        setUIState((s) => ({
          ...s,
          activeTurnStatus: "waiting_permission",
          isStreaming: true,
          pendingPermission: p,
          transcript: appendTranscriptEntry(s.transcript, {
            id: p.tool_id,
            kind: "tool_call",
          }),
          toolCalls: upsertToolCall(s.toolCalls, {
            id: p.tool_id,
            name: p.tool,
            input: p.command,
            status: "waiting_permission",
            permissionRequestId: p.request_id,
          }),
        }));
        break;
      }
      case "mode_changed": {
        const p = event.payload as ModeChangedPayload;
        setUIState((s) => ({
          ...s,
          mode: p.mode,
          showPlanPanel: p.mode === "plan" ? s.showPlanPanel : false,
        }));
        break;
      }
      case "model_changed": {
        const p = event.payload as ModelChangedPayload;
        setUIState((s) => ({
          ...s,
          model: p.model,
          maxContextWindow:
            typeof p.max_context_window === "number" && p.max_context_window > 0
              ? p.max_context_window
              : s.maxContextWindow,
          maxOutputTokens:
            typeof p.max_output_tokens === "number" && p.max_output_tokens > 0
              ? p.max_output_tokens
              : s.maxOutputTokens,
          rateLimits: { fiveHour: null, sevenDay: null },
        }));
        break;
      }
      case "context_window": {
        const p = event.payload as ContextWindowPayload;
        setUIState((s) => ({
          ...s,
          currentContextUsage:
            typeof p.current_usage === "number" && p.current_usage >= 0
              ? p.current_usage
              : s.currentContextUsage,
        }));
        break;
      }
      case "cost_update": {
        const p = event.payload as CostUpdatePayload;
        setUIState((s) => ({
          ...s,
          cost: {
            totalUsd: p.total_usd,
            inputTokens: p.input_tokens,
            outputTokens: p.output_tokens,
          },
        }));
        break;
      }
      case "rate_limit_update": {
        const p = event.payload as RateLimitUpdatePayload;
        setUIState((s) => ({
          ...s,
          rateLimits: {
            fiveHour: p.five_hour
              ? {
                  usedPercentage: p.five_hour.used_percentage,
                  resetsAt: p.five_hour.resets_at,
                }
              : null,
            sevenDay: p.seven_day
              ? {
                  usedPercentage: p.seven_day.used_percentage,
                  resetsAt: p.seven_day.resets_at,
                }
              : null,
          },
        }));
        break;
      }
      case "artifact_created": {
        const p = event.payload as ArtifactCreatedPayload;
        setUIState((s) => ({
          ...s,
          showPlanPanel:
            p.kind === "implementation-plan" ? false : s.showPlanPanel,
          artifacts: upsertArtifact(s.artifacts, {
            id: p.id,
            kind: p.kind,
            scope: p.scope ?? "session",
            title: p.title,
            version: p.version ?? 1,
            source: p.source ?? "",
            status: p.status ?? "",
            content: "",
          }),
        }));
        break;
      }
      case "artifact_updated": {
        const p = event.payload as ArtifactUpdatedPayload;
        setUIState((s) => {
          const existing = s.artifacts.find((a) => a.id === p.id);
          if (!existing) {
            // Ignore updates for artifacts that haven't been created yet
            return s;
          }
          return {
            ...s,
            showPlanPanel:
              existing.kind === "implementation-plan" &&
              p.content.trim().length > 0 &&
              s.mode === "plan",
            artifacts: upsertArtifact(s.artifacts, {
              id: p.id,
              kind: existing.kind,
              scope: existing.scope,
              title: existing.title,
              version: p.version ?? existing.version,
              source: existing.source,
              status: p.status ?? existing.status,
              content: p.content,
            }),
          };
        });
        break;
      }
      case "artifact_focused": {
        const p = event.payload as ArtifactFocusedPayload;
        setUIState((s) => ({
          ...s,
          focusedArtifactId: p.id,
          // Update status on the artifact entry if it changed
          artifacts: s.artifacts.map((a) =>
            a.id === p.id
              ? {
                  ...a,
                  version: p.version ?? a.version,
                  status: p.status ?? a.status,
                }
              : a,
          ),
        }));
        break;
      }
      case "artifact_status_changed": {
        const p = event.payload as ArtifactStatusChangedPayload;
        setUIState((s) => ({
          ...s,
          artifacts: s.artifacts.map((a) =>
            a.id === p.id ? { ...a, status: p.status } : a,
          ),
        }));
        break;
      }
      case "artifact_review_requested": {
        const p = event.payload as ArtifactReviewRequestedPayload;
        setUIState((s) => ({
          ...s,
          pendingArtifactReview: {
            requestId: p.request_id,
            id: p.id,
            kind: p.kind,
            title: p.title,
            version: p.version ?? 1,
          },
        }));
        break;
      }
      case "artifact_review_resolved": {
        const p = event.payload as ArtifactReviewResolvedPayload;
        setUIState((s) => {
          if (s.pendingArtifactReview?.requestId !== p.request_id) return s;
          return { ...s, pendingArtifactReview: null };
        });
        break;
      }
      case "background_agent_updated": {
        const p = event.payload as BackgroundAgentUpdatedPayload;
        setUIState((s) => ({
          ...s,
          backgroundAgents: upsertBackgroundAgent(s.backgroundAgents, {
            agentId: p.agent_id,
            description: stringOrEmpty(p.description),
            subagentType: stringOrEmpty(p.subagent_type),
            status: normalizeBackgroundAgentStatus(p.status),
            summary: summarizeBackgroundAgent(p.status, p.summary, p.error),
            sessionId: stringOrUndefined(p.session_id),
            transcriptPath: stringOrUndefined(p.transcript_path),
            outputFile: stringOrUndefined(p.output_file),
            error: stringOrUndefined(p.error),
            updatedAt: new Date().toISOString(),
          }),
        }));
        break;
      }
      case "session_restored": {
        const p = event.payload as SessionRestoredPayload;
        setUIState((s) => ({
          ...s,
          activeTurnStatus: "idle",
          showPlanPanel: false,
          ready: true,
          mode: p.mode,
          sessionId: p.session_id,
          sessionTitle: null,
          artifacts: [],
          focusedArtifactId: null,
          pendingArtifactReview: null,
          pendingPermission: null,
          isStreaming: false,
          error: null,
          statusLine: `Resumed session ${p.session_id}`,
        }));
        break;
      }
      case "session_updated": {
        const p = event.payload as SessionUpdatedPayload;
        setUIState((s) => {
          const normalizedTitle = p.title?.trim() ? p.title.trim() : null;
          if (
            normalizedTitle &&
            s.sessionId !== null &&
            s.sessionId !== p.session_id
          ) {
            return s;
          }

          return {
            ...s,
            sessionId: p.session_id,
            sessionTitle: normalizedTitle,
          };
        });
        break;
      }
      case "error": {
        const p = event.payload as ErrorPayload;
        setUIState((s) => ({
          ...s,
          activeTurnStatus: p.recoverable ? "working" : "idle",
          error: p.recoverable ? null : p.message,
          isStreaming: p.recoverable ? s.isStreaming : false,
          compact: null,
          statusLine: p.message,
        }));
        break;
      }
    }
  }, []);

  const clearStream = useCallback(() => {
    setUIState((s) => ({
      ...s,
      liveAssistantBlocks: [],
      activeTurnStatus: "idle",
      isStreaming: false,
      compact: null,
      statusLine: null,
      error: null,
    }));
  }, []);

  const cancelActiveTurn = useCallback(() => {
    setUIState((s) => ({
      ...s,
      activeTurnStatus: "cancelling",
      compact: null,
      pendingPermission: null,
      toolCalls: s.pendingPermission
        ? upsertToolCall(s.toolCalls, {
            id: s.pendingPermission.tool_id,
            name: s.pendingPermission.tool,
            input: s.pendingPermission.command,
            status: "waiting_permission",
            permissionRequestId: undefined,
          })
        : s.toolCalls,
      statusLine: "Cancellation requested...",
    }));
  }, []);

  const clearPermission = useCallback((decision?: string) => {
    setUIState((s) => ({
      ...s,
      activeTurnStatus:
        decision === "allow" ||
        decision === "always_allow" ||
        decision === "allow_all_session"
          ? "running_tools"
          : "working",
      pendingPermission: null,
      toolCalls: s.pendingPermission
        ? upsertToolCall(s.toolCalls, {
            id: s.pendingPermission.tool_id,
            name: s.pendingPermission.tool,
            input: s.pendingPermission.command,
            status:
              decision === "allow" ||
              decision === "always_allow" ||
              decision === "allow_all_session"
                ? "running"
                : "waiting_permission",
            permissionRequestId: undefined,
          })
        : s.toolCalls,
    }));
  }, []);

  const appendUserMessage = useCallback((text: string) => {
    setUIState((s) => {
      const message = createUserMessage(text);
      return {
        ...s,
        showPlanPanel: false,
        messages: [...s.messages, message],
        transcript: appendTranscriptEntry(s.transcript, {
          id: message.id,
          kind: "message",
        }),
      };
    });
  }, []);

  const beginAssistantTurn = useCallback(() => {
    setUIState((s) => ({
      ...s,
      liveAssistantBlocks: [],
      activeTurnStatus: "working",
      error: null,
      statusLine: null,
      isStreaming: true,
    }));
  }, []);

  return {
    uiState,
    handleEvent,
    clearStream,
    cancelActiveTurn,
    clearPermission,
    appendUserMessage,
    beginAssistantTurn,
  };
}

interface AgentToolInput {
  description?: string;
  subagent_type?: string;
  agent_id?: string;
  run_in_background?: boolean;
}

interface AgentToolResult {
  status?: string;
  agent_id?: string;
  subagent_type?: string;
  session_id?: string;
  transcript_path?: string;
  output_file?: string;
  summary?: string;
  error?: string;
}

function applyBackgroundAgentResult(
  agents: UIBackgroundAgent[],
  payload: ToolResultPayload,
): UIBackgroundAgent[] {
  const update = parseBackgroundAgentResult(payload);
  if (!update) {
    return agents;
  }
  return upsertBackgroundAgent(agents, update);
}

function parseBackgroundAgentResult(
  payload: ToolResultPayload,
): UIBackgroundAgent | null {
  if (
    payload.name !== "agent" &&
    payload.name !== "agent_status" &&
    payload.name !== "agent_stop"
  ) {
    return null;
  }

  const input = parseJSONObject<AgentToolInput>(payload.input);
  const result = parseJSONObject<AgentToolResult>(payload.output);
  if (!result) {
    return null;
  }

  const agentId = stringOrEmpty(result.agent_id || input?.agent_id);
  if (agentId.length === 0) {
    return null;
  }

  const backgroundLaunch =
    payload.name === "agent" && input?.run_in_background === true;
  const backgroundStatusCheck =
    payload.name === "agent_status" || payload.name === "agent_stop";
  if (!backgroundLaunch && !backgroundStatusCheck) {
    return null;
  }

  const status = normalizeBackgroundAgentStatus(result.status);
  return {
    agentId,
    description: stringOrEmpty(input?.description),
    subagentType: stringOrEmpty(result.subagent_type || input?.subagent_type),
    status,
    summary: summarizeBackgroundAgent(status, result.summary, result.error),
    sessionId: stringOrUndefined(result.session_id),
    transcriptPath: stringOrUndefined(result.transcript_path),
    outputFile: stringOrUndefined(result.output_file),
    error: stringOrUndefined(result.error),
    updatedAt: new Date().toISOString(),
  };
}

function upsertBackgroundAgent(
  agents: UIBackgroundAgent[],
  nextAgent: UIBackgroundAgent,
): UIBackgroundAgent[] {
  const existing = agents.find((agent) => agent.agentId === nextAgent.agentId);
  const merged: UIBackgroundAgent = existing
    ? {
        ...existing,
        ...nextAgent,
        description: nextAgent.description || existing.description,
        subagentType: nextAgent.subagentType || existing.subagentType,
        summary: nextAgent.summary || existing.summary,
        sessionId: nextAgent.sessionId ?? existing.sessionId,
        transcriptPath: nextAgent.transcriptPath ?? existing.transcriptPath,
        outputFile: nextAgent.outputFile ?? existing.outputFile,
        error: nextAgent.error ?? existing.error,
      }
    : nextAgent;

  const remaining = agents.filter((agent) => agent.agentId !== merged.agentId);
  return [merged, ...remaining].sort(compareBackgroundAgents);
}

function compareBackgroundAgents(
  left: UIBackgroundAgent,
  right: UIBackgroundAgent,
): number {
  const leftRank = backgroundAgentStatusRank(left.status);
  const rightRank = backgroundAgentStatusRank(right.status);
  if (leftRank !== rightRank) {
    return leftRank - rightRank;
  }
  return right.updatedAt.localeCompare(left.updatedAt);
}

function backgroundAgentStatusRank(status: string): number {
  switch (status) {
    case "running":
    case "cancelling":
      return 0;
    case "failed":
      return 1;
    case "cancelled":
      return 2;
    case "completed":
      return 3;
    default:
      return 4;
  }
}

function normalizeBackgroundAgentStatus(status?: string): string {
  const normalized = stringOrEmpty(status);
  if (normalized === "async_launched") {
    return "running";
  }
  return normalized || "running";
}

function summarizeBackgroundAgent(
  status: string,
  summary?: string,
  error?: string,
): string {
  const normalizedSummary = stringOrEmpty(summary);
  if (normalizedSummary.length > 0) {
    return normalizedSummary;
  }

  const normalizedError = stringOrEmpty(error);
  if (normalizedError.length > 0) {
    return normalizedError;
  }

  switch (status) {
    case "running":
      return "Child agent running in the background.";
    case "cancelling":
      return "Cancellation requested for the background child agent.";
    case "completed":
      return "Background child agent completed.";
    case "cancelled":
      return "Background child agent cancelled.";
    case "failed":
      return "Background child agent failed.";
    default:
      return "Background child agent updated.";
  }
}

function parseJSONObject<T>(value: string | undefined): T | null {
  if (!value) {
    return null;
  }
  try {
    return JSON.parse(value) as T;
  } catch {
    return null;
  }
}

function stringOrEmpty(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function stringOrUndefined(value: unknown): string | undefined {
  const normalized = stringOrEmpty(value);
  return normalized.length > 0 ? normalized : undefined;
}

function upsertArtifact(
  artifacts: UIArtifact[],
  nextArtifact: UIArtifact,
): UIArtifact[] {
  const remaining = artifacts.filter(
    (artifact) => artifact.id !== nextArtifact.id,
  );
  return [nextArtifact, ...remaining];
}

function appendTranscriptEntry(
  transcript: UITranscriptEntry[],
  entry: UITranscriptEntry,
): UITranscriptEntry[] {
  if (
    transcript.some(
      (current) => current.id === entry.id && current.kind === entry.kind,
    )
  ) {
    return transcript;
  }
  return [...transcript, entry];
}

function upsertToolCall(
  toolCalls: UIToolCall[],
  nextToolCall: UIToolCall,
): UIToolCall[] {
  const existing = toolCalls.find(
    (toolCall) => toolCall.id === nextToolCall.id,
  );
  if (!existing) {
    return [...toolCalls, nextToolCall];
  }

  return toolCalls.map((toolCall) =>
    toolCall.id === nextToolCall.id
      ? {
          ...toolCall,
          ...nextToolCall,
          name: nextToolCall.name || toolCall.name,
          input: nextToolCall.input || toolCall.input,
          output:
            nextToolCall.output !== undefined
              ? nextToolCall.output
              : toolCall.output,
          error:
            nextToolCall.error !== undefined
              ? nextToolCall.error
              : toolCall.error,
          truncated:
            nextToolCall.truncated !== undefined
              ? nextToolCall.truncated
              : toolCall.truncated,
          progressBytes:
            nextToolCall.progressBytes !== undefined
              ? nextToolCall.progressBytes
              : toolCall.progressBytes,
          permissionRequestId:
            nextToolCall.permissionRequestId !== undefined
              ? nextToolCall.permissionRequestId
              : toolCall.permissionRequestId,
          filePath:
            nextToolCall.filePath !== undefined
              ? nextToolCall.filePath
              : toolCall.filePath,
          preview:
            nextToolCall.preview !== undefined
              ? nextToolCall.preview
              : toolCall.preview,
          insertions:
            nextToolCall.insertions !== undefined
              ? nextToolCall.insertions
              : toolCall.insertions,
          deletions:
            nextToolCall.deletions !== undefined
              ? nextToolCall.deletions
              : toolCall.deletions,
        }
      : toolCall,
  );
}

function toolCallName(toolCalls: UIToolCall[], id: string): string {
  return toolCalls.find((toolCall) => toolCall.id === id)?.name ?? "tool";
}

function toolCallInput(toolCalls: UIToolCall[], id: string): string {
  return toolCalls.find((toolCall) => toolCall.id === id)?.input ?? "";
}

function appendAssistantBlock(
  blocks: UIAssistantBlock[],
  kind: UIAssistantBlock["kind"],
  text: string,
): UIAssistantBlock[] {
  if (text.length === 0) {
    return blocks;
  }

  const lastBlock = blocks[blocks.length - 1];
  if (lastBlock?.kind === kind) {
    return [
      ...blocks.slice(0, -1),
      { ...lastBlock, text: lastBlock.text + text },
    ];
  }

  return [...blocks, { kind, text }];
}

function assistantBlocksHaveContent(blocks: UIAssistantBlock[]): boolean {
  return blocks.some((block) => block.text.trim().length > 0);
}

function findArtifactField(
  artifacts: UIArtifact[],
  id: string,
  field: "kind" | "title",
  fallback: string,
): string {
  const artifact = artifacts.find((entry) => entry.id === id);
  return artifact?.[field] ?? fallback;
}
