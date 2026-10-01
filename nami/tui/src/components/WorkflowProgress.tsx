import React, { type FC } from "react";
import { Box, Text } from "silvery";
import type { UIWorkflowRun } from "../hooks/useEvents.js";
import { truncateEnd } from "../utils/text.js";
import {
  singleLine,
  summarizeRunningAgents,
  workflowDoneCount,
} from "../utils/workflowDisplay.js";

const MAX_RUNNING_LABELS = 3;
const MAX_LOG_CHARS = 160;
// Each running workflow takes a line above the input. Past this many, the
// rest are counted on one line so the input keeps its room.
const MAX_LISTED_RUNS = 3;

// The workflows running in the background, pinned above the input next to
// the goal indicator. A run goes on between turns, so unlike GoalProgress
// this shows whether or not a turn is active. /workflows has the detail.
const WorkflowProgress: FC<{ runs: UIWorkflowRun[] }> = ({ runs }) => {
  const running = runs.filter((run) => run.status === "running");
  if (running.length === 0) {
    return null;
  }

  const listed = running.slice(0, MAX_LISTED_RUNS);
  const hidden = running.length - listed.length;

  return (
    <Box
      flexDirection="column"
      paddingX={1}
      minWidth={0}
      flexShrink={0}
      userSelect="none"
    >
      {listed.map((run) => (
        <WorkflowRunLine key={run.runId} run={run} />
      ))}
      {hidden > 0 ? (
        <Text color="$muted" wrap="truncate-end">
          {`+${hidden} more workflow${hidden === 1 ? "" : "s"} running · /workflows`}
        </Text>
      ) : null}
    </Box>
  );
};

export default WorkflowProgress;

// Where the run stands, most telling part first: the line is cut at the
// terminal's edge, so the last log line, the least essential, goes at the end.
const WorkflowRunLine: FC<{ run: UIWorkflowRun }> = ({ run }) => {
  const active = summarizeRunningAgents(run, MAX_RUNNING_LABELS);
  const lastLog = truncateEnd(singleLine(run.logs.at(-1) ?? ""), MAX_LOG_CHARS);

  return (
    <Text wrap="truncate-end">
      <Text color="$primary">● </Text>
      <Text bold>{run.name || run.runId}</Text>
      {run.currentPhase ? (
        <Text color="$primary">{` · ${run.currentPhase}`}</Text>
      ) : null}
      <Text color="$muted">
        {` · ${workflowDoneCount(run)}/${run.agentCount} agents`}
      </Text>
      {active ? <Text color="$muted">{` · ${active}`}</Text> : null}
      {run.failed > 0 ? (
        <Text color="$error">{` · ${run.failed} failed`}</Text>
      ) : null}
      {lastLog ? <Text color="$muted">{` · ${lastLog}`}</Text> : null}
    </Text>
  );
};
