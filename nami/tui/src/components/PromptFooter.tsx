import React, { type FC, useEffect, useMemo, useState } from "react";
import { Box, Text } from "silvery";

interface PromptFooterProps {
  isLoading: boolean;
  blockedReason?: string | null;
  queuedPromptCount?: number;
  showExpandedHint?: boolean;
  showArtifacts?: boolean;
  artifactsShortcutLabel?: string;
  backgroundTasksShortcutLabel?: string;
  reasoningShortcutLabel?: string;
}

const PromptFooter: FC<PromptFooterProps> = ({
  isLoading,
  blockedReason,
  queuedPromptCount = 0,
  showExpandedHint = false,
  showArtifacts = true,
  artifactsShortcutLabel = "Opt+A",
  backgroundTasksShortcutLabel = "Opt+B",
  reasoningShortcutLabel = "Opt+R",
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

  const footerLayout = terminalColumns < 120 ? "column" : "row";
  const hint = useMemo(
    () =>
      buildInputHint(
        isLoading,
        terminalColumns,
        artifactsShortcutLabel,
        reasoningShortcutLabel,
        backgroundTasksShortcutLabel,
        queuedPromptCount,
        showExpandedHint,
      ),
    [
      artifactsShortcutLabel,
      backgroundTasksShortcutLabel,
      isLoading,
      queuedPromptCount,
      reasoningShortcutLabel,
      showExpandedHint,
      terminalColumns,
    ],
  );
  const statusDetails = useMemo(
    () => buildStatusDetails(blockedReason, showArtifacts),
    [blockedReason, showArtifacts],
  );

  return (
    <Box flexDirection="column" userSelect="none">
      <Box
        paddingX={2}
        paddingTop={1}
        flexDirection={footerLayout}
        justifyContent="space-between"
        minWidth={0}
      >
        <Text
          dimColor
          wrap={footerLayout === "row" ? "truncate-end" : undefined}
        >
          {hint}
        </Text>
        {statusDetails ? (
          <Text
            dimColor
            wrap={footerLayout === "row" ? "truncate-start" : undefined}
          >
            {statusDetails}
          </Text>
        ) : null}
      </Box>
    </Box>
  );
};

export default PromptFooter;

function buildStatusDetails(
  blockedReason: string | null | undefined,
  showArtifacts: boolean,
): string | null {
  const parts: string[] = [];
  if (blockedReason) {
    parts.push(blockedReason);
  }
  if (!showArtifacts) {
    parts.push("artifacts hidden");
  }
  return parts.length > 0 ? parts.join(" | ") : null;
}

function buildInputHint(
  isLoading: boolean,
  terminalColumns: number,
  artifactsShortcutLabel: string,
  reasoningShortcutLabel: string,
  backgroundTasksShortcutLabel: string,
  queuedPromptCount: number,
  showExpandedHint: boolean,
): string {
  if (isLoading) {
    return "esc to interrupt";
  }

  if (!showExpandedHint) {
    return "? for shortcuts";
  }

  const pasteHint =
    process.platform === "darwin"
      ? terminalColumns >= 96
        ? " | Cmd+V text | Ctrl+V image"
        : " | Ctrl+V image"
      : "";
  const queueHint =
    queuedPromptCount > 0 ? " | Ctrl+Y send queued | Ctrl+K drop queued" : "";
  const tasksHint = ` | ${backgroundTasksShortcutLabel} tasks`;
  const reasoningHint = ` | ${reasoningShortcutLabel} reason`;

  if (terminalColumns < 72) {
    return `Enter send${pasteHint} | ${artifactsShortcutLabel} arts${tasksHint}${reasoningHint}${queueHint}`;
  }

  if (terminalColumns < 96) {
    return `Enter send${pasteHint} | ${artifactsShortcutLabel} artifacts${tasksHint}${reasoningHint}${queueHint} | Ctrl+G search`;
  }

  return `Enter send${pasteHint} | Ctrl+O newline | Ctrl+G transcript search | ${artifactsShortcutLabel} artifacts${tasksHint}${reasoningHint}${queueHint} | Tab mode`;
}
