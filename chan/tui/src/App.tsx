import React, {
  type FC,
  useCallback,
  useEffect,
  useRef,
  useState,
} from "react";
import {
  Box,
  Screen,
  Spinner,
  Text,
  disableBracketedPaste,
  enableBracketedPaste,
  type ToastData,
  useFocusManager,
  useToast,
} from "silvery";
import { useEngine } from "./hooks/useEngine.js";
import { useEvents, type UIArtifact } from "./hooks/useEvents.js";
import ArtifactReviewPrompt from "./components/ArtifactReviewPrompt.js";
import BackgroundTasksDialog from "./components/BackgroundTasksDialog.js";
import Input from "./components/Input.js";
import ModelSelectionPrompt from "./components/ModelSelectionPrompt.js";
import PromptFooter from "./components/PromptFooter.js";
import RewindSelectionPrompt from "./components/RewindSelectionPrompt.js";
import ResumeSelectionPrompt from "./components/ResumeSelectionPrompt.js";
import StreamOutput from "./components/StreamOutput.js";
import StatusBar from "./components/StatusBar.js";
import TranscriptSearchPrompt from "./components/TranscriptSearchPrompt.js";
import PermissionPrompt from "./components/PermissionPrompt.js";
import ShimmerText from "./components/ShimmerText.js";
import { usePromptHistory } from "./hooks/usePromptHistory.js";
import {
  parseImageReferenceIds,
  type PastedImageData,
} from "./utils/imagePaste.js";
import { activeTurnStatusLabel } from "./utils/activeTurnStatus.js";
import type {
  BackgroundAgentDetailPayload,
  BackgroundCommandDetailPayload,
  BackgroundCommandUpdatedPayload,
  PermissionResponseDecision,
  StreamEvent,
  UserInputImagePayload,
} from "./protocol/types.js";

const THINKING_TOGGLE_SHORTCUT_LABEL = "Opt+T";
const ARTIFACTS_TOGGLE_SHORTCUT_LABEL = "Opt+A";
const REASONING_TOGGLE_SHORTCUT_LABEL = "Opt+R";
const TASKS_TOGGLE_SHORTCUT_LABEL = "Opt+B";
const FOOTER_HINT_REVEAL_MS = 2500;

type BackgroundTaskDetailRecord =
  | BackgroundCommandDetailPayload
  | BackgroundAgentDetailPayload;

interface AppProps {
  enginePath: string;
  model: string;
  mode: string;
  autoMode: boolean;
}

interface QueuedPrompt {
  id: number;
  text: string;
  images: UserInputImagePayload[];
}

interface PendingTaskNotification {
  id: number;
  text: string;
}

function toUserInputImagePayload(
  id: number,
  image: PastedImageData,
): UserInputImagePayload {
  return {
    id,
    data: image.data,
    media_type: image.mediaType,
    filename: image.filename,
    source_path: image.sourcePath,
  };
}

