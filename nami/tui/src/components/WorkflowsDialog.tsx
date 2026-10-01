import React, { type FC, useEffect, useMemo, useState } from "react";
import {
  Box,
  ListView,
  MeasuredBox,
  ModalDialog,
  Text,
  useInput,
} from "silvery";
import type { UIWorkflowAgent, UIWorkflowRun } from "../hooks/useEvents.js";
import type { SavedWorkflowPayload } from "../protocol/types.js";
import { formatTokenCount } from "../utils/modelContext.js";
import { expandTabs, truncateEnd } from "../utils/text.js";
import {
  formatDurationMs,
  singleLine,
  workflowAgentGlyph,
  workflowAgentLabel,
  workflowDoneCount,
  workflowRunDurationMs,
  workflowStatusColor,
  workflowStatusLabel,
} from "../utils/workflowDisplay.js";

type WorkflowItemKind = "run" | "saved";

interface WorkflowsDialogProps {
  /** Already ordered: running first, then the latest. */
  runs: UIWorkflowRun[];
  saved: SavedWorkflowPayload[];
  onClose: () => void;
  onStopRun: (runId: string) => void;
}

interface WorkflowListItem {
  key: string;
  kind: WorkflowItemKind;
  id: string;
  title: string;
  status: string;
  meta: string;
  section?: string;
}

// One scrollable line of a run's detail. Agents are rows of their own so a
// long run reads as a list grouped under its phases.
type RunDetailRow =
  | { key: string; kind: "heading"; text: string; note?: string }
  | { key: string; kind: "agent"; agent: UIWorkflowAgent }
  | {
      key: string;
      kind: "text";
      text: string;
      color?: "$muted" | "$error" | "$warning";
    }
  | { key: string; kind: "gap" };

const ERROR_SNIPPET_CHARS = 160;
const LABEL_CHARS = 72;

