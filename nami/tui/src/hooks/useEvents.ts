import { useState, useCallback, useEffect, useRef } from "react";
import type {
  AskUserQuestionOptionPayload,
  AskUserQuestionRequestedPayload,
  ArtifactCreatedPayload,
  ArtifactFocusedPayload,
  ArtifactReviewRequestedPayload,
  ArtifactReviewResolvedPayload,
  BackgroundCommandUpdatedPayload,
  BackgroundAgentUpdatedPayload,
  ChildAgentMetadata,
  ArtifactStatusChangedPayload,
  ArtifactUpdatedPayload,
  AttemptLogSurfacedPayload,
  CompactEndPayload,
  CompactStartPayload,
  ConversationHydratedMessageBlockPayload,
  ConversationHydratedMessagePayload,
  ConversationHydratedPayload,
  ConversationHydratedProgressPayload,
  ConversationHydratedToolCallPayload,
  ConversationHydratedTranscriptEntryPayload,
  ContextWindowPayload,
  CostUpdatePayload,
  ErrorPayload,
  NoticePayload,
  ModelSelectionOptionPayload,
  ModelSelectionRequestedPayload,
  ModeChangedPayload,
  ModelChangedPayload,
  PermissionRequestPayload,
  ProgressPayload,
  RateLimitUpdatePayload,
  RewindSelectionRequestedPayload,
  RewindSelectionTurnPayload,
  ResumeSelectionRequestedPayload,
  ResumeSelectionSessionPayload,
  ReadyPayload,
  SessionRewoundPayload,
  SessionRestoredPayload,
  SlashCommandDescriptorPayload,
  SessionUpdatedPayload,
  StreamEvent,
  TurnCompletePayload,
  TokenDeltaPayload,
  ToolErrorPayload,
  ToolProgressPayload,
  ToolResultPayload,
  ToolStartPayload,
} from "../protocol/types.js";

const BEL = "\u0007";
const STREAM_FLUSH_INTERVAL_MS = 33;
const MAX_AGENT_TOOL_JSON_CHARS = 128 * 1024;
const MAX_AGENT_TOOL_DISPLAY_CHARS = 16_000;
const MAX_BACKGROUND_AGENT_SUMMARY_CHARS = 4_000;
const AGENT_UI_TRUNCATION_NOTE =
  "\n\n[truncated for live UI; full child-agent result is available in the child session files]";

function ringTerminalBell() {
  if (!process.stdout.isTTY) {
    return;
  }
  process.stdout.write(BEL);
}

export interface UIArtifactReview {
  requestId: string;
  id: string;
  kind: string;
  title: string;
  version: number;
}

export interface UIResumeSelectionSession {
  sessionId: string;
  title: string;
  updatedAt: string | null;
  model: string | null;
  totalCostUsd: number;
}

export interface UIResumeSelection {
  requestId: string;
  sessions: UIResumeSelectionSession[];
}

export interface UIAskUserQuestionOption {
  label: string;
  value: string;
  description: string | null;
  recommended: boolean;
}

export interface UIAskUserQuestionPrompt {
  header: string;
  question: string;
  multiSelect: boolean;
  allowFreeform: boolean;
  options: UIAskUserQuestionOption[];
}

export interface UIAskUserQuestionAnswer {
  header: string;
  selectedValues: string[];
  freeformText: string;
  rawAnswer: string;
}

export interface UIAskUserQuestionRequest {
  requestId: string;
  questions: UIAskUserQuestionPrompt[];
}

export interface UIRewindSelectionTurn {
  messageIndex: number;
  turnNumber: number;
  preview: string;
}

export interface UIRewindSelection {
  requestId: string;
  turns: UIRewindSelectionTurn[];
}

export interface UIModelSelectionOption {
  label: string;
  model: string | null;
  provider: string | null;
  description: string | null;
  isCustom: boolean;
  active: boolean;
}

export interface UIModelSelection {
  requestId: string;
  currentModel: string | null;
  title?: string;
  description?: string;
  options: UIModelSelectionOption[];
}

export type UIArtifactReviewDecision = "approve" | "revise" | "cancel";

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

interface PendingAssistantChunk {
  kind: UIAssistantBlock["kind"];
  text: string;
}

interface UIMessageBase {
  id: string;
  timestamp: string;
  model?: string;
}

export interface UISystemMessage extends UIMessageBase {
  role: "system";
  text: string;
  tone: "info" | "success" | "warning" | "error";
  label?: string;
}

export interface UIUserMessage extends UIMessageBase {
  role: "user";
  text: string;
}

export interface UIAssistantMessage extends UIMessageBase {
  role: "assistant";
  blocks: UIAssistantBlock[];
}

export type UIMessage = UIUserMessage | UIAssistantMessage | UISystemMessage;

export interface UIProgressEntry {
  id: string;
  text: string;
  timestamp: string;
}

export interface UITranscriptEntry {
  id: string;
  kind: "message" | "tool_call" | "artifact" | "progress";
  refId?: string;
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
  diagnostics?: string;
  errorKind?: string;
  errorHint?: string;
}

export interface UIBackgroundAgent {
  agentId: string;
  invocationId: string;
  description: string;
  subagentType: string;
  status: string;
  summary: string;
  lifecycleState?: string;
  statusMessage?: string;
  stopBlockReason?: string;
  stopBlockCount: number;
  sessionId?: string;
  transcriptPath?: string;
  outputFile?: string;
  tools: string[];
  error?: string;
  totalCostUsd: number;
  inputTokens: number;
  outputTokens: number;
  updatedAt: string;
}

export interface UIBackgroundCommand {
  commandId: string;
  command: string;
  cwd?: string;
  status: string;
  running: boolean;
  startedAt?: string;
  updatedAt?: string;
  preview?: string;
  previewKind?: "latest" | "unread";
  unreadBytes: number;
  exitCode?: number;
  error?: string;
  retainedAt: string;
}

export interface UISlashCommand {
  name: string;
  description: string;
  usage?: string;
  takesArguments: boolean;
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
  slashCommands: UISlashCommand[];
  messages: UIMessage[];
  progressEntries: UIProgressEntry[];
  transcript: UITranscriptEntry[];
  liveAssistantMessageId: string | null;
  liveAssistantBlocks: UIAssistantBlock[];
  activeTurnStatus: UIActiveTurnStatus;
  showPlanPanel: boolean;
  mode: string;
  model: string;
  reasoningEffort: string | null;
  sessionId: string | null;
  sessionTitle: string | null;
  maxContextWindow: number | null;
  maxOutputTokens: number | null;
  currentContextUsage: number | null;
  cost: {
    totalUsd: number;
    inputTokens: number;
    outputTokens: number;
    memoryRecallUsd: number;
    memoryRecallInputTokens: number;
    memoryRecallOutputTokens: number;
    childAgentUsd: number;
    childAgentInputTokens: number;
    childAgentOutputTokens: number;
  };
  rateLimits: UIRateLimits;
  artifacts: UIArtifact[];
  focusedArtifactId: string | null;
  pendingArtifactReview: UIArtifactReview | null;
  pendingModelSelection: UIModelSelection | null;
  pendingRewindSelection: UIRewindSelection | null;
  pendingResumeSelection: UIResumeSelection | null;
  pendingAskUserQuestion: UIAskUserQuestionRequest | null;
  submittingArtifactReviewRequestId: string | null;
  toolCalls: UIToolCall[];
  backgroundAgents: UIBackgroundAgent[];
  backgroundCommands: UIBackgroundCommand[];
  compact: {
    active: boolean;
    strategy: string;
    tokensBefore: number;
    tokensAfter?: number;
  } | null;
  allowedPermissionFileTypes: string[];
  statusLine: string | null;
  pendingPermission: PermissionRequestPayload | null;
  error: string | null;
  isStreaming: boolean;
}

const MAX_RETAINED_BACKGROUND_AGENTS = 24;

function emptyCostState(): EngineUIState["cost"] {
  return {
    totalUsd: 0,
    inputTokens: 0,
    outputTokens: 0,
    memoryRecallUsd: 0,
    memoryRecallInputTokens: 0,
    memoryRecallOutputTokens: 0,
    childAgentUsd: 0,
    childAgentInputTokens: 0,
    childAgentOutputTokens: 0,
  };
}