const App: FC<AppProps> = ({ enginePath, model, mode, autoMode }) => {
  const prompt = usePromptHistory();
  const [promptImages, setPromptImages] = useState<UserInputImagePayload[]>([]);
  const [pasteWarning, setPasteWarning] = useState<string | null>(null);
  const nextImageIdRef = useRef(1);
  const [queuedPrompts, setQueuedPrompts] = useState<QueuedPrompt[]>([]);
  const [nextQueuedPromptId, setNextQueuedPromptId] = useState(1);
  const [pendingTaskNotifications, setPendingTaskNotifications] = useState<
    PendingTaskNotification[]
  >([]);
  const nextTaskNotificationIdRef = useRef(1);
  const [transcriptSearchActive, setTranscriptSearchActive] = useState(false);
  const [transcriptSearchQuery, setTranscriptSearchQuery] = useState("");
  const [transcriptSearchSelectedIndex, setTranscriptSearchSelectedIndex] =
    useState(0);
  const [transcriptSearchMatchCount, setTranscriptSearchMatchCount] =
    useState(0);
  const [showThinking, setShowThinking] = useState(false);
  const [showArtifacts, setShowArtifacts] = useState(true);
  const [showBackgroundTasks, setShowBackgroundTasks] = useState(false);
  const [backgroundTaskDetails, setBackgroundTaskDetails] = useState<
    Record<string, BackgroundTaskDetailRecord>
  >({});
  const [showFooterHints, setShowFooterHints] = useState(false);
  const previousStreamingRef = useRef(false);
  const footerHintTimeoutRef = useRef<ReturnType<typeof setTimeout> | null>(
    null,
  );
  const focusManager = useFocusManager();
  const { toast, toasts, dismissAll } = useToast();
  const {
    uiState,
    handleEvent,
    clearStream,
    cancelActiveTurn,
    clearPermission,
    appendUserMessage,
    beginAssistantTurn,
    submitArtifactReview,
    submitModelSelection,
    submitRewindSelection,
    submitResumeSelection,
  } = useEvents(model, mode);
  const handleEngineEvent = useCallback(
    (event: StreamEvent) => {
      if (event.type === "background_tasks_requested") {
        setShowBackgroundTasks(true);
      }

      if (event.type === "background_command_detail") {
        const payload = event.payload as BackgroundCommandDetailPayload | undefined;
        const commandId = payload?.command_id?.trim();
        if (payload && commandId) {
          setBackgroundTaskDetails((current) => ({
            ...current,
            [backgroundTaskKey("command", commandId)]: payload,
          }));
        }
      }

      if (event.type === "background_agent_detail") {
        const payload = event.payload as BackgroundAgentDetailPayload | undefined;
        const agentId = payload?.agent_id?.trim();
        if (payload && agentId) {
          setBackgroundTaskDetails((current) => ({
            ...current,
            [backgroundTaskKey("agent", agentId)]: payload,
          }));
        }
      }

      handleEvent(event);

      const taskNotification = buildBackgroundCommandTaskNotification(event);
      if (!taskNotification) {
        return;
      }

      const id = nextTaskNotificationIdRef.current;
      nextTaskNotificationIdRef.current += 1;
      setPendingTaskNotifications((current) => [
        ...current,
        { id, text: taskNotification },
      ]);
    },
    [handleEvent],
  );
  const engine = useEngine(enginePath, {
    model,
    mode,
    autoMode,
    onEvent: handleEngineEvent,
  });
  const visibleArtifacts = showArtifacts
    ? selectVisibleArtifacts(
        uiState.artifacts,
        uiState.focusedArtifactId,
        uiState.showPlanPanel,
      )
    : [];
  const isEngineReady = uiState.ready || engine.ready;

  useEffect(() => {
    enableBracketedPaste(process.stdout);
    return () => {
      disableBracketedPaste(process.stdout);
    };
  }, []);

  useEffect(() => {
    return () => {
      if (footerHintTimeoutRef.current !== null) {
        clearTimeout(footerHintTimeoutRef.current);
      }
    };
  }, []);

  useEffect(() => {
    if (uiState.isStreaming) {
      dismissAll();
    }
  }, [dismissAll, uiState.isStreaming]);

  useEffect(() => {
    if (
      uiState.pendingPermission ||
      uiState.pendingResumeSelection ||
      uiState.pendingRewindSelection ||
      uiState.pendingModelSelection ||
      showBackgroundTasks ||
      uiState.pendingArtifactReview
    ) {
      focusManager.blur();
    }
  }, [
    focusManager,
    uiState.pendingArtifactReview,
    uiState.pendingModelSelection,
    uiState.pendingPermission,
    uiState.pendingRewindSelection,
    uiState.pendingResumeSelection,
    showBackgroundTasks,
  ]);

  useEffect(() => {
    const wasStreaming = previousStreamingRef.current;
    previousStreamingRef.current = uiState.isStreaming;

    if (!wasStreaming || uiState.isStreaming || uiState.error) {
      return;
    }

    if (
      !uiState.statusLine?.startsWith("Turn complete (") ||
      uiState.statusLine.includes("(cancelled)")
    ) {
      return;
    }

    const lastUserMessage = [...uiState.messages]
      .reverse()
      .find((message) => message.role === "user");
    if (lastUserMessage?.text.trim().startsWith("/")) {
      return;
    }

    toast({
      title: "Done ✓",
      variant: "success",
      duration: 2500,
    });
  }, [
    toast,
    uiState.error,
    uiState.isStreaming,
    uiState.messages,
    uiState.statusLine,
  ]);

  const submitPrompt = useCallback(
    (text: string, images: UserInputImagePayload[]) => {
      appendUserMessage(text);
      clearStream();
      if (text.startsWith("/") && images.length === 0) {
        const [cmd, ...rest] = text.slice(1).split(" ");
        engine.sendCommand(cmd!, rest.join(" "));
        return;
      }

      beginAssistantTurn();
      engine.sendInput(text, images);
    },
    [appendUserMessage, beginAssistantTurn, clearStream, engine],
  );

  const submitTaskNotification = useCallback(
    (text: string) => {
      clearStream();
      beginAssistantTurn();
      engine.sendInput(text);
    },
    [beginAssistantTurn, clearStream, engine],
  );

  useEffect(() => {
    setPromptImages((current) => {
      const referencedIds = parseImageReferenceIds(prompt.value);
      const next = current.filter((image) => referencedIds.has(image.id));
      return next.length === current.length ? current : next;
    });
  }, [prompt.value]);

  useEffect(() => {
    if (isQueuedPromptDispatchBlocked(uiState, isEngineReady)) {
      return;
    }

    const nextNotification = pendingTaskNotifications[0];
    if (!nextNotification) {
      return;
    }

    setPendingTaskNotifications((current) =>
      current[0]?.id === nextNotification.id ? current.slice(1) : current,
    );
    submitTaskNotification(nextNotification.text);
  }, [
    isEngineReady,
    pendingTaskNotifications,
    submitTaskNotification,
    uiState.isStreaming,
    uiState.pendingPermission,
    uiState.pendingArtifactReview,
    uiState.pendingModelSelection,
    uiState.pendingRewindSelection,
    uiState.pendingResumeSelection,
  ]);

  useEffect(() => {
    if (pendingTaskNotifications.length > 0) {
      return;
    }

    const nextPrompt = queuedPrompts[0];
    if (!nextPrompt) {
      return;
    }

    setQueuedPrompts((current) =>
      current[0]?.id === nextPrompt.id ? current.slice(1) : current,
    );
    submitPrompt(nextPrompt.text, nextPrompt.images);
  }, [
    isEngineReady,
    pendingTaskNotifications.length,
    queuedPrompts,
    submitPrompt,
    uiState.isStreaming,
    uiState.pendingPermission,
    uiState.pendingArtifactReview,
    uiState.pendingModelSelection,
    uiState.pendingRewindSelection,
    uiState.pendingResumeSelection,
  ]);

  const handleSendNextQueuedPrompt = useCallback(() => {
    if (
      pendingTaskNotifications.length > 0 ||
      isQueuedPromptDispatchBlocked(uiState, isEngineReady)
    ) {
      return;
    }

    const queuedPrompt = queuedPrompts[0];
    if (!queuedPrompt) {
      return;
    }

    setQueuedPrompts((current) => current.slice(1));
    setPasteWarning(null);
    submitPrompt(queuedPrompt.text, queuedPrompt.images);
  }, [
    isEngineReady,
    pendingTaskNotifications.length,
    queuedPrompts,
    submitPrompt,
    uiState,
  ]);

  const handleRemoveNextQueuedPrompt = useCallback(() => {
    setQueuedPrompts((current) => current.slice(1));
  }, []);

  const handleRevealFooterHints = useCallback(() => {
    if (footerHintTimeoutRef.current !== null) {
      clearTimeout(footerHintTimeoutRef.current);
    }

    setShowFooterHints(true);
    footerHintTimeoutRef.current = setTimeout(() => {
      setShowFooterHints(false);
      footerHintTimeoutRef.current = null;
    }, FOOTER_HINT_REVEAL_MS);
  }, []);

  const handleImagePaste = useCallback((images: PastedImageData[]) => {
    const startId = nextImageIdRef.current;
    const nextImages = images.map((image, index) => {
      const id = startId + index;
      prompt.insertImageReference(id);
      return toUserInputImagePayload(id, image);
    });

    nextImageIdRef.current = startId + images.length;
    setPromptImages((current) => [...current, ...nextImages]);
    setPasteWarning(null);
  }, [prompt]);

  const handlePasteWarning = useCallback((warnings: string[]) => {
    setPasteWarning(warnings.length > 0 ? warnings.join(" | ") : null);
  }, []);

  const handleSubmit = (overrideText?: string) => {
    // Derive text before calling submit – silvery's renderer may defer
    // setState callbacks, so prompt.submit()'s return value can be stale.
    const text = (overrideText ?? prompt.value).trim();
    prompt.submit(overrideText); // side-effect: clear prompt + add to history
    if (!text) {
      return;
    }
    setPasteWarning(null);

    const referencedIds = parseImageReferenceIds(text);
    const images = promptImages.filter((image) => referencedIds.has(image.id));
    setPromptImages((current) =>
      current.filter((image) => !referencedIds.has(image.id)),
    );

    if (
      uiState.isStreaming ||
      uiState.pendingPermission ||
      uiState.pendingArtifactReview ||
      uiState.pendingModelSelection ||
      uiState.pendingRewindSelection ||
      uiState.pendingResumeSelection ||
      pendingTaskNotifications.length > 0 ||
      queuedPrompts.length
    ) {
      if (queuedPrompts.length > 0) {
        setQueuedPrompts((current) => {
          const lastQueuedPrompt = current.at(-1);
          if (!lastQueuedPrompt) {
            return current;
          }

          return [
            ...current.slice(0, -1),
            mergeQueuedPrompt(lastQueuedPrompt, text, images),
          ];
        });
        return;
      }

      const queuedPrompt: QueuedPrompt = {
        id: nextQueuedPromptId,
        text,
        images,
      };
      setQueuedPrompts((current) => [...current, queuedPrompt]);
      setNextQueuedPromptId((current) => current + 1);
      return;
    }

    submitPrompt(text, images);
  };

  const handlePermissionResponse = (
    decision: PermissionResponseDecision,
    feedback?: string,
  ) => {
    if (uiState.pendingPermission) {
      clearPermission(decision);
      engine.sendPermissionResponse(
        uiState.pendingPermission.request_id,
        decision,
        feedback,
      );
    }
  };

  const handleArtifactReviewResponse = (
    decision: "approve" | "revise" | "cancel",
    feedback?: string,
  ) => {
    if (uiState.pendingArtifactReview) {
      submitArtifactReview(uiState.pendingArtifactReview.requestId, decision);
      engine.sendArtifactReviewResponse(
        uiState.pendingArtifactReview.requestId,
        decision,
        feedback,
      );
    }
  };

  const handleResumeSelection = (sessionId?: string) => {
    if (!uiState.pendingResumeSelection) {
      return;
    }

    submitResumeSelection(uiState.pendingResumeSelection.requestId);
    engine.sendResumeSelectionResponse({
      request_id: uiState.pendingResumeSelection.requestId,
      session_id: sessionId,
      cancel: !sessionId,
    });
  };

  const handleModelSelection = (modelId?: string, provider?: string) => {
    if (!uiState.pendingModelSelection) {
      return;
    }

    submitModelSelection(uiState.pendingModelSelection.requestId);
    engine.sendModelSelectionResponse({
      request_id: uiState.pendingModelSelection.requestId,
      model: modelId,
      provider,
      cancel: !modelId,
    });
  };

  const handleRewindSelection = (messageIndex?: number) => {
    if (!uiState.pendingRewindSelection) {
      return;
    }

    submitRewindSelection(uiState.pendingRewindSelection.requestId);
    engine.sendRewindSelectionResponse({
      request_id: uiState.pendingRewindSelection.requestId,
      message_index: messageIndex,
      cancel: typeof messageIndex !== "number",
    });
  };

  const handleCancel = () => {
    if (transcriptSearchActive) {
      setTranscriptSearchActive(false);
      setTranscriptSearchQuery("");
      setTranscriptSearchSelectedIndex(0);
      setTranscriptSearchMatchCount(0);
      return;
    }
    if (!uiState.isStreaming) {
      return;
    }
    cancelActiveTurn();
    engine.sendCancel();
  };

  const isPromptDisabled =
    !isEngineReady ||
    !!engine.error ||
    transcriptSearchActive ||
    showBackgroundTasks ||
    uiState.pendingPermission !== null ||
    uiState.pendingArtifactReview !== null ||
    uiState.pendingModelSelection !== null ||
    uiState.pendingRewindSelection !== null ||
    uiState.pendingResumeSelection !== null;
  const keepPromptVisibleWithOverlay =
    uiState.pendingResumeSelection !== null ||
    uiState.pendingModelSelection !== null ||
    uiState.pendingRewindSelection !== null;
  const showPromptArea =
    uiState.pendingPermission === null &&
    uiState.pendingArtifactReview === null;
  const promptBlockedReason = getPromptBlockedReason({
    isEngineReady,
    engineError: engine.error,
    transcriptSearchActive,
    isStreaming: uiState.isStreaming,
    pendingModelSelection: uiState.pendingModelSelection !== null,
    pendingRewindSelection: uiState.pendingRewindSelection !== null,
    pendingResumeSelection: uiState.pendingResumeSelection !== null,
    backgroundTasksOpen: showBackgroundTasks,
  });
  const promptActivityLabel = uiState.isStreaming
    ? activeTurnStatusLabel(
        uiState.liveAssistantBlocks,
        uiState.activeTurnStatus,
      )
    : null;

  const openTranscriptSearch = useCallback(() => {
    setTranscriptSearchActive(true);
    setTranscriptSearchSelectedIndex(0);
  }, []);

  const closeTranscriptSearch = useCallback(() => {
    setTranscriptSearchActive(false);
    setTranscriptSearchQuery("");
    setTranscriptSearchSelectedIndex(0);
    setTranscriptSearchMatchCount(0);
  }, []);

  const handleTranscriptSearchQueryChange = useCallback((value: string) => {
    setTranscriptSearchQuery(value);
    setTranscriptSearchSelectedIndex(0);
  }, []);

  const handleTranscriptSearchNext = useCallback(() => {
    setTranscriptSearchSelectedIndex((current) => {
      if (transcriptSearchMatchCount <= 0) {
        return 0;
      }
      return (current + 1) % transcriptSearchMatchCount;
    });
  }, [transcriptSearchMatchCount]);

  const handleTranscriptSearchPrevious = useCallback(() => {
    setTranscriptSearchSelectedIndex((current) => {
      if (transcriptSearchMatchCount <= 0) {
        return 0;
      }
      return (
        (current - 1 + transcriptSearchMatchCount) % transcriptSearchMatchCount
      );
    });
  }, [transcriptSearchMatchCount]);

  const handleTranscriptSearchStatsChange = useCallback(
    (totalMatches: number, selectedIndex: number) => {
      setTranscriptSearchMatchCount(totalMatches);
      setTranscriptSearchSelectedIndex(
        totalMatches > 0 ? Math.max(0, selectedIndex) : 0,
      );
    },
    [],
  );

  const handleThinkingVisibilityToggle = useCallback(() => {
    setShowThinking((current) => !current);
  }, []);

  const handleArtifactVisibilityToggle = useCallback(() => {
    setShowArtifacts((current) => !current);
  }, []);

  const handleReasoningToggle = useCallback(() => {
    const levels = ["default", "low", "medium", "high", "xhigh"];
    const current = uiState.reasoningEffort ?? "default";
    const currentIndex = levels.indexOf(current);
    const nextIndex = (currentIndex + 1) % levels.length;
    const nextEffort = levels[nextIndex];

    engine.sendCommand("reasoning", nextEffort);
  }, [engine, uiState.reasoningEffort]);

  const handleBackgroundTasksToggle = useCallback(() => {
    setShowBackgroundTasks((current) => !current);
  }, []);

  const handleBackgroundTasksClose = useCallback(() => {
    setShowBackgroundTasks(false);
  }, []);

  const handleBackgroundTaskInspect = useCallback(
    (kind: "command" | "agent", id: string) => {
      if (kind === "command") {
        engine.sendBackgroundCommandInspect({ command_id: id });
        return;
      }
      engine.sendBackgroundAgentInspect({ agent_id: id });
    },
    [engine],
  );

  const handleBackgroundTaskStop = useCallback(
    (kind: "command" | "agent", id: string) => {
      if (kind === "command") {
        engine.sendBackgroundCommandStop({ command_id: id, wait_ms: 1000 });
        return;
      }
      engine.sendBackgroundAgentStop({ agent_id: id, wait_ms: 1000 });
    },
    [engine],
  );

  return (
    <Screen>
      <Box
        backgroundColor="$bg"
        width="100%"
        height="100%"
        flexDirection="column"
      >
        <Box flexShrink={0}>
          <StatusBar
            ready={isEngineReady}
            mode={uiState.mode}
            model={uiState.model}
            reasoningEffort={uiState.reasoningEffort}
            sessionId={uiState.sessionId}
            sessionTitle={uiState.sessionTitle}
            maxContextWindow={uiState.maxContextWindow}
            maxOutputTokens={uiState.maxOutputTokens}
            currentContextUsage={uiState.currentContextUsage}
            totalCostUsd={uiState.cost.totalUsd}
            inputTokens={uiState.cost.inputTokens}
            outputTokens={uiState.cost.outputTokens}
            memoryRecallUsd={uiState.cost.memoryRecallUsd}
            memoryRecallInputTokens={uiState.cost.memoryRecallInputTokens}
            memoryRecallOutputTokens={uiState.cost.memoryRecallOutputTokens}
            childAgentUsd={uiState.cost.childAgentUsd}
            childAgentInputTokens={uiState.cost.childAgentInputTokens}
            childAgentOutputTokens={uiState.cost.childAgentOutputTokens}
            artifacts={uiState.artifacts}
            focusedArtifactId={uiState.focusedArtifactId}
            pendingArtifactReview={uiState.pendingArtifactReview}
            backgroundAgents={uiState.backgroundAgents}
            backgroundCommands={uiState.backgroundCommands}
            rateLimits={uiState.rateLimits}
          />
        </Box>

      <Box
        flexDirection="row"
        flexGrow={1}
        flexShrink={1}
        minWidth={0}
        minHeight={0}
        gap={1}
      >
        <Box
          flexDirection="column"
          flexGrow={1}
          flexShrink={1}
          minWidth={0}
          minHeight={0}
        >
          {engine.error && !uiState.error && (
            <Box
              borderStyle="round"
              borderColor="$error"
              paddingX={1}
              marginTop={1}
            >
              <Text color="$error">{engine.error}</Text>
            </Box>
          )}

          {!isEngineReady && !engine.error && (
            <Box paddingLeft={1} marginTop={1}>
              <Text color="$muted">
                <Spinner type="dots" /> <ShimmerText text="Starting Go engine..." />
              </Text>
            </Box>
          )}

          {uiState.statusLine && (
            <Box paddingLeft={1} marginTop={1}>
              <Text color={uiState.error ? "$error" : "$primary"}>
                {uiState.statusLine}
              </Text>
            </Box>
          )}

          {uiState.compact && (
            <Box paddingLeft={1} marginTop={1}>
              <Text color="$warning">
                {uiState.compact.active
                  ? `Compacting conversation (${uiState.compact.strategy}, ${uiState.compact.tokensBefore} tokens)...`
                  : `Compaction complete (${uiState.compact.tokensAfter} tokens)`}
              </Text>
            </Box>
          )}

          <StreamOutput
            messages={uiState.messages}
            progressEntries={uiState.progressEntries}
            toolCalls={uiState.toolCalls}
            transcript={uiState.transcript}
            artifacts={visibleArtifacts}
            queuedPrompts={queuedPrompts.map((queuedPrompt) => ({
              id: queuedPrompt.id,
              text: queuedPrompt.text,
              imageCount: queuedPrompt.images.length,
            }))}
            liveBlocks={uiState.liveAssistantBlocks}
            liveAssistantMessageId={uiState.liveAssistantMessageId}
            isStreaming={uiState.isStreaming}
            activeTurnStatus={uiState.activeTurnStatus}
            model={uiState.model}
            showThinking={showThinking}
            thinkingShortcutLabel={THINKING_TOGGLE_SHORTCUT_LABEL}
            transcriptSearchQuery={transcriptSearchQuery}
            transcriptSearchSelectedIndex={transcriptSearchSelectedIndex}
            onTranscriptSearchStatsChange={handleTranscriptSearchStatsChange}
          />

          {uiState.error && (
            <Box
              borderStyle="round"
              borderColor="$error"
              paddingX={1}
              marginTop={1}
            >
              <Text color="$error">{uiState.error}</Text>
            </Box>
          )}
        </Box>
      </Box>

      {uiState.pendingPermission ? (
        <Box flexDirection="column" flexShrink={1} minHeight={0} marginTop={1}>
          <PermissionPrompt
            tool={uiState.pendingPermission.tool}
            command={uiState.pendingPermission.command}
            rawInput={uiState.pendingPermission.raw_input}
            risk={uiState.pendingPermission.risk}
            riskReason={uiState.pendingPermission.risk_reason}
            permissionLevel={uiState.pendingPermission.permission_level}
            targetKind={uiState.pendingPermission.target_kind}
            targetValue={uiState.pendingPermission.target_value}
            workingDir={uiState.pendingPermission.working_dir}
            onRespond={handlePermissionResponse}
            onCancelTurn={handleCancel}
          />
        </Box>
      ) : uiState.pendingArtifactReview ? (
        <Box flexDirection="column" flexShrink={0} minHeight={0} marginTop={1}>
          <ArtifactReviewPrompt
            review={uiState.pendingArtifactReview}
            onRespond={handleArtifactReviewResponse}
          />
        </Box>
      ) : showPromptArea ? (
        <Box
          flexDirection="column"
          flexShrink={0}
          maxHeight="45%"
          position="relative"
          overflow="scroll"
        >
          <SafeToastContainer toasts={toasts} />
          {transcriptSearchActive && !keepPromptVisibleWithOverlay ? (
            <TranscriptSearchPrompt
              query={transcriptSearchQuery}
              matchCount={transcriptSearchMatchCount}
              selectedIndex={transcriptSearchSelectedIndex}
              onChange={handleTranscriptSearchQueryChange}
              onNext={handleTranscriptSearchNext}
              onPrevious={handleTranscriptSearchPrevious}
              onClose={closeTranscriptSearch}
            />
          ) : (
            <Input
              prompt={prompt}
              mode={uiState.mode}
              slashCommands={uiState.slashCommands}
              isLoading={uiState.isStreaming}
              statusLabel={promptActivityLabel}
              onSubmit={handleSubmit}
              onOpenTranscriptSearch={openTranscriptSearch}
              onImagePaste={handleImagePaste}
              onPasteWarning={handlePasteWarning}
              onModeToggle={engine.sendModeToggle}
              onThinkingVisibilityToggle={handleThinkingVisibilityToggle}
              onArtifactVisibilityToggle={handleArtifactVisibilityToggle}
              onReasoningToggle={handleReasoningToggle}
              onBackgroundTasksToggle={handleBackgroundTasksToggle}
              onRevealFooterHints={handleRevealFooterHints}
              onSendQueuedPromptNow={handleSendNextQueuedPrompt}
              onRemoveQueuedPrompt={handleRemoveNextQueuedPrompt}
              onCancel={handleCancel}
              disabled={isPromptDisabled}
            />
          )}
          {pasteWarning && (
            <Box paddingLeft={1} marginTop={1}>
              <Text color="$warning">{pasteWarning}</Text>
            </Box>
          )}
          <PromptFooter
            mode={uiState.mode}
            model={uiState.model}
            maxContextWindow={uiState.maxContextWindow}
            maxOutputTokens={uiState.maxOutputTokens}
            currentContextUsage={uiState.currentContextUsage}
            isLoading={uiState.isStreaming}
            disabled={isPromptDisabled}
            promptValue={prompt.value}
            totalCostUsd={uiState.cost.totalUsd}
            inputTokens={uiState.cost.inputTokens}
            outputTokens={uiState.cost.outputTokens}
            memoryRecall={uiState.memoryRecall}
            retrieval={uiState.retrieval}
            turnTiming={uiState.turnTiming}
            cursorOffset={prompt.cursorOffset}
            blockedReason={promptBlockedReason}
            queuedPromptCount={queuedPrompts.length}
            showExpandedHint={showFooterHints}
            showArtifacts={showArtifacts}
            artifactsShortcutLabel={ARTIFACTS_TOGGLE_SHORTCUT_LABEL}
            backgroundTasksShortcutLabel={TASKS_TOGGLE_SHORTCUT_LABEL}
            reasoningShortcutLabel={REASONING_TOGGLE_SHORTCUT_LABEL}
          />
        </Box>
      ) : null}

      {uiState.pendingResumeSelection ? (
        <CenteredViewportOverlay>
          <ResumeSelectionPrompt
            selection={uiState.pendingResumeSelection}
            onSelect={handleResumeSelection}
            onCancel={() => handleResumeSelection()}
          />
        </CenteredViewportOverlay>
      ) : uiState.pendingRewindSelection ? (
        <CenteredViewportOverlay>
          <RewindSelectionPrompt
            selection={uiState.pendingRewindSelection}
            onSelect={handleRewindSelection}
            onCancel={() => handleRewindSelection()}
          />
        </CenteredViewportOverlay>
      ) : uiState.pendingModelSelection ? (
        <CenteredViewportOverlay>
          <ModelSelectionPrompt
            selection={uiState.pendingModelSelection}
            onSelect={handleModelSelection}
            onCancel={() => handleModelSelection()}
          />
        </CenteredViewportOverlay>
      ) : showBackgroundTasks ? (
        <CenteredViewportOverlay>
          <BackgroundTasksDialog
            commands={uiState.backgroundCommands}
            agents={uiState.backgroundAgents}
            details={backgroundTaskDetails}
            onClose={handleBackgroundTasksClose}
            onInspectTask={handleBackgroundTaskInspect}
            onStopTask={handleBackgroundTaskStop}
          />
        </CenteredViewportOverlay>
      ) : null}
      </Box>
    </Screen>
  );
};