const WorkflowsDialog: FC<WorkflowsDialogProps> = ({
  runs,
  saved,
  onClose,
  onStopRun,
}) => {
  const [terminalRows, setTerminalRows] = useState(process.stdout.rows ?? 24);
  const [terminalColumns, setTerminalColumns] = useState(
    process.stdout.columns ?? 80,
  );
  const now = useClockWhile(runs.some((run) => run.status === "running"));
  const items = useMemo(
    () => buildWorkflowItems(runs, saved, now),
    [runs, saved, now],
  );
  // Runs move as their status changes, so the selection is held by key. An
  // index would slide the cursor, the detail view and the stop shortcut onto
  // whichever entry moved into its slot.
  const [selectedKey, setSelectedKey] = useState<string | null>(null);
  const [view, setView] = useState<"list" | "detail">("list");
  // The engine handles a stop between turns, so a run can stay "running"
  // for a while after X. Remembering the request keeps the dialog from
  // offering the same stop again and says why nothing has changed yet.
  const [stopRequested, setStopRequested] = useState<ReadonlySet<string>>(
    () => new Set(),
  );
  const keyedIndex = items.findIndex((item) => item.key === selectedKey);
  const selectedIndex = Math.max(0, keyedIndex);
  // Until an entry is picked the list cursor rests on the first row; the
  // detail view only ever shows the entry that was picked.
  const selectedItem =
    keyedIndex >= 0
      ? items[keyedIndex]
      : view === "list"
        ? (items[0] ?? null)
        : null;
  const selectedRun =
    selectedItem?.kind === "run"
      ? runs.find((run) => run.runId === selectedItem.id)
      : undefined;
  const selectedSaved =
    selectedItem?.kind === "saved"
      ? saved.find((entry) => entry.name === selectedItem.id)
      : undefined;
  const canStopSelected =
    selectedRun?.status === "running" && !stopRequested.has(selectedRun.runId);

  const onlyItemKey = items.length === 1 ? items[0]?.key : undefined;
  useEffect(() => {
    if (!onlyItemKey) {
      return;
    }
    setSelectedKey(onlyItemKey);
    setView("detail");
  }, [onlyItemKey]);

  useEffect(() => {
    const handleResize = () => {
      setTerminalRows(process.stdout.rows ?? 24);
      setTerminalColumns(process.stdout.columns ?? 80);
    };

    handleResize();
    process.stdout.on("resize", handleResize);

    return () => {
      process.stdout.off("resize", handleResize);
    };
  }, []);

  // A run can leave the list while its detail is open: the session changed,
  // or newer runs pushed it out.
  useEffect(() => {
    if (view !== "detail" || selectedItem) {
      return;
    }
    setView("list");
  }, [selectedItem, view]);

  const stopSelectedRun = () => {
    if (!selectedRun || !canStopSelected) {
      return;
    }
    onStopRun(selectedRun.runId);
    setStopRequested((current) => new Set(current).add(selectedRun.runId));
  };

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

    if (key.leftArrow) {
      if (view === "detail" && items.length > 1) {
        setView("list");
        return;
      }
      onClose();
      return;
    }

    if (shortcut === "x") {
      stopSelectedRun();
    }
  });

  const dialogWidth =
    terminalColumns > 60
      ? Math.min(110, terminalColumns - 4)
      : Math.max(28, terminalColumns - 2);
  const dialogHeight =
    terminalRows > 16
      ? Math.min(30, terminalRows - 4)
      : Math.max(10, terminalRows - 2);

  return (
    <ModalDialog
      title="Workflows"
      width={dialogWidth}
      height={dialogHeight}
      borderStyle="single"
      borderColor="$inputborder"
      footer={
        <FooterHint
          view={view}
          canStop={canStopSelected}
          canGoBack={items.length > 1}
        />
      }
    >
      <Box
        flexDirection="column"
        flexGrow={1}
        flexShrink={1}
        minWidth={0}
        minHeight={0}
      >
        {view === "list" ? (
          <>
            <Header runs={runs} saved={saved} />
            <WorkflowList
              items={items}
              selectedIndex={selectedIndex}
              onCursor={(index) => setSelectedKey(items[index]?.key ?? null)}
              onSelectIndex={(index) => {
                const item = items[index];
                if (!item) {
                  return;
                }
                setSelectedKey(item.key);
                setView("detail");
              }}
            />
          </>
        ) : selectedRun ? (
          <RunDetail
            // A fresh list per run, so the scroll position of one run's
            // detail does not carry over to the next.
            key={selectedRun.runId}
            run={selectedRun}
            now={now}
            stopRequested={stopRequested.has(selectedRun.runId)}
          />
        ) : selectedSaved ? (
          <SavedDetail workflow={selectedSaved} />
        ) : (
          <Box marginTop={1} flexGrow={1} justifyContent="center">
            <Text color="$muted">Workflow not available anymore.</Text>
          </Box>
        )}
      </Box>
    </ModalDialog>
  );
};

export default WorkflowsDialog;

const Header: FC<{ runs: UIWorkflowRun[]; saved: SavedWorkflowPayload[] }> = ({
  runs,
  saved,
}) => {
  const runningCount = runs.filter((run) => run.status === "running").length;

  return (
    <Box flexDirection="column" flexShrink={0} minWidth={0}>
      <Text>Workflow runs of this session and saved workflow scripts.</Text>
      <Text color="$muted">
        {runs.length} run{runs.length === 1 ? "" : "s"}
        {runningCount > 0 ? ` · ${runningCount} running` : ""}
        {` · ${saved.length} saved`}
      </Text>
    </Box>
  );
};

interface WorkflowListProps {
  items: WorkflowListItem[];
  selectedIndex: number;
  onCursor: (index: number) => void;
  onSelectIndex: (index: number) => void;
}

