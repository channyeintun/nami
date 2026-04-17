import React, {
  type FC,
  useCallback,
  useEffect,
  useMemo,
  useState,
} from "react";
import { Box, ListView, Text, useBoxRect } from "silvery";
import { DEFAULT_PROMPT_MARKER } from "../constants/prompt.js";
import type {
  UIActiveTurnStatus,
  UIAssistantBlock,
  UIArtifact,
  UIAssistantMessage,
  UIMessage,
  UIProgressEntry,
  UISystemMessage,
  UIUserMessage,
  UIToolCall,
  UITranscriptEntry,
} from "../hooks/useEvents.js";
import ArtifactView from "./ArtifactView.js";
import MessageRow from "./MessageRow.js";
import PlanPanel from "./PlanPanel.js";
import PreservedText from "./PreservedText.js";
import ToolProgress from "./ToolProgress.js";
import { activeTurnStatusLabel } from "../utils/activeTurnStatus.js";
import AssistantTextMessage from "./messages/AssistantTextMessage.js";
import StreamingAssistantMessage from "./messages/StreamingAssistantMessage.js";
import SystemTextMessage from "./messages/SystemTextMessage.js";
import UserTextMessage from "./messages/UserTextMessage.js";

interface StreamOutputProps {
  messages: UIMessage[];
  progressEntries: UIProgressEntry[];
  toolCalls: UIToolCall[];
  transcript: UITranscriptEntry[];
  artifacts: UIArtifact[];
  queuedPrompts: QueuedPromptPreview[];
  liveBlocks: UIAssistantBlock[];
  liveAssistantMessageId?: string | null;
  isStreaming: boolean;
  activeTurnStatus: UIActiveTurnStatus;
  model: string;
  showThinking?: boolean;
  thinkingShortcutLabel?: string;
  transcriptSearchQuery?: string;
  transcriptSearchSelectedIndex?: number;
  onTranscriptSearchStatsChange?: (
    totalMatches: number,
    selectedIndex: number,
  ) => void;
}

interface QueuedPromptPreview {
  id: number;
  text: string;
  imageCount: number;
}

type TranscriptBlock =
  | {
      kind: "message";
      key: string;
      message: UIAssistantMessage | UISystemMessage | UIUserMessage;
      continuation: boolean;
    }
  | {
      kind: "queued_prompt";
      key: string;
      prompt: QueuedPromptPreview;
    }
  | { kind: "progress"; key: string; progress: UIProgressEntry }
  | { kind: "artifact"; key: string; artifact: UIArtifact }
  | { kind: "tool_call"; key: string; toolCall: UIToolCall };

type DisplayBlock =
  | {
      kind: "transcript";
      key: string;
      block: TranscriptBlock;
    }
  | {
      kind: "streaming";
      key: string;
    };

