import React, { type FC } from "react";
import { Box, Text } from "ink";
import type {
  UIAssistantBlock,
  UIAssistantMessage,
} from "../../hooks/useEvents.js";
import MessageRow from "../MessageRow.js";
import MarkdownText from "../MarkdownText.js";

interface AssistantTextMessageProps {
  message: UIAssistantMessage;
  continuation?: boolean;
}

const AssistantTextMessage: FC<AssistantTextMessageProps> = ({
  message,
  continuation = false,
}) => {
  const visibleBlocks = message.blocks.filter(
    (block) => block.kind === "text" && block.text.trim().length > 0,
  );

  if (visibleBlocks.length === 0) {
    return null;
  }

  return (
    <MessageRow
      markerColor="green"
      label={
        continuation ? null : (
          <Text color="green" bold>
            Assistant
          </Text>
        )
      }
      meta={renderMetadata(message)}
    >
      <Box flexDirection="column">
        {visibleBlocks.map((block, index) =>
          renderAssistantBlock(block, index, visibleBlocks.length),
        )}
      </Box>
    </MessageRow>
  );
};

export default AssistantTextMessage;

function renderAssistantBlock(
  block: UIAssistantBlock,
  index: number,
  blockCount: number,
) {
  return (
    <Box key={`${block.kind}-${index}`} marginTop={index === 0 ? 0 : 1}>
      <MarkdownText text={block.text} streaming={index === blockCount - 1} />
    </Box>
  );
}

function renderMetadata(message: UIAssistantMessage) {
  const parts: string[] = [];

  if (message.timestamp) {
    parts.push(
      new Date(message.timestamp).toLocaleTimeString("en-US", {
        hour: "2-digit",
        minute: "2-digit",
        hour12: true,
      }),
    );
  }

  if (message.model) {
    parts.push(message.model);
  }

  if (parts.length === 0) {
    return null;
  }

  return <Text dimColor>{parts.join("  ")}</Text>;
}
