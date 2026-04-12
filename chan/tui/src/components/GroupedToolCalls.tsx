import React, { type FC } from "react";
import { Box, Text } from "ink";
import type { UIToolCall } from "../hooks/useEvents.js";
import { describeTool } from "./ToolProgress.js";
import MessageRow from "./MessageRow.js";

export interface ToolCallGroup {
  id: string;
  kind: "read_search";
  toolCalls: UIToolCall[];
}

interface GroupedToolCallsProps {
  group: ToolCallGroup;
}

const RESPONSE_PREFIX = "  ⎿  ";

const GroupedToolCalls: FC<GroupedToolCallsProps> = ({ group }) => {
  const headerColor = groupHeaderColor(group.toolCalls);
  const isDim = group.toolCalls.some(
    (toolCall) =>
      toolCall.status === "running" || toolCall.status === "waiting_permission",
  );
  const summary = summarizeReadSearchCounts(group.toolCalls);
  const response = renderReadSearchResponse(group.toolCalls);

  return (
    <MessageRow markerColor={headerColor} markerDim={isDim}>
      <Text color={headerColor} dimColor={isDim}>
        <Text bold>Explored Workspace</Text>
        {summary ? ` (${summary})` : ""}
      </Text>
      <Box flexDirection="column">
        {response.map((line, index) => (
          <Box key={`${group.id}-${index}`} flexDirection="row">
            <Text dimColor>{RESPONSE_PREFIX}</Text>
            <Text color={line.color} dimColor={line.dim}>
              {line.text}
            </Text>
          </Box>
        ))}
      </Box>
    </MessageRow>
  );
};

export default GroupedToolCalls;

function groupHeaderColor(
  toolCalls: UIToolCall[],
): "green" | "red" | undefined {
  if (toolCalls.some((toolCall) => toolCall.status === "error")) {
    return "red";
  }
  if (toolCalls.every((toolCall) => toolCall.status === "completed")) {
    return "green";
  }
  return undefined;
}

function summarizeReadSearchCounts(toolCalls: UIToolCall[]): string {
  const counts = new Map<string, number>();

  for (const toolCall of toolCalls) {
    const label = readSearchLabel(toolCall.name);
    if (!label) {
      continue;
    }
    counts.set(label, (counts.get(label) ?? 0) + 1);
  }

  return Array.from(counts.entries())
    .map(([label, count]) => `${count} ${count === 1 ? label : `${label}s`}`)
    .join(", ");
}

function renderReadSearchResponse(toolCalls: UIToolCall[]): Array<{
  text: string;
  color?: "red";
  dim?: boolean;
}> {
  const waitingCount = toolCalls.filter(
    (toolCall) => toolCall.status === "waiting_permission",
  ).length;
  const runningCount = toolCalls.filter(
    (toolCall) => toolCall.status === "running",
  ).length;
  const errorCalls = toolCalls.filter(
    (toolCall) => toolCall.status === "error",
  );
  const latest = toolCalls[toolCalls.length - 1];
  const descriptor = latest ? describeTool(latest) : null;
  const lines: Array<{ text: string; color?: "red"; dim?: boolean }> = [];

  if (waitingCount > 0) {
    lines.push({
      text: `Waiting for permission on ${waitingCount} related ${waitingCount === 1 ? "call" : "calls"}.`,
      dim: true,
    });
  } else if (runningCount > 0) {
    lines.push({
      text: `Running ${runningCount} related ${runningCount === 1 ? "call" : "calls"}.`,
      dim: true,
    });
  } else {
    lines.push({
      text: `Completed ${toolCalls.length} related calls.`,
      dim: true,
    });
  }

  if (descriptor) {
    lines.push({
      text: `Latest: ${descriptor.title}${descriptor.summary ? ` (${descriptor.summary})` : ""}`,
      dim: true,
    });
  }

  if (errorCalls.length > 0) {
    const firstError = errorCalls[0];
    lines.push({
      text: `${errorCalls.length} ${errorCalls.length === 1 ? "call failed" : "calls failed"}: ${truncateInline(firstError.error ?? "Tool failed")}`,
      color: "red",
    });
  }

  return lines;
}

function readSearchLabel(name: string): string | null {
  switch (name) {
    case "file_read":
      return "read";
    case "grep":
    case "web_search":
      return "search";
    case "glob":
      return "list";
    case "web_fetch":
      return "fetch";
    case "git":
      return "git check";
    default:
      return null;
  }
}

function truncateInline(value: string): string {
  const trimmed = value.trim();
  if (trimmed.length <= 96) {
    return trimmed;
  }
  return `${trimmed.slice(0, 93)}...`;
}
