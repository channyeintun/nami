import React, { type FC, useEffect, useMemo, useState } from "react";
import { Box, ListView, Text, useBoxRect, useInput } from "silvery";
import type {
  UIBackgroundAgent,
  UIBackgroundCommand,
} from "../hooks/useEvents.js";
import type {
  BackgroundAgentDetailPayload,
  BackgroundCommandDetailPayload,
  SwarmDashboardSnapshotPayload,
} from "../protocol/types.js";
import { formatTokenCount } from "../utils/modelContext.js";

type TaskKind = "command" | "agent";

type TaskDetail = BackgroundCommandDetailPayload | BackgroundAgentDetailPayload;

interface BackgroundTasksDialogProps {
  commands: UIBackgroundCommand[];
  agents: UIBackgroundAgent[];
  details: Record<string, TaskDetail>;
  swarmDashboard: SwarmDashboardSnapshotPayload | null;
  onClose: () => void;
  onInspectTask: (kind: TaskKind, id: string) => void;
  onStopTask: (kind: TaskKind, id: string) => void;
}

interface TaskListItem {
  key: string;
  kind: TaskKind;
  id: string;
  title: string;
  status: string;
  meta: string;
  section?: string;
}

const BackgroundTasksDialog: FC<BackgroundTasksDialogProps> = ({
  commands,
  agents,
  details,
  swarmDashboard,
  onClose,
  onInspectTask,
  onStopTask,
}) => {
  const items = useMemo(() => buildTaskItems(commands, agents), [commands, agents]);
  const [selectedIndex, setSelectedIndex] = useState(0);
  const [view, setView] = useState<"list" | "detail">("list");
  const selectedItem = items[selectedIndex] ?? null;
  const liveDetail = selectedItem ? details[selectedItem.key] : undefined;
  const detail = selectedItem
    ? liveDetail ?? buildTaskDetailFallback(selectedItem, commands, agents)
    : undefined;

  useEffect(() => {
    if (items.length === 0) {
      setSelectedIndex(0);
      setView("list");
      return;
    }

    if (selectedIndex >= items.length) {
      setSelectedIndex(items.length - 1);
    }
  }, [items, selectedIndex]);

  useEffect(() => {
    if (items.length !== 1) {
      return;
    }
    setSelectedIndex(0);
    setView("detail");
  }, [items.length]);

  useEffect(() => {
    if (view !== "detail" || !selectedItem) {
      return;
    }

    onInspectTask(selectedItem.kind, selectedItem.id);

    const taskStatus = detailStatus(selectedItem, detail);
    if (taskStatus !== "running" && taskStatus !== "cancelling") {
      return;
    }

    const timer = setInterval(() => {
      onInspectTask(selectedItem.kind, selectedItem.id);
    }, 1000);

    return () => clearInterval(timer);
  }, [detail?.status, onInspectTask, selectedItem, view]);

  useEffect(() => {
    if (view !== "detail" || selectedItem) {
      return;
    }
    setView("list");
  }, [selectedItem, view]);

  useInput((input, key) => {
    const shortcut = input?.toLowerCase() ?? "";

    if (key.escape || shortcut === "q") {
      if (view === "detail" && items.length > 1) {
        setView("list");
        return;
      }
      onClose();
      return;
    }

    if (view === "list") {
      if (key.leftArrow) {
        onClose();
        return;
      }

      if (shortcut === "x" && selectedItem && canStopTask(selectedItem.status)) {
        onStopTask(selectedItem.kind, selectedItem.id);
      }
      return;
    }

    if (key.leftArrow) {
      if (items.length > 1) {
        setView("list");
      } else {
        onClose();
      }
      return;
    }

    if (
      shortcut === "x" &&
      selectedItem &&
      canStopTask(detailStatus(selectedItem, detail))
    ) {
      onStopTask(selectedItem.kind, selectedItem.id);
      return;
    }

    if (key.return || input === " ") {
      onClose();
    }
  });

  return (
    <Box
      flexDirection="column"
      flexGrow={1}
      flexShrink={1}
      minWidth={0}
      minHeight={0}
      backgroundColor="$popover-bg"
      borderStyle="double"
      borderColor="$inputborder"
      overflow="hidden"
      paddingX={2}
      paddingY={1}
    >
      <Header items={items} />

      <SwarmOverview agents={agents} swarmDashboard={swarmDashboard} />

      {view === "list" ? (
        <TaskList
          items={items}
          selectedIndex={selectedIndex}
          onCursor={setSelectedIndex}
          onSelectIndex={(index) => {
            if (!items[index]) {
              return;
            }
            setSelectedIndex(index);
            setView("detail");
          }}
        />
      ) : (
        <TaskDetailView
          item={selectedItem}
          detail={detail}
          pendingRefresh={Boolean(selectedItem) && !liveDetail}
        />
      )}

      <FooterHint
        view={view}
        selectedItem={selectedItem}
        canStop={selectedItem ? canStopTask(detailStatus(selectedItem, detail)) : false}
      />
    </Box>
  );
};

