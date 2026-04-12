import React, { type FC } from "react";
import { Box, Text } from "ink";
import Spinner from "ink-spinner";
import type { UIAssistantBlock } from "../../hooks/useEvents.js";
import MessageRow from "../MessageRow.js";
import MarkdownText from "../MarkdownText.js";
import AssistantThinkingMessage from "./AssistantThinkingMessage.js";

interface StreamingAssistantMessageProps {
  blocks: UIAssistantBlock[];
  statusLabel: string;
  model?: string;
}

const StreamingAssistantMessage: FC<StreamingAssistantMessageProps> = ({
  blocks,
  statusLabel,
  model,
}) => {
  const activeThinkingIndex =
    statusLabel === "Thinking" ? findLastBlockIndex(blocks, "thinking") : -1;
  const showStatusRow = !(
    statusLabel === "Thinking" && activeThinkingIndex >= 0
  );

  return (
    <MessageRow
      markerColor="green"
      markerDim
      label={
        <Text color="green" dimColor>
          Assistant
        </Text>
      }
      meta={model ? <Text dimColor>{model}</Text> : null}
    >
      <Box flexDirection="column">
        {showStatusRow ? (
          <Text color="gray">
            <Spinner type="dots" /> {statusLabel}
          </Text>
        ) : null}
        {blocks.map((block, index) => (
          <Box
            key={`${block.kind}-${index}`}
            marginTop={showStatusRow || index > 0 ? 1 : 0}
          >
            {block.kind === "thinking" ? (
              <AssistantThinkingMessage
                text={block.text}
                streaming={index === activeThinkingIndex}
              />
            ) : (
              <MarkdownText
                text={block.text}
                streaming={index === blocks.length - 1}
              />
            )}
          </Box>
        ))}
      </Box>
    </MessageRow>
  );
};

export default StreamingAssistantMessage;

function findLastBlockIndex(
  blocks: UIAssistantBlock[],
  kind: UIAssistantBlock["kind"],
): number {
  for (let index = blocks.length - 1; index >= 0; index -= 1) {
    if (blocks[index]?.kind === kind) {
      return index;
    }
  }
  return -1;
}
