import React, { type FC, useMemo } from "react";
import { Box, MeasuredBox, Text } from "silvery";
import type { Tokens } from "marked";
import { formatTable } from "../utils/markdown.js";

interface MarkdownTableProps {
  token: Tokens.Table;
}

interface MarkdownTableBodyProps {
  token: Tokens.Table;
  boxWidth: number;
}

const MarkdownTableBody: FC<MarkdownTableBodyProps> = ({ token, boxWidth }) => {
  const rendered = useMemo(
    () =>
      formatTable(token, Math.max(20, boxWidth || process.stdout.columns || 80)),
    [boxWidth, token],
  );

  return (
    <Box minWidth={0}>
      <Text>{rendered}</Text>
    </Box>
  );
};

// The table sizes its columns against the width actually available to it, so
// the measurement has to come from a box in the tree rather than a render-path
// read. MeasuredBox holds the body back until the first committed layout, which
// keeps the columns from being laid out against a zero rect on first paint.
const MarkdownTable: FC<MarkdownTableProps> = ({ token }) => (
  <MeasuredBox minWidth={0}>
    {({ width }) => <MarkdownTableBody token={token} boxWidth={width} />}
  </MeasuredBox>
);

export default MarkdownTable;