export default BackgroundTasksDialog;

const Header: FC<{ items: TaskListItem[] }> = ({ items }) => {
  const runningCount = items.filter((item) => canStopTask(item.status)).length;

  return (
    <Box flexDirection="column" flexShrink={0} minWidth={0}>
      <Text bold color="$primary">
        Background Tasks
      </Text>
      <Box marginTop={1} flexDirection="column" minWidth={0}>
        <Text>Inspect and manage retained background commands and child agents.</Text>
        <Text color="$muted">
          {items.length} task{items.length === 1 ? "" : "s"}
          {runningCount > 0 ? ` · ${runningCount} active` : ""}
        </Text>
      </Box>
    </Box>
  );
};

const SwarmOverview: FC<{
  agents: UIBackgroundAgent[];
  swarmDashboard: SwarmDashboardSnapshotPayload | null;
}> = ({ agents, swarmDashboard }) => {
  const handoffs = swarmDashboard?.handoffs ?? [];
  const activeRoleWorkspaces = Array.from(
    agents.reduce((acc, agent) => {
      if (!agent.role || !canStopTask(agent.status) || acc.has(agent.role)) {
        return acc;
      }
      const workspaceLabel =
        agent.worktreeBranch ||
        (agent.workspacePath ? basename(agent.workspacePath) : undefined) ||
        agent.workspaceStrategy ||
        "shared";
      acc.set(agent.role, workspaceLabel);
      return acc;
    }, new Map<string, string>()),
  );
  const activeRoles = Array.from(
    new Set(
      agents
        .filter((agent) => agent.role && canStopTask(agent.status))
        .map((agent) => agent.role as string),
    ),
  );
  const queuedHandoffs = handoffs.filter(
    (handoff) => handoff.status !== "completed",
  );
  const blockedCount = handoffs.filter((handoff) => handoff.status === "blocked").length;
  const queueByRole = Array.from(
    queuedHandoffs.reduce((acc, handoff) => {
      const key = handoff.target_role || "unassigned";
      acc.set(key, (acc.get(key) ?? 0) + 1);
      return acc;
    }, new Map<string, number>()),
  )
    .sort((left, right) => right[1] - left[1] || left[0].localeCompare(right[0]))
    .slice(0, 4);
  const recentHandoffs = queuedHandoffs.slice(0, 4);

  if (
    activeRoles.length === 0 &&
    queueByRole.length === 0 &&
    recentHandoffs.length === 0
  ) {
    return null;
  }

  return (
    <Box marginTop={1} flexDirection="column" flexShrink={0} minWidth={0}>
      <Text bold color="$accent">
        Swarm Overview
      </Text>
      <Text color="$muted">
        {activeRoles.length} active role{activeRoles.length === 1 ? "" : "s"}
        {queuedHandoffs.length > 0 ? ` · ${queuedHandoffs.length} queued handoff${queuedHandoffs.length === 1 ? "" : "s"}` : ""}
        {blockedCount > 0 ? ` · ${blockedCount} blocked` : ""}
      </Text>
      {activeRoles.length > 0 ? (
        <Text wrap="wrap">
          <Text bold>Active Roles:</Text> {activeRoles.join(", ")}
        </Text>
      ) : null}
      {activeRoleWorkspaces.length > 0 ? (
        <Text wrap="wrap">
          <Text bold>Workspaces:</Text>{" "}
          {activeRoleWorkspaces
            .map(([role, workspace]) => `${role} @ ${workspace}`)
            .join(" · ")}
        </Text>
      ) : null}
      {queueByRole.length > 0 ? (
        <Text wrap="wrap">
          <Text bold>Queue:</Text>{" "}
          {queueByRole.map(([role, count]) => `${role} ${count}`).join(" · ")}
        </Text>
      ) : null}
      {recentHandoffs.length > 0 ? (
        <Box marginTop={1} flexDirection="column" minWidth={0}>
          {recentHandoffs.map((handoff) => (
            <Text key={handoff.id} color="$muted" wrap="wrap">
              {handoff.source_role} → {handoff.target_role}: {truncate(handoff.summary, 72)}
            </Text>
          ))}
        </Box>
      ) : null}
    </Box>
  );
};