const initialState = (model: string, mode: string): EngineUIState => ({
  ready: false,
  slashCommands: [],
  messages: [],
  progressEntries: [],
  transcript: [],
  liveAssistantMessageId: null,
  liveAssistantBlocks: [],
  activeTurnStatus: "idle",
  showPlanPanel: false,
  mode,
  model,
  reasoningEffort: null,
  sessionId: null,
  sessionTitle: null,
  maxContextWindow: null,
  maxOutputTokens: null,
  currentContextUsage: null,
  cost: emptyCostState(),
  rateLimits: { fiveHour: null, sevenDay: null },
  artifacts: [],
  focusedArtifactId: null,
  pendingArtifactReview: null,
  pendingModelSelection: null,
  pendingRewindSelection: null,
  pendingResumeSelection: null,
  pendingAskUserQuestion: null,
  submittingArtifactReviewRequestId: null,
  toolCalls: [],
  backgroundAgents: [],
  backgroundCommands: [],
  compact: null,
  allowedPermissionFileTypes: [],
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
  options?: { id?: string; model?: string },
): UIAssistantMessage {
  const id = options?.id ?? nextGeneratedMessageID();
  return {
    id,
    role: "assistant",
    blocks,
    timestamp: new Date().toISOString(),
    model: options?.model,
  };
}

function nextGeneratedMessageID(): string {
  nextMessageId += 1;
  return `msg-${nextMessageId}`;
}

function createSystemMessage(
  text: string,
  tone: UISystemMessage["tone"],
  label?: string,
): UISystemMessage {
  nextMessageId += 1;
  return {
    id: `msg-${nextMessageId}`,
    role: "system",
    text,
    tone,
    label,
    timestamp: new Date().toISOString(),
  };
}

function createProgressEntry(id: string, text: string): UIProgressEntry {
  return {
    id,
    text,
    timestamp: new Date().toISOString(),
  };
}

export function useEvents(initialModel: string, initialMode: string) {
  const [uiState, setUIState] = useState<EngineUIState>(() =>
    initialState(initialModel, initialMode),
  );
  const pendingAssistantChunksRef = useRef<PendingAssistantChunk[]>([]);
  const streamFlushTimerRef = useRef<ReturnType<typeof setTimeout> | null>(
    null,
  );

  const flushQueuedAssistantBlocks = useCallback(() => {
    if (streamFlushTimerRef.current) {
      clearTimeout(streamFlushTimerRef.current);
      streamFlushTimerRef.current = null;
    }

    const queued = pendingAssistantChunksRef.current;
    if (queued.length === 0) {
      return;
    }

    pendingAssistantChunksRef.current = [];
    setUIState((s) => {
      let liveAssistantBlocks = s.liveAssistantBlocks;
      let activeTurnStatus = s.activeTurnStatus;

      for (const chunk of queued) {
        liveAssistantBlocks = appendAssistantBlock(
          liveAssistantBlocks,
          chunk.kind,
          chunk.text,
        );
        activeTurnStatus =
          chunk.kind === "thinking" ? "thinking" : "responding";
      }

      return {
        ...s,
        liveAssistantBlocks,
        activeTurnStatus,
        isStreaming: true,
        statusLine: null,
        error: null,
      };
    });
  }, []);

  const resetQueuedAssistantBlocks = useCallback(() => {
    if (streamFlushTimerRef.current) {
      clearTimeout(streamFlushTimerRef.current);
      streamFlushTimerRef.current = null;
    }

    pendingAssistantChunksRef.current = [];
  }, []);

  const scheduleAssistantBlockFlush = useCallback(() => {
    if (streamFlushTimerRef.current) {
      return;
    }

    streamFlushTimerRef.current = setTimeout(() => {
      flushQueuedAssistantBlocks();
    }, STREAM_FLUSH_INTERVAL_MS);
  }, [flushQueuedAssistantBlocks]);

  const queueAssistantBlock = useCallback(
    (kind: UIAssistantBlock["kind"], text: string) => {
      if (text.length === 0) {
        return;
      }

      const queued = pendingAssistantChunksRef.current;
      const lastChunk = queued[queued.length - 1];
      if (lastChunk?.kind === kind) {
        lastChunk.text += text;
      } else {
        queued.push({ kind, text });
      }

      scheduleAssistantBlockFlush();
    },
    [scheduleAssistantBlockFlush],
  );

  useEffect(() => {
    return () => {
      resetQueuedAssistantBlocks();
    };
  }, [resetQueuedAssistantBlocks]);

  const handleEvent = useCallback((event: StreamEvent) => {
    switch (event.type) {
      case "ready": {
        const p = event.payload as ReadyPayload;
        setUIState((s) => ({
          ...s,
          ready: p.protocol_version > 0,
          slashCommands: normalizeSlashCommands(p.slash_commands),
          statusLine: null,
        }));
        break;
      }
      case "token_delta": {
        const p = event.payload as TokenDeltaPayload;
        setUIState((s) => {
          const liveAssistantMessageId =
            s.liveAssistantMessageId ?? nextGeneratedMessageID();
          return {
            ...s,
            transcript:
              s.liveAssistantMessageId === null
                ? appendTranscriptEntry(s.transcript, {
                    id: liveAssistantMessageId,
                    kind: "message",
                  })
                : s.transcript,
            liveAssistantMessageId,
            liveAssistantBlocks: appendAssistantBlock(
              s.liveAssistantBlocks,
              "text",
              p.text,
            ),
            activeTurnStatus: "responding",
            isStreaming: true,
            statusLine: null,
            error: null,
          };
        });
        break;
      }
      case "thinking_delta": {
        const p = event.payload as TokenDeltaPayload;
        setUIState((s) => {
          const liveAssistantMessageId =
            s.liveAssistantMessageId ?? nextGeneratedMessageID();
          return {
            ...s,
            transcript:
              s.liveAssistantMessageId === null
                ? appendTranscriptEntry(s.transcript, {
                    id: liveAssistantMessageId,
                    kind: "message",
                  })
                : s.transcript,
            liveAssistantMessageId,
            liveAssistantBlocks: appendAssistantBlock(
              s.liveAssistantBlocks,
              "thinking",
              p.text,
            ),
            activeTurnStatus: "thinking",
            isStreaming: true,
            statusLine: null,
            error: null,
          };
        });
        break;
      }
      case "progress": {
        const p = event.payload as ProgressPayload;
        const progressID = stringOrEmpty(p.id);
        const progressMessage = stringOrEmpty(p.message);
        if (!progressID || !progressMessage) {
          break;
        }

        setUIState((s) => ({
          ...s,
          progressEntries: upsertProgressEntry(
            s.progressEntries,
            createProgressEntry(progressID, progressMessage),
          ),
          transcript: appendTranscriptEntry(s.transcript, {
            id: progressID,
            kind: "progress",
          }),
          activeTurnStatus:
            s.activeTurnStatus === "idle" ? "working" : s.activeTurnStatus,
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
            const partialBlocks = completedAssistantBlocks(
              s.liveAssistantBlocks,
            );
            const hasPartialResponse = partialBlocks.length > 0;
            const partialMessage = hasPartialResponse
              ? createAssistantMessage(partialBlocks, {
                  id: s.liveAssistantMessageId ?? undefined,
                  model: s.model,
                })
              : null;

            return {
              ...s,
              messages: partialMessage
                ? [...s.messages, partialMessage]
                : s.messages,
              transcript: s.transcript,
              liveAssistantMessageId: null,
              liveAssistantBlocks: [],
              activeTurnStatus: "idle",
              pendingPermission: null,
              submittingArtifactReviewRequestId: null,
              isStreaming: false,
              compact: null,
              statusLine: buildTurnCompleteStatusLine("cancelled"),
            };
          }

          const completedBlocks = completedAssistantBlocks(
            s.liveAssistantBlocks,
          );
          const blocks: UIAssistantBlock[] =
            completedBlocks.length > 0
              ? completedBlocks
              : [{ kind: "text", text: "(Model returned an empty response)" }];
          const message = createAssistantMessage(blocks, {
            id: s.liveAssistantMessageId ?? undefined,
            model: s.model,
          });
          return {
            ...s,
            messages: [...s.messages, message],
            liveAssistantMessageId: null,
            liveAssistantBlocks: [],
            activeTurnStatus: "idle",
            submittingArtifactReviewRequestId: null,
            isStreaming: false,
            compact: null,
            statusLine: buildTurnCompleteStatusLine(p.stop_reason),
          };
        });
        if (p.stop_reason !== "cancelled") {
          ringTerminalBell();
        }
        break;
      }
      case "turn_timing": {
        break;
      }
      case "tool_start": {
        const p = event.payload as ToolStartPayload;
        resetQueuedAssistantBlocks();
        setUIState((s) => {
          const completedBlocks = completedAssistantBlocks(
            s.liveAssistantBlocks,
          );
          const streamedMessage =
            completedBlocks.length > 0
              ? createAssistantMessage(completedBlocks, {
                  id: s.liveAssistantMessageId ?? undefined,
                  model: s.model,
                })
              : null;

          return {
            ...s,
            messages: streamedMessage
              ? [...s.messages, streamedMessage]
              : s.messages,
            activeTurnStatus: "running_tools",
            isStreaming: true,
            statusLine: null,
            error: null,
            liveAssistantMessageId: null,
            liveAssistantBlocks: [],
            transcript: appendTranscriptEntry(s.transcript, {
              id: p.tool_id,
              kind: "tool_call",
            }),
            toolCalls: upsertToolCall(s.toolCalls, {
              id: p.tool_id,
              name: p.name,
              input: sanitizeToolCallInput(p.name, p.input),
              status: "running",
              output: undefined,
              error: undefined,
              truncated: false,
              progressBytes: undefined,
              permissionRequestId: undefined,
            }),
          };
        });
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
        setUIState((s) => {
          const normalizedToolResult = sanitizeToolResultPayload(p);
          const backgroundNotice = buildBackgroundCommandNotice(
            s.backgroundCommands,
            p,
          );
          const nextBackgroundCommands = reduceBackgroundCommands(
            s.backgroundCommands,
            p,
          );
          const noticeMessage = backgroundNotice
            ? createSystemMessage(
                backgroundNotice.text,
                backgroundNotice.tone,
                "Background Command",
              )
            : null;

          return {
            ...s,
            activeTurnStatus: "working",
            isStreaming: true,
            transcript: appendTranscriptEntry(
              noticeMessage
                ? appendTranscriptEntry(s.transcript, {
                    id: p.tool_id,
                    kind: "tool_call",
                  })
                : s.transcript,
              noticeMessage
                ? {
                    id: noticeMessage.id,
                    kind: "message",
                  }
                : {
                    id: p.tool_id,
                    kind: "tool_call",
                  },
            ),
            toolCalls: upsertToolCall(s.toolCalls, {
              id: normalizedToolResult.tool_id,
              name:
                normalizedToolResult.name ??
                toolCallName(s.toolCalls, normalizedToolResult.tool_id),
              input:
                normalizedToolResult.input ??
                toolCallInput(s.toolCalls, normalizedToolResult.tool_id),
              status: "completed",
              output: normalizedToolResult.output,
              truncated: normalizedToolResult.truncated,
              error: undefined,
              permissionRequestId: undefined,
              filePath: normalizedToolResult.file_path,
              preview: normalizedToolResult.preview,
              insertions: normalizedToolResult.insertions,
              deletions: normalizedToolResult.deletions,
              diagnostics: normalizedToolResult.diagnostics,
              errorKind: normalizedToolResult.error_kind,
              errorHint: normalizedToolResult.error_hint,
            }),
            backgroundAgents: applyBackgroundAgentResult(
              s.backgroundAgents,
              normalizedToolResult,
            ),
            backgroundCommands: nextBackgroundCommands,
            messages: noticeMessage
              ? [...s.messages, noticeMessage]
              : s.messages,
            statusLine: backgroundNotice?.text ?? s.statusLine,
          };
        });
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
            input: sanitizeToolCallInput(
              p.name ?? toolCallName(s.toolCalls, p.tool_id),
              p.input ?? toolCallInput(s.toolCalls, p.tool_id),
            ),
            status: "error",
            error: p.error,
            filePath: p.file_path,
            errorKind: p.error_kind,
            errorHint: p.error_hint,
            permissionRequestId: undefined,
          }),
        }));
        break;
      }
      case "compact_start": {
        const p = event.payload as CompactStartPayload;
        setUIState((s) => ({
          ...s,
          activeTurnStatus: "working",
          isStreaming: true,
          compact: {
            active: true,
            strategy: p.strategy,
            tokensBefore: p.tokens_before,
          },
          error: null,
          statusLine: null,
        }));
        break;
      }
      case "compact_end": {
        const p = event.payload as CompactEndPayload;
        setUIState((s) => ({
          ...s,
          activeTurnStatus: "working",
          isStreaming: true,
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
        const permissionInput = permissionRequestDisplayInput(p);
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
            input: permissionInput,
            status: "waiting_permission",
            permissionRequestId: p.request_id,
          }),
        }));
        break;
      }
      case "ask_user_question_requested": {
        const p = event.payload as AskUserQuestionRequestedPayload;
        setUIState((s) => ({
          ...s,
          activeTurnStatus: "working",
          isStreaming: true,
          pendingAskUserQuestion: {
            requestId: p.request_id,
            questions: normalizeAskUserQuestionPrompts(p.questions),
          },
          statusLine: "Answer the question to continue.",
          error: null,
        }));
        break;
      }
      case "model_selection_requested": {
        const p = event.payload as ModelSelectionRequestedPayload;
        const title =
          typeof p.title === "string" && p.title.trim().length > 0
            ? p.title.trim()
            : undefined;
        const description =
          typeof p.description === "string" && p.description.trim().length > 0
            ? p.description.trim()
            : undefined;
        setUIState((s) => ({
          ...s,
          pendingModelSelection: {
            requestId: p.request_id,
            currentModel:
              typeof p.current_model === "string" &&
              p.current_model.trim().length > 0
                ? p.current_model.trim()
                : null,
            title,
            description,
            options: normalizeModelSelectionOptions(p.options),
          },
          statusLine: title ?? "Select a model.",
          error: null,
        }));
        break;
      }
      case "rewind_selection_requested": {
        const p = event.payload as RewindSelectionRequestedPayload;
        setUIState((s) => ({
          ...s,
          pendingRewindSelection: {
            requestId: p.request_id,
            turns: normalizeRewindSelectionTurns(p.turns),
          },
          statusLine: "Select a turn to rewind to.",
          error: null,
        }));
        break;
      }
      case "resume_selection_requested": {
        const p = event.payload as ResumeSelectionRequestedPayload;
        setUIState((s) => ({
          ...s,
          pendingResumeSelection: {
            requestId: p.request_id,
            sessions: normalizeResumeSelectionSessions(p.sessions),
          },
          statusLine: "Select a session to resume.",
          error: null,
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
          reasoningEffort:
            typeof p.reasoning_effort === "string" && p.reasoning_effort.trim()
              ? p.reasoning_effort.trim()
              : null,
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
            memoryRecallUsd:
              typeof p.memory_recall_usd === "number" ? p.memory_recall_usd : 0,
            memoryRecallInputTokens:
              typeof p.memory_recall_input_tokens === "number"
                ? p.memory_recall_input_tokens
                : 0,
            memoryRecallOutputTokens:
              typeof p.memory_recall_output_tokens === "number"
                ? p.memory_recall_output_tokens
                : 0,
            childAgentUsd:
              typeof p.child_agent_usd === "number" ? p.child_agent_usd : 0,
            childAgentInputTokens:
              typeof p.child_agent_input_tokens === "number"
                ? p.child_agent_input_tokens
                : 0,
            childAgentOutputTokens:
              typeof p.child_agent_output_tokens === "number"
                ? p.child_agent_output_tokens
                : 0,
          },
        }));
        break;
      }
      case "conversation_hydrated": {
        const p = event.payload as ConversationHydratedPayload;
        setUIState((s) => ({
          ...s,
          messages: normalizeHydratedMessages(p.messages),
          progressEntries: normalizeHydratedProgressEntries(p.progress),
          toolCalls: normalizeHydratedToolCalls(p.tool_calls),
          transcript: normalizeHydratedTranscriptEntries(p.transcript),
          liveAssistantMessageId: null,
          liveAssistantBlocks: [],
          activeTurnStatus: "idle",
          isStreaming: false,
          error: null,
        }));
        break;
      }
      case "memory_recalled": {
        break;
      }
      case "retrieval_used": {
        break;
      }
      case "attempt_log_surfaced": {
        // Telemetry-only event; no UI state change needed.
        break;
      }
      case "attempt_repeated": {
        // Telemetry-only event; no UI state change needed.
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
          transcript: appendTranscriptEntry(s.transcript, {
            id: p.id,
            kind: "artifact",
          }),
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
        setUIState((s) => {
          const nextArtifacts = s.artifacts.map((a) =>
            a.id === p.id
              ? {
                  ...a,
                  version: p.version ?? a.version,
                  status: p.status ?? a.status,
                }
              : a,
          );
          const focusedArtifact = nextArtifacts.find(
            (artifact) => artifact.id === p.id,
          );

          return {
            ...s,
            focusedArtifactId: p.id,
            artifacts: nextArtifacts,
            statusLine: buildArtifactFocusStatusLine(focusedArtifact, p.id),
          };
        });
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
          submittingArtifactReviewRequestId: null,
        }));
        break;
      }
      case "artifact_review_resolved": {
        const p = event.payload as ArtifactReviewResolvedPayload;
        setUIState((s) => {
          if (s.pendingArtifactReview?.requestId !== p.request_id) return s;
          return {
            ...s,
            pendingArtifactReview: null,
            submittingArtifactReviewRequestId: null,
          };
        });
        break;
      }
      case "background_agent_updated": {
        const p = event.payload as BackgroundAgentUpdatedPayload;
        setUIState((s) => {
          const previousAgent = s.backgroundAgents.find(
            (agent) => agent.agentId === p.agent_id,
          );
          const metadata = normalizeChildAgentMetadata(
            sanitizeChildAgentMetadataPayload(p.metadata),
            {
              invocationId: p.invocation_id,
              description: p.description,
              subagentType: p.subagent_type,
              sessionId: p.session_id,
              transcriptPath: p.transcript_path,
              resultPath: p.output_file,
            },
          );
          const nextAgent = {
            agentId: p.agent_id,
            invocationId: metadata.invocationId,
            description: metadata.description,
            subagentType: metadata.subagentType,
            status: normalizeBackgroundAgentStatus(p.status),
            summary: summarizeBackgroundAgent(
              p.status,
              metadata.subagentType,
              sanitizeAgentDisplayText(
                p.summary,
                MAX_BACKGROUND_AGENT_SUMMARY_CHARS,
              ),
              sanitizeAgentDisplayText(
                p.error,
                MAX_BACKGROUND_AGENT_SUMMARY_CHARS,
              ),
              metadata.statusMessage,
            ),
            lifecycleState: metadata.lifecycleState,
            statusMessage: metadata.statusMessage,
            stopBlockReason: metadata.stopBlockReason,
            stopBlockCount: metadata.stopBlockCount,
            sessionId: metadata.sessionId,
            transcriptPath: metadata.transcriptPath,
            outputFile: metadata.resultPath,
            tools: metadata.tools,
            error: stringOrUndefined(p.error),
            totalCostUsd: numberOrZero(p.total_cost_usd),
            inputTokens: numberOrZero(p.input_tokens),
            outputTokens: numberOrZero(p.output_tokens),
            updatedAt: new Date().toISOString(),
          };
          const notice = buildBackgroundAgentNotice(previousAgent, nextAgent);
          const noticeMessage = notice
            ? createSystemMessage(notice.text, notice.tone, "Background Agent")
            : null;

          return {
            ...s,
            backgroundAgents: upsertBackgroundAgent(
              s.backgroundAgents,
              nextAgent,
            ),
            messages: noticeMessage
              ? [...s.messages, noticeMessage]
              : s.messages,
            transcript: noticeMessage
              ? appendTranscriptEntry(s.transcript, {
                  id: noticeMessage.id,
                  kind: "message",
                })
              : s.transcript,
            statusLine: notice?.text ?? s.statusLine,
          };
        });
        break;
      }
      case "background_command_updated": {
        const p = event.payload as BackgroundCommandUpdatedPayload;
        setUIState((s) => {
          const nextCommand = backgroundCommandFromEventPayload(p);
          if (!nextCommand) {
            return s;
          }

          const previousCommand = s.backgroundCommands.find(
            (command) => command.commandId === nextCommand.commandId,
          );
          const notice = buildBackgroundCommandTransitionNotice(
            previousCommand,
            mergeBackgroundCommand(previousCommand, nextCommand),
            "background_command_updated",
          );
          const noticeMessage = notice
            ? createSystemMessage(
                notice.text,
                notice.tone,
                "Background Command",
              )
            : null;

          return {
            ...s,
            backgroundCommands: upsertBackgroundCommand(
              s.backgroundCommands,
              nextCommand,
            ),
            messages: noticeMessage
              ? [...s.messages, noticeMessage]
              : s.messages,
            transcript: noticeMessage
              ? appendTranscriptEntry(s.transcript, {
                  id: noticeMessage.id,
                  kind: "message",
                })
              : s.transcript,
            statusLine: notice?.text ?? s.statusLine,
          };
        });
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
          pendingModelSelection: null,
          pendingRewindSelection: null,
          pendingResumeSelection: null,
          pendingAskUserQuestion: null,
          submittingArtifactReviewRequestId: null,
          pendingPermission: null,
          isStreaming: false,
          error: null,
          statusLine: `Resumed session ${p.session_id}`,
        }));
        break;
      }
      case "session_rewound": {
        const p = event.payload as SessionRewoundPayload;
        setUIState((s) => ({
          ...s,
          messages: [],
          progressEntries: [],
          transcript: [],
          liveAssistantMessageId: null,
          liveAssistantBlocks: [],
          activeTurnStatus: "idle",
          showPlanPanel: false,
          sessionId: p.session_id,
          artifacts: [],
          focusedArtifactId: null,
          pendingArtifactReview: null,
          pendingModelSelection: null,
          pendingRewindSelection: null,
          pendingResumeSelection: null,
          pendingAskUserQuestion: null,
          submittingArtifactReviewRequestId: null,
          pendingPermission: null,
          toolCalls: [],
          backgroundAgents: [],
          backgroundCommands: [],
          compact: null,
          isStreaming: false,
          error: null,
          statusLine:
            typeof p.message_count === "number" && p.message_count >= 0
              ? `Rewound session ${p.session_id} to ${p.message_count} messages`
              : `Rewound session ${p.session_id}`,
        }));
        break;
      }
      case "session_updated": {
        const p = event.payload as SessionUpdatedPayload;
        setUIState((s) => {
          const normalizedTitle = p.title?.trim() ? p.title.trim() : null;
          const hasSessionScopedState =
            s.messages.length > 0 ||
            s.progressEntries.length > 0 ||
            s.transcript.length > 0 ||
            s.liveAssistantBlocks.length > 0 ||
            s.toolCalls.length > 0 ||
            s.artifacts.length > 0 ||
            s.pendingArtifactReview !== null ||
            s.pendingAskUserQuestion !== null ||
            s.pendingModelSelection !== null ||
            s.pendingRewindSelection !== null ||
            s.pendingPermission !== null ||
            s.isStreaming;
          const sessionChanged =
            typeof p.session_id === "string" &&
            p.session_id.length > 0 &&
            p.session_id !== s.sessionId;
          if (normalizedTitle && sessionChanged) {
            return s;
          }

          if (
            sessionChanged &&
            (s.sessionId !== null || hasSessionScopedState)
          ) {
            return {
              ...s,
              messages: [],
              progressEntries: [],
              transcript: [],
              liveAssistantMessageId: null,
              liveAssistantBlocks: [],
              activeTurnStatus: "idle",
              showPlanPanel: false,
              sessionId: p.session_id,
              sessionTitle: normalizedTitle,
              currentContextUsage: 0,
              cost: emptyCostState(),
              artifacts: [],
              focusedArtifactId: null,
              pendingArtifactReview: null,
              pendingModelSelection: null,
              pendingRewindSelection: null,
              pendingResumeSelection: null,
              pendingAskUserQuestion: null,
              submittingArtifactReviewRequestId: null,
              toolCalls: [],
              backgroundAgents: [],
              backgroundCommands: [],
              compact: null,
              statusLine: `Started new session ${p.session_id}`,
              pendingPermission: null,
              error: null,
              isStreaming: false,
            };
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
        setUIState((s) => {
          const stopCompact = p.recoverable && s.compact?.active;
          return {
            ...s,
            activeTurnStatus: p.recoverable
              ? stopCompact
                ? "idle"
                : "working"
              : "idle",
            error: p.recoverable ? null : p.message,
            submittingArtifactReviewRequestId: null,
            isStreaming: p.recoverable
              ? stopCompact
                ? false
                : s.isStreaming
              : false,
            compact: null,
            pendingAskUserQuestion: p.recoverable
              ? s.pendingAskUserQuestion
              : null,
            statusLine: p.message,
          };
        });
        break;
      }
      case "notice": {
        const p = event.payload as NoticePayload;
        setUIState((s) => ({
          ...s,
          statusLine: p.message,
        }));
        break;
      }
    }
  }, []);

  const clearStream = useCallback(() => {
    setUIState((s) => ({
      ...s,
      liveAssistantMessageId: null,
      liveAssistantBlocks: [],
      activeTurnStatus: "idle",
      isStreaming: false,
      compact: null,
      pendingModelSelection: null,
      pendingRewindSelection: null,
      pendingResumeSelection: null,
      pendingAskUserQuestion: null,
      submittingArtifactReviewRequestId: null,
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
            input: permissionRequestDisplayInput(s.pendingPermission),
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
      allowedPermissionFileTypes:
        decision === "allow" ||
        decision === "always_allow" ||
        decision === "allow_all_session"
          ? mergeAllowedPermissionFileTypes(
              s.allowedPermissionFileTypes,
              extractAllowedPermissionFileTypes(s.pendingPermission),
            )
          : s.allowedPermissionFileTypes,
      pendingPermission: null,
      toolCalls: s.pendingPermission
        ? upsertToolCall(s.toolCalls, {
            id: s.pendingPermission.tool_id,
            name: s.pendingPermission.tool,
            input: permissionRequestDisplayInput(s.pendingPermission),
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
      liveAssistantMessageId: null,
      activeTurnStatus: "working",
      error: null,
      statusLine: null,
      isStreaming: true,
    }));
  }, []);

  const submitArtifactReview = useCallback(
    (requestId: string, decision: UIArtifactReviewDecision) => {
      setUIState((s) => {
        if (s.pendingArtifactReview?.requestId !== requestId) {
          return s;
        }

        return {
          ...s,
          // Clear the review prompt immediately on approve so the
          // spinner/input area becomes visible without waiting for
          // the backend artifact_review_resolved event.
          pendingArtifactReview:
            decision === "approve" ? null : s.pendingArtifactReview,
          submittingArtifactReviewRequestId: requestId,
          activeTurnStatus:
            decision === "approve" ? "working" : s.activeTurnStatus,
          isStreaming: decision === "approve" ? true : s.isStreaming,
          statusLine: decision === "approve" ? null : s.statusLine,
          error: null,
        };
      });
    },
    [],
  );

  const submitResumeSelection = useCallback((requestId: string) => {
    setUIState((s) => {
      if (s.pendingResumeSelection?.requestId !== requestId) {
        return s;
      }

      return {
        ...s,
        pendingResumeSelection: null,
        statusLine: null,
        error: null,
      };
    });
  }, []);

  const submitAskUserQuestion = useCallback((requestId: string) => {
    setUIState((s) => {
      if (s.pendingAskUserQuestion?.requestId !== requestId) {
        return s;
      }

      return {
        ...s,
        pendingAskUserQuestion: null,
        statusLine: null,
        error: null,
      };
    });
  }, []);

  const submitRewindSelection = useCallback((requestId: string) => {
    setUIState((s) => {
      if (s.pendingRewindSelection?.requestId !== requestId) {
        return s;
      }

      return {
        ...s,
        pendingRewindSelection: null,
        statusLine: null,
        error: null,
      };
    });
  }, []);

  const submitModelSelection = useCallback((requestId: string) => {
    setUIState((s) => {
      if (s.pendingModelSelection?.requestId !== requestId) {
        return s;
      }

      return {
        ...s,
        pendingModelSelection: null,
        statusLine: null,
        error: null,
      };
    });
  }, []);

  return {
    uiState,
    handleEvent,
    clearStream,
    cancelActiveTurn,
    clearPermission,
    appendUserMessage,
    beginAssistantTurn,
    submitArtifactReview,
    submitAskUserQuestion,
    submitModelSelection,
    submitRewindSelection,
    submitResumeSelection,
  };
}

function mergeAllowedPermissionFileTypes(
  current: string[],
  next: string[],
): string[] {
  if (next.length === 0) {
    return current;
  }

  const seen = new Set(current);
  const merged = [...current];
  for (const value of next) {
    if (seen.has(value)) {
      continue;
    }
    seen.add(value);
    merged.push(value);
  }

  return merged;
}

function extractAllowedPermissionFileTypes(
  permission: PermissionRequestPayload | null,
): string[] {
  if (!permission) {
    return [];
  }

  const kind = permission.target_kind?.trim().toLowerCase();
  if (kind !== "file" && kind !== "files") {
    return [];
  }

  const fileTypes = new Set<string>();
  for (const target of extractPermissionTargetPaths(permission)) {
    const fileType = normalizePermissionFileType(target);
    if (fileType) {
      fileTypes.add(fileType);
    }
  }

  return Array.from(fileTypes).sort();
}

function extractPermissionTargetPaths(
  permission: PermissionRequestPayload,
): string[] {
  const targets = new Set<string>();
  const rawInput = parsePermissionRawInput(permission.raw_input);

  const addTarget = (value: unknown) => {
    if (typeof value !== "string") {
      return;
    }
    const trimmed = value.trim();
    if (trimmed) {
      targets.add(trimmed);
    }
  };

  if (rawInput && typeof rawInput === "object") {
    const params = rawInput as Record<string, unknown>;
    addTarget(params.file_path);
    addTarget(params.filePath);

    if (Array.isArray(params.replacements)) {
      for (const replacement of params.replacements) {
        if (!replacement || typeof replacement !== "object") {
          continue;
        }
        const entry = replacement as Record<string, unknown>;
        addTarget(entry.file_path);
        addTarget(entry.filePath);
      }
    }

    const patch =
      typeof params.input === "string"
        ? params.input
        : typeof params.patch === "string"
          ? params.patch
          : "";
    for (const target of extractApplyPatchPermissionTargets(patch)) {
      addTarget(target);
    }
  }

  if (targets.size === 0) {
    addTarget(permission.target_value);
  }

  return Array.from(targets);
}

function parsePermissionRawInput(rawInput: string | undefined): unknown {
  if (typeof rawInput !== "string" || rawInput.trim().length === 0) {
    return null;
  }

  try {
    return JSON.parse(rawInput);
  } catch {
    return null;
  }
}

function extractApplyPatchPermissionTargets(patch: string): string[] {
  if (!patch.trim()) {
    return [];
  }

  const targets = new Set<string>();
  const matches = patch.matchAll(/^\*\*\* (?:Add|Update|Delete) File: (.+)$/gm);
  for (const match of matches) {
    const target = match[1]?.trim();
    if (target) {
      targets.add(target);
    }
  }

  return Array.from(targets);
}

function normalizePermissionFileType(target: string): string | null {
  const trimmed = target.trim();
  if (!trimmed) {
    return null;
  }

  const baseName = trimmed.split(/[\\/]/).at(-1)?.trim() ?? "";
  const extensionMatch = baseName.match(/(\.[A-Za-z0-9_-]+)$/);
  if (!extensionMatch) {
    return null;
  }

  return extensionMatch[1].toLowerCase();
}

function normalizeAskUserQuestionPrompts(
  payload: AskUserQuestionRequestedPayload["questions"] | undefined,
): UIAskUserQuestionPrompt[] {
  if (!Array.isArray(payload)) {
    return [];
  }

  return payload
    .filter(
      (question) =>
        typeof question?.header === "string" &&
        question.header.trim().length > 0 &&
        typeof question?.question === "string" &&
        question.question.trim().length > 0,
    )
    .map((question) => ({
      header: question.header.trim(),
      question: question.question.trim(),
      multiSelect: Boolean(question.multi_select),
      allowFreeform: Boolean(question.allow_freeform),
      options: normalizeAskUserQuestionOptions(question.options),
    }));
}

function normalizeAskUserQuestionOptions(
  payload: AskUserQuestionOptionPayload[] | undefined,
): UIAskUserQuestionOption[] {
  if (!Array.isArray(payload)) {
    return [];
  }

  return payload
    .filter(
      (option) =>
        typeof option?.label === "string" &&
        option.label.trim().length > 0 &&
        typeof option?.value === "string" &&
        option.value.trim().length > 0,
    )
    .map((option) => ({
      label: option.label.trim(),
      value: option.value.trim(),
      description:
        typeof option.description === "string" && option.description.trim()
          ? option.description.trim()
          : null,
      recommended: Boolean(option.recommended),
    }));
}

function normalizeRewindSelectionTurns(
  payload: RewindSelectionTurnPayload[] | undefined,
): UIRewindSelectionTurn[] {
  if (!Array.isArray(payload)) {
    return [];
  }

  return payload
    .filter(
      (turn) =>
        typeof turn?.message_index === "number" && turn.message_index >= 0,
    )
    .map((turn, index) => ({
      messageIndex: turn.message_index,
      turnNumber:
        typeof turn.turn_number === "number" && turn.turn_number > 0
          ? turn.turn_number
          : index + 1,
      preview:
        typeof turn.preview === "string" && turn.preview.trim().length > 0
          ? turn.preview.trim()
          : "(no prompt text)",
    }));
}

function normalizeHydratedMessages(
  payload: ConversationHydratedMessagePayload[] | undefined,
): UIMessage[] {
  if (!Array.isArray(payload)) {
    return [];
  }

  const timestamp = new Date().toISOString();

  return payload.flatMap((message): UIMessage[] => {
    if (typeof message?.id !== "string" || message.id.trim().length === 0) {
      return [];
    }

    switch (message.role) {
      case "user": {
        const text = stringOrEmpty(message.text);
        if (text.length === 0) {
          return [];
        }
        const taskNotification = parseTaskNotificationMessage(text);
        if (taskNotification) {
          return [
            {
              id: message.id.trim(),
              role: "system",
              text: taskNotification.summary,
              tone: taskNotification.tone,
              label: taskNotification.label,
              timestamp,
            },
          ];
        }
        return [
          {
            id: message.id.trim(),
            role: "user",
            text,
            timestamp,
          },
        ];
      }
      case "assistant": {
        const blocks = normalizeHydratedAssistantBlocks(message.blocks);
        if (blocks.length === 0) {
          return [];
        }
        return [
          {
            id: message.id.trim(),
            role: "assistant",
            blocks,
            timestamp,
            model: stringOrUndefined(message.model),
          },
        ];
      }
      case "system": {
        const text = stringOrEmpty(message.text);
        if (text.length === 0) {
          return [];
        }
        return [
          {
            id: message.id.trim(),
            role: "system",
            text,
            tone: normalizeHydratedSystemTone(message.tone),
            timestamp,
          },
        ];
      }
      default:
        return [];
    }
  });
}

function normalizeHydratedAssistantBlocks(
  payload: ConversationHydratedMessageBlockPayload[] | undefined,
): UIAssistantBlock[] {
  if (!Array.isArray(payload)) {
    return [];
  }

  return payload.flatMap((block) => {
    if (block?.kind !== "text" && block?.kind !== "thinking") {
      return [];
    }

    const text = stringOrEmpty(block.text);
    if (text.length === 0) {
      return [];
    }

    return [{ kind: block.kind, text }];
  });
}

function normalizeHydratedSystemTone(
  tone: string | undefined,
): UISystemMessage["tone"] {
  switch (tone) {
    case "success":
    case "warning":
    case "error":
      return tone;
    default:
      return "info";
  }
}

function normalizeHydratedToolCalls(
  payload: ConversationHydratedToolCallPayload[] | undefined,
): UIToolCall[] {
  if (!Array.isArray(payload)) {
    return [];
  }

  return payload.flatMap((toolCall) => {
    if (typeof toolCall?.id !== "string" || toolCall.id.trim().length === 0) {
      return [];
    }

    const name = stringOrEmpty(toolCall.name) || "tool";
    const input = sanitizeToolCallInput(name, stringOrEmpty(toolCall.input));
    const output = sanitizeHydratedToolOutput(name, toolCall.output);
    const error = stringOrUndefined(toolCall.error);

    return [
      {
        id: toolCall.id.trim(),
        name,
        input,
        status: normalizeHydratedToolStatus(toolCall.status, error),
        output,
        truncated: toolCall.truncated === true,
        error,
        filePath: stringOrUndefined(toolCall.file_path),
        preview: stringOrUndefined(toolCall.preview),
        insertions: numberOrUndefined(toolCall.insertions),
        deletions: numberOrUndefined(toolCall.deletions),
        diagnostics: stringOrUndefined(toolCall.diagnostics),
        errorKind: stringOrUndefined(toolCall.error_kind),
        errorHint: stringOrUndefined(toolCall.error_hint),
      },
    ];
  });
}

function normalizeHydratedProgressEntries(
  payload: ConversationHydratedProgressPayload[] | undefined,
): UIProgressEntry[] {
  if (!Array.isArray(payload)) {
    return [];
  }

  return payload.flatMap((entry) => {
    const id = stringOrUndefined(entry?.id);
    const text = stringOrUndefined(entry?.message);
    if (!id || !text) {
      return [];
    }

    return [createProgressEntry(id, text)];
  });
}

function normalizeHydratedToolStatus(
  status: string | undefined,
  error: string | undefined,
): UIToolStatus {
  switch (status) {
    case "running":
    case "waiting_permission":
    case "completed":
    case "error":
      return status;
    default:
      return error ? "error" : "completed";
  }
}

function sanitizeHydratedToolOutput(
  name: string,
  output: string | undefined,
): string | undefined {
  if (typeof output !== "string") {
    return undefined;
  }

  if (isAgentToolName(name)) {
    return sanitizeAgentToolOutput(output);
  }

  return output;
}

function normalizeHydratedTranscriptEntries(
  payload: ConversationHydratedTranscriptEntryPayload[] | undefined,
): UITranscriptEntry[] {
  if (!Array.isArray(payload)) {
    return [];
  }

  return payload.flatMap((entry) => {
    if (typeof entry?.id !== "string" || entry.id.trim().length === 0) {
      return [];
    }
    if (
      entry.kind !== "message" &&
      entry.kind !== "tool_call" &&
      entry.kind !== "artifact" &&
      entry.kind !== "progress"
    ) {
      return [];
    }

    return [
      {
        id: entry.id.trim(),
        kind: entry.kind,
        refId: stringOrUndefined(entry.ref_id),
      },
    ];
  });
}

function normalizeModelSelectionOptions(
  payload: ModelSelectionOptionPayload[] | undefined,
): UIModelSelectionOption[] {
  if (!Array.isArray(payload)) {
    return [];
  }

  return payload
    .filter(
      (option) =>
        typeof option?.label === "string" && option.label.trim().length > 0,
    )
    .map((option) => ({
      label: option.label.trim(),
      model:
        typeof option.model === "string" && option.model.trim().length > 0
          ? option.model.trim()
          : null,
      provider:
        typeof option.provider === "string" && option.provider.trim().length > 0
          ? option.provider.trim()
          : null,
      description:
        typeof option.description === "string" &&
        option.description.trim().length > 0
          ? option.description.trim()
          : null,
      isCustom: option.is_custom === true,
      active: option.active === true,
    }));
}

function normalizeResumeSelectionSessions(
  payload: ResumeSelectionSessionPayload[] | undefined,
): UIResumeSelectionSession[] {
  if (!Array.isArray(payload)) {
    return [];
  }

  return payload
    .filter(
      (session) =>
        typeof session?.session_id === "string" &&
        session.session_id.trim().length > 0,
    )
    .map((session) => ({
      sessionId: session.session_id.trim(),
      title:
        typeof session.title === "string" && session.title.trim().length > 0
          ? session.title.trim()
          : "(untitled)",
      updatedAt:
        typeof session.updated_at === "string" &&
        session.updated_at.trim().length > 0
          ? session.updated_at.trim()
          : null,
      model:
        typeof session.model === "string" && session.model.trim().length > 0
          ? session.model.trim()
          : null,
      totalCostUsd:
        typeof session.total_cost_usd === "number" ? session.total_cost_usd : 0,
    }));
}

function normalizeSlashCommands(
  payload: SlashCommandDescriptorPayload[] | undefined,
): UISlashCommand[] {
  if (!Array.isArray(payload)) {
    return [];
  }

  return payload
    .filter(
      (command) =>
        typeof command?.name === "string" &&
        command.name.trim().length > 0 &&
        typeof command?.description === "string" &&
        command.description.trim().length > 0,
    )
    .map((command) => ({
      name: command.name.trim(),
      description: command.description.trim(),
      usage:
        typeof command.usage === "string" && command.usage.trim().length > 0
          ? command.usage.trim()
          : undefined,
      takesArguments: command.takes_arguments === true,
    }));
}

function buildTurnCompleteStatusLine(stopReason: string): string {
  return `Turn complete (${stopReason})`;
}

function buildArtifactFocusStatusLine(
  artifact: UIArtifact | undefined,
  fallbackID: string,
): string {
  if (!artifact) {
    return `Focused artifact ${fallbackID}`;
  }

  const label = artifact.title.trim() || artifact.id;
  const kind = artifactKindLabel(artifact.kind);
  const status = artifact.status.trim();
  if (status.length > 0) {
    return `Focused ${kind}: ${label} [${status}]`;
  }
  return `Focused ${kind}: ${label}`;
}

function artifactKindLabel(kind: string): string {
  switch (kind) {
    case "implementation-plan":
      return "implementation plan";
    case "task-list":
      return "task list";
    case "tool-log":
      return "tool log";
    case "search-report":
      return "search report";
    case "diff-preview":
      return "diff preview";
    case "compact-summary":
      return "compact summary";
    case "knowledge-item":
      return "knowledge item";
    default:
      return kind.replace(/-/g, " ");
  }
}

interface AgentToolInput {
  description?: string;
  prompt?: string;
  subagent_type?: string;
  agent_id?: string;
  run_in_background?: boolean;
  wait_ms?: number;
}

interface AgentToolResult {
  status?: string;
  invocation_id?: string;
  agent_id?: string;
  subagent_type?: string;
  session_id?: string;
  transcript_path?: string;
  output_file?: string;
  summary?: string;
  error?: string;
  total_cost_usd?: number;
  input_tokens?: number;
  output_tokens?: number;
  metadata?: ChildAgentMetadata;
}

interface BackgroundCommandToolInput {
  command?: string;
  background?: boolean;
  cwd?: string;
  CommandId?: string;
  command_id?: string;
}

interface BackgroundCommandToolResult {
  CommandId?: string;
  Command?: string;
  Cwd?: string;
  Running?: boolean;
  StartedAt?: string;
  UpdatedAt?: string;
  Output?: string;
  Error?: string;
  ExitCode?: number;
}

interface BackgroundCommandSummaryResult {
  CommandId?: string;
  Command?: string;
  Cwd?: string;
  Running?: boolean;
  Error?: string;
  ExitCode?: number;
  StartedAt?: string;
  UpdatedAt?: string;
  HasUnreadOutput?: boolean;
  UnreadBytes?: number;
  UnreadPreview?: string;
}

interface BackgroundAgentNotice {
  text: string;
  tone: UISystemMessage["tone"];
}

interface BackgroundCommandNotice {
  text: string;
  tone: UISystemMessage["tone"];
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

function applyBackgroundCommandResult(
  commands: UIBackgroundCommand[],
  payload: ToolResultPayload,
): UIBackgroundCommand[] {
  const updates = parseBackgroundCommandResult(payload);
  if (updates.length === 0) {
    return commands;
  }

  return updates.reduce(
    (nextCommands, update) => upsertBackgroundCommand(nextCommands, update),
    commands,
  );
}

function buildBackgroundCommandNotice(
  commands: UIBackgroundCommand[],
  payload: ToolResultPayload,
): BackgroundCommandNotice | null {
  if (payload.name === "forget_command") {
    const forgottenCommand = parseForgottenBackgroundCommand(payload);
    if (!forgottenCommand) {
      return null;
    }
    return {
      text: summarizeNoticeWithDetail(
        `${backgroundCommandSubject(forgottenCommand)} removed from retention.`,
        forgottenCommand.preview || "",
      ),
      tone: "info",
    };
  }

  if (payload.name === "list_commands") {
    return null;
  }

  const updates = parseBackgroundCommandResult(payload);
  if (updates.length === 0) {
    return null;
  }

  let previousCommands = commands;
  let nextNotice: BackgroundCommandNotice | null = null;

  for (const update of updates) {
    const previousCommand = previousCommands.find(
      (command) => command.commandId === update.commandId,
    );
    const mergedCommand = mergeBackgroundCommand(previousCommand, update);
    const notice = buildBackgroundCommandTransitionNotice(
      previousCommand,
      mergedCommand,
      payload.name,
    );
    if (notice) {
      nextNotice = notice;
    }
    previousCommands = upsertBackgroundCommand(previousCommands, update);
  }

  return nextNotice;
}

function reduceBackgroundCommands(
  commands: UIBackgroundCommand[],
  payload: ToolResultPayload,
): UIBackgroundCommand[] {
  if (payload.name === "forget_command") {
    const forgottenCommand = parseForgottenBackgroundCommand(payload);
    if (!forgottenCommand) {
      return commands;
    }
    return removeBackgroundCommand(commands, forgottenCommand.commandId);
  }

  return applyBackgroundCommandResult(commands, payload);
}

function backgroundCommandFromEventPayload(
  payload: BackgroundCommandUpdatedPayload,
): UIBackgroundCommand | null {
  const commandId = stringOrEmpty(payload.command_id);
  if (commandId.length === 0) {
    return null;
  }

  const command = stringOrEmpty(payload.command);
  const cwd = stringOrUndefined(payload.cwd);
  const running = payload.running === true;
  const status = normalizeBackgroundCommandEventStatus(
    payload.status,
    running,
    payload.exit_code,
    payload.error,
  );
  const preview = stringOrEmpty(payload.output_preview);
  const unreadBytes = numberOrZero(payload.unread_bytes);

  return {
    commandId,
    command,
    cwd,
    status,
    running,
    startedAt: stringOrUndefined(payload.started_at),
    updatedAt: stringOrUndefined(payload.updated_at),
    preview: preview || undefined,
    previewKind:
      preview.length > 0 && payload.has_unread_output === true
        ? "unread"
        : preview.length > 0
          ? "latest"
          : undefined,
    unreadBytes,
    exitCode: numberOrUndefined(payload.exit_code),
    error: stringOrUndefined(payload.error),
    retainedAt: new Date().toISOString(),
  };
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

  const input = parseJSONObject<AgentToolInput>(
    payload.input,
    MAX_AGENT_TOOL_JSON_CHARS,
  );
  const result = parseJSONObject<AgentToolResult>(
    payload.output,
    MAX_AGENT_TOOL_JSON_CHARS,
  );
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

  const metadata = normalizeChildAgentMetadata(result.metadata, {
    invocationId: result.invocation_id || result.session_id,
    description: input?.description,
    subagentType: result.subagent_type || input?.subagent_type,
    sessionId: result.session_id,
    transcriptPath: result.transcript_path,
    resultPath: result.output_file,
  });

  const status = normalizeBackgroundAgentStatus(result.status);
  return {
    agentId,
    invocationId: metadata.invocationId,
    description: metadata.description,
    subagentType: metadata.subagentType,
    status,
    summary: summarizeBackgroundAgent(
      status,
      metadata.subagentType,
      result.summary,
      result.error,
      metadata.statusMessage,
    ),
    lifecycleState: metadata.lifecycleState,
    statusMessage: metadata.statusMessage,
    stopBlockReason: metadata.stopBlockReason,
    stopBlockCount: metadata.stopBlockCount,
    sessionId: metadata.sessionId,
    transcriptPath: metadata.transcriptPath,
    outputFile: metadata.resultPath,
    tools: metadata.tools,
    error: stringOrUndefined(result.error),
    totalCostUsd: numberOrZero(result.total_cost_usd),
    inputTokens: numberOrZero(result.input_tokens),
    outputTokens: numberOrZero(result.output_tokens),
    updatedAt: new Date().toISOString(),
  };
}

function parseBackgroundCommandResult(
  payload: ToolResultPayload,
): UIBackgroundCommand[] {
  switch (payload.name) {
    case "bash":
      return parseBackgroundBashLaunch(payload);
    case "command_status":
    case "send_command_input":
    case "stop_command":
      return parseSingleBackgroundCommandResult(payload);
    case "list_commands":
      return parseBackgroundCommandListResult(payload);
    default:
      return [];
  }
}

function parseForgottenBackgroundCommand(
  payload: ToolResultPayload,
): UIBackgroundCommand | null {
  if (payload.name !== "forget_command") {
    return null;
  }

  const input = parseJSONObject<BackgroundCommandToolInput>(payload.input);
  const result = parseJSONObject<BackgroundCommandToolResult>(payload.output);
  if (!result) {
    return null;
  }

  return buildBackgroundCommandEntry(result, {
    sourceToolName: payload.name,
    fallbackCommandId: input?.CommandId || input?.command_id,
    fallbackCommand: input?.command,
    fallbackCwd: input?.cwd,
  });
}

function parseBackgroundBashLaunch(
  payload: ToolResultPayload,
): UIBackgroundCommand[] {
  const input = parseJSONObject<BackgroundCommandToolInput>(payload.input);
  if (!input?.background) {
    return [];
  }

  const result = parseJSONObject<BackgroundCommandToolResult>(payload.output);
  if (!result) {
    return [];
  }

  const command = buildBackgroundCommandEntry(result, {
    sourceToolName: payload.name,
    fallbackCommand: input.command,
    fallbackCwd: input.cwd,
  });
  return command ? [command] : [];
}

function parseSingleBackgroundCommandResult(
  payload: ToolResultPayload,
): UIBackgroundCommand[] {
  const input = parseJSONObject<BackgroundCommandToolInput>(payload.input);
  const result = parseJSONObject<BackgroundCommandToolResult>(payload.output);
  if (!result) {
    return [];
  }

  const command = buildBackgroundCommandEntry(result, {
    sourceToolName: payload.name,
    fallbackCommandId: input?.CommandId || input?.command_id,
    fallbackCommand: input?.command,
    fallbackCwd: input?.cwd,
  });
  return command ? [command] : [];
}

function parseBackgroundCommandListResult(
  payload: ToolResultPayload,
): UIBackgroundCommand[] {
  const result = parseJSONArray<BackgroundCommandSummaryResult>(payload.output);
  if (!result) {
    return [];
  }

  return result
    .map((entry) =>
      buildBackgroundCommandEntry(entry, { sourceToolName: payload.name }),
    )
    .filter((entry): entry is UIBackgroundCommand => entry !== null);
}

function buildBackgroundCommandEntry(
  result: BackgroundCommandToolResult | BackgroundCommandSummaryResult,
  fallback?: {
    sourceToolName?: string;
    fallbackCommandId?: string;
    fallbackCommand?: string;
    fallbackCwd?: string;
  },
): UIBackgroundCommand | null {
  const commandId = stringOrEmpty(
    result.CommandId || fallback?.fallbackCommandId,
  );
  if (commandId.length === 0) {
    return null;
  }

  const command = stringOrEmpty(result.Command || fallback?.fallbackCommand);
  const cwd = stringOrUndefined(result.Cwd || fallback?.fallbackCwd);
  const running = result.Running === true;
  const exitCode = numberOrUndefined(result.ExitCode);
  const error = stringOrUndefined(result.Error);
  const preview = buildBackgroundCommandPreview(result);
  const unreadBytes = numberOrZero(
    "UnreadBytes" in result ? result.UnreadBytes : undefined,
  );
  const previewKind = determineBackgroundCommandPreviewKind(result, preview);

  return {
    commandId,
    command,
    cwd,
    status: summarizeBackgroundCommandStatus(
      running,
      exitCode,
      error,
      fallback?.sourceToolName,
    ),
    running,
    startedAt: stringOrUndefined(result.StartedAt),
    updatedAt: stringOrUndefined(result.UpdatedAt),
    preview: preview || undefined,
    previewKind,
    unreadBytes,
    exitCode,
    error,
    retainedAt: new Date().toISOString(),
  };
}

function buildBackgroundCommandPreview(
  result: BackgroundCommandToolResult | BackgroundCommandSummaryResult,
): string {
  if ("UnreadPreview" in result) {
    return stringOrEmpty(result.UnreadPreview);
  }
  if ("Output" in result) {
    return stringOrEmpty(result.Output);
  }
  return "";
}

function determineBackgroundCommandPreviewKind(
  result: BackgroundCommandToolResult | BackgroundCommandSummaryResult,
  preview: string,
): UIBackgroundCommand["previewKind"] | undefined {
  if (preview.length === 0) {
    return undefined;
  }
  if ("HasUnreadOutput" in result && result.HasUnreadOutput) {
    return "unread";
  }
  return "latest";
}

function summarizeBackgroundCommandStatus(
  running: boolean,
  exitCode?: number,
  error?: string,
  sourceToolName?: string,
): string {
  if (running) {
    return "running";
  }
  if (sourceToolName === "stop_command") {
    return "stopped";
  }
  if (typeof exitCode === "number" && exitCode !== 0) {
    return "failed";
  }
  if (error && error.length > 0) {
    return "failed";
  }
  return "completed";
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
        invocationId: nextAgent.invocationId || existing.invocationId,
        description: nextAgent.description || existing.description,
        subagentType: nextAgent.subagentType || existing.subagentType,
        summary: nextAgent.summary || existing.summary,
        lifecycleState: nextAgent.lifecycleState || existing.lifecycleState,
        statusMessage: nextAgent.statusMessage || existing.statusMessage,
        stopBlockReason: nextAgent.stopBlockReason || existing.stopBlockReason,
        stopBlockCount:
          nextAgent.stopBlockCount > 0
            ? nextAgent.stopBlockCount
            : existing.stopBlockCount,
        sessionId: nextAgent.sessionId ?? existing.sessionId,
        transcriptPath: nextAgent.transcriptPath ?? existing.transcriptPath,
        outputFile: nextAgent.outputFile ?? existing.outputFile,
        tools: nextAgent.tools.length > 0 ? nextAgent.tools : existing.tools,
        error: nextAgent.error ?? existing.error,
        totalCostUsd:
          nextAgent.totalCostUsd > 0
            ? nextAgent.totalCostUsd
            : existing.totalCostUsd,
        inputTokens:
          nextAgent.inputTokens > 0
            ? nextAgent.inputTokens
            : existing.inputTokens,
        outputTokens:
          nextAgent.outputTokens > 0
            ? nextAgent.outputTokens
            : existing.outputTokens,
      }
    : nextAgent;

  const remaining = agents.filter((agent) => agent.agentId !== merged.agentId);
  return [merged, ...remaining]
    .sort(compareBackgroundAgents)
    .slice(0, MAX_RETAINED_BACKGROUND_AGENTS);
}

function normalizeChildAgentMetadata(
  metadata?: ChildAgentMetadata,
  fallback?: {
    invocationId?: string;
    description?: string;
    subagentType?: string;
    sessionId?: string;
    transcriptPath?: string;
    resultPath?: string;
  },
): {
  invocationId: string;
  description: string;
  subagentType: string;
  lifecycleState?: string;
  statusMessage?: string;
  stopBlockReason?: string;
  stopBlockCount: number;
  sessionId?: string;
  transcriptPath?: string;
  resultPath?: string;
  tools: string[];
} {
  return {
    invocationId: stringOrEmpty(
      metadata?.invocation_id || fallback?.invocationId || fallback?.sessionId,
    ),
    description: stringOrEmpty(metadata?.description || fallback?.description),
    subagentType: stringOrEmpty(
      metadata?.subagent_type || fallback?.subagentType,
    ),
    lifecycleState: stringOrUndefined(metadata?.lifecycle_state),
    statusMessage: stringOrUndefined(metadata?.status_message),
    stopBlockReason: stringOrUndefined(metadata?.stop_block_reason),
    stopBlockCount: numberOrZero(metadata?.stop_block_count),
    sessionId: stringOrUndefined(metadata?.session_id || fallback?.sessionId),
    transcriptPath: stringOrUndefined(
      metadata?.transcript_path || fallback?.transcriptPath,
    ),
    resultPath: stringOrUndefined(
      metadata?.result_path || fallback?.resultPath,
    ),
    tools: Array.isArray(metadata?.tools)
      ? metadata.tools.filter(
          (toolName): toolName is string => typeof toolName === "string",
        )
      : [],
  };
}

function upsertBackgroundCommand(
  commands: UIBackgroundCommand[],
  nextCommand: UIBackgroundCommand,
): UIBackgroundCommand[] {
  const existing = commands.find(
    (command) => command.commandId === nextCommand.commandId,
  );
  const merged = mergeBackgroundCommand(existing, nextCommand);

  const remaining = commands.filter(
    (command) => command.commandId !== merged.commandId,
  );
  return [merged, ...remaining]
    .sort(compareBackgroundCommands)
    .slice(0, MAX_RETAINED_BACKGROUND_AGENTS);
}

function removeBackgroundCommand(
  commands: UIBackgroundCommand[],
  commandId: string,
): UIBackgroundCommand[] {
  return commands.filter((command) => command.commandId !== commandId);
}

function mergeBackgroundCommand(
  existing: UIBackgroundCommand | undefined,
  nextCommand: UIBackgroundCommand,
): UIBackgroundCommand {
  if (!existing) {
    return nextCommand;
  }

  const nextStatus =
    existing.status === "stopped" && nextCommand.status === "failed"
      ? "stopped"
      : nextCommand.status;

  return {
    ...existing,
    ...nextCommand,
    status: nextStatus,
    command: nextCommand.command || existing.command,
    cwd: nextCommand.cwd ?? existing.cwd,
    startedAt: nextCommand.startedAt ?? existing.startedAt,
    updatedAt: nextCommand.updatedAt ?? existing.updatedAt,
    preview: nextCommand.preview ?? existing.preview,
    previewKind: nextCommand.previewKind ?? existing.previewKind,
    unreadBytes:
      nextCommand.previewKind === "unread" || nextCommand.unreadBytes > 0
        ? nextCommand.unreadBytes
        : existing.unreadBytes,
    exitCode:
      nextCommand.exitCode !== undefined
        ? nextCommand.exitCode
        : existing.exitCode,
    error: nextCommand.error ?? existing.error,
  };
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

function compareBackgroundCommands(
  left: UIBackgroundCommand,
  right: UIBackgroundCommand,
): number {
  const leftRank = backgroundCommandStatusRank(left.status);
  const rightRank = backgroundCommandStatusRank(right.status);
  if (leftRank !== rightRank) {
    return leftRank - rightRank;
  }

  const leftUpdated = left.updatedAt ?? left.retainedAt;
  const rightUpdated = right.updatedAt ?? right.retainedAt;
  return rightUpdated.localeCompare(leftUpdated);
}

function backgroundCommandStatusRank(status: string): number {
  switch (status) {
    case "running":
      return 0;
    case "failed":
      return 1;
    case "stopped":
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
  subagentType: string,
  summary?: string,
  error?: string,
  statusMessage?: string,
): string {
  const normalizedSummary = stringOrEmpty(summary);
  if (normalizedSummary.length > 0) {
    return normalizedSummary;
  }

  const normalizedStatusMessage = stringOrEmpty(statusMessage);
  if (normalizedStatusMessage.length > 0) {
    return normalizedStatusMessage;
  }

  const normalizedError = stringOrEmpty(error);
  if (normalizedError.length > 0) {
    return normalizedError;
  }

  const subject = friendlySubagentLabel(subagentType) || "child agent";

  switch (status) {
    case "running":
      return `${capitalize(subject)} running in the background.`;
    case "cancelling":
      return `Cancellation requested for the background ${subject}.`;
    case "completed":
      return `Background ${subject} completed.`;
    case "cancelled":
      return `Background ${subject} cancelled.`;
    case "failed":
      return `Background ${subject} failed.`;
    default:
      return `Background ${subject} updated.`;
  }
}

function buildBackgroundAgentNotice(
  previousAgent: UIBackgroundAgent | undefined,
  nextAgent: UIBackgroundAgent,
): BackgroundAgentNotice | null {
  if (previousAgent?.status === nextAgent.status) {
    return null;
  }

  const subject = backgroundAgentSubject(nextAgent);

  switch (nextAgent.status) {
    case "running":
      if (previousAgent) {
        return null;
      }
      return {
        text: `${subject} started in the background.`,
        tone: "info",
      };
    case "cancelling":
      return {
        text: `${subject} is stopping.`,
        tone: "warning",
      };
    case "completed":
      return {
        text: summarizeNoticeWithDetail(
          `${subject} completed.`,
          nextAgent.summary,
        ),
        tone: "success",
      };
    case "cancelled":
      return {
        text: summarizeNoticeWithDetail(
          `${subject} was cancelled.`,
          nextAgent.error || nextAgent.summary,
        ),
        tone: "warning",
      };
    case "failed":
      return {
        text: summarizeNoticeWithDetail(
          `${subject} failed.`,
          nextAgent.error || nextAgent.summary,
        ),
        tone: "error",
      };
    default:
      return null;
  }
}

function buildBackgroundCommandTransitionNotice(
  previousCommand: UIBackgroundCommand | undefined,
  nextCommand: UIBackgroundCommand,
  sourceToolName?: string,
): BackgroundCommandNotice | null {
  const subject = backgroundCommandSubject(nextCommand);

  if (sourceToolName === "bash" && !previousCommand && nextCommand.running) {
    return {
      text: `${subject} started in the background.`,
      tone: "info",
    };
  }

  if (previousCommand?.status === nextCommand.status) {
    if (
      nextCommand.previewKind === "unread" &&
      nextCommand.unreadBytes > previousCommand.unreadBytes &&
      nextCommand.status === "running"
    ) {
      return {
        text: `${subject} produced new unread output.`,
        tone: "info",
      };
    }
    return null;
  }

  switch (nextCommand.status) {
    case "running":
      return previousCommand
        ? {
            text: `${subject} is still running.`,
            tone: "info",
          }
        : null;
    case "stopped":
      return {
        text: summarizeNoticeWithDetail(
          `${subject} stopped.`,
          nextCommand.preview || "",
        ),
        tone: "warning",
      };
    case "completed":
      return {
        text: summarizeNoticeWithDetail(
          `${subject} completed.`,
          nextCommand.preview || "",
        ),
        tone: "success",
      };
    case "failed":
      return {
        text: summarizeNoticeWithDetail(
          `${subject} failed.`,
          nextCommand.error || nextCommand.preview || "",
        ),
        tone: "error",
      };
    default:
      return null;
  }
}

function backgroundCommandSubject(command: UIBackgroundCommand): string {
  const label = command.command || command.commandId;
  return `Background command ${truncateNoticeLabel(label)}`;
}

function truncateNoticeLabel(value: string): string {
  const normalized = value.replace(/\s+/g, " ").trim();
  if (normalized.length <= 72) {
    return normalized;
  }
  return `${normalized.slice(0, 69)}...`;
}

function backgroundAgentSubject(agent: UIBackgroundAgent): string {
  const label = agent.description || agent.invocationId || agent.agentId;
  const subject =
    friendlySubagentLabel(agent.subagentType) || "background agent";
  return `${capitalize(subject)} ${label}`;
}

function friendlySubagentLabel(subagentType: string): string {
  switch (subagentType) {
    case "verification":
      return "verification agent";
    case "general-purpose":
      return "general-purpose agent";
    case "Explore":
    case "explore":
      return "explore agent";
    default:
      return "";
  }
}

function capitalize(value: string): string {
  if (value.length === 0) {
    return value;
  }
  return value[0].toUpperCase() + value.slice(1);
}

function summarizeNoticeWithDetail(prefix: string, detail: string): string {
  const normalized = detail.replace(/\s+/g, " ").trim();
  if (normalized.length === 0) {
    return prefix;
  }
  const truncated =
    normalized.length > 120 ? `${normalized.slice(0, 117)}...` : normalized;
  return `${prefix} ${truncated}`;
}

interface ParsedTaskNotification {
  summary: string;
  tone: UISystemMessage["tone"];
  label: string;
}

function parseTaskNotificationMessage(
  text: string,
): ParsedTaskNotification | null {
  const normalized = text.trim();
  if (!normalized.startsWith("<task-notification>")) {
    return null;
  }

  const status = extractTaskNotificationTag(normalized, "status");
  const summary = extractTaskNotificationTag(normalized, "summary");
  return {
    summary:
      decodeTaskNotificationText(summary?.trim() || "") ||
      fallbackTaskNotificationSummary(status?.trim() || "updated"),
    tone: taskNotificationTone(status?.trim() || "updated"),
    label: "Background Command",
  };
}

function extractTaskNotificationTag(text: string, tag: string): string | null {
  const match = text.match(new RegExp(`<${tag}>([\\s\\S]*?)</${tag}>`, "i"));
  return match?.[1] ?? null;
}

function decodeTaskNotificationText(value: string): string {
  return value
    .replaceAll("&lt;", "<")
    .replaceAll("&gt;", ">")
    .replaceAll("&amp;", "&");
}

function fallbackTaskNotificationSummary(status: string): string {
  switch (status) {
    case "completed":
      return "Background task completed.";
    case "failed":
      return "Background task failed.";
    case "stopped":
    case "killed":
      return "Background task stopped.";
    default:
      return "Background task updated.";
  }
}

function taskNotificationTone(status: string): UISystemMessage["tone"] {
  switch (status) {
    case "completed":
      return "success";
    case "failed":
      return "error";
    case "stopped":
    case "killed":
      return "warning";
    default:
      return "info";
  }
}

function normalizeBackgroundCommandEventStatus(
  status: string | undefined,
  running: boolean,
  exitCode: number | undefined,
  error: string | undefined,
): string {
  const normalized = stringOrEmpty(status).toLowerCase();
  if (normalized.length > 0) {
    return normalized;
  }
  if (running) {
    return "running";
  }
  if (
    (typeof exitCode === "number" && exitCode !== 0) ||
    stringOrEmpty(error)
  ) {
    return "failed";
  }
  return "completed";
}

function parseJSONObject<T>(
  value: string | undefined,
  maxChars?: number,
): T | null {
  if (!value) {
    return null;
  }
  if (typeof maxChars === "number" && value.length > maxChars) {
    return null;
  }
  try {
    return JSON.parse(value) as T;
  } catch {
    return null;
  }
}

function permissionRequestDisplayInput(
  permission: PermissionRequestPayload | null | undefined,
): string {
  if (!permission) {
    return "";
  }

  const command = stringOrEmpty(permission.command).trim();
  if (command.length > 0) {
    return command;
  }

  const targetValue = stringOrEmpty(permission.target_value).trim();
  if (targetValue.length > 0) {
    return targetValue;
  }

  const rawInput = stringOrEmpty(permission.raw_input).trim();
  if (rawInput.length > 0) {
    return rawInput;
  }

  return stringOrEmpty(permission.tool);
}

function parseJSONArray<T>(
  value: string | undefined,
  maxChars?: number,
): T[] | null {
  if (!value) {
    return null;
  }
  if (typeof maxChars === "number" && value.length > maxChars) {
    return null;
  }
  try {
    const parsed = JSON.parse(value);
    return Array.isArray(parsed) ? (parsed as T[]) : null;
  } catch {
    return null;
  }
}

function sanitizeToolCallInput(
  name: string | undefined,
  input: string,
): string {
  if (!isAgentToolName(name)) {
    return input;
  }
  return sanitizeAgentToolInput(input) ?? "";
}

function sanitizeToolResultPayload(
  payload: ToolResultPayload,
): ToolResultPayload {
  if (!isAgentToolName(payload.name)) {
    return payload;
  }

  const input = sanitizeAgentToolInput(payload.input);
  const output = sanitizeAgentToolOutput(payload.output);
  const mutated = input !== payload.input || output !== payload.output;
  if (!mutated) {
    return payload;
  }

  return {
    ...payload,
    input,
    output,
    truncated: payload.truncated || mutated,
  };
}

function sanitizeAgentToolInput(input: string | undefined): string | undefined {
  if (!input) {
    return input;
  }

  const parsed = parseJSONObject<AgentToolInput>(
    input,
    MAX_AGENT_TOOL_JSON_CHARS,
  );
  if (!parsed) {
    return sanitizeAgentDisplayText(input, MAX_AGENT_TOOL_DISPLAY_CHARS);
  }

  return JSON.stringify(
    {
      description: stringOrUndefined(parsed.description),
      subagent_type: stringOrUndefined(parsed.subagent_type),
      agent_id: stringOrUndefined(parsed.agent_id),
      run_in_background: parsed.run_in_background === true ? true : undefined,
      wait_ms: numberOrUndefined(parsed.wait_ms),
    },
    null,
    2,
  );
}

function sanitizeAgentToolOutput(output: string | undefined): string {
  if (!output) {
    return "";
  }

  const parsed = parseJSONObject<AgentToolResult>(
    output,
    MAX_AGENT_TOOL_JSON_CHARS,
  );
  if (!parsed) {
    return JSON.stringify(
      {
        status: "truncated",
        summary:
          "Child agent result omitted from live UI because the payload exceeded the replay safety limit. See the child session files for the full output.",
      },
      null,
      2,
    );
  }

  const compact: AgentToolResult = {
    status: parsed.status,
    invocation_id: parsed.invocation_id,
    agent_id: parsed.agent_id,
    subagent_type: parsed.subagent_type,
    session_id: parsed.session_id,
    transcript_path: parsed.transcript_path,
    output_file: parsed.output_file,
    summary: stringOrUndefined(
      sanitizeAgentDisplayText(parsed.summary, MAX_AGENT_TOOL_DISPLAY_CHARS),
    ),
    error: stringOrUndefined(
      sanitizeAgentDisplayText(parsed.error, MAX_AGENT_TOOL_DISPLAY_CHARS),
    ),
    total_cost_usd: parsed.total_cost_usd,
    input_tokens: parsed.input_tokens,
    output_tokens: parsed.output_tokens,
    metadata: sanitizeChildAgentMetadataPayload(parsed.metadata),
  };

  return JSON.stringify(compact, null, 2);
}

function sanitizeChildAgentMetadataPayload(
  metadata?: ChildAgentMetadata,
): ChildAgentMetadata | undefined {
  if (!metadata) {
    return metadata;
  }

  return {
    ...metadata,
    status_message: stringOrUndefined(
      sanitizeAgentDisplayText(
        metadata.status_message,
        MAX_BACKGROUND_AGENT_SUMMARY_CHARS,
      ),
    ),
    tools: Array.isArray(metadata.tools) ? [...metadata.tools] : undefined,
  };
}

function sanitizeAgentDisplayText(
  value: string | undefined,
  maxChars: number,
): string {
  const normalized = stringOrEmpty(value);
  if (normalized.length <= maxChars) {
    return normalized;
  }
  if (AGENT_UI_TRUNCATION_NOTE.length >= maxChars) {
    return normalized.slice(0, maxChars);
  }
  return `${normalized.slice(0, maxChars - AGENT_UI_TRUNCATION_NOTE.length)}${AGENT_UI_TRUNCATION_NOTE}`;
}

function isAgentToolName(name: string | undefined): boolean {
  return name === "agent" || name === "agent_status" || name === "agent_stop";
}

function stringOrEmpty(value: unknown): string {
  return typeof value === "string" ? value.trim() : "";
}

function stringOrUndefined(value: unknown): string | undefined {
  const normalized = stringOrEmpty(value);
  return normalized.length > 0 ? normalized : undefined;
}

function numberOrZero(value: unknown): number {
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

function numberOrUndefined(value: unknown): number | undefined {
  return typeof value === "number" && Number.isFinite(value)
    ? value
    : undefined;
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

function upsertProgressEntry(
  progressEntries: UIProgressEntry[],
  nextProgressEntry: UIProgressEntry,
): UIProgressEntry[] {
  const existing = progressEntries.find(
    (progressEntry) => progressEntry.id === nextProgressEntry.id,
  );
  if (!existing) {
    return [...progressEntries, nextProgressEntry];
  }

  return progressEntries.map((progressEntry) =>
    progressEntry.id === nextProgressEntry.id
      ? {
          ...progressEntry,
          ...nextProgressEntry,
        }
      : progressEntry,
  );
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

function assistantBlocksHaveText(blocks: UIAssistantBlock[]): boolean {
  return blocks.some(
    (block) => block.kind === "text" && block.text.trim().length > 0,
  );
}

function completedAssistantBlocks(
  blocks: UIAssistantBlock[],
): UIAssistantBlock[] {
  return blocks.filter((block) => block.text.trim().length > 0);
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
