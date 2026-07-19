import React, { type FC } from "react";
import { Box, Spinner, Text } from "silvery";
import type { UIGoalProgress } from "../hooks/useEvents.js";

const BAR_WIDTH = 20;

// Live progress indicator for the current goal, driven by ::progress
// directives the model emits while working. Pinned above the input area.
const GoalProgress: FC<{ progress: UIGoalProgress }> = ({ progress }) => {
  const percent = Math.max(0, Math.min(100, progress.percent));
  const filled = Math.round((percent / 100) * BAR_WIDTH);
  const bar = "█".repeat(filled) + "░".repeat(BAR_WIDTH - filled);

  return (
    <Box flexDirection="row" paddingX={1} minWidth={0} userSelect="none">
      <Spinner type="dots" />
      <Box marginLeft={1} minWidth={0} flexShrink={1}>
        <Text wrap="truncate-end">
          <Text bold>{progress.goal || "Working"}</Text>
          <Text> </Text>
          <Text color="$primary">{bar}</Text>
          <Text color="$muted">{` ${percent}%`}</Text>
          {progress.label ? (
            <Text color="$muted">{` · ${progress.label}`}</Text>
          ) : null}
        </Text>
      </Box>
    </Box>
  );
};

export default GoalProgress;