interface TaskListProps {
  items: TaskListItem[];
  selectedIndex: number;
  onCursor: (index: number) => void;
  onSelectIndex: (index: number) => void;
}

const TaskList: FC<TaskListProps> = ({
  items,
  selectedIndex,
  onCursor,
  onSelectIndex,
}) => {
  const { height: rectHeight } = useBoxRect();
  const viewportHeight = Math.max(1, rectHeight);

  if (items.length === 0) {
    return (
      <Box
        marginTop={1}
        flexDirection="column"
        flexGrow={1}
        flexShrink={1}
        minHeight={0}
        justifyContent="center"
      >
        <Text color="$muted">No retained background tasks.</Text>
      </Box>
    );
  }

  return (
    <Box
      marginTop={1}
      flexDirection="column"
      flexGrow={1}
      flexShrink={1}
      minHeight={0}
      minWidth={0}
      overflow="hidden"
    >
      <ListView
        items={items}
        height={viewportHeight}
        nav
        cursorKey={selectedIndex}
        onCursor={onCursor}
        onSelect={onSelectIndex}
        active
        estimateHeight={3}
        overflowIndicator
        getKey={(item) => item.key}
        renderItem={(item, _index, meta) => {
          const isSelected = meta.isCursor;
          return (
            <Box
              key={item.key}
              flexDirection="column"
              backgroundColor={isSelected ? "$selectionbg" : undefined}
              paddingX={1}
              marginBottom={1}
              minWidth={0}
            >
              {item.section ? (
                <Text color="$muted" bold>
                  {item.section}
                </Text>
              ) : null}
              <Text color={isSelected ? "$selection" : "$fg"} bold={isSelected}>
                {isSelected ? "›" : " "} <Text color={statusColor(item.status)}>{statusLabel(item.status)}</Text>{" "}
                {truncate(item.title, 84)}
              </Text>
              <Text color={isSelected ? "$selection" : "$muted"}>{item.meta}</Text>
            </Box>
          );
        }}
      />
    </Box>
  );
};

interface TaskDetailViewProps {
  item: TaskListItem | null;
  detail: TaskDetail | undefined;
  pendingRefresh: boolean;
}

const TaskDetailView: FC<TaskDetailViewProps> = ({
  item,
  detail,
  pendingRefresh,
}) => {
  if (!item) {
    return (
      <Box marginTop={1} flexGrow={1} justifyContent="center">
        <Text color="$muted">Task not available anymore.</Text>
      </Box>
    );
  }

  if (!detail) {
    return (
      <Box marginTop={1} flexGrow={1} justifyContent="center">
        <Text color="$muted">Loading task details…</Text>
      </Box>
    );
  }

  if (item.kind === "command") {
    return (
      <CommandDetail
        detail={detail as BackgroundCommandDetailPayload}
        pendingRefresh={pendingRefresh}
      />
    );
  }

  return (
    <AgentDetail
      detail={detail as BackgroundAgentDetailPayload}
      pendingRefresh={pendingRefresh}
    />
  );
};

