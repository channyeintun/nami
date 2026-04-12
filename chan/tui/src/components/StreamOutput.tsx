import React, { type FC, useEffect, useMemo, useRef, useState } from "react";
import { Box, Text, useInput } from "ink";
import type {
  UIActiveTurnStatus,
  UIAssistantBlock,
  UIAssistantMessage,
  UIMessage,
  UISystemMessage,
  UIUserMessage,
  UIToolCall,
  UITranscriptEntry,
} from "../hooks/useEvents.js";
import GroupedToolCalls, { type ToolCallGroup } from "./GroupedToolCalls.js";
import ToolProgress from "./ToolProgress.js";
import AssistantTextMessage from "./messages/AssistantTextMessage.js";
import StreamingAssistantMessage from "./messages/StreamingAssistantMessage.js";
import SystemTextMessage from "./messages/SystemTextMessage.js";
import UserTextMessage from "./messages/UserTextMessage.js";

interface StreamOutputProps {
  messages: UIMessage[];
  toolCalls: UIToolCall[];
  transcript: UITranscriptEntry[];
  liveBlocks: UIAssistantBlock[];
  isStreaming: boolean;
  activeTurnStatus: UIActiveTurnStatus;
  model: string;
  transcriptSearchQuery?: string;
  transcriptSearchSelectedIndex?: number;
  onTranscriptSearchStatsChange?: (
    totalMatches: number,
    selectedIndex: number,
  ) => void;
}

// Keep a few screens of recent transcript mounted without letting long sessions
// bog down Ink diffing or grow memory usage indefinitely.
const MAX_TRANSCRIPT_BLOCKS = 200;
// Reveal hidden history in noticeable chunks without making each page jump so
// large that the user loses their place in the conversation.
const TRANSCRIPT_CAP_STEP = 50;

type TranscriptSliceAnchor = {
  key: string;
  idx: number;
} | null;

type TranscriptBlock =
  | {
      kind: "message";
      key: string;
      message: UIAssistantMessage | UISystemMessage | UIUserMessage;
      continuation: boolean;
    }
  | { kind: "tool_call"; key: string; toolCall: UIToolCall }
  | { kind: "tool_group"; key: string; group: ToolCallGroup };

