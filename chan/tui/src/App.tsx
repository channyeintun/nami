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
import Input from "./components/Input.js";
import ModelSelectionPrompt from "./components/ModelSelectionPrompt.js";
import PromptFooter from "./components/PromptFooter.js";
import RewindSelectionPrompt from "./components/RewindSelectionPrompt.js";
import ResumeSelectionPrompt from "./components/ResumeSelectionPrompt.js";
import StreamOutput from "./components/StreamOutput.js";
import StatusBar from "./components/StatusBar.js";
import TranscriptSearchPrompt from "./components/TranscriptSearchPrompt.js";
import PermissionPrompt from "./components/PermissionPrompt.js";
import { usePromptHistory } from "./hooks/usePromptHistory.js";
import {
  parseImageReferenceIds,
  type PastedImageData,
} from "./utils/imagePaste.js";
import { activeTurnStatusLabel } from "./utils/activeTurnStatus.js";
import type {
  PermissionResponseDecision,
  UserInputImagePayload,
} from "./protocol/types.js";

const THINKING_TOGGLE_SHORTCUT_LABEL = "Opt+T";
const ARTIFACTS_TOGGLE_SHORTCUT_LABEL = "Opt+A";
const FOOTER_HINT_REVEAL_MS = 2500;

interface AppProps {
  enginePath: string;
  model: string;
  mode: string;
}

interface QueuedPrompt {
  id: number;
  text: string;
  images: UserInputImagePayload[];
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

const App: FC<AppProps> = ({ enginePath, model, mode }) => {
  const prompt = usePromptHistory();
  const [promptImages, setPromptImages] = useState<UserInputImagePayload[]>([]);
  const [pasteWarning, setPasteWarning] = useState<string | null>(null);
  const [nextImageId, setNextImageId] = useState(1);
  const [queuedPrompts, setQueuedPrompts] = useState<QueuedPrompt[]>([]);
  const [nextQueuedPromptId, setNextQueuedPromptId] = useState(1);
  const [transcriptSearchActive, setTranscriptSearchActive] = useState(false);
  const [transcriptSearchQuery, setTranscriptSearchQuery] = useState("");
  const [transcriptSearchSelectedIndex, setTranscriptSearchSelectedIndex] =
    useState(0);
  const [transcriptSearchMatchCount, setTranscriptSearchMatchCount] =
    useState(0);
  const [showThinking, setShowThinking] = useState(false);
  const [showArtifacts, setShowArtifacts] = useState(true);
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
  const engine = useEngine(enginePath, { model, mode, onEvent: handleEvent });
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
      title: "Complete",
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
    if (isQueuedPromptDispatchBlocked(uiState, isEngineReady)) {
      return;
    }

    const queuedPrompt = queuedPrompts[0];
    if (!queuedPrompt) {
      return;
    }

    setQueuedPrompts((current) => current.slice(1));
    setPasteWarning(null);
    submitPrompt(queuedPrompt.text, queuedPrompt.images);
  }, [isEngineReady, queuedPrompts, submitPrompt, uiState]);

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

  const handleImagePaste = (images: PastedImageData[]) => {
    let startId = nextImageId;
    const nextImages = images.map((image, index) => {
      const id = startId + index;
      prompt.insertImageReference(id);
      return toUserInputImagePayload(id, image);
    });

    setPromptImages((current) => [...current, ...nextImages]);
    setNextImageId(startId + images.length);
    setPasteWarning(null);
  };

  const handlePasteWarning = (warnings: string[]) => {
    setPasteWarning(warnings.length > 0 ? warnings.join(" | ") : null);
  };

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

  return (
    <Screen>
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
                <Spinner type="dots" /> Starting Go engine...
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
          overflow="scroll"
        >
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
      ) : (
        null
      )}

      <SafeToastContainer toasts={toasts} />
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
      height="100%"
      justifyContent="center"
      alignItems="center"
    >
      <SafeToastItem toast={latestToast} />
    </Box>
  );
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
  const accentColor = toastAccentColor(toast.variant);
  const description =
    typeof toast.description === "string" && toast.description.trim().length > 0
      ? toast.description.trim()
      : null;

  return (
    <Box
      backgroundColor="$surface-bg"
      borderColor={accentColor}
      borderStyle="round"
      flexDirection="column"
      flexShrink={0}
      paddingY={1}
      paddingX={2}
    >
      <Text color={accentColor} bold>
        {toastVariantIcon(toast.variant)} {toast.title}
      </Text>
      {description ? <Text color="$muted">{description}</Text> : null}
    </Box>
  );
}

function toastAccentColor(variant: ToastData["variant"]): string {
  switch (variant) {
    case "success":
      return "$success";
    case "error":
      return "$error";
    case "warning":
      return "$warning";
    case "info":
      return "$info";
    default:
      return "$fg";
  }
}

function toastVariantIcon(variant: ToastData["variant"]): string {
  switch (variant) {
    case "success":
      return "+";
    case "error":
      return "x";
    case "warning":
      return "!";
    default:
      return "i";
  }
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
  pendingModelSelection,
  pendingRewindSelection,
  pendingResumeSelection,
  transcriptSearchActive,
  isStreaming,
}: {
  isEngineReady: boolean;
  engineError: string | null;
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
  if (transcriptSearchActive) {
    return "search open";
  }
  if (isStreaming) {
    return "turn active";
  }
  return null;
}
