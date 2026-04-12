import React, { type FC } from "react";
import { Box, Text } from "ink";
import type { UIToolCall } from "../hooks/useEvents.js";
import { formatSubagentType } from "../utils/subagentLabels.js";
import FileDiffPreview from "./FileDiffPreview.js";
import MarkdownText from "./MarkdownText.js";
import MessageRow from "./MessageRow.js";

interface ToolProgressProps {
  toolCall: UIToolCall;
}

const RESPONSE_PREFIX = "  ⎿  ";

interface ToolDescriptor {
  title: string;
  summary: string;
}

function summarizeInput(name: string, raw: string): string {
  try {
    const obj = JSON.parse(raw);
    if (name === "bash" && obj.command) return obj.command;
    if (name === "agent" && obj.description) return obj.description;
    if ((name === "agent_status" || name === "agent_stop") && obj.agent_id)
      return obj.agent_id;
    if (
      (name === "create_file" ||
        name === "file_read" ||
        name === "file_write" ||
        name === "file_edit") &&
      obj.file_path
    )
      return obj.file_path;
    if (name === "multi_replace_file_content" && obj.target_file) {
      return obj.target_file;
    }
    if (name === "apply_patch" && typeof obj.patch === "string") {
      return summarizePatchTarget(obj.patch);
    }
    if (name === "glob" && obj.pattern) return obj.pattern;
    if (name === "grep" && obj.pattern) return obj.pattern;
    if (name === "git" && obj.subcommand) return obj.subcommand;
    if (name === "web_search" && obj.query) return obj.query;
    if (name === "web_fetch" && obj.url) return obj.url;
  } catch {
    // ignore
  }
  return raw.length > 60 ? raw.slice(0, 57) + "..." : raw;
}

const ToolProgress: FC<ToolProgressProps> = ({ toolCall }) => {
  const descriptor = describeTool(toolCall);
  const headerColor =
    toolCall.status === "error"
      ? "red"
      : toolCall.status === "completed"
        ? "green"
        : undefined;
  const isDim =
    toolCall.status === "running" || toolCall.status === "waiting_permission";
  const response = renderResponse(toolCall);

  return (
    <MessageRow markerColor={headerColor} markerDim={isDim}>
      <Text color={headerColor} dimColor={isDim}>
        <Text bold>{descriptor.title}</Text>
        {descriptor.summary ? ` (${descriptor.summary})` : ""}
      </Text>
      {response ? (
        <Box flexDirection="row">
          <Text dimColor>{RESPONSE_PREFIX}</Text>
          <Box flexGrow={1}>{response}</Box>
        </Box>
      ) : null}
    </MessageRow>
  );
};

export default ToolProgress;

export { describeTool };

function renderResponse(toolCall: UIToolCall) {
  if (toolCall.status === "waiting_permission") {
    return <Text dimColor>{permissionLabel(toolCall)}</Text>;
  }

  if (toolCall.status === "running") {
    return <Text dimColor>{runningLabel(toolCall)}</Text>;
  }

  if (toolCall.status === "error") {
    return renderError(toolCall);
  }

  return renderSuccess(toolCall);
}

// Keep generic tool previews tall enough to show a couple of meaningful lines
// while preventing one verbose result from crowding out the surrounding turn.
const MAX_SUMMARIZED_OUTPUT_LINES = 6;
// Stay under roughly a full terminal card on common widths so preview text
// remains scannable before the user drills into the full transcript or artifact.
const MAX_SUMMARIZED_OUTPUT_CHARS = 320;