export default App;

function SafeToastContainer({ toasts }: { toasts: ToastData[] }) {
  const latestToast = toasts.at(-1);

  if (!latestToast) {
    return null;
  }

  return (
    <Box
      position="absolute"
      width="100%"
      flexDirection="row"
      justifyContent="center"
    >
      <SafeToastItem toast={latestToast} />
    </Box>
  );
}

function buildBackgroundCommandTaskNotification(event: StreamEvent): string | null {
  if (event.type !== "background_command_updated") {
    return null;
  }

  const payload = event.payload as BackgroundCommandUpdatedPayload | undefined;
  if (!payload) {
    return null;
  }

  const commandId = payload.command_id?.trim();
  if (!commandId) {
    return null;
  }

  const status = normalizeBackgroundCommandTaskStatus(payload.status);
  if (status !== "completed" && status !== "failed") {
    return null;
  }

  const summary = buildBackgroundCommandTaskSummary(payload, status);
  const lines = [
    "<task-notification>",
    `  <task-id>${escapeTaskNotificationText(commandId)}</task-id>`,
    `  <status>${escapeTaskNotificationText(status)}</status>`,
    `  <summary>${escapeTaskNotificationText(summary)}</summary>`,
    `  <command-id>${escapeTaskNotificationText(commandId)}</command-id>`,
  ];

  const command = payload.command?.trim();
  if (command) {
    lines.push(`  <command>${escapeTaskNotificationText(command)}</command>`);
  }

  const cwd = payload.cwd?.trim();
  if (cwd) {
    lines.push(`  <cwd>${escapeTaskNotificationText(cwd)}</cwd>`);
  }

  if (typeof payload.exit_code === "number") {
    lines.push(`  <exit-code>${payload.exit_code}</exit-code>`);
  }

  const preview = payload.output_preview?.trim();
  if (preview) {
    lines.push(`  <result>${escapeTaskNotificationText(preview)}</result>`);
  }

  if (typeof payload.unread_bytes === "number" && payload.unread_bytes > 0) {
    lines.push(`  <unread-bytes>${payload.unread_bytes}</unread-bytes>`);
  }

  if (payload.error?.trim()) {
    lines.push(
      `  <error>${escapeTaskNotificationText(payload.error.trim())}</error>`,
    );
  }

  lines.push("</task-notification>");
  lines.push("");
  lines.push(
    `This is a background command update, not a new user request. If the preview is insufficient, call command_status with CommandId \"${escapeTaskNotificationText(commandId)}\" before replying.`,
  );

  return lines.join("\n");
}