const StreamOutput: FC<StreamOutputProps> = ({
  messages,
  toolCalls,
  transcript,
  liveBlocks,
  isStreaming,
  activeTurnStatus,
  model,
  transcriptSearchQuery = "",
  transcriptSearchSelectedIndex = 0,
  onTranscriptSearchStatsChange,
}) => {
  const [sliceStartOverride, setSliceStartOverride] = useState<number | null>(
    null,
  );
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
  const sliceAnchorRef = useRef<TranscriptSliceAnchor>(null);
  const latestSliceStart = useMemo(
    () =>
      computeTranscriptSliceStart(
        transcriptBlocks,
        sliceAnchorRef,
        MAX_TRANSCRIPT_BLOCKS,
        TRANSCRIPT_CAP_STEP,
      ),
    [transcriptBlocks],
  );
  const maxSliceStart = Math.max(
    0,
    transcriptBlocks.length - MAX_TRANSCRIPT_BLOCKS,
  );
  const visibleSliceStart =
    sliceStartOverride === null
      ? latestSliceStart
      : clampSliceStart(sliceStartOverride, maxSliceStart);
  const visibleTranscriptBlocks = useMemo(
    () =>
      transcriptBlocks.slice(
        visibleSliceStart,
        visibleSliceStart + MAX_TRANSCRIPT_BLOCKS,
      ),
    [transcriptBlocks, visibleSliceStart],
  );
  const hiddenBeforeCount = visibleSliceStart;
  const hiddenAfterCount = Math.max(
    0,
    transcriptBlocks.length -
      visibleSliceStart -
      visibleTranscriptBlocks.length,
  );
  const searchQuery = transcriptSearchQuery.trim().toLowerCase();
  const searchMatchIndices = useMemo(() => {
    if (!searchQuery) {
      return [] as number[];
    }

    const matches: number[] = [];
    transcriptBlocks.forEach((block, index) => {
      if (blockSearchText(block).includes(searchQuery)) {
        matches.push(index);
      }
    });
    return matches;
  }, [searchQuery, transcriptBlocks]);
  const normalizedSearchSelectedIndex =
    searchMatchIndices.length === 0
      ? -1
      : Math.max(
          0,
          Math.min(transcriptSearchSelectedIndex, searchMatchIndices.length - 1),
        );

  useEffect(() => {
    if (transcriptBlocks.length <= MAX_TRANSCRIPT_BLOCKS) {
      if (sliceStartOverride !== null) {
        setSliceStartOverride(null);
      }
      return;
    }

    if (sliceStartOverride === null) {
      return;
    }

    const clamped = clampSliceStart(sliceStartOverride, maxSliceStart);
    if (clamped !== sliceStartOverride) {
      setSliceStartOverride(clamped);
    }
  }, [maxSliceStart, sliceStartOverride, transcriptBlocks.length]);

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

  useEffect(() => {
    if (!searchQuery || normalizedSearchSelectedIndex < 0) {
      return;
    }

    const targetIndex = searchMatchIndices[normalizedSearchSelectedIndex];
    if (typeof targetIndex !== "number") {
      return;
    }

    const desiredStart = clampSliceStart(
      targetIndex - Math.floor(MAX_TRANSCRIPT_BLOCKS / 3),
      maxSliceStart,
    );
    setSliceStartOverride(desiredStart >= maxSliceStart ? null : desiredStart);
  }, [
    maxSliceStart,
    normalizedSearchSelectedIndex,
    searchMatchIndices,
    searchQuery,
  ]);

  useInput(
    (_input, key) => {
      if (!key.pageUp && !key.pageDown && !key.home && !key.end) {
        return;
      }

      if (key.home) {
        setSliceStartOverride(0);
        return;
      }

      if (key.end) {
        setSliceStartOverride(null);
        return;
      }

      if (key.pageUp) {
        setSliceStartOverride((current) => {
          const base = current === null ? latestSliceStart : current;
          return clampSliceStart(base - TRANSCRIPT_CAP_STEP, maxSliceStart);
        });
        return;
      }

      setSliceStartOverride((current) => {
        const base = current === null ? latestSliceStart : current;
        const next = clampSliceStart(base + TRANSCRIPT_CAP_STEP, maxSliceStart);
        return next >= maxSliceStart ? null : next;
      });
    },
    { isActive: transcriptBlocks.length > MAX_TRANSCRIPT_BLOCKS },
  );

  if (transcript.length === 0 && liveBlocks.length === 0 && !isStreaming) {
    return null;
  }

  return (
    <Box flexDirection="column" paddingLeft={1} marginTop={1}>
      {hiddenBeforeCount > 0 || hiddenAfterCount > 0 ? (
        <Box marginBottom={1}>
          <Text dimColor>
            Showing transcript rows {hiddenBeforeCount + 1}-
            {hiddenBeforeCount + visibleTranscriptBlocks.length} of{" "}
            {transcriptBlocks.length} to keep long sessions responsive. PageUp
            shows older rows. PageDown returns to newer rows. Home jumps to the
            oldest visible window. End returns to the live tail.
          </Text>
        </Box>
      ) : null}

      {searchQuery ? (
        <Box marginBottom={1}>
          <Text color={searchMatchIndices.length > 0 ? "cyan" : "yellow"}>
            {searchMatchIndices.length > 0
              ? `Transcript search: ${searchMatchIndices.length} match${searchMatchIndices.length === 1 ? "" : "es"} · showing ${normalizedSearchSelectedIndex + 1}/${searchMatchIndices.length}`
              : `Transcript search: no matches for \"${transcriptSearchQuery}\"`}
          </Text>
        </Box>
      ) : null}

      {visibleTranscriptBlocks.map((block) => {
        const absoluteIndex = transcriptBlocks.indexOf(block);
        const matchOrdinal = searchMatchIndices.indexOf(absoluteIndex);
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
      })}

      {isStreaming ? (
        <StreamingAssistantMessage
          blocks={liveBlocks}
          model={model}
          statusLabel={activeTurnStatusLabel(liveBlocks, activeTurnStatus)}
        />
      ) : null}
    </Box>
  );
};

export default StreamOutput;

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

function computeTranscriptSliceStart(
  blocks: ReadonlyArray<{ key: string }>,
  anchorRef: { current: TranscriptSliceAnchor },
  cap = MAX_TRANSCRIPT_BLOCKS,
  step = TRANSCRIPT_CAP_STEP,
): number {
  const anchor = anchorRef.current;
  const anchorIndex = anchor
    ? blocks.findIndex((block) => block.key === anchor.key)
    : -1;
  let start =
    anchorIndex >= 0
      ? anchorIndex
      : anchor
        ? Math.min(anchor.idx, Math.max(0, blocks.length - cap))
        : 0;

  if (blocks.length - start > cap + step) {
    start = blocks.length - cap;
  }

  const blockAtStart = blocks[start];
  if (blockAtStart) {
    if (anchor?.key !== blockAtStart.key || anchor.idx !== start) {
      anchorRef.current = { key: blockAtStart.key, idx: start };
    }
  } else if (anchor) {
    anchorRef.current = null;
  }

  return start;
}

function clampSliceStart(value: number, maxSliceStart: number): number {
  return Math.max(0, Math.min(value, maxSliceStart));
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

function messageSearchText(message: UIMessage): string {
  switch (message.role) {
    case "assistant":
      return message.blocks.map((block) => block.text).join("\n").toLowerCase();
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
    .filter((value): value is string => typeof value === "string" && value.length > 0)
    .join("\n")
    .toLowerCase();
}
