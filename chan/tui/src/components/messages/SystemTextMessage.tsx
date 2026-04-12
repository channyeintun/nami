import React, { type FC } from "react";
import { Text } from "ink";
import type { UISystemMessage } from "../../hooks/useEvents.js";
import MessageRow from "../MessageRow.js";

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
          Background Agent
        </Text>
      }
      meta={renderMetadata(message.timestamp)}
    >
      <Text color={toneColor(message.tone)}>{message.text}</Text>
    </MessageRow>
  );
};

export default SystemTextMessage;

function renderMetadata(timestamp: string) {
  if (!timestamp) {
    return null;
  }

  return (
    <Text dimColor>
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
): "cyan" | "green" | "yellow" | "red" {
  switch (tone) {
    case "success":
      return "green";
    case "warning":
      return "yellow";
    case "error":
      return "red";
    case "info":
    default:
      return "cyan";
  }
}
