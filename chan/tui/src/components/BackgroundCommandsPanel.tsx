import React, { type FC } from "react";
import { Box, Text } from "ink";
import type { UIBackgroundCommand } from "../hooks/useEvents.js";

interface BackgroundCommandsPanelProps {
  commands: UIBackgroundCommand[];
}

const MAX_ACTIVE_COMMANDS = 3;
const MAX_RECENT_COMMANDS = 3;

const BackgroundCommandsPanel: FC<BackgroundCommandsPanelProps> = ({
  commands,
}) => {
  const activeCommands = commands
    .filter((command) => command.status === "running")
    .slice(0, MAX_ACTIVE_COMMANDS);
  const recentCommands = commands
    .filter((command) => command.status !== "running")
    .slice(0, MAX_RECENT_COMMANDS);
  const hiddenCount = Math.max(
    0,
    commands.length - activeCommands.length - recentCommands.length,
  );

  return (
    <Box
      flexDirection="column"
      borderStyle="round"
      borderColor="yellow"
      paddingX={1}
      marginTop={1}
    >
      <Text color="yellow">
        Background Commands
        {renderCounts(activeCommands.length, recentCommands.length)}
      </Text>

      {activeCommands.length > 0 ? (
        <Box flexDirection="column" marginTop={1}>
          <Text color="yellow">Active</Text>
          {activeCommands.map((command, index) => (
            <CommandRow
              key={command.commandId}
              command={command}
              marginTop={index === 0 ? 0 : 1}
            />
          ))}
        </Box>
      ) : null}

      {recentCommands.length > 0 ? (
        <Box
          flexDirection="column"
          marginTop={activeCommands.length > 0 ? 1 : 0}
        >
          <Text color="gray">Recent</Text>
          {recentCommands.map((command, index) => (
            <CommandRow
              key={command.commandId}
              command={command}
              marginTop={index === 0 ? 0 : 1}
            />
          ))}
        </Box>
      ) : null}

      {hiddenCount > 0 ? (
        <Text
          dimColor
        >{`+${hiddenCount} more retained background commands`}</Text>
      ) : null}
    </Box>
  );
};

export default BackgroundCommandsPanel;

interface CommandRowProps {
  command: UIBackgroundCommand;
  marginTop: number;
}

const CommandRow: FC<CommandRowProps> = ({ command, marginTop }) => {
  return (
    <Box flexDirection="column" marginTop={marginTop}>
      <Box flexDirection="row" gap={1}>
        <Text color={statusColor(command.status)}>
          {statusLabel(command.status)}
        </Text>
        <Text bold>{truncate(command.command || command.commandId, 88)}</Text>
      </Box>
      {command.preview ? (
        <Text dimColor>
          {previewPrefix(command.previewKind, command.unreadBytes)}
          {truncate(command.preview, 120)}
        </Text>
      ) : null}
      <Text dimColor>{formatMeta(command)}</Text>
    </Box>
  );
};

function statusLabel(status: string): string {
  switch (status) {
    case "running":
      return "RUNNING";
    case "failed":
      return "FAILED";
    case "stopped":
      return "STOPPED";
    case "completed":
      return "DONE";
    default:
      return status.toUpperCase() || "UPDATED";
  }
}

function statusColor(status: string): "yellow" | "green" | "red" {
  switch (status) {
    case "running":
      return "yellow";
    case "failed":
      return "red";
    case "stopped":
      return "yellow";
    case "completed":
      return "green";
    default:
      return "yellow";
  }
}

function formatMeta(command: UIBackgroundCommand): string {
  const parts = [command.commandId];

  if (command.cwd) {
    parts.push(`cwd ${basename(command.cwd)}`);
  }

  if (command.status === "failed") {
    if (typeof command.exitCode === "number") {
      parts.push(`exit ${command.exitCode}`);
    }
    if (command.error) {
      parts.push(truncate(command.error, 64));
    }
  } else if (command.status === "stopped") {
    if (typeof command.exitCode === "number") {
      parts.push(`exit ${command.exitCode}`);
    }
  } else if (typeof command.exitCode === "number" && command.exitCode !== 0) {
    parts.push(`exit ${command.exitCode}`);
  }

  parts.push(
    `updated ${formatUpdatedAt(command.updatedAt ?? command.retainedAt)}`,
  );

  return parts.join(" | ");
}

function previewPrefix(
  previewKind: UIBackgroundCommand["previewKind"],
  unreadBytes: number,
): string {
  if (previewKind === "unread") {
    return unreadBytes > 0
      ? `[unread ${formatBytes(unreadBytes)}] `
      : "[unread] ";
  }
  if (previewKind === "latest") {
    return "[latest] ";
  }
  return "";
}

function renderCounts(activeCount: number, recentCount: number): string {
  const parts: string[] = [];

  if (activeCount > 0) {
    parts.push(`${activeCount} active`);
  }
  if (recentCount > 0) {
    parts.push(`${recentCount} recent`);
  }

  return parts.length > 0 ? ` (${parts.join(", ")})` : "";
}

function basename(value: string): string {
  const parts = value.split("/").filter(Boolean);
  return parts[parts.length - 1] ?? value;
}

function formatUpdatedAt(value: string): string {
  const timestamp = Date.parse(value);
  if (Number.isNaN(timestamp)) {
    return "recently";
  }

  const elapsedSeconds = Math.max(
    0,
    Math.floor((Date.now() - timestamp) / 1000),
  );
  if (elapsedSeconds < 10) {
    return "just now";
  }
  if (elapsedSeconds < 60) {
    return `${elapsedSeconds}s ago`;
  }

  const elapsedMinutes = Math.floor(elapsedSeconds / 60);
  if (elapsedMinutes < 60) {
    return `${elapsedMinutes}m ago`;
  }

  const elapsedHours = Math.floor(elapsedMinutes / 60);
  return `${elapsedHours}h ago`;
}

function truncate(value: string, limit: number): string {
  const flattened = value.replace(/\s+/g, " ").trim();
  if (flattened.length <= limit) {
    return flattened;
  }
  return `${flattened.slice(0, limit - 3)}...`;
}

function formatBytes(value: number): string {
  if (value < 1024) {
    return `${value}B`;
  }
  if (value < 1024 * 1024) {
    return `${(value / 1024).toFixed(value >= 10 * 1024 ? 0 : 1)}KB`;
  }
  return `${(value / (1024 * 1024)).toFixed(1)}MB`;
}