const CommandDetail: FC<{
  detail: BackgroundCommandDetailPayload;
  pendingRefresh: boolean;
}> = ({ detail, pendingRefresh }) => {
  const runtime = formatRuntime(detail.started_at, detail.updated_at, detail.running);

  return (
    <Box marginTop={1} flexDirection="column" flexGrow={1} flexShrink={1} minHeight={0}>
      <Box flexDirection="column" flexShrink={0} minWidth={0}>
        {pendingRefresh ? (
          <Text color="$muted">Refreshing retained command detail…</Text>
        ) : null}
        <Text bold color="$accent">
          Background Command
        </Text>
        <Text>
          <Text bold>Status:</Text> <Text color={statusColor(detail.status)}>{statusLabel(detail.status)}</Text>
          {typeof detail.exit_code === "number" ? ` (exit ${detail.exit_code})` : ""}
        </Text>
        {runtime ? (
          <Text>
            <Text bold>Runtime:</Text> {runtime}
          </Text>
        ) : null}
        {detail.cwd ? (
          <Text wrap="wrap">
            <Text bold>CWD:</Text> {detail.cwd}
          </Text>
        ) : null}
        {detail.command ? (
          <Text wrap="wrap">
            <Text bold>Command:</Text> {detail.command}
          </Text>
        ) : null}
        {detail.has_unread_output && detail.unread_bytes ? (
          <Text color="$muted">Unread output retained: {formatBytes(detail.unread_bytes)}</Text>
        ) : null}
        {detail.error ? (
          <Text color="$error" wrap="wrap">
            <Text bold>Error:</Text> {detail.error}
          </Text>
        ) : null}
      </Box>

      <Box marginTop={1} flexDirection="column" flexGrow={1} flexShrink={1} minHeight={0}>
        <Text bold>Output</Text>
        <Box
          marginTop={1}
          flexDirection="column"
          flexGrow={1}
          flexShrink={1}
          minHeight={0}
          borderStyle="round"
          borderColor="$border"
          paddingX={1}
          overflow="scroll"
        >
          <Text wrap="wrap">{detail.output?.trim() || "No retained output available."}</Text>
        </Box>
      </Box>
    </Box>
  );
};