const WorkflowList: FC<WorkflowListProps> = ({
  items,
  selectedIndex,
  onCursor,
  onSelectIndex,
}) => {
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
        <Text color="$muted">No workflow runs and no saved workflows.</Text>
        <Text color="$muted" wrap="wrap">
          A saved workflow is a .js script in .nami/workflows of the project,
          or in the workflows folder of nami's config directory.
        </Text>
      </Box>
    );
  }

  return (
    <MeasuredBox
      marginTop={1}
      flexDirection="column"
      flexGrow={1}
      flexShrink={1}
      minHeight={0}
      minWidth={0}
      overflow="hidden"
    >
      {({ height }) => (
        <ListView
          items={items}
          height={Math.max(1, height)}
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
                <Text
                  color={isSelected ? "$selection" : "$fg"}
                  bold={isSelected}
                  wrap="truncate-end"
                >
                  {isSelected ? "›" : " "}{" "}
                  <Text color={itemStatusColor(item)}>
                    {itemStatusLabel(item)}
                  </Text>{" "}
                  {truncateEnd(singleLine(item.title), 84)}
                </Text>
                <Text
                  color={isSelected ? "$selection" : "$muted"}
                  wrap="truncate-end"
                >
                  {item.meta}
                </Text>
              </Box>
            );
          }}
        />
      )}
    </MeasuredBox>
  );
};

const RunDetail: FC<{
  run: UIWorkflowRun;
  now: number;
  stopRequested: boolean;
}> = ({ run, now, stopRequested }) => {
  const rows = useMemo(() => buildRunDetailRows(run), [run]);
  const duration = workflowRunDurationMs(run, now);
  const usage = buildUsageLine(run);

  return (
    <Box
      flexDirection="column"
      flexGrow={1}
      flexShrink={1}
      minWidth={0}
      minHeight={0}
    >
      <Box flexDirection="column" flexShrink={0} minWidth={0}>
        <Text bold color="$primary" wrap="truncate-end">
          {run.name || run.runId}
        </Text>
        {run.description ? (
          <Text color="$muted" wrap="truncate-end">
            {singleLine(run.description)}
          </Text>
        ) : null}
        <Text wrap="truncate-end">
          <Text bold>Status:</Text>{" "}
          <Text color={workflowStatusColor(run.status)}>
            {workflowStatusLabel(run.status)}
          </Text>
          {stopRequested && run.status === "running" ? (
            <Text color="$warning"> · stop requested</Text>
          ) : null}
          <Text color="$muted">{` · ${run.runId}`}</Text>
        </Text>
        {run.phases.length > 0 ? (
          <Text wrap="truncate-end">
            <Text bold>Phases:</Text>{" "}
            {run.phases.map((phase, index) => (
              <Text
                key={`${phase.workflow}/${phase.title}`}
                color={
                  phase.title === run.currentPhase && run.status === "running"
                    ? "$primary"
                    : undefined
                }
              >
                {index > 0 ? " → " : ""}
                {phaseTitle(phase.title, phase.workflow)}
              </Text>
            ))}
          </Text>
        ) : null}
        <Text wrap="truncate-end">
          <Text bold>Agents:</Text> {buildCountsLine(run)}
        </Text>
        <Text wrap="truncate-end">
          <Text bold>Time:</Text> {duration > 0 ? formatDurationMs(duration) : "—"}
          {usage ? ` · ${usage}` : ""}
        </Text>
      </Box>

      <MeasuredBox
        marginTop={1}
        flexDirection="column"
        flexGrow={1}
        flexShrink={1}
        minHeight={0}
        minWidth={0}
        overflow="hidden"
      >
        {({ height }) => (
          <ListView
            items={rows}
            height={Math.max(1, height)}
            nav
            active
            estimateHeight={1}
            overflowIndicator
            getKey={(row) => row.key}
            renderItem={(row, _index, meta) => (
              <RunDetailLine row={row} isCursor={meta.isCursor} />
            )}
          />
        )}
      </MeasuredBox>
    </Box>
  );
};

