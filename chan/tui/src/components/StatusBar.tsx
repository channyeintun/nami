import path from "node:path";
import React, { type FC } from "react";
import { Box, Text } from "ink";
import {
  formatTokenCount,
  getEffectiveContextWindow,
} from "../utils/modelContext.js";
import type {
  UIArtifact,
  UIArtifactReview,
  UIBackgroundCommand,
  UIRateLimits,
} from "../hooks/useEvents.js";

interface StatusBarProps {
  ready: boolean;
  mode: string;
  model: string;
  sessionId?: string | null;
  sessionTitle?: string | null;
  maxContextWindow?: number | null;
  maxOutputTokens?: number | null;
  currentContextUsage?: number | null;
  totalCostUsd: number;
  inputTokens: number;
  outputTokens: number;
  memoryRecallUsd: number;
  memoryRecallInputTokens: number;
  memoryRecallOutputTokens: number;
  childAgentUsd: number;
  childAgentInputTokens: number;
  childAgentOutputTokens: number;
  artifacts: UIArtifact[];
  focusedArtifactId?: string | null;
  pendingArtifactReview?: UIArtifactReview | null;
  backgroundCommands: UIBackgroundCommand[];
  rateLimits: UIRateLimits;
  queuedPromptCount?: number;
}

const StatusBar: FC<StatusBarProps> = ({
  ready,
  mode,
  model,
  sessionId,
  sessionTitle,
  maxContextWindow,
  maxOutputTokens,
  currentContextUsage,
  totalCostUsd,
  inputTokens,
  outputTokens,
  memoryRecallUsd,
  memoryRecallInputTokens,
  memoryRecallOutputTokens,
  childAgentUsd,
  childAgentInputTokens,
  childAgentOutputTokens,
  artifacts,
  focusedArtifactId,
  pendingArtifactReview,
  backgroundCommands,
  rateLimits,
  queuedPromptCount = 0,
}) => {
  const modeColor = mode === "plan" ? "blue" : "green";
  const readinessLabel = ready ? "READY" : "BOOTING";
  const readinessColor = ready ? "green" : "yellow";
  const workspaceLabel = path.basename(process.cwd());
  const sessionLabel = sessionTitle?.trim()
    ? sessionTitle.trim()
    : sessionId
      ? `session ${sessionId.slice(0, 8)}`
      : null;
  const modelLabel = formatModelLabel(model);
  const contextWindow = getEffectiveContextWindow(
    model,
    maxContextWindow,
    maxOutputTokens,
  );
  const contextTokens = currentContextUsage ?? inputTokens + outputTokens;
  const contextPercent = Math.min(
    999,
    contextWindow > 0 ? Math.round((contextTokens / contextWindow) * 100) : 0,
  );
  const contextColor =
    contextPercent >= 90 ? "red" : contextPercent >= 70 ? "yellow" : "gray";
  const hasRateLimits = !!rateLimits.fiveHour || !!rateLimits.sevenDay;
  const hasMemoryRecallCost =
    memoryRecallUsd > 0 ||
    memoryRecallInputTokens > 0 ||
    memoryRecallOutputTokens > 0;
  const hasChildAgentCost =
    childAgentUsd > 0 ||
    childAgentInputTokens > 0 ||
    childAgentOutputTokens > 0;
  const artifactSummary = summarizeArtifacts(
    artifacts,
    focusedArtifactId,
    pendingArtifactReview,
  );
  const backgroundCommandSummary =
    summarizeBackgroundCommands(backgroundCommands);

  return (
    <Box paddingX={1} paddingY={0}>
      <Text wrap="truncate-end">
        <Text color={readinessColor}>{readinessLabel.toLowerCase()}</Text>
        <Text color="gray"> · </Text>
        <Text bold>{workspaceLabel}</Text>
        {sessionLabel ? (
          <>
            <Text color="gray"> · </Text>
            <Text color="gray">{sessionLabel}</Text>
          </>
        ) : null}
        <Text color="gray"> · </Text>
        <Text color={modeColor}>{mode.toUpperCase()}</Text>
        <Text color="gray"> · </Text>
        <Text color="yellow">{modelLabel}</Text>
        <Text color="gray"> · </Text>
        <Text color={contextColor}>{`ctx ~${contextPercent}%`}</Text>
        <Text color="gray"> {formatTokenCount(contextTokens)}/</Text>
        <Text color="gray">{formatTokenCount(contextWindow)}</Text>
        {hasRateLimits ? (
          <>
            <Text color="gray"> · </Text>
            {rateLimits.fiveHour ? (
              <>
                <Text
                  color={rateLimitColor(rateLimits.fiveHour.usedPercentage)}
                >
                  {formatRateLimitWindow(
                    "5h",
                    rateLimits.fiveHour.usedPercentage,
                  )}
                </Text>
                {rateLimits.sevenDay ? <Text color="gray"> </Text> : null}
              </>
            ) : null}
            {rateLimits.sevenDay ? (
              <Text color={rateLimitColor(rateLimits.sevenDay.usedPercentage)}>
                {formatRateLimitWindow(
                  "7d",
                  rateLimits.sevenDay.usedPercentage,
                )}
              </Text>
            ) : null}
          </>
        ) : null}
        <Text color="gray"> · </Text>
        <Text color="gray">{`${formatTokenCount(inputTokens)}↑ ${formatTokenCount(outputTokens)}↓`}</Text>
        {hasMemoryRecallCost ? (
          <>
            <Text color="gray"> · </Text>
            <Text color="cyan">mem</Text>
            <Text color="gray">
              {" "}
              {`${formatTokenCount(memoryRecallInputTokens)}↑ ${formatTokenCount(memoryRecallOutputTokens)}↓ $${memoryRecallUsd.toFixed(4)}`}
            </Text>
          </>
        ) : null}
        {hasChildAgentCost ? (
          <>
            <Text color="gray"> · </Text>
            <Text color="cyan">agent</Text>
            <Text color="gray">
              {" "}
              {`${formatTokenCount(childAgentInputTokens)}↑ ${formatTokenCount(childAgentOutputTokens)}↓ $${childAgentUsd.toFixed(4)}`}
            </Text>
          </>
        ) : null}
        {backgroundCommandSummary ? (
          <>
            <Text color="gray"> · </Text>
            <Text color="yellow">cmd</Text>
            <Text color="gray"> {backgroundCommandSummary}</Text>
          </>
        ) : null}
        {queuedPromptCount > 0 ? (
          <>
            <Text color="gray"> · </Text>
            <Text color="yellow">queue</Text>
            <Text color="gray">
              {queuedPromptCount === 1
                ? " 1 prompt"
                : ` ${queuedPromptCount} prompts`}
            </Text>
          </>
        ) : null}
        {artifactSummary ? (
          <>
            <Text color="gray"> · </Text>
            <Text color="cyan">art</Text>
            <Text color="gray"> {artifactSummary}</Text>
          </>
        ) : null}
        <Text color="gray"> · </Text>
        <Text color="gray">{`$${totalCostUsd.toFixed(4)}`}</Text>
      </Text>
    </Box>
  );
};