const AgentDetail: FC<{
  detail: BackgroundAgentDetailPayload;
  pendingRefresh: boolean;
}> = ({ detail, pendingRefresh }) => {
  const tools = detail.metadata?.tools ?? [];
  const statusMessage = detail.metadata?.status_message?.trim();
  const stopBlockReason = detail.metadata?.stop_block_reason?.trim();
  const costLine =
    detail.total_cost_usd || detail.input_tokens || detail.output_tokens
      ? `${formatTokenCount(detail.input_tokens ?? 0)}↑ ${formatTokenCount(detail.output_tokens ?? 0)}↓ · $${(detail.total_cost_usd ?? 0).toFixed(4)}`
      : null;

  return (
    <Box marginTop={1} flexDirection="column" flexGrow={1} flexShrink={1} minHeight={0}>
      <Box flexDirection="column" flexShrink={0} minWidth={0}>
        {pendingRefresh ? (
          <Text color="$muted">Refreshing retained agent detail…</Text>
        ) : null}
        <Text bold color="$primary">
          Background Agent
        </Text>
        <Text>
          <Text bold>Status:</Text> <Text color={statusColor(detail.status)}>{statusLabel(detail.status)}</Text>
        </Text>
        {detail.description ? (
          <Text wrap="wrap">
            <Text bold>Task:</Text> {detail.description}
          </Text>
        ) : null}
        {detail.subagent_type ? (
          <Text>
            <Text bold>Type:</Text> {detail.subagent_type}
          </Text>
        ) : null}
        {detail.metadata?.role ? (
          <Text>
            <Text bold>Role:</Text> {detail.metadata.role}
          </Text>
        ) : null}
        {detail.metadata?.workspace_strategy ? (
          <Text>
            <Text bold>Workspace:</Text> {detail.metadata.workspace_strategy}
          </Text>
        ) : null}
        {detail.metadata?.workspace_path ? (
          <Text wrap="wrap">
            <Text bold>Workspace Path:</Text> {detail.metadata.workspace_path}
          </Text>
        ) : null}
        {detail.metadata?.worktree_branch ? (
          <Text wrap="wrap">
            <Text bold>Branch:</Text> {detail.metadata.worktree_branch}
          </Text>
        ) : null}
        {detail.session_id ? (
          <Text wrap="wrap">
            <Text bold>Session:</Text> {detail.session_id}
          </Text>
        ) : null}
        {costLine ? (
          <Text>
            <Text bold>Usage:</Text> {costLine}
          </Text>
        ) : null}
        {statusMessage ? (
          <Text color="$muted" wrap="wrap">
            <Text bold>State:</Text> {statusMessage}
          </Text>
        ) : null}
        {stopBlockReason ? (
          <Text color="$warning" wrap="wrap">
            <Text bold>Stop Block:</Text> {stopBlockReason}
          </Text>
        ) : null}
        {detail.transcript_path ? (
          <Text wrap="wrap">
            <Text bold>Transcript:</Text> {detail.transcript_path}
          </Text>
        ) : null}
        {detail.output_file ? (
          <Text wrap="wrap">
            <Text bold>Result File:</Text> {detail.output_file}
          </Text>
        ) : null}
        {tools.length > 0 ? (
          <Text wrap="wrap">
            <Text bold>Tools:</Text> {tools.join(", ")}
          </Text>
        ) : null}
        {detail.error ? (
          <Text color="$error" wrap="wrap">
            <Text bold>Error:</Text> {detail.error}
          </Text>
        ) : null}
      </Box>

      <Box marginTop={1} flexDirection="column" flexGrow={1} flexShrink={1} minHeight={0}>
        <Text bold>Latest Report</Text>
        <Box
          marginTop={1}
          flexDirection="column"
          flexGrow={1}
          flexShrink={1}
          minHeight={0}
          borderStyle="round"
          borderColor="$border"
          paddingX={1}
          overflow="scroll"
        >
          <Text wrap="wrap">{detail.summary?.trim() || "No summary available."}</Text>
        </Box>
      </Box>
    </Box>
  );
};

const FooterHint: FC<{
  view: "list" | "detail";
  selectedItem: TaskListItem | null;
  canStop: boolean;
}> = ({ view, selectedItem, canStop }) => {
  const stopHint = canStop ? " · X stop" : "";

  if (view === "list") {
    return (
      <Box marginTop={1} flexDirection="column" flexShrink={0}>
        <Text color="$fg">
          <Text color="$primary" bold>
            Enter
          </Text>{" "}
          open · <Text color="$primary" bold>Up/Down</Text> change selection
          {stopHint} · <Text color="$primary" bold>Esc</Text> or <Text color="$primary" bold>Q</Text> close
        </Text>
        {selectedItem ? <Text color="$muted">{selectedItem.kind === "command" ? "Shell output is tailed live in detail view." : "Agent reports refresh while the task is active."}</Text> : null}
      </Box>
    );
  }

  return (
    <Box marginTop={1} flexDirection="column" flexShrink={0}>
      <Text color="$fg">
        <Text color="$primary" bold>
          Left
        </Text>{" "}
        back · <Text color="$primary" bold>Enter</Text> or <Text color="$primary" bold>Space</Text> close
        {stopHint} · <Text color="$primary" bold>Esc</Text> close
      </Text>
    </Box>
  );
};

