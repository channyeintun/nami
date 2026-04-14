import React, { type FC, useCallback, useEffect, useMemo, useState } from "react";
import { Box, ListView, Text, useBoxRect } from "silvery";
import type {
  UIActiveTurnStatus,
  UIAssistantBlock,
  UIArtifact,
  UIAssistantMessage,
  UIMessage,
  UISystemMessage,
  UIUserMessage,
  UIToolCall,
  UITranscriptEntry,
} from "../hooks/useEvents.js";
import ArtifactView from "./ArtifactView.js";
import GroupedToolCalls, { type ToolCallGroup } from "./GroupedToolCalls.js";
import PlanPanel from "./PlanPanel.js";
import ToolProgress from "./ToolProgress.js";
import AssistantTextMessage from "./messages/AssistantTextMessage.js";
import StreamingAssistantMessage from "./messages/StreamingAssistantMessage.js";
import SystemTextMessage from "./messages/SystemTextMessage.js";
import UserTextMessage from "./messages/UserTextMessage.js";

interface StreamOutputProps {
  messages: UIMessage[];
  toolCalls: UIToolCall[];
  transcript: UITranscriptEntry[];
  artifacts: UIArtifact[];
  liveBlocks: UIAssistantBlock[];
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

type TranscriptBlock =
  | {
      kind: "message";
      key: string;
      message: UIAssistantMessage | UISystemMessage | UIUserMessage;
      continuation: boolean;
    }
  | { kind: "tool_call"; key: string; toolCall: UIToolCall }
  | { kind: "tool_group"; key: string; group: ToolCallGroup };

type DisplayBlock =
  | {
      kind: "transcript";
      key: string;
      block: TranscriptBlock;
    }
  | {
      kind: "artifact";
      key: string;
      artifact: UIArtifact;
    }
  | {
      kind: "streaming";
      key: string;
    };

const StreamOutput: FC<StreamOutputProps> = ({
  messages,
  toolCalls,
  transcript,
  artifacts,
  liveBlocks,
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
  const transcriptBlocks = useMemo(
    () => buildTranscriptBlocks(transcript, messageById, toolCallById),
    [messageById, toolCallById, transcript],
  );
  const displayBlocks = useMemo(() => {
    const items: DisplayBlock[] = transcriptBlocks.map((block) => ({
      kind: "transcript",
      key: block.key,
      block,
    }));

    items.push(
      ...artifacts.map((artifact) => ({
        kind: "artifact" as const,
        key: `artifact-${artifact.id}`,
        artifact,
      })),
    );

    if (isStreaming) {
      items.push({ kind: "streaming", key: "live-stream" });
    }

    return items;
  }, [artifacts, isStreaming, transcriptBlocks]);
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
    >
      <ListView
        items={displayBlocks}
        height={viewportHeight}
        nav
        cursorKey={cursorIndex}
        onCursor={handleCursorChange}
        active={!searchQuery}
        overflowIndicator
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

          if (item.kind === "artifact") {
            return (
              <Box key={item.key} flexDirection="column">
                {renderArtifactBlock(item.artifact)}
              </Box>
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
  const isSearchMatch = matchOrdinal >= 0;

  if (block.kind === "tool_group") {
    return (
      <Box key={block.key} flexDirection="column">
        {isSelectedSearchMatch ? (
          <Text color="cyan">Search match</Text>
        ) : isSearchMatch ? (
          <Text dimColor>match</Text>
        ) : null}
        <GroupedToolCalls group={block.group} />
      </Box>
    );
  }

  if (block.kind === "tool_call") {
    return (
      <Box key={block.key} flexDirection="column">
        {isSelectedSearchMatch ? (
          <Text color="cyan">Search match</Text>
        ) : isSearchMatch ? (
          <Text dimColor>match</Text>
        ) : null}
        <ToolProgress toolCall={block.toolCall} />
      </Box>
    );
  }

  if (block.message.role === "assistant") {
    return (
      <Box key={block.key} flexDirection="column">
        {isSelectedSearchMatch ? (
          <Text color="cyan">Search match</Text>
        ) : isSearchMatch ? (
          <Text dimColor>match</Text>
        ) : null}
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
        {isSelectedSearchMatch ? (
          <Text color="cyan">Search match</Text>
        ) : isSearchMatch ? (
          <Text dimColor>match</Text>
        ) : null}
        <SystemTextMessage message={block.message} />
      </Box>
    );
  }

  return (
    <Box key={block.key} flexDirection="column">
      {isSelectedSearchMatch ? (
        <Text color="cyan">Search match</Text>
      ) : isSearchMatch ? (
        <Text dimColor>match</Text>
      ) : null}
      <UserTextMessage
        message={block.message}
        continuation={block.continuation}
      />
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

  if (block.kind === "artifact") {
    return estimateArtifactHeight(block.artifact);
  }

  switch (block.block.kind) {
    case "tool_group":
      return Math.max(4, block.block.group.toolCalls.length * 2);
    case "tool_call":
      return 4;
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

  return (
    <ArtifactView artifacts={[artifact]} />
  );
}

function estimateArtifactHeight(artifact: UIArtifact): number {
  const contentLines = artifact.content.split("\n").length;
  return Math.max(6, Math.min(24, contentLines + 4));
}

function buildTranscriptBlocks(
  transcript: UITranscriptEntry[],
  messageById: Map<string, UIMessage>,
  toolCallById: Map<string, UIToolCall>,
): TranscriptBlock[] {
  const blocks: TranscriptBlock[] = [];
  let previousMessageRole: UIMessage["role"] | null = null;

  for (let index = 0; index < transcript.length; index += 1) {
    const entry = transcript[index];
    if (!entry) {
      continue;
    }

    if (entry.kind !== "tool_call") {
      const message = messageById.get(entry.id);
      if (!message) {
        continue;
      }

      blocks.push({
        kind: "message",
        key: `message-${message.id}`,
        message,
        continuation: previousMessageRole === message.role,
      });
      previousMessageRole = message.role;
      continue;
    }

    const run: UIToolCall[] = [];
    let cursor = index;
    while (
      cursor < transcript.length &&
      transcript[cursor]?.kind === "tool_call"
    ) {
      const toolCall = toolCallById.get(transcript[cursor]!.id);
      if (toolCall) {
        run.push(toolCall);
      }
      cursor += 1;
    }

    blocks.push(...buildToolBlocks(run));
    previousMessageRole = null;
    index = cursor - 1;
  }

  return blocks;
}

function activeTurnStatusLabel(
  blocks: UIAssistantBlock[],
  activeTurnStatus: UIActiveTurnStatus,
): string {
  switch (activeTurnStatus) {
    case "thinking":
      return "Thinking";
    case "responding":
      return "Responding";
    case "running_tools":
      return "Running tools";
    case "waiting_permission":
      return "Waiting for permission";
    case "cancelling":
      return "Cancelling";
    case "working":
    case "idle":
    default:
      break;
  }

  const lastBlock = blocks[blocks.length - 1];
  if (!lastBlock) {
    return "Working";
  }
  return lastBlock.kind === "thinking" ? "Thinking" : "Responding";
}

function buildToolBlocks(toolCalls: UIToolCall[]): TranscriptBlock[] {
  const blocks: TranscriptBlock[] = [];

  for (let index = 0; index < toolCalls.length; index += 1) {
    const toolCall = toolCalls[index];
    const groupKind = toolGroupKind(toolCall);

    if (groupKind !== "read_search") {
      blocks.push({
        kind: "tool_call",
        key: `tool-${toolCall.id}`,
        toolCall,
      });
      continue;
    }

    const grouped: UIToolCall[] = [toolCall];
    let cursor = index + 1;
    while (
      cursor < toolCalls.length &&
      toolGroupKind(toolCalls[cursor]!) === groupKind
    ) {
      grouped.push(toolCalls[cursor]!);
      cursor += 1;
    }

    if (grouped.length >= 2) {
      blocks.push({
        kind: "tool_group",
        key: `tool-group-${grouped[0]!.id}-${grouped[grouped.length - 1]!.id}`,
        group: {
          id: `tool-group-${grouped[0]!.id}-${grouped[grouped.length - 1]!.id}`,
          kind: "read_search",
          toolCalls: grouped,
        },
      });
      index = cursor - 1;
      continue;
    }

    blocks.push({
      kind: "tool_call",
      key: `tool-${toolCall.id}`,
      toolCall,
    });
  }

  return blocks;
}

function toolGroupKind(toolCall: UIToolCall): ToolCallGroup["kind"] | null {
  switch (toolCall.name) {
    case "file_read":
    case "grep":
    case "glob":
    case "web_search":
    case "web_fetch":
    case "git":
      return "read_search";
    default:
      return null;
  }
}

function blockSearchText(block: TranscriptBlock): string {
  switch (block.kind) {
    case "message":
      return messageSearchText(block.message);
    case "tool_call":
      return toolCallSearchText(block.toolCall);
    case "tool_group":
      return block.group.toolCalls.map(toolCallSearchText).join("\n");
    default:
      return "";
  }
}

function displayBlockSearchText(block: DisplayBlock): string {
  if (block.kind === "transcript") {
    return blockSearchText(block.block);
  }

  if (block.kind === "artifact") {
    return [
      block.artifact.title,
      block.artifact.kind,
      block.artifact.content,
      block.artifact.source,
      block.artifact.status,
    ]
      .filter(
        (value): value is string => typeof value === "string" && value.length > 0,
      )
      .join("\n")
      .toLowerCase();
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