function describeTool(toolCall: UIToolCall): ToolDescriptor {
  switch (toolCall.name) {
    case "bash":
      return {
        title: "Bash",
        summary: summarizeInput(toolCall.name, toolCall.input),
      };
    case "file_read":
      return {
        title: "Read File",
        summary: basenameOrFallback(
          summarizeInput(toolCall.name, toolCall.input),
        ),
      };
    case "create_file":
      return {
        title: "Create File",
        summary: basenameOrFallback(
          summarizeInput(toolCall.name, toolCall.input),
        ),
      };
    case "file_write":
      return {
        title: "Overwrite File",
        summary: basenameOrFallback(
          summarizeInput(toolCall.name, toolCall.input),
        ),
      };
    case "file_edit":
      return {
        title: "Edit File",
        summary: basenameOrFallback(
          summarizeInput(toolCall.name, toolCall.input),
        ),
      };
    case "apply_patch":
      return {
        title: "Apply Patch",
        summary: basenameOrFallback(
          summarizeInput(toolCall.name, toolCall.input),
        ),
      };
    case "multi_replace_file_content":
      return {
        title: "Multi Replace",
        summary: basenameOrFallback(
          summarizeInput(toolCall.name, toolCall.input),
        ),
      };
    case "grep":
      return {
        title: "Search Files",
        summary: summarizeInput(toolCall.name, toolCall.input),
      };
    case "glob":
      return {
        title: "Find Files",
        summary: summarizeInput(toolCall.name, toolCall.input),
      };
    case "git":
      return { title: "Git", summary: summarizeGitInput(toolCall.input) };
    case "agent":
      return {
        title: agentToolTitle(toolCall.input),
        summary: summarizeInput(toolCall.name, toolCall.input),
      };
    case "agent_status":
      return {
        title: "Agent Status",
        summary: summarizeInput(toolCall.name, toolCall.input),
      };
    case "agent_stop":
      return {
        title: "Stop Agent",
        summary: summarizeInput(toolCall.name, toolCall.input),
      };
    case "web_search":
      return {
        title: "Web Search",
        summary: summarizeInput(toolCall.name, toolCall.input),
      };
    case "web_fetch":
      return {
        title: "Fetch URL",
        summary: summarizeInput(toolCall.name, toolCall.input),
      };
    default:
      return {
        title: toolCall.name,
        summary: summarizeInput(toolCall.name, toolCall.input),
      };
  }
}

function permissionLabel(toolCall: UIToolCall): string {
  switch (toolCall.name) {
    case "bash":
      return "Waiting for permission to run command…";
    case "create_file":
      return "Waiting for permission to create file…";
    case "file_write":
      return "Waiting for permission to overwrite file…";
    case "file_edit":
      return "Waiting for permission to modify file…";
    case "apply_patch":
      return "Waiting for permission to apply patch…";
    case "multi_replace_file_content":
      return "Waiting for permission to modify file ranges…";
    case "web_fetch":
      return "Waiting for permission to fetch URL…";
    default:
      return "Waiting for permission…";
  }
}

function runningLabel(toolCall: UIToolCall): string {
  const progressSuffix =
    toolCall.progressBytes !== undefined
      ? ` ${toolCall.progressBytes} bytes processed`
      : "";

  switch (toolCall.name) {
    case "bash":
      return `Running command…${progressSuffix}`;
    case "file_read":
      return `Reading file…${progressSuffix}`;
    case "create_file":
      return `Creating file…${progressSuffix}`;
    case "file_write":
      return `Overwriting file…${progressSuffix}`;
    case "file_edit":
      return `Editing file…${progressSuffix}`;
    case "apply_patch":
      return `Applying patch…${progressSuffix}`;
    case "multi_replace_file_content":
      return `Replacing file ranges…${progressSuffix}`;
    case "grep":
      return `Searching files…${progressSuffix}`;
    case "glob":
      return `Finding files…${progressSuffix}`;
    case "git":
      return `Running git command…${progressSuffix}`;
    case "agent":
      return `Launching child agent…${progressSuffix}`;
    case "agent_status":
      return `Checking child agent status…${progressSuffix}`;
    case "agent_stop":
      return `Stopping child agent…${progressSuffix}`;
    case "web_search":
      return `Searching the web…${progressSuffix}`;
    case "web_fetch":
      return `Fetching page content…${progressSuffix}`;
    default:
      return `Working…${progressSuffix}`;
  }
}

