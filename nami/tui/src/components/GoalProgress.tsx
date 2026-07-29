import React, { type FC } from "react";
import { Box, Text } from "silvery";
import type { UIGoalProgress } from "../hooks/useEvents.js";

const BAR_WIDTH = 20;

// Live progress indicator for the current goal, driven by ::progress
// directives the model emits while working. Pinned above the input area,
// which already renders the streaming spinner and activity label.
const GoalProgress: FC<{ progress: UIGoalProgress }> = ({ progress }) => {
  const filled = Math.round((progress.percent / 100) * BAR_WIDTH);
  const bar = "█".repeat(filled) + "░".repeat(BAR_WIDTH - filled);

  return (
    <Box paddingX={1} minWidth={0} flexShrink={1} userSelect="none">
      <Text wrap="truncate-end">
        <Text bold>{progress.goal || "Working"}</Text>
        <Text> </Text>
        <Text color="$primary">{bar}</Text>
        <Text color="$muted">{` ${progress.percent}%`}</Text>
        {progress.label ? (
          <Text color="$muted">{` · ${progress.label}`}</Text>
        ) : null}
      </Text>
    </Box>
  );
};

export default GoalProgress;