const StreamOutput: FC<StreamOutputProps> = ({
  messages,
  progressEntries,
  toolCalls,
  transcript,
  artifacts,
  queuedPrompts,
  liveBlocks,
  liveAssistantMessageId = null,
  isStreaming,
  activeTurnStatus,
  model,
  showThinking = false,
  thinkingShortcutLabel = "Opt+T",
  transcriptSearchQuery = "",
  transcriptSearchSelectedIndex = 0,
  onTranscriptSearchStatsChange,
}) => {
  const [cursorIndex, setCursorIndex] = useState(0);
  const [userScrolled, setUserScrolled] = useState(false);
  const messageById = useMemo(
    () => new Map(messages.map((message) => [message.id, message])),
    [messages],
  );
  const toolCallById = useMemo(
    () => new Map(toolCalls.map((toolCall) => [toolCall.id, toolCall])),
    [toolCalls],
  );
  const progressById = useMemo(
    () => new Map(progressEntries.map((progress) => [progress.id, progress])),
    [progressEntries],
  );
  const artifactById = useMemo(
    () => new Map(artifacts.map((artifact) => [artifact.id, artifact])),
    [artifacts],
  );
  const transcriptBlocks = useMemo(
    () =>
      buildTranscriptBlocks(
        transcript,
        messageById,
        toolCallById,
        progressById,
        artifactById,
        liveAssistantMessageId,
        liveBlocks,
        model,
      ),
    [
      artifactById,
      liveAssistantMessageId,
      liveBlocks,
      messageById,
      model,
      progressById,
      toolCallById,
      transcript,
    ],
  );
  const displayBlocks = useMemo(() => {
    const items: DisplayBlock[] = transcriptBlocks.map((block) => ({
      kind: "transcript",
      key: block.key,
      block,
    }));

    if (isStreaming && (!liveAssistantMessageId || liveBlocks.length === 0)) {
      items.push({ kind: "streaming", key: "live-stream" });
    }

    items.push(
      ...queuedPrompts.map((prompt) => ({
        kind: "transcript" as const,
        key: `queued-${prompt.id}`,
        block: {
          kind: "queued_prompt" as const,
          key: `queued-${prompt.id}`,
          prompt,
        },
      })),
    );

    return items;
  }, [isStreaming, queuedPrompts, transcriptBlocks]);
  const { height: rectHeight } = useBoxRect();
  const viewportHeight = Math.max(1, rectHeight);
  const searchQuery = transcriptSearchQuery.trim().toLowerCase();
  const searchMatchIndices = useMemo(() => {
    if (!searchQuery) {
      return [] as number[];
    }

    const matches: number[] = [];
    displayBlocks.forEach((block, index) => {
      if (displayBlockSearchText(block).includes(searchQuery)) {
        matches.push(index);
      }
    });
    return matches;
  }, [displayBlocks, searchQuery]);
  const normalizedSearchSelectedIndex =
    searchMatchIndices.length === 0
      ? -1
      : Math.max(
          0,
          Math.min(
            transcriptSearchSelectedIndex,
            searchMatchIndices.length - 1,
          ),
        );

  // Auto-tail: when user hasn't scrolled away, follow new content
  useEffect(() => {
    if (!userScrolled && displayBlocks.length > 0) {
      setCursorIndex(displayBlocks.length - 1);
    }
  }, [displayBlocks.length, userScrolled]);

  // Jump cursor to search match
  useEffect(() => {
    if (searchQuery && normalizedSearchSelectedIndex >= 0) {
      const target = searchMatchIndices[normalizedSearchSelectedIndex];
      if (typeof target === "number") {
        setCursorIndex(target);
      }
    }
  }, [searchQuery, normalizedSearchSelectedIndex, searchMatchIndices]);

  // Reset userScrolled when search activates
  useEffect(() => {
    if (searchQuery) {
      setUserScrolled(false);
    }
  }, [searchQuery]);

  // Clamp cursor when items shrink
  useEffect(() => {
    if (displayBlocks.length > 0 && cursorIndex >= displayBlocks.length) {
      setCursorIndex(displayBlocks.length - 1);
    }
  }, [displayBlocks.length, cursorIndex]);

  const handleCursorChange = useCallback(
    (next: number) => {
      setCursorIndex(next);
      // If user scrolled to the tail, re-enable auto-tail
      setUserScrolled(next < displayBlocks.length - 1);
    },
    [displayBlocks.length],
  );

  useEffect(() => {
    onTranscriptSearchStatsChange?.(
      searchMatchIndices.length,
      normalizedSearchSelectedIndex,
    );
  }, [
    normalizedSearchSelectedIndex,
    onTranscriptSearchStatsChange,
    searchMatchIndices.length,
  ]);

  if (
    transcript.length === 0 &&
    artifacts.length === 0 &&
    queuedPrompts.length === 0 &&
    liveBlocks.length === 0 &&
    !isStreaming
  ) {
    return null;
  }

  return (
    <Box
      flexDirection="column"
      flexGrow={1}
      flexShrink={1}
      minHeight={0}
      marginTop={1}
      userSelect="contain"
    >
      <ListView
        items={displayBlocks}
        height={viewportHeight}
        nav
        cursorKey={cursorIndex}
        onCursor={handleCursorChange}
        active={!searchQuery}
        estimateHeight={(index) =>
          estimateDisplayBlockHeight(displayBlocks[index])
        }
        getKey={(item) => item.key}
        renderItem={(item, index) => {
          if (item.kind === "streaming") {
            return (
              <StreamingAssistantMessage
                blocks={liveBlocks}
                model={model}
                showThinking={showThinking}
                thinkingShortcutLabel={thinkingShortcutLabel}
                statusLabel={activeTurnStatusLabel(
                  liveBlocks,
                  activeTurnStatus,
                )}
              />
            );
          }

          return renderTranscriptBlock(
            item.block,
            index,
            searchMatchIndices,
            normalizedSearchSelectedIndex,
          );
        }}
      />
    </Box>
  );
};

export default StreamOutput;