function buildTaskItems(
  commands: UIBackgroundCommand[],
  agents: UIBackgroundAgent[],
): TaskListItem[] {
  const commandItems = [...commands]
    .sort(compareCommands)
    .map((command, index) => ({
      key: taskKey("command", command.commandId),
      kind: "command" as const,
      id: command.commandId,
      title: command.command || command.commandId,
      status: command.status,
      meta: buildCommandMeta(command),
      section: index === 0 ? "Commands" : undefined,
    }));

  const agentItems = [...agents]
    .sort(compareAgents)
    .map((agent, index) => ({
      key: taskKey("agent", agent.agentId),
      kind: "agent" as const,
      id: agent.agentId,
      title: agent.description || agent.invocationId || agent.agentId,
      status: agent.status,
      meta: buildAgentMeta(agent),
      section: index === 0 ? "Agents" : undefined,
    }));

  return [...commandItems, ...agentItems];
}

function taskKey(kind: TaskKind, id: string): string {
  return `${kind}:${id}`;
}

function buildTaskDetailFallback(
  item: TaskListItem,
  commands: UIBackgroundCommand[],
  agents: UIBackgroundAgent[],
): TaskDetail | undefined {
  if (item.kind === "command") {
    const command = commands.find((entry) => entry.commandId === item.id);
    if (!command) {
      return undefined;
    }

    return {
      command_id: command.commandId,
      command: command.command,
      cwd: command.cwd,
      status: command.status,
      running: command.running,
      started_at: command.startedAt,
      updated_at: command.updatedAt ?? command.retainedAt,
      output: command.preview,
      has_unread_output:
        command.previewKind === "unread" && command.unreadBytes > 0,
      unread_bytes: command.unreadBytes,
      exit_code: command.exitCode,
      error: command.error,
    };
  }

  const agent = agents.find((entry) => entry.agentId === item.id);
  if (!agent) {
    return undefined;
  }

  return {
    agent_id: agent.agentId,
    invocation_id: agent.invocationId || undefined,
    description: agent.description || undefined,
    subagent_type: agent.subagentType || undefined,
    status: agent.status,
    summary: agent.summary || undefined,
    session_id: agent.sessionId,
    transcript_path: agent.transcriptPath,
    output_file: agent.outputFile,
    error: agent.error,
    total_cost_usd: agent.totalCostUsd || undefined,
    input_tokens: agent.inputTokens || undefined,
    output_tokens: agent.outputTokens || undefined,
    metadata: {
      invocation_id: agent.invocationId || undefined,
      agent_id: agent.agentId,
      description: agent.description || undefined,
      role: agent.role || undefined,
      subagent_type: agent.subagentType || undefined,
      workspace_strategy: agent.workspaceStrategy,
      workspace_path: agent.workspacePath,
      repository_root: agent.repositoryRoot,
      worktree_branch: agent.worktreeBranch,
      worktree_created: agent.worktreeCreated,
      lifecycle_state: agent.lifecycleState,
      status_message: agent.statusMessage,
      stop_block_reason: agent.stopBlockReason,
      stop_block_count:
        agent.stopBlockCount > 0 ? agent.stopBlockCount : undefined,
      session_id: agent.sessionId,
      transcript_path: agent.transcriptPath,
      result_path: agent.outputFile,
      tools: agent.tools.length > 0 ? [...agent.tools] : undefined,
    },
  };
}

function detailStatus(item: TaskListItem, detail: TaskDetail | undefined): string {
  if (!detail) {
    return item.status;
  }
  return detail.status || item.status;
}

function canStopTask(status: string): boolean {
  return status === "running" || status === "cancelling";
}

function compareCommands(left: UIBackgroundCommand, right: UIBackgroundCommand): number {
  const rankDiff = statusRank(left.status) - statusRank(right.status);
  if (rankDiff !== 0) {
    return rankDiff;
  }

  return parseTimestamp(right.updatedAt ?? right.retainedAt) - parseTimestamp(left.updatedAt ?? left.retainedAt);
}