function renderError(toolCall: UIToolCall) {
  if (
    toolCall.name === "create_file" ||
    toolCall.name === "file_write" ||
    toolCall.name === "file_edit" ||
    toolCall.name === "apply_patch" ||
    toolCall.name === "multi_replace_file_content"
  ) {
    return renderFileMutationError(toolCall);
  }

  if (toolCall.name === "bash") {
    return (
      <MarkdownText
        text={summarizeOutput(toolCall.error ?? "Command failed")}
      />
    );
  }

  return (
    <Text color="red">{summarizeOutput(toolCall.error ?? "Tool failed")}</Text>
  );
}

function renderSuccess(toolCall: UIToolCall) {
  switch (toolCall.name) {
    case "file_write":
    case "create_file":
    case "file_edit":
    case "apply_patch":
    case "multi_replace_file_content":
      return renderFileMutation(toolCall);
    case "file_read":
      return (
        <MarkdownText
          text={summarizeFileRead(toolCall.output, toolCall.truncated)}
        />
      );
    case "grep":
      return (
        <MarkdownText
          text={summarizeSearchMatches(
            toolCall.output,
            toolCall.truncated,
            "match",
          )}
        />
      );
    case "glob":
      return (
        <MarkdownText
          text={summarizeSearchMatches(
            toolCall.output,
            toolCall.truncated,
            "file",
          )}
        />
      );
    case "web_search":
      return <MarkdownText text={summarizeWebSearch(toolCall.output)} />;
    case "web_fetch":
      return (
        <MarkdownText
          text={summarizeWebFetch(toolCall.output, toolCall.truncated)}
        />
      );
    case "git":
      return (
        <MarkdownText
          text={summarizeGitOutput(toolCall.output, toolCall.truncated)}
        />
      );
    case "agent":
    case "agent_status":
    case "agent_stop":
      return <MarkdownText text={summarizeAgentOutput(toolCall.output)} />;
    case "bash":
      return (
        <MarkdownText
          text={summarizeShellOutput(toolCall.output, toolCall.truncated)}
        />
      );
    default:
      if (!toolCall.output) {
        return <Text color="green">Completed.</Text>;
      }
      return (
        <MarkdownText
          text={summarizeOutput(toolCall.output, toolCall.truncated)}
        />
      );
  }
}

function renderFileMutationError(toolCall: UIToolCall) {
  const summary = summarizeOutput(toolCall.error ?? "Tool failed");
  const kindLabel = toolCall.errorKind
    ? toolCall.errorKind.replaceAll("_", " ")
    : null;
  return (
    <Box flexDirection="column">
      <Text color="red">
        File update failed{kindLabel ? ` (${kindLabel})` : ""}: {summary}
      </Text>
      {toolCall.errorHint ? (
        <Text color="yellow">Recovery: {toolCall.errorHint}</Text>
      ) : null}
    </Box>
  );
}

interface AgentResultSummary {
  status?: string;
  invocation_id?: string;
  agent_id?: string;
  subagent_type?: string;
  session_id?: string;
  transcript_path?: string;
  output_file?: string;
  summary?: string;
  error?: string;
  metadata?: {
    invocation_id?: string;
    agent_id?: string;
    description?: string;
    subagent_type?: string;
    lifecycle_state?: string;
    status_message?: string;
    stop_block_reason?: string;
    stop_block_count?: number;
    session_id?: string;
    transcript_path?: string;
    result_path?: string;
    tools?: string[];
  };
}

