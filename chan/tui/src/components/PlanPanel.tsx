import React, { type FC } from "react";
import { Box, Text } from "ink";
import MarkdownText from "./MarkdownText.js";

interface PlanPanelProps {
  title: string;
  content: string;
  version?: number;
  source?: string;
  status?: string;
}

const PlanPanel: FC<PlanPanelProps> = ({
  title,
  content,
  version,
  source,
  status,
}) => {
  const body = content.trim();
  if (!body) return null;

  const metaParts: string[] = [];
  if (version && version > 0) metaParts.push(`v${version}`);
  if (source) metaParts.push(`src:${source}`);
  if (status) metaParts.push(`[${status}]`);
  const meta = metaParts.join("  ·  ");

  const statusColor =
    status === "final" ? "green" : status === "draft" ? "yellow" : "gray";

  return (
    <Box
      flexDirection="column"
      borderStyle="round"
      borderColor="blue"
      paddingX={1}
      marginBottom={1}
    >
      <Box flexDirection="row" gap={2}>
        <Text bold color="blue">
          {title}
        </Text>
        {meta ? (
          <Text color={statusColor}>{meta}</Text>
        ) : (
          <Text color="gray">{"Implementation Plan"}</Text>
        )}
      </Box>
      <Box marginTop={1}>
        <MarkdownText text={body} />
      </Box>
    </Box>
  );
};

export default PlanPanel;