function compareAgents(left: UIBackgroundAgent, right: UIBackgroundAgent): number {
  const rankDiff = statusRank(left.status) - statusRank(right.status);
  if (rankDiff !== 0) {
    return rankDiff;
  }

  return parseTimestamp(right.updatedAt) - parseTimestamp(left.updatedAt);
}

function statusRank(status: string): number {
  switch (status) {
    case "running":
      return 0;
    case "cancelling":
      return 1;
    case "failed":
      return 2;
    case "completed":
      return 3;
    case "stopped":
      return 4;
    case "cancelled":
      return 5;
    default:
      return 6;
  }
}

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
    case "stopped":
      return "STOPPED";
    case "cancelled":
      return "CANCELLED";
    default:
      return status.toUpperCase() || "UPDATED";
  }
}

function statusColor(
  status: string,
): "$info" | "$warning" | "$success" | "$error" | "$primary" | "$accent" {
  switch (status) {
    case "running":
      return "$info";
    case "cancelling":
    case "stopped":
      return "$warning";
    case "completed":
      return "$success";
    case "failed":
    case "cancelled":
      return "$error";
    case "running_tools":
      return "$accent";
    default:
      return "$primary";
  }
}

function buildCommandMeta(command: UIBackgroundCommand): string {
  const parts = [command.commandId];

  if (command.cwd) {
    parts.push(`cwd ${basename(command.cwd)}`);
  }
  if (command.previewKind === "unread" && command.unreadBytes > 0) {
    parts.push(`unread ${formatBytes(command.unreadBytes)}`);
  }
  if (typeof command.exitCode === "number") {
    parts.push(`exit ${command.exitCode}`);
  }
  parts.push(`updated ${formatUpdatedAt(command.updatedAt ?? command.retainedAt)}`);

  return parts.join(" · ");
}

function buildAgentMeta(agent: UIBackgroundAgent): string {
  const parts = [agent.agentId];

  if (agent.role) {
    parts.push(`role ${agent.role}`);
  }
  if (agent.subagentType) {
    parts.push(agent.subagentType);
  }
  if (agent.worktreeBranch) {
    parts.push(`branch ${agent.worktreeBranch}`);
  }
  if (agent.sessionId) {
    parts.push(`session ${agent.sessionId.slice(0, 8)}`);
  }
  if (agent.stopBlockCount > 0) {
    parts.push(`stop blocks ${agent.stopBlockCount}`);
  }
  parts.push(`updated ${formatUpdatedAt(agent.updatedAt)}`);

  return parts.join(" · ");
}

function parseTimestamp(value: string | undefined): number {
  if (!value) {
    return 0;
  }
  const parsed = Date.parse(value);
  return Number.isNaN(parsed) ? 0 : parsed;
}

function formatUpdatedAt(value: string | undefined): string {
  const timestamp = parseTimestamp(value);
  if (timestamp === 0) {
    return "recently";
  }

  const elapsedSeconds = Math.max(0, Math.floor((Date.now() - timestamp) / 1000));
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

function formatRuntime(
  startedAt: string | undefined,
  updatedAt: string | undefined,
  running: boolean,
): string | null {
  const start = parseTimestamp(startedAt);
  if (start === 0) {
    return null;
  }
  const end = running ? Date.now() : parseTimestamp(updatedAt) || Date.now();
  const durationMs = Math.max(0, end - start);
  const totalSeconds = Math.floor(durationMs / 1000);
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

function truncate(value: string, limit: number): string {
  const flattened = value.replace(/\s+/g, " ").trim();
  if (flattened.length <= limit) {
    return flattened;
  }
  return `${flattened.slice(0, limit - 3)}...`;
}

function basename(value: string): string {
  const parts = value.split("/").filter(Boolean);
  return parts[parts.length - 1] ?? value;
}

function formatBytes(value: number): string {
  if (value < 1024) {
    return `${value}B`;
  }
  if (value < 1024 * 1024) {
    return `${(value / 1024).toFixed(value >= 10 * 1024 ? 0 : 1)}KB`;
  }
  return `${(value / (1024 * 1024)).toFixed(1)}MB`;
}