const RunDetailLine: FC<{ row: RunDetailRow; isCursor: boolean }> = ({
  row,
  isCursor,
}) => {
  const background = isCursor ? "$selectionbg" : undefined;

  switch (row.kind) {
    case "gap":
      return <Text> </Text>;
    case "heading":
      return (
        <Box backgroundColor={background} minWidth={0}>
          <Text bold color="$accent" wrap="truncate-end">
            {row.text}
            {row.note ? <Text color="$muted">{`  ${row.note}`}</Text> : null}
          </Text>
        </Box>
      );
    case "agent": {
      const { agent } = row;
      const color = workflowStatusColor(agent.status);
      return (
        <Box
          flexDirection="column"
          backgroundColor={background}
          paddingLeft={1}
          minWidth={0}
        >
          <Text wrap="truncate-end">
            <Text color={color}>{workflowAgentGlyph(agent.status)}</Text>{" "}
            {truncateEnd(singleLine(workflowAgentLabel(agent)), LABEL_CHARS)}
            <Text color="$muted">
              {agent.durationMs > 0
                ? `  ${formatDurationMs(agent.durationMs)}`
                : ""}
              {agent.status === "cached" ? "  replayed" : ""}
              {agent.status === "queued" ? "  queued" : ""}
            </Text>
          </Text>
          {agent.error ? (
            <Text color="$error" wrap="truncate-end">
              {`  ${truncateEnd(singleLine(agent.error), ERROR_SNIPPET_CHARS)}`}
            </Text>
          ) : null}
        </Box>
      );
    }
    case "text":
      return (
        <Box backgroundColor={background} paddingLeft={1} minWidth={0}>
          <Text color={row.color} wrap="wrap">
            {expandTabs(row.text)}
          </Text>
        </Box>
      );
  }
};

const SavedDetail: FC<{ workflow: SavedWorkflowPayload }> = ({ workflow }) => (
  <Box
    marginTop={1}
    flexDirection="column"
    flexGrow={1}
    flexShrink={1}
    minWidth={0}
    minHeight={0}
    overflow="scroll"
  >
    <Text bold color="$primary" wrap="truncate-end">
      {workflow.name}
    </Text>
    <Text color="$muted">
      Saved {workflow.scope === "user" ? "for this user" : "in this project"}
    </Text>
    <Box marginTop={1} flexDirection="column" minWidth={0}>
      <Text bold>Description</Text>
      <Text wrap="wrap" color={workflow.description ? undefined : "$muted"}>
        {workflow.description?.trim() || "None given."}
      </Text>
    </Box>
    <Box marginTop={1} flexDirection="column" minWidth={0}>
      <Text bold>When to use</Text>
      <Text wrap="wrap" color={workflow.when_to_use ? undefined : "$muted"}>
        {workflow.when_to_use?.trim() || "None given."}
      </Text>
    </Box>
    <Box marginTop={1} flexDirection="column" minWidth={0}>
      <Text bold>Path</Text>
      <Text wrap="wrap">{workflow.path}</Text>
    </Box>
  </Box>
);

const FooterHint: FC<{
  view: "list" | "detail";
  canStop: boolean;
  canGoBack: boolean;
}> = ({ view, canStop, canGoBack }) => {
  const stopHint = canStop ? (
    <>
      {" · "}
      <Text color="$primary" bold>
        X
      </Text>{" "}
      stop
    </>
  ) : null;

  if (view === "list") {
    return (
      <Box marginTop={1} flexDirection="column" flexShrink={0}>
        <Text color="$fg">
          <Text color="$primary" bold>
            Enter
          </Text>{" "}
          open · <Text color="$primary" bold>Up/Down</Text> change selection
          {stopHint} · <Text color="$primary" bold>Esc</Text> or{" "}
          <Text color="$primary" bold>
            Q
          </Text>{" "}
          close
        </Text>
      </Box>
    );
  }

  return (
    <Box marginTop={1} flexDirection="column" flexShrink={0}>
      <Text color="$fg">
        <Text color="$primary" bold>
          Up/Down
        </Text>{" "}
        scroll · <Text color="$primary" bold>Left</Text>{" "}
        {canGoBack ? "back" : "close"}
        {stopHint} · <Text color="$primary" bold>Esc</Text>{" "}
        {canGoBack ? "back" : "close"}
      </Text>
    </Box>
  );
};

/**
 * The current time, ticking each second while `active`. A running run's
 * duration keeps growing even when the engine has nothing new to send.
 */
