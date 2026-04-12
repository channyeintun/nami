import React, { type FC, useEffect, useMemo, useState } from "react";
import { Box, Text } from "ink";
import {
  calculateTokenWarningState,
  formatTokenCount,
} from "../utils/modelContext.js";
import type { UIMemoryRecallEntry } from "../hooks/useEvents.js";

interface PromptFooterProps {
  mode: string;
  model: string;
  maxContextWindow?: number | null;
  maxOutputTokens?: number | null;
  currentContextUsage?: number | null;
  isLoading: boolean;
  disabled?: boolean;
  promptValue: string;
  totalCostUsd: number;
  inputTokens: number;
  outputTokens: number;
  memoryRecall: {
    source: string | null;
    entries: UIMemoryRecallEntry[];
  };
  turnTiming: {
    firstTokenMs: number | null;
    firstToolResultMs: number | null;
    firstArtifactFocusMs: number | null;
    totalMs: number | null;
  };
  cursorOffset?: number;
  blockedReason?: string | null;
  queuedPromptCount?: number;
}

const INPUT_HINT =
  "Enter send | Ctrl+O newline | Ctrl+G transcript search | PgUp/PgDn transcript | Home/End jump | Tab mode | Esc cancel";
const DISABLED_HINT =
  "Engine busy | PgUp/PgDn transcript | Home/End jump | Esc cancel";

const PromptFooter: FC<PromptFooterProps> = ({
  mode,
  model,
  maxContextWindow,
  maxOutputTokens,
  currentContextUsage,
  isLoading,
  disabled,
  promptValue,
  totalCostUsd,
  inputTokens,
  outputTokens,
  memoryRecall,
  turnTiming,
  cursorOffset = 0,
  blockedReason,
  queuedPromptCount = 0,
}) => {
  const [terminalColumns, setTerminalColumns] = useState(
    process.stdout.columns ?? 80,
  );

  useEffect(() => {
    const handleResize = () => {
      setTerminalColumns(process.stdout.columns ?? 80);
    };

    handleResize();
    process.stdout.on("resize", handleResize);

    return () => {
      process.stdout.off("resize", handleResize);
    };
  }, []);

  const footerLayout = terminalColumns < 96 ? "column" : "row";
  const promptTextColumns = useMemo(
    () => getPromptTextColumns(terminalColumns),
    [terminalColumns],
  );
  const wrappedLineCount = useMemo(
    () => getWrappedLineSegments(promptValue, promptTextColumns).length,
    [promptTextColumns, promptValue],
  );
  const tokenUsage = currentContextUsage ?? inputTokens + outputTokens;
  const tokenWarning = useMemo(
    () =>
      calculateTokenWarningState(
        tokenUsage,
        model,
        maxContextWindow,
        maxOutputTokens,
      ),
    [maxContextWindow, maxOutputTokens, model, tokenUsage],
  );
  const showWrappedIndicator = promptValue.length > 0 && wrappedLineCount > 1;
  const promptMetrics = useMemo(
    () => buildPromptMetrics(promptValue, cursorOffset),
    [cursorOffset, promptValue],
  );
  const activityLabel = isLoading ? "running" : disabled ? "blocked" : "ready";
  const activityDetails = useMemo(
    () => buildActivityDetails(blockedReason, queuedPromptCount),
    [blockedReason, queuedPromptCount],
  );
  const hint = disabled ? DISABLED_HINT : INPUT_HINT;
  const costWarningText = useMemo(
    () => buildCostWarningText(totalCostUsd),
    [totalCostUsd],
  );
  const memoryRecallText = useMemo(
    () => buildMemoryRecallText(memoryRecall),
    [memoryRecall],
  );
  const latencyText = useMemo(() => buildLatencyText(turnTiming), [turnTiming]);
  const warningText = tokenWarning.isWarning
    ? `Compact soon (~${tokenWarning.percentLeft}% until threshold) · ${formatTokenCount(tokenUsage)}/${formatTokenCount(tokenWarning.effectiveContextWindow)} used · Run /compact before the next long turn`
    : null;

  return (
    <Box flexDirection="column">
      {costWarningText ? (
        <Box paddingX={2} paddingTop={1}>
          <Text color="yellow">{costWarningText}</Text>
        </Box>
      ) : null}
      {warningText ? (
        <Box paddingX={2} paddingTop={costWarningText ? 0 : 1}>
          <Text color={tokenWarning.isError ? "red" : "yellow"}>
            {warningText}
          </Text>
        </Box>
      ) : null}
      {memoryRecallText ? (
        <Box paddingX={2} paddingTop={warningText || costWarningText ? 0 : 1}>
          <Text dimColor>{memoryRecallText}</Text>
        </Box>
      ) : null}
      <Box
        paddingX={2}
        paddingTop={warningText || costWarningText || memoryRecallText ? 0 : 1}
        flexDirection={footerLayout}
        justifyContent="space-between"
      >
        <Text dimColor>
          <Text color={getModeColor(mode)} bold>
            {formatModeLabel(mode)}
          </Text>
          {"  "}
          <Text>{activityLabel}</Text>
          {activityDetails ? `  ${activityDetails}` : ""}
          {latencyText ? `  ${latencyText}` : ""}
          {showWrappedIndicator ? `  wrapped:${wrappedLineCount}` : ""}
          {promptMetrics ? `  ${promptMetrics}` : ""}
        </Text>
        <Text dimColor>{hint}</Text>
      </Box>
    </Box>
  );
};

