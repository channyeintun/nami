import React, { type FC, useMemo, useRef } from "react";
import { Box, Text } from "ink";
import type {
  UIAssistantBlock,
  UIAssistantMessage,
  UIMessage,
  UIUserMessage,
  UIToolCall,
  UITranscriptEntry,
} from "../hooks/useEvents.js";
import GroupedToolCalls, { type ToolCallGroup } from "./GroupedToolCalls.js";
import ToolProgress from "./ToolProgress.js";
import AssistantTextMessage from "./messages/AssistantTextMessage.js";
import StreamingAssistantMessage from "./messages/StreamingAssistantMessage.js";
import UserTextMessage from "./messages/UserTextMessage.js";

interface StreamOutputProps {
  messages: UIMessage[];
  toolCalls: UIToolCall[];
  transcript: UITranscriptEntry[];
  liveBlocks: UIAssistantBlock[];
  isStreaming: boolean;
  model: string;
}

const MAX_TRANSCRIPT_BLOCKS = 200;
const TRANSCRIPT_CAP_STEP = 50;

type TranscriptSliceAnchor = {
  key: string;
  idx: number;
} | null;

type TranscriptBlock =
  | {
      kind: "message";
      key: string;
      message: UIAssistantMessage | UIUserMessage;
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
  model,
}) => {
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
  const visibleTranscriptBlocks = useMemo(() => {
    const sliceStart = computeTranscriptSliceStart(
      transcriptBlocks,
      sliceAnchorRef,
    );
    return sliceStart > 0
      ? transcriptBlocks.slice(sliceStart)
      : transcriptBlocks;
  }, [transcriptBlocks]);
  const hiddenBlockCount =
    transcriptBlocks.length - visibleTranscriptBlocks.length;

  if (transcript.length === 0 && liveBlocks.length === 0 && !isStreaming) {
    return null;
  }

  return (
    <Box flexDirection="column" paddingLeft={1} marginTop={1}>
      {hiddenBlockCount > 0 ? (
        <Box marginBottom={1}>
          <Text dimColor>
            Showing latest {visibleTranscriptBlocks.length} transcript rows to
            keep long sessions responsive. {hiddenBlockCount} earlier
            {hiddenBlockCount === 1 ? " row is" : " rows are"} not re-rendered.
          </Text>
        </Box>
      ) : null}

      {visibleTranscriptBlocks.map((block) => {
        if (block.kind === "tool_group") {
          return <GroupedToolCalls key={block.key} group={block.group} />;
        }

        if (block.kind === "tool_call") {
          return <ToolProgress key={block.key} toolCall={block.toolCall} />;
        }

        return block.message.role === "assistant" ? (
          <AssistantTextMessage
            key={block.key}
            message={block.message}
            continuation={block.continuation}
          />
        ) : (
          <UserTextMessage
            key={block.key}
            message={block.message}
            continuation={block.continuation}
          />
        );
      })}

      {isStreaming ? (
        <StreamingAssistantMessage
          blocks={liveBlocks}
          model={model}
          statusLabel={streamingStatusLabel(liveBlocks)}
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

function streamingStatusLabel(blocks: UIAssistantBlock[]): string {
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