function useClockWhile(active: boolean): number {
  const [now, setNow] = useState(() => Date.now());

  useEffect(() => {
    if (!active) {
      return;
    }
    setNow(Date.now());
    const timer = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(timer);
  }, [active]);

  return now;
}

function buildWorkflowItems(
  runs: UIWorkflowRun[],
  saved: SavedWorkflowPayload[],
  now: number,
): WorkflowListItem[] {
  const runItems = runs.map((run, index) => ({
    key: `run:${run.runId}`,
    kind: "run" as const,
    id: run.runId,
    title: run.name || run.runId,
    status: run.status,
    meta: buildRunMeta(run, now),
    section: index === 0 ? "Runs" : undefined,
  }));

  const savedItems = [...saved]
    .sort((left, right) => left.name.localeCompare(right.name))
    .map((workflow, index) => ({
      key: `saved:${workflow.name}`,
      kind: "saved" as const,
      id: workflow.name,
      title: workflow.name,
      status: workflow.scope,
      meta: singleLine(
        workflow.when_to_use || workflow.description || workflow.path,
      ),
      section: index === 0 ? "Saved" : undefined,
    }));

  return [...runItems, ...savedItems];
}

function buildRunMeta(run: UIWorkflowRun, now: number): string {
  const parts = [
    run.runId,
    `${workflowDoneCount(run)}/${run.agentCount} agents`,
  ];

  if (run.failed > 0) {
    parts.push(`${run.failed} failed`);
  }
  if (run.status === "running" && run.currentPhase) {
    parts.push(run.currentPhase);
  }
  const duration = workflowRunDurationMs(run, now);
  if (duration > 0) {
    parts.push(formatDurationMs(duration));
  }
  if (run.status === "failed" && run.error) {
    parts.push(singleLine(run.error));
  }

  return parts.join(" · ");
}

function buildCountsLine(run: UIWorkflowRun): string {
  const parts = [`${workflowDoneCount(run)}/${run.agentCount} done`];
  const counts: [number, string][] = [
    [run.running, "running"],
    [run.queued, "queued"],
    [run.succeeded, "succeeded"],
    [run.cached, "replayed"],
    [run.failed, "failed"],
    [run.stopped, "stopped"],
  ];
  for (const [count, label] of counts) {
    if (count > 0) {
      parts.push(`${count} ${label}`);
    }
  }
  return parts.join(" · ");
}

function buildUsageLine(run: UIWorkflowRun): string | null {
  if (!run.totalCostUsd && !run.inputTokens && !run.outputTokens) {
    return null;
  }
  return `${formatTokenCount(run.inputTokens)}↑ ${formatTokenCount(run.outputTokens)}↓ · $${run.totalCostUsd.toFixed(4)}`;
}

function phaseTitle(title: string, workflow: string): string {
  return workflow ? `${workflow} › ${title}` : title;
}

/**
 * The scrollable part of a run's detail: its agents grouped under their
 * phases, then what the script logged, the result or error, and the files
 * the run keeps on disk.
 */
