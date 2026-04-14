import React, { type FC, useCallback, useEffect, useState } from "react";
import { Spinner } from "silvery";
import { Box, Screen, Text } from "silvery";
import { useEngine } from "./hooks/useEngine.js";
import { useEvents, type UIArtifact } from "./hooks/useEvents.js";
import ArtifactReviewPrompt from "./components/ArtifactReviewPrompt.js";
import Input from "./components/Input.js";
import PromptFooter from "./components/PromptFooter.js";
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
  const {
    uiState,
    handleEvent,
    clearStream,
    cancelActiveTurn,
    clearPermission,
    appendUserMessage,
    beginAssistantTurn,
    submitArtifactReview,
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

  const submitPrompt = useCallback(
    (text: string, images: UserInputImagePayload[]) => {
      appendUserMessage(text);
      clearStream();
      beginAssistantTurn();
      if (text.startsWith("/") && images.length === 0) {
        const [cmd, ...rest] = text.slice(1).split(" ");
        engine.sendCommand(cmd!, rest.join(" "));
        return;
      }

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
    if (
      !isEngineReady ||
      uiState.isStreaming ||
      uiState.pendingPermission ||
      uiState.pendingArtifactReview
    ) {
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
  ]);

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

  const handleSubmit = () => {
    const text = prompt.submit();
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
      queuedPrompts.length
    ) {
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
    uiState.pendingArtifactReview !== null;
  const promptBlockedReason = getPromptBlockedReason({
    isEngineReady,
    engineError: engine.error,
    transcriptSearchActive,
    isStreaming: uiState.isStreaming,
  });
  const promptActivityLabel = uiState.isStreaming
    ? activeTurnStatusLabel(uiState.liveAssistantBlocks, uiState.activeTurnStatus)
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
          queuedPromptCount={queuedPrompts.length}
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
            toolCalls={uiState.toolCalls}
            transcript={uiState.transcript}
            artifacts={visibleArtifacts}
            liveBlocks={uiState.liveAssistantBlocks}
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
        <Box
          flexDirection="column"
          flexShrink={1}
          minHeight={0}
          marginTop={1}
        >
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
        <Box
          flexDirection="column"
          flexShrink={0}
          minHeight={0}
          marginTop={1}
        >
          <ArtifactReviewPrompt
            review={uiState.pendingArtifactReview}
            onRespond={handleArtifactReviewResponse}
          />
        </Box>
      ) : (
        <Box
          flexDirection="column"
          flexShrink={0}
          maxHeight="45%"
          overflow="scroll"
        >
          {queuedPrompts.length > 0 && (
            <Box flexDirection="column" paddingLeft={1} marginBottom={1}>
              <Text color="yellow">
                Queued prompts ({queuedPrompts.length})
              </Text>
              {queuedPrompts.slice(0, 3).map((queuedPrompt) => (
                <Box key={queuedPrompt.id} flexDirection="row">
                  <Text dimColor>{"> "}</Text>
                  <Text dimColor>{summarizeQueuedPrompt(queuedPrompt)}</Text>
                </Box>
              ))}
              {queuedPrompts.length > 3 && (
                <Text
                  dimColor
                >{`+${queuedPrompts.length - 3} more queued`}</Text>
              )}
            </Box>
          )}
          {transcriptSearchActive ? (
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
            showArtifacts={showArtifacts}
            artifactsShortcutLabel={ARTIFACTS_TOGGLE_SHORTCUT_LABEL}
          />
        </Box>
      )}
    </Screen>
  );
};

export default App;

function selectVisibleArtifacts(
  artifacts: UIArtifact[],
  focusedArtifactId: string | null,
  showPlanPanel: boolean,
) {
  const visibleArtifacts = artifacts.filter(
    (artifact) =>
      artifact.kind !== "tool-log" &&
      artifact.kind !== "diff-preview",
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

function summarizeQueuedPrompt(queuedPrompt: QueuedPrompt): string {
  const flattened = queuedPrompt.text.replace(/\s+/g, " ").trim();
  const summary =
    flattened.length > 88 ? `${flattened.slice(0, 85)}...` : flattened;

  if (queuedPrompt.images.length === 0) {
    return summary;
  }

  const suffix =
    queuedPrompt.images.length === 1
      ? " [1 image]"
      : ` [${queuedPrompt.images.length} images]`;

  return `${summary}${suffix}`;
}

function getPromptBlockedReason({
  isEngineReady,
  engineError,
  transcriptSearchActive,
  isStreaming,
}: {
  isEngineReady: boolean;
  engineError: string | null;
  transcriptSearchActive: boolean;
  isStreaming: boolean;
}): string | null {
  if (engineError) {
    return "engine error";
  }
  if (!isEngineReady) {
    return "booting";
  }
  if (transcriptSearchActive) {
    return "search open";
  }
  if (isStreaming) {
    return "turn active";
  }
  return null;
}
