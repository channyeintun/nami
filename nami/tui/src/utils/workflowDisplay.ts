import type { UIWorkflowAgent, UIWorkflowRun } from "../hooks/useEvents.js";

export type WorkflowStatusColor =
  | "$primary"
  | "$success"
  | "$error"
  | "$warning"
  | "$muted";

/**
 * Agents that are finished one way or another: succeeded, replayed from the
 * journal of the run this one resumed, failed, or stopped.
 */
export function workflowDoneCount(run: UIWorkflowRun): number {
  return run.succeeded + run.cached + run.failed + run.stopped;
}

export function workflowAgentLabel(agent: UIWorkflowAgent): string {
  return agent.label || `agent ${agent.index}`;
}

/**
 * The labels of up to `limit` running agents, then "+N more". The snapshot
 * caps its agent list, so the run's own running count can be larger than
 * the number of running agents it holds.
 */
export function summarizeRunningAgents(
  run: UIWorkflowRun,
  limit: number,
): string {
  const running = run.agents.filter((agent) => agent.status === "running");
  if (running.length === 0) {
    return "";
  }

  const shown = running.slice(0, limit).map(workflowAgentLabel);
  const hidden = Math.max(run.running, running.length) - shown.length;
  return hidden > 0 ? `${shown.join(", ")} +${hidden} more` : shown.join(", ");
}

// Covers both a run's status and an agent's: a run ends completed, an agent
// succeeded or cached.
export function workflowStatusColor(status: string): WorkflowStatusColor {
  switch (status) {
    case "running":
      return "$primary";
    case "completed":
    case "succeeded":
    case "cached":
      return "$success";
    case "failed":
      return "$error";
    case "stopped":
      return "$warning";
    default:
      return "$muted";
  }
}

export function workflowAgentGlyph(status: string): string {
  switch (status) {
    case "running":
      return "●";
    case "succeeded":
      return "✓";
    case "cached":
      return "↺";
    case "failed":
      return "✗";
    case "stopped":
      return "■";
    default:
      return "○";
  }
}

export function workflowStatusLabel(status: string): string {
  switch (status) {
    case "running":
      return "RUNNING";
    case "completed":
      return "DONE";
    case "failed":
      return "FAILED";
    case "stopped":
      return "STOPPED";
    default:
      return status.toUpperCase() || "UPDATED";
  }
}

/**
 * How long the run has taken. A running run is measured against `now`, as
 * the engine sends no update while its agents are busy but unchanged.
 */
export function workflowRunDurationMs(run: UIWorkflowRun, now: number): number {
  const started = parseTimestamp(run.startedAt);
  if (run.status === "running" && started > 0) {
    return Math.max(0, now - started);
  }
  if (run.durationMs > 0) {
    return run.durationMs;
  }
  const completed = parseTimestamp(run.completedAt);
  return started > 0 && completed > 0 ? Math.max(0, completed - started) : 0;
}

export function formatDurationMs(durationMs: number): string {
  const totalSeconds = Math.floor(Math.max(0, durationMs) / 1000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;

  if (hours > 0) {
    return `${hours}h ${minutes}m ${seconds}s`;
  }
  if (minutes > 0) {
    return `${minutes}m ${seconds}s`;
  }
  return `${seconds}s`;
}

/** Collapses whitespace, line breaks included, so text fits on one line. */
export function singleLine(value: string): string {
  return value.replace(/\s+/g, " ").trim();
}

function parseTimestamp(value: string | undefined): number {
  if (!value) {
    return 0;
  }
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? 0 : parsed;
}
