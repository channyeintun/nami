import React, { type FC } from "react";
import { Spinner } from "silvery";
import { Box, Text } from "silvery";
import type { UIAssistantBlock } from "../../hooks/useEvents.js";
import { stripProviderPrefix } from "../../utils/formatModel.js";
import MessageRow from "../MessageRow.js";
import MarkdownText from "../MarkdownText.js";
import AssistantThinkingMessage from "./AssistantThinkingMessage.js";
import ShimmerText from "../ShimmerText.js";

interface StreamingAssistantMessageProps {
  blocks: UIAssistantBlock[];
  statusLabel: string;
  model?: string;
  showThinking?: boolean;
  thinkingShortcutLabel?: string;
}

const StreamingAssistantMessage: FC<StreamingAssistantMessageProps> = ({
  blocks,
  statusLabel,
  model,
  showThinking = false,
  thinkingShortcutLabel = "Opt+T",
}) => {
  const visibleBlocks = showThinking
    ? blocks
    : blocks.filter((block) => block.kind !== "thinking");
  const activeThinkingIndex =
    statusLabel === "Thinking" && showThinking
      ? findLastBlockIndex(visibleBlocks, "thinking")
      : -1;
  const showStatusRow = statusLabel === "Thinking" && activeThinkingIndex < 0;
  const statusText = formatStatusLabel(
    statusLabel,
    showThinking,
    thinkingShortcutLabel,
  );

  if (!showStatusRow && visibleBlocks.length === 0) {
    return null;
  }

  return (
    <MessageRow
      marker=" "
      markerColor="$muted"
      markerDim
      label={null}
      marginBottom={0}
      meta={
        model ? (
          <Text color="$muted">{stripProviderPrefix(model) ?? model}</Text>
        ) : null
      }
    >
      <Box flexDirection="column" minWidth={0}>
        {showStatusRow ? (
          <Box flexDirection="row" minWidth={0}>
            <Spinner type="dots" />
            <Box flexDirection="row" minWidth={0} marginLeft={1}>
              <ShimmerText text="Thinking" />
              <Text color="$muted" italic>
                {statusText.slice("Thinking".length)}
              </Text>
            </Box>
          </Box>
        ) : null}
        {visibleBlocks.map((block, index) => (
          <Box
            key={`${block.kind}-${index}`}
            marginTop={showStatusRow || index > 0 ? 1 : 0}
            minWidth={0}
          >
            {block.kind === "thinking" ? (
              <AssistantThinkingMessage
                text={block.text}
                streaming={index === activeThinkingIndex}
                toggleHint={`${thinkingShortcutLabel} to hide`}
              />
            ) : (
              <MarkdownText
                text={block.text}
                streaming={index === visibleBlocks.length - 1}
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

function formatStatusLabel(
  statusLabel: string,
  showThinking: boolean,
  thinkingShortcutLabel: string,
): string {
  if (statusLabel !== "Thinking") {
    return statusLabel;
  }

  return showThinking
    ? `Thinking (${thinkingShortcutLabel} to hide)`
    : `Thinking (${thinkingShortcutLabel} to show)`;
}