function renderTranscriptBlock(
  block: TranscriptBlock,
  index: number,
  searchMatchIndices: number[],
  normalizedSearchSelectedIndex: number,
) {
  const matchOrdinal = searchMatchIndices.indexOf(index);
  const isSelectedSearchMatch =
    matchOrdinal >= 0 && matchOrdinal === normalizedSearchSelectedIndex;
  const searchLabel = renderSearchLabel(
    isSelectedSearchMatch,
    matchOrdinal,
    searchMatchIndices.length,
  );

  if (block.kind === "artifact") {
    return (
      <Box key={block.key} flexDirection="column">
        {searchLabel}
        {renderArtifactBlock(block.artifact)}
      </Box>
    );
  }

  if (block.kind === "progress") {
    return (
      <Box key={block.key} flexDirection="column">
        {searchLabel}
        <ProgressMessage progress={block.progress} />
      </Box>
    );
  }

  if (block.kind === "tool_call") {
    return (
      <Box key={block.key} flexDirection="column">
        {searchLabel}
        <ToolProgress toolCall={block.toolCall} />
      </Box>
    );
  }

  if (block.kind === "queued_prompt") {
    return (
      <Box key={block.key} flexDirection="column">
        {searchLabel}
        <QueuedPromptMessage prompt={block.prompt} />
      </Box>
    );
  }

  if (block.message.role === "assistant") {
    return (
      <Box key={block.key} flexDirection="column">
        {searchLabel}
        <AssistantTextMessage
          message={block.message}
          continuation={block.continuation}
        />
      </Box>
    );
  }

  if (block.message.role === "system") {
    return (
      <Box key={block.key} flexDirection="column">
        {searchLabel}
        <SystemTextMessage message={block.message} />
      </Box>
    );
  }

  return (
    <Box key={block.key} flexDirection="column">
      {searchLabel}
      <UserTextMessage
        message={block.message}
        continuation={block.continuation}
      />
    </Box>
  );
}

function renderSearchLabel(
  isSelectedSearchMatch: boolean,
  matchOrdinal: number,
  totalMatches: number,
) {
  if (!isSelectedSearchMatch || matchOrdinal < 0 || totalMatches <= 0) {
    return null;
  }

  return (
    <Box flexDirection="row" paddingLeft={1} marginBottom={1}>
      <Text
        color="$primary"
        bold
      >{`Match ${matchOrdinal + 1}/${totalMatches}`}</Text>
      <Text color="$muted">{" in transcript"}</Text>
    </Box>
  );
}

function estimateDisplayBlockHeight(block: DisplayBlock | undefined): number {
  if (!block) {
    return 1;
  }

  if (block.kind === "streaming") {
    return 8;
  }

  switch (block.block.kind) {
    case "artifact":
      return estimateArtifactHeight(block.block.artifact);
    case "progress":
      return 3;
    case "tool_call":
      return 4;
    case "queued_prompt":
      return estimateQueuedPromptHeight(block.block.prompt);
    case "message":
      return block.block.message.role === "assistant" ? 6 : 3;
    default:
      return 3;
  }
}

function renderArtifactBlock(artifact: UIArtifact) {
  if (artifact.kind === "implementation-plan") {
    return (
      <PlanPanel
        title={artifact.title}
        content={artifact.content}
        version={artifact.version}
        source={artifact.source}
        status={artifact.status}
      />
    );
  }

  return <ArtifactView artifacts={[artifact]} />;
}

function estimateArtifactHeight(artifact: UIArtifact): number {
  const contentLines = artifact.content.split("\n").length;
  return Math.max(6, Math.min(24, contentLines + 4));
}

function buildTranscriptBlocks(
  transcript: UITranscriptEntry[],
  messageById: Map<string, UIMessage>,
  toolCallById: Map<string, UIToolCall>,
  progressById: Map<string, UIProgressEntry>,
  artifactById: Map<string, UIArtifact>,
  liveAssistantMessageId: string | null,
  liveBlocks: UIAssistantBlock[],
  model: string,
): TranscriptBlock[] {
  // First pass: resolve all entries into flat blocks.
  const flat: TranscriptBlock[] = [];

  for (const entry of transcript) {
    if (!entry) {
      continue;
    }

    const refID = entry.refId ?? entry.id;

    if (entry.kind === "artifact") {
      const artifact = artifactById.get(refID);
      if (!artifact) {
        continue;
      }

      flat.push({
        kind: "artifact",
        key: `artifact-${artifact.id}`,
        artifact,
      });
      continue;
    }

    if (entry.kind === "progress") {
      const progress = progressById.get(refID);
      if (!progress) {
        continue;
      }

      flat.push({
        kind: "progress",
        key: `progress-${progress.id}`,
        progress,
      });
      continue;
    }

    if (entry.kind !== "tool_call") {
      const message =
        messageById.get(refID) ??
        resolveLiveAssistantMessage(
          refID,
          liveAssistantMessageId,
          liveBlocks,
          model,
        );
      if (!message) {
        continue;
      }

      flat.push({
        kind: "message",
        key: `message-${message.id}`,
        message,
        continuation: false,
      });
      continue;
    }

    const toolCall = toolCallById.get(refID);
    if (!toolCall) {
      continue;
    }

    flat.push({
      kind: "tool_call",
      key: `tool-${toolCall.id}`,
      toolCall,
    });
  }

  return flat;
}

