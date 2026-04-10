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
        <Text color="gray">
          <Spinner type="dots" /> {statusLabel}
        </Text>
        {blocks.map((block, index) => (
          <Box key={`${block.kind}-${index}`} marginTop={1}>
            {block.kind === "thinking" ? (
              <AssistantThinkingMessage text={block.text} streaming />
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