function buildBackgroundCommandTaskSummary(
  payload: BackgroundCommandUpdatedPayload,
  status: string,
): string {
  const label = payload.command?.trim() || payload.command_id;
  switch (status) {
    case "completed":
      return `Background command ${label} completed.`;
    case "failed":
      if (typeof payload.exit_code === "number") {
        return `Background command ${label} failed with exit code ${payload.exit_code}.`;
      }
      return `Background command ${label} failed.`;
    default:
      return `Background command ${label} updated.`;
  }
}

function normalizeBackgroundCommandTaskStatus(status: string | undefined): string {
  const normalized = status?.trim().toLowerCase();
  if (!normalized) {
    return "updated";
  }
  return normalized;
}

function escapeTaskNotificationText(value: string): string {
  return value
    .replaceAll("&", "&amp;")
    .replaceAll("<", "&lt;")
    .replaceAll(">", "&gt;");
}

function CenteredViewportOverlay({ children }: { children: React.ReactNode }) {
  return (
    <Box
      position="absolute"
      width="100%"
      height="100%"
      justifyContent="center"
      alignItems="center"
      paddingX={1}
      paddingY={1}
      userSelect="none"
    >
      <Box
        flexDirection="column"
        width="100%"
        maxWidth={96}
        height="100%"
        maxHeight="85%"
        flexShrink={1}
        minWidth={0}
        minHeight={0}
        userSelect="contain"
      >
        {children}
      </Box>
    </Box>
  );
}

