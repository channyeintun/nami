import path from "node:path";
import React, { type FC } from "react";
import { Box, Text } from "silvery";
import {
  formatTokenCount,
  inferContextWindow,
} from "../utils/modelContext.js";
import { stripProviderPrefix } from "../utils/formatModel.js";
import type {
  UIArtifact,
  UIArtifactReview,
  UIBackgroundAgent,
  UIBackgroundCommand,
  UIRateLimits,
} from "../hooks/useEvents.js";

interface StatusBarProps {
  ready: boolean;
  mode: string;
  model: string;
  reasoningEffort?: string | null;
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
  backgroundAgents: UIBackgroundAgent[];
  backgroundCommands: UIBackgroundCommand[];
  allowedPermissionFileTypes: string[];
  rateLimits: UIRateLimits;
}

const StatusBar: FC<StatusBarProps> = ({
  ready,
  mode,
  model,
  reasoningEffort,
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
  backgroundAgents,
  backgroundCommands,
  allowedPermissionFileTypes,
  rateLimits,
}) => {
  const readinessLabel = ready ? "READY" : "BOOTING";
  const readinessColor = ready ? "$success" : "$warning";
  const workspaceLabel = path.basename(process.cwd());
  const sessionLabel = sessionTitle?.trim()
    ? sessionTitle.trim()
    : sessionId
      ? `session ${sessionId.slice(0, 8)}`
      : null;
  const modelLabel = formatModelLabel(model);
  const reasoningLabel = formatReasoningEffortLabel(reasoningEffort);
  const contextWindow =
    typeof maxContextWindow === "number" && maxContextWindow > 0
      ? maxContextWindow
      : inferContextWindow(model);
  const contextTokens = currentContextUsage ?? 0;
  const contextPercent = Math.min(
    999,
    contextWindow > 0 ? Math.round((contextTokens / contextWindow) * 100) : 0,
  );
  const contextColor =
    contextPercent >= 90
      ? "$error"
      : contextPercent >= 70
        ? "$warning"
        : "$muted";
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
  const backgroundAgentSummary = summarizeBackgroundAgents(backgroundAgents);
  const backgroundCommandSummary =
    summarizeBackgroundCommands(backgroundCommands);
  const allowedFileTypeSummary = summarizeAllowedFileTypes(
    allowedPermissionFileTypes,
  );

  return (
    <Box paddingX={1} paddingY={0} userSelect="none" flexDirection="column">
      <Box flexDirection="row" minWidth={0}>
        <Text wrap="truncate-end">
          <Text color={readinessColor}>{readinessLabel.toLowerCase()}</Text>
          <Text color="$muted"> · </Text>
          <Text bold>{workspaceLabel}</Text>
          {sessionLabel ? (
            <>
              <Text color="$muted"> · </Text>
              <Text color="$muted">{sessionLabel}</Text>
            </>
          ) : null}
          <Text color="$muted"> · </Text>
          <Text color={modeLabelColor(mode)} bold>
            {formatModeLabel(mode)}
          </Text>
          <Text color="$muted"> · </Text>
          <Text backgroundColor="$surface-bg" color="$fg" bold>
            {` ${modelLabel} `}
          </Text>
          {reasoningLabel ? <Text color="$muted"> [{reasoningLabel}]</Text> : null}
          <Text color="$muted"> · </Text>
          <Text color={contextColor}>{`ctx ~${contextPercent}%`}</Text>
          <Text color="$muted"> {formatTokenCount(contextTokens)}/</Text>
          <Text color="$muted">{formatTokenCount(contextWindow)}</Text>
          {hasRateLimits ? (
            <>
              <Text color="$muted"> · </Text>
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
                  {rateLimits.sevenDay ? <Text color="$muted"> </Text> : null}
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
          <Text color="$muted"> · </Text>
          <Text color="$muted">{`${formatTokenCount(inputTokens)}↑ ${formatTokenCount(outputTokens)}↓`}</Text>
          {hasMemoryRecallCost ? (
            <>
              <Text color="$muted"> · </Text>
              <Text color="$primary">mem</Text>
              <Text color="$muted">
                {" "}
                {`${formatTokenCount(memoryRecallInputTokens)}↑ ${formatTokenCount(memoryRecallOutputTokens)}↓ $${memoryRecallUsd.toFixed(4)}`}
              </Text>
            </>
          ) : null}
          {hasChildAgentCost ? (
            <>
              <Text color="$muted"> · </Text>
              <Text color="$primary">agent</Text>
              <Text color="$muted">
                {" "}
                {`${formatTokenCount(childAgentInputTokens)}↑ ${formatTokenCount(childAgentOutputTokens)}↓ $${childAgentUsd.toFixed(4)}`}
              </Text>
            </>
          ) : null}
          {backgroundAgentSummary ? (
            <>
              <Text color="$muted"> · </Text>
              <Text color="$primary">bg</Text>
              <Text color="$muted"> {backgroundAgentSummary}</Text>
            </>
          ) : null}
          {backgroundCommandSummary ? (
            <>
              <Text color="$muted"> · </Text>
              <Text color="$accent">cmd</Text>
              <Text color="$muted"> {backgroundCommandSummary}</Text>
            </>
          ) : null}
          {artifactSummary ? (
            <>
              <Text color="$muted"> · </Text>
              <Text color="$primary">art</Text>
              <Text color="$muted"> {artifactSummary}</Text>
            </>
          ) : null}
          <Text color="$muted"> · </Text>
          <Text color="$muted">{`$${totalCostUsd.toFixed(4)}`}</Text>
        </Text>
      </Box>
      {allowedFileTypeSummary ? (
        <Box justifyContent="flex-end" flexDirection="row" minWidth={0}>
          <Text color="$muted" wrap="truncate-start">
            <Text color="$primary">allow</Text>
            {` ${allowedFileTypeSummary}`}
          </Text>
        </Box>
      ) : null}
    </Box>
  );
};

export default StatusBar;

function formatModeLabel(mode: string): string {
  return `[${mode.toUpperCase()}]`;
}

function modeLabelColor(mode: string): "$primary" | "$accent" | undefined {
  if (mode === "plan") {
    return "$primary";
  }
  if (mode === "fast") {
    return "$accent";
  }
  return undefined;
}

function formatModelLabel(model: string): string {
  const compact = stripProviderPrefix(model) ?? "";
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
function formatReasoningEffortLabel(
  reasoningEffort: string | null | undefined,
): string | null {
  if (typeof reasoningEffort !== "string") {
    return null;
  }
  const compact = reasoningEffort.trim().toLowerCase();
  return compact || null;
}

function formatRateLimitWindow(label: string, usedPercentage: number): string {
  const rounded = Math.max(0, Math.min(999, Math.round(usedPercentage)));
  return `${label} ${rounded}%`;
}

function rateLimitColor(
  usedPercentage: number,
): "$muted" | "$warning" | "$error" {
  if (usedPercentage >= 90) {
    return "$error";
  }
  if (usedPercentage >= 70) {
    return "$warning";
  }
  return "$muted";
}

function summarizeBackgroundAgents(
  backgroundAgents: UIBackgroundAgent[],
): string | null {
  if (!Array.isArray(backgroundAgents) || backgroundAgents.length === 0) {
    return null;
  }

  const activeCount = backgroundAgents.filter(
    (agent) => agent.status === "running" || agent.status === "cancelling",
  ).length;
  const failedCount = backgroundAgents.filter(
    (agent) => agent.status === "failed",
  ).length;
  const completedCount = backgroundAgents.filter(
    (agent) => agent.status === "completed",
  ).length;

  const parts: string[] = [];
  if (activeCount > 0) {
    parts.push(`${activeCount} active`);
  }
  if (failedCount > 0) {
    parts.push(`${failedCount} failed`);
  }
  if (completedCount > 0) {
    parts.push(`${completedCount} done`);
  }

  return parts.length > 0 ? parts.join(" ") : null;
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

function summarizeAllowedFileTypes(fileTypes: string[]): string | null {
  if (!Array.isArray(fileTypes) || fileTypes.length === 0) {
    return null;
  }

  const preview = fileTypes.slice(0, 4).join(" ");
  if (fileTypes.length <= 4) {
    return preview;
  }

  return `${preview} +${fileTypes.length - 4}`;
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
