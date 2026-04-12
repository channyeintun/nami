import React, { type FC } from "react";
import { Box, Text } from "ink";
import type { UIArtifact } from "../hooks/useEvents.js";
import MarkdownText from "./MarkdownText.js";

interface ArtifactViewProps {
  artifacts: UIArtifact[];
  focusedArtifactId?: string | null;
}

const kindLabel: Record<string, string> = {
  "task-list": "Task List",
  "implementation-plan": "Implementation Plan",
  walkthrough: "Walkthrough",
  "tool-log": "Tool Log",
  "search-report": "Search Report",
  "diff-preview": "Diff Preview",
  diagram: "Diagram",
  "codegen-output": "Codegen Output",
  "compact-summary": "Compact Summary",
  "knowledge-item": "Knowledge Item",
};

function artifactMeta(artifact: UIArtifact): string {
  const parts: string[] = [];
  const label = kindLabel[artifact.kind] ?? artifact.kind;
  parts.push(label);
  if (artifact.version > 0) {
    parts.push(`v${artifact.version}`);
  }
  if (artifact.scope) {
    parts.push(artifact.scope);
  }
  if (artifact.source) {
    parts.push(`src:${artifact.source}`);
  }
  if (artifact.status) {
    parts.push(`[${artifact.status}]`);
  }
  return parts.join("  ·  ");
}

function statusColor(status: string): string {
  switch (status) {
    case "final":
      return "green";
    case "draft":
      return "yellow";
    default:
      return "gray";
  }
}

const ArtifactView: FC<ArtifactViewProps> = ({
  artifacts,
  focusedArtifactId,
}) => {
  if (artifacts.length === 0) return null;

  return (
    <Box flexDirection="column" marginTop={1}>
      {artifacts.map((artifact, index) => (
        <Box
          key={artifact.id}
          flexDirection="column"
          borderStyle="round"
          borderColor={artifact.id === focusedArtifactId ? "cyan" : "gray"}
          paddingX={1}
          marginTop={index === 0 ? 0 : 1}
        >
          <Box flexDirection="row" gap={2}>
            {artifact.id === focusedArtifactId ? (
              <Text color="cyan">FOCUSED</Text>
            ) : null}
            <Text bold>{artifact.title}</Text>
            <Text color={statusColor(artifact.status)}>
              {artifactMeta(artifact)}
            </Text>
          </Box>
          <Box marginTop={1}>
            {artifact.content.trim() ? (
              <MarkdownText text={artifact.content} />
            ) : (
              <Text color="gray">(loading…)</Text>
            )}
          </Box>
        </Box>
      ))}
    </Box>
  );
};

export default ArtifactView;
