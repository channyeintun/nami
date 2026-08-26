import React, { type FC } from "react";
import { Box, Text } from "silvery";
import type { UIWorkflowNode, UIWorkflowRun } from "../hooks/useEvents.js";

const MAX_ACTIVE_LABELS = 3;

// Colors carry the run's shape at a glance, so a long graph is readable
// without counting: what is moving, what landed, and what will never run.
function statusColor(status: string): string {
  switch (status) {
    case "running":
      return "$primary";
    case "succeeded":
    case "cached":
      return "$success";
    case "failed":
      return "$error";
    case "skipped":
      return "$muted";
    default:
      return "$muted";
  }
}

function nodeLabel(node: UIWorkflowNode): string {
  return node.label || node.id;
}

function summarize(nodes: UIWorkflowNode[]): string {
  const running = nodes.filter((node) => node.status === "running");
  if (running.length === 0) {
    return "";
  }
  const shown = running.slice(0, MAX_ACTIVE_LABELS).map(nodeLabel).join(", ");
  const hidden = running.length - MAX_ACTIVE_LABELS;
  return hidden > 0 ? `${shown} +${hidden} more` : shown;
}

// Live view of the workflow graph running in this turn. Pinned above the input
// area next to the goal indicator. A graph can hold dozens of nodes, so this
// stays a single line: counts, a per-node status strip, and the names of what
// is running right now.
const WorkflowProgress: FC<{ run: UIWorkflowRun }> = ({ run }) => {
  const active = summarize(run.nodes);
  const failed = run.nodes.filter((node) => node.status === "failed").length;
  const skipped = run.nodes.filter((node) => node.status === "skipped").length;

  return (
    <Box paddingX={1} minWidth={0} flexShrink={1} userSelect="none">
      <Text wrap="truncate-end">
        <Text bold>{run.description || "Workflow"}</Text>
        <Text color="$muted">{` ${run.completed}/${run.total} `}</Text>
        {run.nodes.map((node) => (
          <Text key={node.id} color={statusColor(node.status)}>
            ●
          </Text>
        ))}
        {active ? <Text color="$muted">{` · ${active}`}</Text> : null}
        {failed > 0 ? <Text color="$error">{` · ${failed} failed`}</Text> : null}
        {skipped > 0 ? (
          <Text color="$muted">{` · ${skipped} skipped`}</Text>
        ) : null}
      </Text>
    </Box>
  );
};

export default WorkflowProgress;