function resolveLiveAssistantMessage(
  refID: string,
  liveAssistantMessageId: string | null,
  liveBlocks: UIAssistantBlock[],
  model: string,
): UIAssistantMessage | null {
  if (
    !liveAssistantMessageId ||
    refID !== liveAssistantMessageId ||
    liveBlocks.length === 0
  ) {
    return null;
  }

  return {
    id: liveAssistantMessageId,
    role: "assistant",
    blocks: liveBlocks,
    timestamp: "",
    model,
  };
}

function ProgressMessage({ progress }: { progress: UIProgressEntry }) {
  return (
    <MessageRow markerColor="$muted" markerDim marginBottom={0}>
      <Text color="$muted" dimColor wrap="wrap">
        {progress.text}
      </Text>
    </MessageRow>
  );
}

function blockSearchText(block: TranscriptBlock): string {
  switch (block.kind) {
    case "message":
      return messageSearchText(block.message);
    case "progress":
      return block.progress.text.toLowerCase();
    case "artifact":
      return [
        block.artifact.title,
        block.artifact.kind,
        block.artifact.content,
        block.artifact.source,
        block.artifact.status,
      ]
        .filter(
          (value): value is string =>
            typeof value === "string" && value.length > 0,
        )
        .join("\n")
        .toLowerCase();
    case "tool_call":
      return toolCallSearchText(block.toolCall);
    case "queued_prompt":
      return queuedPromptSearchText(block.prompt);
    default:
      return "";
  }
}

function displayBlockSearchText(block: DisplayBlock): string {
  if (block.kind === "transcript") {
    return blockSearchText(block.block);
  }

  return "";
}

function messageSearchText(message: UIMessage): string {
  switch (message.role) {
    case "assistant":
      return message.blocks
        .map((block) => block.text)
        .join("\n")
        .toLowerCase();
    case "system":
    case "user":
      return message.text.toLowerCase();
    default:
      return "";
  }
}

function toolCallSearchText(toolCall: UIToolCall): string {
  return [
    toolCall.name,
    toolCall.input,
    toolCall.output,
    toolCall.error,
    toolCall.preview,
  ]
    .filter(
      (value): value is string => typeof value === "string" && value.length > 0,
    )
    .join("\n")
    .toLowerCase();
}

function QueuedPromptMessage({ prompt }: { prompt: QueuedPromptPreview }) {
  return (
    <Box marginTop={1}>
      <MessageRow
        marker={DEFAULT_PROMPT_MARKER.trimEnd()}
        markerColor="$primary"
        label={
          <Text color="$muted" bold>
            Queued
          </Text>
        }
        meta={renderQueuedPromptMeta(prompt.imageCount)}
      >
        <Box width="100%" minWidth={0}>
          <PreservedText text={renderQueuedPromptText(prompt)} />
        </Box>
      </MessageRow>
    </Box>
  );
}

function renderQueuedPromptMeta(imageCount: number) {
  const parts = ["pending"];
  if (imageCount > 0) {
    parts.push(imageCount === 1 ? "1 image" : `${imageCount} images`);
  }

  return <Text dimColor>{parts.join("  ")}</Text>;
}

function renderQueuedPromptText(prompt: QueuedPromptPreview): string {
  const text = prompt.text.trim();
  if (text.length > 0) {
    return text;
  }

  return prompt.imageCount > 0 ? "(queued image prompt)" : "(queued prompt)";
}

function estimateQueuedPromptHeight(prompt: QueuedPromptPreview): number {
  const lines = renderQueuedPromptText(prompt).split("\n").length;
  return Math.max(3, Math.min(8, lines + 2));
}

function queuedPromptSearchText(prompt: QueuedPromptPreview): string {
  return [renderQueuedPromptText(prompt), "queued"]
    .filter((value) => value.length > 0)
    .join("\n")
    .toLowerCase();
}
