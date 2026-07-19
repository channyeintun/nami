import React, { type FC } from "react";
import { Box, Text } from "silvery";
import type { UISystemMessage } from "../../hooks/useEvents.js";
import MessageRow from "../MessageRow.js";
import PreservedText from "../PreservedText.js";

interface SystemTextMessageProps {
  message: UISystemMessage;
}

const SystemTextMessage: FC<SystemTextMessageProps> = ({ message }) => {
  return (
    <MessageRow
      marker="◦"
      markerColor={toneColor(message.tone)}
      label={
        <Text color={toneColor(message.tone)} bold>
          {message.label?.trim() || "Notice"}
        </Text>
      }
      meta={renderMetadata(message.timestamp)}
    >
      <Box width="100%" minWidth={0}>
        <PreservedText text={message.text} color={toneColor(message.tone)} />
      </Box>
    </MessageRow>
  );
};

export default SystemTextMessage;

function renderMetadata(timestamp: string) {
  if (!timestamp) {
    return null;
  }

  return (
    <Text color="$muted">
      {new Date(timestamp).toLocaleTimeString("en-US", {
        hour: "2-digit",
        minute: "2-digit",
        hour12: true,
      })}
    </Text>
  );
}

function toneColor(
  tone: UISystemMessage["tone"],
): "$info" | "$success" | "$warning" | "$error" {
  switch (tone) {
    case "success":
      return "$success";
    case "warning":
      return "$warning";
    case "error":
      return "$error";
    case "info":
    default:
      return "$info";
  }
}
