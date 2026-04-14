import React, { type FC, useMemo } from "react";
import { Spinner } from "silvery";
import { Box, Text } from "silvery";

interface AssistantThinkingMessageProps {
  text: string;
  streaming?: boolean;
  toggleHint?: string;
}

function truncateThinking(
  text: string,
  maxLines: number,
  maxChars: number,
): string {
  const trimmed = text.trimEnd();
  if (!trimmed) {
    return "";
  }

  const tail =
    trimmed.length > maxChars ? trimmed.slice(trimmed.length - maxChars) : trimmed;
  const lines = tail.split("\n");
  return lines.slice(-maxLines).join("\n").trimStart();
}

const AssistantThinkingMessage: FC<AssistantThinkingMessageProps> = ({
  text,
  streaming = false,
  toggleHint,
}) => {
  const content = useMemo(
    () => (streaming ? truncateThinking(text, 6, 800) : text.trimEnd()),
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
        {toggleHint ? ` (${toggleHint})` : ""}
      </Text>
      <Text color="gray">{content}</Text>
    </Box>
  );
};

export default AssistantThinkingMessage;
