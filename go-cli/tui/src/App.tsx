import React, { type FC, useEffect, useState } from "react";
import { Box, Text } from "ink";
import { useEngine } from "./hooks/useEngine.js";
import { useEvents } from "./hooks/useEvents.js";
import ArtifactView from "./components/ArtifactView.js";
import Input from "./components/Input.js";
import PlanPanel from "./components/PlanPanel.js";
import PromptFooter from "./components/PromptFooter.js";
import StreamOutput from "./components/StreamOutput.js";
import StatusBar from "./components/StatusBar.js";
import PermissionPrompt from "./components/PermissionPrompt.js";
import { usePromptHistory } from "./hooks/usePromptHistory.js";
import {
  parseImageReferenceIds,
  type PastedImageData,
} from "./utils/imagePaste.js";
import type {
  PermissionResponseDecision,
  UserInputImagePayload,
} from "./protocol/types.js";

interface AppProps {
  enginePath: string;
  model: string;
  mode: string;
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
  const engine = useEngine(enginePath, { model, mode });
  const prompt = usePromptHistory();
  const [promptImages, setPromptImages] = useState<UserInputImagePayload[]>([]);
  const [nextImageId, setNextImageId] = useState(1);
  const {
    uiState,
    handleEvent,
    clearStream,
    clearPermission,
    appendUserMessage,
    beginAssistantTurn,
  } = useEvents(model, mode);
  const planArtifact =
    uiState.artifacts.find(
      (artifact) => artifact.kind === "implementation-plan",
    ) ?? null;
  const recentArtifacts = uiState.artifacts
    .filter(
      (artifact) =>
        artifact.kind !== "implementation-plan" && artifact.kind !== "tool-log",
    )
    .slice(0, 2);

  // Dispatch incoming events to the UI state handler
  useEffect(() => {
    if (!engine.lastEvent) return;
    handleEvent(engine.lastEvent);
  }, [engine.eventVersion, engine.lastEvent, handleEvent]);

  useEffect(() => {
    setPromptImages((current) => {
      const referencedIds = parseImageReferenceIds(prompt.value);
      const next = current.filter((image) => referencedIds.has(image.id));
      return next.length === current.length ? current : next;
    });
  }, [prompt.value]);

  const handleImagePaste = (images: PastedImageData[]) => {
    let startId = nextImageId;
    const nextImages = images.map((image, index) => {
      const id = startId + index;
      prompt.insertImageReference(id);
      return toUserInputImagePayload(id, image);
    });

    setPromptImages((current) => [...current, ...nextImages]);
    setNextImageId(startId + images.length);
  };

  const handleSubmit = () => {
    const text = prompt.submit();
    if (!text) {
      return;
    }

    const referencedIds = parseImageReferenceIds(text);
    const images = promptImages.filter((image) => referencedIds.has(image.id));
    setPromptImages((current) =>
      current.filter((image) => !referencedIds.has(image.id)),
    );

    appendUserMessage(text);
    clearStream();
    beginAssistantTurn();
    if (text.startsWith("/") && images.length === 0) {
      const [cmd, ...rest] = text.slice(1).split(" ");
      engine.sendCommand(cmd!, rest.join(" "));
    } else {
      engine.sendInput(text, images);
    }
  };

  const handlePermissionResponse = (
    decision: PermissionResponseDecision,
    feedback?: string,
  ) => {
    if (uiState.pendingPermission) {
      beginAssistantTurn();
      clearPermission(decision);
      engine.sendPermissionResponse(
        uiState.pendingPermission.request_id,
        decision,
        feedback,
      );
    }
  };

  const isPromptDisabled =
    !uiState.ready || !!engine.error || uiState.pendingPermission !== null;

  return (
    <Box flexDirection="column" height="100%">
      <StatusBar
        ready={uiState.ready || engine.ready}
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
        rateLimits={uiState.rateLimits}
      />

      <Box flexDirection="column" flexGrow={1}>
        {engine.error && !uiState.error && (
          <Box borderStyle="round" borderColor="red" paddingX={1} marginTop={1}>
            <Text color="red">{engine.error}</Text>
          </Box>
        )}

        {!uiState.ready && !engine.error && (
          <Box paddingLeft={1} marginTop={1}>
            <Text color="gray">Starting Go engine...</Text>
          </Box>
        )}

        {uiState.statusLine && (
          <Box paddingLeft={1} marginTop={1}>
            <Text color="cyan">{uiState.statusLine}</Text>
          </Box>
        )}

        {uiState.compact && (
          <Box paddingLeft={1} marginTop={1}>
            <Text color="yellow">
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
          liveBlocks={uiState.liveAssistantBlocks}
          isStreaming={uiState.isStreaming}
          model={uiState.model}
        />

        {uiState.error && (
          <Box borderStyle="round" borderColor="red" paddingX={1} marginTop={1}>
            <Text color="red">{uiState.error}</Text>
          </Box>
        )}

        {planArtifact && (
          <PlanPanel
            title={planArtifact.title}
            content={planArtifact.content}
          />
        )}

        {recentArtifacts.length > 0 && (
          <ArtifactView artifacts={recentArtifacts} />
        )}
      </Box>

      {uiState.pendingPermission ? (
        <PermissionPrompt
          tool={uiState.pendingPermission.tool}
          command={uiState.pendingPermission.command}
          risk={uiState.pendingPermission.risk}
          permissionLevel={uiState.pendingPermission.permission_level}
          targetKind={uiState.pendingPermission.target_kind}
          targetValue={uiState.pendingPermission.target_value}
          workingDir={uiState.pendingPermission.working_dir}
          onRespond={handlePermissionResponse}
          onCancel={() => handlePermissionResponse("deny")}
        />
      ) : (
        <Box flexDirection="column">
          <Input
            prompt={prompt}
            mode={uiState.mode}
            isLoading={uiState.isStreaming}
            onSubmit={handleSubmit}
            onImagePaste={handleImagePaste}
            onModeToggle={engine.sendModeToggle}
            onCancel={engine.sendCancel}
            disabled={isPromptDisabled}
          />
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
          />
        </Box>
      )}
    </Box>
  );
};

export default App;
