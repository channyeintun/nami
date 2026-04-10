import React, { type FC, useMemo } from "react";
import { Box, Text } from "ink";
import Spinner from "ink-spinner";

interface AssistantThinkingMessageProps {
  text: string;
  streaming?: boolean;
}

function truncateThinking(text: string, maxLines: number): string {
  const lines = text.split("\n").filter((line) => line.trim().length > 0);
  return lines.slice(-maxLines).join("\n");
}

const AssistantThinkingMessage: FC<AssistantThinkingMessageProps> = ({
  text,
  streaming = false,
}) => {
  const content = useMemo(
    () => (streaming ? truncateThinking(text, 4) : text.trimEnd()),
    [streaming, text],
  );
  if (!content) {
    return null;
  }

  return (
    <Box flexDirection="column">
      <Text color="gray" italic>
        {streaming ? <Spinner type="dots" /> : null}
        {streaming ? " Thinking" : "Thinking"}
      </Text>
      <Text color="gray">{content}</Text>
    </Box>
  );
};

export default AssistantThinkingMessage;