export default StatusBar;

function formatModelLabel(model: string): string {
  const compact = model.trim();
  if (!compact) {
    return "Unknown model";
  }

  if (compact.startsWith("claude-")) {
    return compact
      .replace(/^claude-/, "Claude ")
      .replace(/-(\d{8}|latest)$/i, "")
      .replace(/-/g, " ");
  }

  if (compact.startsWith("gemini-")) {
    return compact.replace(/^gemini-/, "Gemini ").replace(/-/g, " ");
  }

  if (compact.startsWith("gpt-")) {
    return compact.toUpperCase();
  }

  return compact.replace(/-/g, " ");
}

function formatRateLimitWindow(label: string, usedPercentage: number): string {
  const rounded = Math.max(0, Math.min(999, Math.round(usedPercentage)));
  return `${label} ${rounded}%`;
}

function rateLimitColor(usedPercentage: number): "gray" | "yellow" | "red" {
  if (usedPercentage >= 90) {
    return "red";
  }
  if (usedPercentage >= 70) {
    return "yellow";
  }
  return "gray";
}

function summarizeBackgroundCommands(
  backgroundCommands: UIBackgroundCommand[],
): string | null {
  if (!Array.isArray(backgroundCommands) || backgroundCommands.length === 0) {
    return null;
  }

  const activeCount = backgroundCommands.filter(
    (command) => command.status === "running",
  ).length;
  const unreadCount = backgroundCommands.filter(
    (command) => command.previewKind === "unread" && command.unreadBytes > 0,
  ).length;
  const failedCount = backgroundCommands.filter(
    (command) => command.status === "failed",
  ).length;
  const stoppedCount = backgroundCommands.filter(
    (command) => command.status === "stopped",
  ).length;

  const parts: string[] = [];
  if (activeCount > 0) {
    parts.push(`${activeCount} run`);
  }
  if (unreadCount > 0) {
    parts.push(`${unreadCount} unread`);
  }
  if (failedCount > 0) {
    parts.push(`${failedCount} failed`);
  }
  if (stoppedCount > 0) {
    parts.push(`${stoppedCount} stopped`);
  }

  return parts.length > 0 ? parts.join(" ") : null;
}

function summarizeArtifacts(
  artifacts: UIArtifact[],
  focusedArtifactId?: string | null,
  pendingArtifactReview?: UIArtifactReview | null,
): string | null {
  if (
    (!Array.isArray(artifacts) || artifacts.length === 0) &&
    !pendingArtifactReview
  ) {
    return null;
  }

  const parts: string[] = [];
  if (Array.isArray(artifacts) && artifacts.length > 0) {
    parts.push(`${artifacts.length} total`);
  }

  if (focusedArtifactId) {
    const focusedArtifact = artifacts.find(
      (artifact) => artifact.id === focusedArtifactId,
    );
    if (focusedArtifact) {
      parts.push(`focus ${artifactSummaryLabel(focusedArtifact)}`);
    }
  }

  if (pendingArtifactReview) {
    parts.push(
      `review ${pendingArtifactReview.title.trim() || pendingArtifactReview.kind}`,
    );
  }

  return parts.length > 0 ? parts.join(" · ") : null;
}

function artifactSummaryLabel(artifact: UIArtifact): string {
  const label = artifact.title.trim() || artifact.kind;
  const compact = label.replace(/\s+/g, " ").trim();
  if (compact.length <= 28) {
    return compact;
  }
  return `${compact.slice(0, 25)}...`;
}
