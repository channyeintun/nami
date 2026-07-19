import React, { type FC } from "react";
import { Box, Text } from "silvery";
import type {
  UIAssistantBlock,
  UIAssistantMessage,
} from "../../hooks/useEvents.js";
import { stripProviderPrefix } from "../../utils/formatModel.js";
import MessageRow from "../MessageRow.js";
import MarkdownText from "../MarkdownText.js";
import AssistantThinkingMessage from "./AssistantThinkingMessage.js";

interface AssistantTextMessageProps {
  message: UIAssistantMessage;
  continuation?: boolean;
  showThinking?: boolean;
  thinkingShortcutLabel?: string;
}

const AssistantTextMessage: FC<AssistantTextMessageProps> = ({
  message,
  continuation = false,
  showThinking = false,
  thinkingShortcutLabel = "Opt+T",
}) => {
  const contentBlocks = message.blocks.filter(
    (block) => block.text.trim().length > 0,
  );
  const visibleBlocks = showThinking
    ? contentBlocks
    : contentBlocks.filter((block) => block.kind === "text");
  const hasHiddenThinking =
    !showThinking &&
    contentBlocks.some((block) => block.kind === "thinking");

  if (visibleBlocks.length === 0 && !hasHiddenThinking) {
    return null;
  }

  return (
    <MessageRow
      marker=" "
      markerColor="$muted"
      label={null}
      meta={continuation ? null : renderMetadata(message)}
      marginBottom={continuation ? 0 : 1}
    >
      <Box flexDirection="column" minWidth={0}>
        {hasHiddenThinking && visibleBlocks.length === 0 ? (
          <Text color="$muted" italic>
            {`Thinking (${thinkingShortcutLabel} to show)`}
          </Text>
        ) : null}
        {visibleBlocks.map((block, index) =>
          renderAssistantBlock(
            block,
            index,
            visibleBlocks.length,
            thinkingShortcutLabel,
          ),
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
  thinkingShortcutLabel: string,
) {
  return (
    <Box
      key={`${block.kind}-${index}`}
      marginTop={index === 0 ? 0 : 1}
      minWidth={0}
    >
      {block.kind === "thinking" ? (
        <AssistantThinkingMessage
          text={block.text}
          toggleHint={`${thinkingShortcutLabel} to hide`}
        />
      ) : (
        <MarkdownText text={block.text} streaming={false} />
      )}
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
    parts.push(stripProviderPrefix(message.model) ?? message.model);
  }

  if (parts.length === 0) {
    return null;
  }

  return <Text color="$muted">{parts.join("  ")}</Text>;
}