function SafeToastItem({ toast }: { toast: ToastData }) {
  return (
    <Box
      backgroundColor="$success"
      flexDirection="row"
      flexShrink={0}
      paddingY={0}
      paddingX={1}
    >
      <Text color="$surface-bg" bold>
        {toast.title}
      </Text>
    </Box>
  );
}

function selectVisibleArtifacts(
  artifacts: UIArtifact[],
  focusedArtifactId: string | null,
  showPlanPanel: boolean,
) {
  const visibleArtifacts = artifacts.filter(
    (artifact) =>
      artifact.kind !== "tool-log" && artifact.kind !== "diff-preview",
  );

  const filtered = visibleArtifacts.filter(
    (artifact) =>
      artifact.kind !== "implementation-plan" ||
      showPlanPanel ||
      artifact.id === focusedArtifactId,
  );

  if (!focusedArtifactId) {
    return filtered;
  }

  const focusedArtifact = filtered.find(
    (artifact) => artifact.id === focusedArtifactId,
  );
  const remainingArtifacts = filtered.filter(
    (artifact) => artifact.id !== focusedArtifactId,
  );

  return focusedArtifact ? [focusedArtifact, ...remainingArtifacts] : filtered;
}

function isQueuedPromptDispatchBlocked(
  uiState: ReturnType<typeof useEvents>["uiState"],
  isEngineReady: boolean,
): boolean {
  return (
    !isEngineReady ||
    uiState.isStreaming ||
    uiState.pendingPermission !== null ||
    uiState.pendingArtifactReview !== null ||
    uiState.pendingModelSelection !== null ||
    uiState.pendingRewindSelection !== null ||
    uiState.pendingResumeSelection !== null
  );
}