export default PromptFooter;

function formatModeLabel(mode: string): string {
  return mode === "plan" ? "PLAN" : mode.toUpperCase();
}

function getModeColor(mode: string): "blue" | "green" | "yellow" {
  if (mode === "plan") {
    return "blue";
  }

  if (mode === "fast") {
    return "green";
  }

  return "yellow";
}

function getPromptTextColumns(terminalColumns: number): number {
  return Math.max(8, terminalColumns - 7);
}

function getWrappedLineSegments(value: string, columns: number): string[] {
  const wrapWidth = Math.max(1, columns - 1);
  const logicalLines = value.split("\n");
  const segments: string[] = [];

  for (const line of logicalLines) {
    if (line.length === 0) {
      segments.push("");
      continue;
    }

    for (let offset = 0; offset < line.length; offset += wrapWidth) {
      segments.push(line.slice(offset, offset + wrapWidth));
    }
  }

  return segments;
}

function buildCostWarningText(totalCostUsd: number): string | null {
  const threshold = getCostWarningThresholdUsd();
  if (threshold <= 0 || totalCostUsd < threshold) {
    return null;
  }

  return `Session cost passed $${threshold.toFixed(2)} · current spend $${totalCostUsd.toFixed(4)} · Review API usage before the next long turn`;
}

function buildLatencyText(
  turnTiming: PromptFooterProps["turnTiming"],
): string | null {
  const parts: string[] = [];
  if (turnTiming.firstTokenMs !== null) {
    parts.push(`token:${formatLatencyMs(turnTiming.firstTokenMs)}`);
  }
  if (turnTiming.firstToolResultMs !== null) {
    parts.push(`tool:${formatLatencyMs(turnTiming.firstToolResultMs)}`);
  }
  if (turnTiming.firstArtifactFocusMs !== null) {
    parts.push(`artifact:${formatLatencyMs(turnTiming.firstArtifactFocusMs)}`);
  }
  if (turnTiming.totalMs !== null) {
    parts.push(`total:${formatLatencyMs(turnTiming.totalMs)}`);
  }
  return parts.length > 0 ? parts.join("  ") : null;
}

function buildPromptMetrics(
  promptValue: string,
  cursorOffset: number,
): string | null {
  if (promptValue.length === 0) {
    return null;
  }

  const clampedCursor = Math.max(0, Math.min(cursorOffset, promptValue.length));
  const beforeCursor = promptValue.slice(0, clampedCursor);
  const lines = beforeCursor.split("\n");
  const line = lines.length;
  const column = (lines[lines.length - 1] ?? "").length + 1;
  const lineCount = promptValue.split("\n").length;

  return `${promptValue.length}ch ${lineCount}ln L${line}:C${column}`;
}

function buildActivityDetails(
  blockedReason: string | null | undefined,
  queuedPromptCount: number,
): string | null {
  const parts: string[] = [];
  if (blockedReason) {
    parts.push(blockedReason);
  }
  if (queuedPromptCount > 0) {
    parts.push(
      queuedPromptCount === 1 ? "1 queued" : `${queuedPromptCount} queued`,
    );
  }
  return parts.length > 0 ? parts.join("  ") : null;
}

function buildMemoryRecallText(
  memoryRecall: PromptFooterProps["memoryRecall"],
): string | null {
  if (
    !Array.isArray(memoryRecall.entries) ||
    memoryRecall.entries.length === 0
  ) {
    return null;
  }

  const labels = memoryRecall.entries
    .map((entry) => entry.title.trim())
    .filter(
      (title, index, items) =>
        title.length > 0 && items.indexOf(title) === index,
    );
  if (labels.length === 0) {
    return null;
  }

  const visible = labels.slice(0, 2);
  const parts = [`Memory recall: ${visible.join(", ")}`];
  if (labels.length > visible.length) {
    parts.push(`+${labels.length - visible.length} more`);
  }
  if (memoryRecall.source) {
    parts.push(`via ${memoryRecall.source}`);
  }
  return parts.join(" · ");
}

function formatLatencyMs(value: number): string {
  if (!Number.isFinite(value) || value < 0) {
    return "0ms";
  }
  if (value < 1000) {
    return `${Math.round(value)}ms`;
  }

  const totalSeconds = Math.round(value / 1000);
  if (totalSeconds < 60) {
    return `${totalSeconds}s`;
  }

  const totalMinutes = Math.floor(totalSeconds / 60);
  const seconds = totalSeconds % 60;
  if (totalMinutes < 60) {
    return seconds === 0
      ? `${totalMinutes}m`
      : `${totalMinutes}m ${seconds}s`;
  }

  const hours = Math.floor(totalMinutes / 60);
  const minutes = totalMinutes % 60;
  return minutes === 0 ? `${hours}h` : `${hours}h ${minutes}m`;
}

function getCostWarningThresholdUsd(): number {
  if (isEnvTruthy(process.env.CHAN_DISABLE_COST_WARNINGS)) {
    return 0;
  }

  const raw = process.env.CHAN_COST_WARNING_THRESHOLD_USD?.trim();
  if (!raw) {
    return 5;
  }

  const parsed = Number(raw);
  return Number.isFinite(parsed) ? parsed : 5;
}

function isEnvTruthy(value: string | undefined): boolean {
  if (!value) {
    return false;
  }

  switch (value.trim().toLowerCase()) {
    case "1":
    case "true":
    case "yes":
    case "on":
      return true;
    default:
      return false;
  }
}