function summarizeAgentOutput(output?: string): string {
  if (!output) {
    return "Agent call completed.";
  }

  try {
    const result = JSON.parse(output) as AgentResultSummary;
    const lines: string[] = [];
    const status = summarizeAgentStatus(result.status);
    const metadata = result.metadata;
    lines.push(status);

    if (result.summary) {
      lines.push(result.summary.trim());
    }
    if (
      metadata?.status_message &&
      metadata.status_message !== result.summary
    ) {
      lines.push(metadata.status_message.trim());
    }
    if (result.error) {
      lines.push(`Error: ${result.error.trim()}`);
    }
    if (result.invocation_id || metadata?.invocation_id || result.session_id) {
      lines.push(
        `Invocation: ${result.invocation_id || metadata?.invocation_id || result.session_id}`,
      );
    }
    if (result.agent_id) {
      lines.push(`Agent ID: ${result.agent_id}`);
    }
    if (result.subagent_type || metadata?.subagent_type) {
      lines.push(
        `Type: ${formatSubagentType(result.subagent_type || metadata?.subagent_type || "")}`,
      );
    }
    if (result.session_id) {
      lines.push(`Session: ${result.session_id}`);
    }
    if (result.transcript_path || metadata?.transcript_path) {
      lines.push(
        `Transcript: ${basenameOrFallback(result.transcript_path || metadata?.transcript_path || "")}`,
      );
    }
    if (result.output_file || metadata?.result_path) {
      lines.push(
        `Result file: ${basenameOrFallback(result.output_file || metadata?.result_path || "")}`,
      );
    }
    if (metadata?.lifecycle_state) {
      lines.push(`Lifecycle: ${metadata.lifecycle_state}`);
    }
    if (metadata?.stop_block_reason) {
      lines.push(`Stop blocked: ${metadata.stop_block_reason}`);
    }
    if (
      typeof metadata?.stop_block_count === "number" &&
      metadata.stop_block_count > 0
    ) {
      lines.push(`Stop blocks: ${metadata.stop_block_count}`);
    }
    if (Array.isArray(metadata?.tools) && metadata.tools.length > 0) {
      lines.push(`Tools: ${metadata.tools.join(", ")}`);
    }

    return lines.join("\n");
  } catch {
    return summarizeOutput(output);
  }
}

function summarizeAgentStatus(status?: string): string {
  switch (status) {
    case "async_launched":
      return "Launched background child agent.";
    case "running":
      return "Background child agent is still running.";
    case "cancelling":
      return "Cancellation requested for background child agent.";
    case "completed":
      return "Background child agent completed.";
    case "cancelled":
      return "Background child agent cancelled.";
    case "failed":
      return "Background child agent failed.";
    default:
      return "Agent call completed.";
  }
}

function agentToolTitle(rawInput: string): string {
  try {
    const input = JSON.parse(rawInput) as { subagent_type?: string };
    const subagentType = formatSubagentType(input.subagent_type ?? "");
    return subagentType ? `${subagentType} Agent` : "Agent";
  } catch {
    return "Agent";
  }
}

function summarizeOutput(raw: string, truncated?: boolean): string {
  const trimmed = raw.trim();
  if (!trimmed) {
    return truncated ? "Completed. Output truncated." : "Completed.";
  }

  const lines = trimmed.split("\n");
  const clippedLines = lines.slice(0, MAX_SUMMARIZED_OUTPUT_LINES);
  const clipped = clippedLines.join("\n");
  const shortened =
    clipped.length > MAX_SUMMARIZED_OUTPUT_CHARS
      ? `${clipped.slice(0, MAX_SUMMARIZED_OUTPUT_CHARS - 3)}...`
      : clipped;

  if (
    lines.length > clippedLines.length ||
    clipped.length < trimmed.length ||
    truncated
  ) {
    return `${shortened}\n\n_Output truncated._`;
  }

  return shortened;
}

function summarizeFileMutation(toolCall: UIToolCall): string {
  const parts: string[] = [];

  if (toolCall.output) {
    parts.push(summarizeOutput(toolCall.output, toolCall.truncated));
  } else {
    parts.push(
      toolCall.truncated ? "Completed. Output truncated." : "Completed.",
    );
  }

  const statLine = formatMutationStats(toolCall.insertions, toolCall.deletions);
  if (statLine) {
    parts.push(statLine);
  }

  if (toolCall.preview) {
    parts.push(["```diff", toolCall.preview, "```"].join("\n"));
  }

  return parts.join("\n\n");
}