function clonePromptImages(
  images: UserInputImagePayload[],
): UserInputImagePayload[] {
  return images.map((image) => ({ ...image }));
}

function mergeQueuedPrompt(
  queuedPrompt: QueuedPrompt,
  nextText: string,
  nextImages: UserInputImagePayload[],
): QueuedPrompt {
  return {
    ...queuedPrompt,
    text: mergeQueuedPromptText(queuedPrompt.text, nextText),
    images: [...queuedPrompt.images, ...clonePromptImages(nextImages)],
  };
}

function mergeQueuedPromptText(currentText: string, nextText: string): string {
  const currentTrimmed = currentText.trim();
  const nextTrimmed = nextText.trim();

  if (currentTrimmed.length === 0) {
    return nextTrimmed;
  }

  if (nextTrimmed.length === 0) {
    return currentTrimmed;
  }

  return `${currentTrimmed}\n\n${nextTrimmed}`;
}

function getPromptBlockedReason({
  isEngineReady,
  engineError,
  backgroundTasksOpen,
  pendingModelSelection,
  pendingRewindSelection,
  pendingResumeSelection,
  transcriptSearchActive,
  isStreaming,
}: {
  isEngineReady: boolean;
  engineError: string | null;
  backgroundTasksOpen: boolean;
  pendingModelSelection: boolean;
  pendingRewindSelection: boolean;
  pendingResumeSelection: boolean;
  transcriptSearchActive: boolean;
  isStreaming: boolean;
}): string | null {
  if (engineError) {
    return "engine error";
  }
  if (!isEngineReady) {
    return "booting";
  }
  if (pendingResumeSelection) {
    return "resume selection open";
  }
  if (pendingModelSelection) {
    return "model selection open";
  }
  if (pendingRewindSelection) {
    return "rewind selection open";
  }
  if (backgroundTasksOpen) {
    return "tasks open";
  }
  if (transcriptSearchActive) {
    return "search open";
  }
  if (isStreaming) {
    return "turn active";
  }
  return null;
}

function backgroundTaskKey(kind: "command" | "agent", id: string): string {
  return `${kind}:${id}`;
}
