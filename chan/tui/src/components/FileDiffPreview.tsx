import React, { type FC } from "react";
import { Box, Text } from "ink";

interface FileDiffPreviewProps {
  filePath?: string;
  preview?: string;
  insertions?: number;
  deletions?: number;
}

const DIFF_PREFIX = "     ";

const FileDiffPreview: FC<FileDiffPreviewProps> = ({
  filePath,
  preview,
  insertions,
  deletions,
}) => {
  const statLine = formatMutationStats(insertions, deletions);
  const diffLines = preview ? preview.split("\n") : [];

  if (!statLine && diffLines.length === 0) {
    return <Text color="green">Updated file.</Text>;
  }

  return (
    <Box flexDirection="column">
      {statLine ? <Text>{statLine}</Text> : null}
      {diffLines.length > 0 ? (
        <Box flexDirection="column" borderStyle="round" borderColor="gray">
          {filePath ? (
            <Box paddingX={1}>
              <Text dimColor>{filePath}</Text>
            </Box>
          ) : null}
          <Box flexDirection="column" paddingX={1}>
            {diffLines.map((line, index) => (
              <Text key={`${line}-${index}`} color={diffLineColor(line)}>
                {DIFF_PREFIX}
                {line || " "}
              </Text>
            ))}
          </Box>
        </Box>
      ) : null}
    </Box>
  );
};

export default FileDiffPreview;

function diffLineColor(line: string): "green" | "red" | "yellow" | undefined {
  if (line.startsWith("+")) {
    return "green";
  }
  if (line.startsWith("-")) {
    return "red";
  }
  if (line.startsWith("@@") || line === "...") {
    return "yellow";
  }
  return undefined;
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