function buildRunDetailRows(run: UIWorkflowRun): RunDetailRow[] {
  const rows: RunDetailRow[] = [];

  for (const group of groupAgentsByPhase(run)) {
    const done = group.agents.filter(
      (agent) => agent.status !== "running" && agent.status !== "queued",
    ).length;
    rows.push({
      key: `phase:${group.key}`,
      kind: "heading",
      text: group.title,
      note: `${done}/${group.agents.length}`,
    });
    for (const agent of group.agents) {
      rows.push({ key: `agent:${agent.index}`, kind: "agent", agent });
    }
    rows.push({ key: `gap:${group.key}`, kind: "gap" });
  }

  if (run.agentCount === 0) {
    rows.push({
      key: "agents:none",
      kind: "text",
      text: "No agents started yet.",
      color: "$muted",
    });
    rows.push({ key: "gap:agents", kind: "gap" });
  } else if (run.agents.length < run.agentCount) {
    rows.push({
      key: "agents:capped",
      kind: "text",
      text: `Showing ${run.agents.length} of ${run.agentCount} agents: the running ones, then the failed ones, then the latest.`,
      color: "$muted",
    });
    rows.push({ key: "gap:agents", kind: "gap" });
  }

  if (run.logs.length > 0) {
    rows.push({ key: "logs", kind: "heading", text: "Log" });
    run.logs.forEach((line, index) => {
      rows.push({ key: `log:${index}`, kind: "text", text: line, color: "$muted" });
    });
    rows.push({ key: "gap:logs", kind: "gap" });
  }

  if (run.error) {
    rows.push({ key: "error", kind: "heading", text: "Error" });
    pushTextLines(rows, "error", run.error, "$error");
    rows.push({ key: "gap:error", kind: "gap" });
  }

  if (run.resultPreview) {
    rows.push({ key: "result", kind: "heading", text: "Result" });
    pushTextLines(rows, "result", run.resultPreview);
    rows.push({ key: "gap:result", kind: "gap" });
  }

  if (run.warnings.length > 0) {
    rows.push({ key: "warnings", kind: "heading", text: "Warnings" });
    run.warnings.forEach((warning, index) => {
      rows.push({
        key: `warning:${index}`,
        kind: "text",
        text: warning,
        color: "$warning",
      });
    });
    rows.push({ key: "gap:warnings", kind: "gap" });
  }

  const files: [string, string][] = [
    ["Script", run.scriptPath],
    ["Journal", run.journalPath],
    ["Result", run.resultPath],
  ];
  const presentFiles = files.filter(([, path]) => path.length > 0);
  if (presentFiles.length > 0) {
    rows.push({ key: "files", kind: "heading", text: "Files" });
    for (const [label, path] of presentFiles) {
      rows.push({ key: `file:${label}`, kind: "text", text: `${label}: ${path}` });
    }
  }

  return rows;
}

function pushTextLines(
  rows: RunDetailRow[],
  keyPrefix: string,
  text: string,
  color?: "$muted" | "$error" | "$warning",
) {
  text.split("\n").forEach((line, index) => {
    rows.push({
      key: `${keyPrefix}:${index}`,
      kind: "text",
      // An empty Text would collapse to no height and swallow the blank line.
      text: line.length > 0 ? line : " ",
      color,
    });
  });
}

interface AgentGroup {
  key: string;
  title: string;
  agents: UIWorkflowAgent[];
}

/**
 * The run's agents under the phase each was started in, phases in the order
 * the run first reached them. Agents of a nested workflow group under that
 * workflow's phases; agents started outside any phase come last.
 */
function groupAgentsByPhase(run: UIWorkflowRun): AgentGroup[] {
  const groups = new Map<string, AgentGroup>();
  for (const phase of run.phases) {
    const key = `${phase.workflow}/${phase.title}`;
    if (!groups.has(key)) {
      groups.set(key, {
        key,
        title: phaseTitle(phase.title, phase.workflow),
        agents: [],
      });
    }
  }

  const sortedAgents = [...run.agents].sort(
    (left, right) => left.index - right.index,
  );
  for (const agent of sortedAgents) {
    const key = agent.phase ? `${agent.workflow}/${agent.phase}` : "";
    let group = groups.get(key);
    if (!group) {
      group = {
        key,
        title: agent.phase
          ? phaseTitle(agent.phase, agent.workflow)
          : "No phase",
        agents: [],
      };
      groups.set(key, group);
    }
    group.agents.push(agent);
  }

  const withAgents = [...groups.values()].filter(
    (group) => group.agents.length > 0,
  );
  // Map keeps insertion order, which is phase order, except that an agent
  // with no phase may have opened its group first; that group goes last.
  return [
    ...withAgents.filter((group) => group.key !== ""),
    ...withAgents.filter((group) => group.key === ""),
  ];
}

function itemStatusLabel(item: WorkflowListItem): string {
  if (item.kind === "saved") {
    return item.status === "user" ? "USER" : "PROJECT";
  }
  return workflowStatusLabel(item.status);
}

function itemStatusColor(item: WorkflowListItem): string {
  if (item.kind === "saved") {
    return "$accent";
  }
  return workflowStatusColor(item.status);
}
