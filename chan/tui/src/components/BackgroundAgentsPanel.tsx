import React, { type FC } from "react";
import { Box, Text } from "ink";
import type { UIBackgroundAgent } from "../hooks/useEvents.js";
import { formatTokenCount } from "../utils/modelContext.js";
import { formatSubagentType } from "../utils/subagentLabels.js";

interface BackgroundAgentsPanelProps {
  agents: UIBackgroundAgent[];
}

const MAX_ACTIVE_AGENTS = 3;
const MAX_RECENT_AGENTS = 3;

const BackgroundAgentsPanel: FC<BackgroundAgentsPanelProps> = ({ agents }) => {
  const activeAgents = agents.filter(isActiveAgent).slice(0, MAX_ACTIVE_AGENTS);
  const recentAgents = agents
    .filter((agent) => !isActiveAgent(agent))
    .slice(0, MAX_RECENT_AGENTS);
  const hiddenCount = Math.max(
    0,
    agents.length - activeAgents.length - recentAgents.length,
  );

  return (
    <Box
      flexDirection="column"
      borderStyle="round"
      borderColor="cyan"
      paddingX={1}
      marginTop={1}
    >
      <Text color="cyan">
        Background Agents
        {renderCounts(activeAgents.length, recentAgents.length)}
      </Text>

      {activeAgents.length > 0 ? (
        <Box flexDirection="column" marginTop={1}>
          <Text color="cyan">Active</Text>
          {activeAgents.map((agent, index) => (
            <AgentRow
              key={agent.agentId}
              agent={agent}
              marginTop={index === 0 ? 0 : 1}
            />
          ))}
        </Box>
      ) : null}

      {recentAgents.length > 0 ? (
        <Box flexDirection="column" marginTop={activeAgents.length > 0 ? 1 : 0}>
          <Text color="gray">Recent</Text>
          {recentAgents.map((agent, index) => (
            <AgentRow
              key={agent.agentId}
              agent={agent}
              marginTop={index === 0 ? 0 : 1}
            />
          ))}
        </Box>
      ) : null}

      {hiddenCount > 0 ? (
        <Text dimColor>{`+${hiddenCount} more retained child agents`}</Text>
      ) : null}
    </Box>
  );
};

export default BackgroundAgentsPanel;

interface AgentRowProps {
  agent: UIBackgroundAgent;
  marginTop: number;
}

const AgentRow: FC<AgentRowProps> = ({ agent, marginTop }) => {
  return (
    <Box flexDirection="column" marginTop={marginTop}>
      <Box flexDirection="row" gap={1}>
        <Text color={statusColor(agent.status)}>
          {statusLabel(agent.status)}
        </Text>
        <Text bold>
          {agent.description || agent.invocationId || agent.agentId}
        </Text>
        <Text dimColor>{formatSubagentTypeLabel(agent.subagentType)}</Text>
      </Box>
      <Text dimColor>{truncate(agent.summary, 120)}</Text>
      <Text dimColor>{formatMeta(agent)}</Text>
    </Box>
  );
};

function statusLabel(status: string): string {
  switch (status) {
    case "running":
      return "RUNNING";
    case "cancelling":
      return "STOPPING";
    case "completed":
      return "DONE";
    case "failed":
      return "FAILED";
    case "cancelled":
      return "CANCELLED";
    default:
      return status.toUpperCase() || "UPDATED";
  }
}

function statusColor(status: string): "cyan" | "yellow" | "green" | "red" {
  switch (status) {
    case "running":
      return "cyan";
    case "cancelling":
      return "yellow";
    case "completed":
      return "green";
    case "failed":
    case "cancelled":
      return "red";
    default:
      return "cyan";
  }
}

function formatSubagentTypeLabel(subagentType: string): string {
  const label = formatSubagentType(subagentType);
  return label ? `(${label})` : "";
}

function formatMeta(agent: UIBackgroundAgent): string {
  const parts = [
    agent.invocationId ? `run ${agent.invocationId}` : agent.agentId,
    `updated ${formatUpdatedAt(agent.updatedAt)}`,
  ];

  if (agent.agentId && agent.agentId !== agent.invocationId) {
    parts.push(`handle ${agent.agentId}`);
  }

  if (agent.lifecycleState) {
    parts.push(`state ${agent.lifecycleState}`);
  }
  if (agent.stopBlockCount > 0) {
    parts.push(`stop blocks ${agent.stopBlockCount}`);
  }
  if (agent.stopBlockReason) {
    parts.push(`blocked ${truncate(agent.stopBlockReason, 48)}`);
  }

  const costSummary = formatCostSummary(agent);
  if (costSummary) {
    parts.push(costSummary);
  }

  if (agent.sessionId) {
    parts.push(agent.sessionId);
  }
  if (agent.transcriptPath) {
    parts.push(`transcript ${basename(agent.transcriptPath)}`);
  }
  if (agent.outputFile) {
    parts.push(`result ${basename(agent.outputFile)}`);
  }
  if (agent.tools.length > 0) {
    parts.push(`${agent.tools.length} tools`);
  }

  return parts.join(" | ");
}

function formatCostSummary(agent: UIBackgroundAgent): string {
  if (
    agent.totalCostUsd <= 0 &&
    agent.inputTokens <= 0 &&
    agent.outputTokens <= 0
  ) {
    return "";
  }

  return `cost $${agent.totalCostUsd.toFixed(4)} · ${formatTokenCount(agent.inputTokens)}↑ ${formatTokenCount(agent.outputTokens)}↓`;
}

function renderCounts(activeCount: number, recentCount: number): string {
  const parts: string[] = [];

  if (activeCount > 0) {
    parts.push(`${activeCount} active`);
  }
  if (recentCount > 0) {
    parts.push(`${recentCount} recent`);
  }

  return parts.length > 0 ? ` (${parts.join(", ")})` : "";
}

function isActiveAgent(agent: UIBackgroundAgent): boolean {
  return agent.status === "running" || agent.status === "cancelling";
}

function basename(value: string): string {
  const parts = value.split("/").filter(Boolean);
  return parts[parts.length - 1] ?? value;
}

function formatUpdatedAt(value: string): string {
  const timestamp = Date.parse(value);
  if (Number.isNaN(timestamp)) {
    return "recently";
  }

  const elapsedSeconds = Math.max(
    0,
    Math.floor((Date.now() - timestamp) / 1000),
  );
  if (elapsedSeconds < 10) {
    return "just now";
  }
  if (elapsedSeconds < 60) {
    return `${elapsedSeconds}s ago`;
  }

  const elapsedMinutes = Math.floor(elapsedSeconds / 60);
  if (elapsedMinutes < 60) {
    return `${elapsedMinutes}m ago`;
  }

  const elapsedHours = Math.floor(elapsedMinutes / 60);
  return `${elapsedHours}h ago`;
}

function truncate(value: string, limit: number): string {
  const flattened = value.replace(/\s+/g, " ").trim();
  if (flattened.length <= limit) {
    return flattened;
  }
  return `${flattened.slice(0, limit - 3)}...`;
}