function renderFileMutation(toolCall: UIToolCall) {
  if (
    toolCall.preview ||
    toolCall.insertions ||
    toolCall.deletions ||
    toolCall.diagnostics
  ) {
    return (
      <Box flexDirection="column">
        <FileDiffPreview
          filePath={
            toolCall.filePath || summarizeInput(toolCall.name, toolCall.input)
          }
          preview={toolCall.preview}
          insertions={toolCall.insertions}
          deletions={toolCall.deletions}
        />
        {toolCall.diagnostics ? (
          <MarkdownText text={summarizeDiagnostics(toolCall.diagnostics)} />
        ) : null}
      </Box>
    );
  }

  return <MarkdownText text={summarizeFileMutation(toolCall)} />;
}

function summarizeDiagnostics(raw?: string): string {
  if (!raw) {
    return "";
  }
  return [`Diagnostics after edit:`, "", raw].join("\n");
}

function summarizeFileRead(raw?: string, truncated?: boolean): string {
  if (!raw) {
    return truncated ? "Read completed. Output truncated." : "Read completed.";
  }
  return summarizeOutput(raw, truncated);
}

function summarizeSearchMatches(
  raw?: string,
  truncated?: boolean,
  noun?: string,
): string {
  if (!raw) {
    return truncated
      ? `Results truncated for ${noun ?? "result"} search.`
      : "No output.";
  }
  const trimmed = raw.trim();
  if (trimmed === "No matches found" || trimmed === "No files found") {
    return trimmed;
  }

  const lines = trimmed.split("\n").filter(Boolean);
  const preview = lines.slice(0, 8).join("\n");
  const count = lines.filter((line) => !line.startsWith("(")).length;
  const suffix =
    count > 0 ? `Found ${count} ${count === 1 ? noun : `${noun}s`}.\n\n` : "";
  return `${suffix}${summarizeOutput(preview, truncated || lines.length > 8)}`;
}

function summarizeWebSearch(raw?: string): string {
  if (!raw) {
    return "Search completed.";
  }
  const lines = raw.trim().split("\n");
  const preview = lines.slice(0, 10).join("\n");
  return summarizeOutput(preview, lines.length > 10);
}

function summarizeWebFetch(raw?: string, truncated?: boolean): string {
  if (!raw) {
    return truncated
      ? "Fetch completed. Output truncated."
      : "Fetch completed.";
  }
  const lines = raw.trim().split("\n");
  const preview = lines.slice(0, 14).join("\n");
  return summarizeOutput(preview, truncated || lines.length > 14);
}

function summarizeGitOutput(raw?: string, truncated?: boolean): string {
  if (!raw) {
    return truncated
      ? "Git command completed. Output truncated."
      : "Git command completed.";
  }
  return summarizeOutput(raw, truncated);
}

function summarizeShellOutput(raw?: string, truncated?: boolean): string {
  if (!raw) {
    return truncated
      ? "Command completed. Output truncated."
      : "Command completed with no output.";
  }
  return summarizeOutput(raw, truncated);
}

function summarizeGitInput(raw: string): string {
  try {
    const obj = JSON.parse(raw) as {
      operation?: string;
      revision?: string;
      file_path?: string;
    };
    const parts = [obj.operation, obj.revision, obj.file_path].filter(
      (value): value is string => typeof value === "string" && value.length > 0,
    );
    return parts.join(" ");
  } catch {
    return summarizeInput("git", raw);
  }
}

function summarizePatchTarget(patch: string): string {
  const matches = [
    ...patch.matchAll(/^\*\*\* (?:Add|Update|Delete) File:\s+(.+)$/gm),
  ];
  if (matches.length === 0) {
    return "patch";
  }
  if (matches.length === 1) {
    return matches[0]?.[1]?.trim() || "patch";
  }
  return `${matches.length} files`;
}

function basenameOrFallback(value: string): string {
  if (!value) {
    return value;
  }
  const parts = value.split("/");
  return parts[parts.length - 1] || value;
}

function formatMutationStats(insertions?: number, deletions?: number): string {
  const additions = insertions ?? 0;
  const removals = deletions ?? 0;
  const parts: string[] = [];
  if (additions > 0) {
    parts.push(`Added ${additions} ${additions === 1 ? "line" : "lines"}`);
  }
  if (removals > 0) {
    parts.push(`Removed ${removals} ${removals === 1 ? "line" : "lines"}`);
  }
  return parts.join(", ");
}
