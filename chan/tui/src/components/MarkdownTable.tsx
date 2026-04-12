import React, { type FC, useMemo } from "react";
import { Box, Text } from "ink";
import type { Tokens } from "marked";
import { displayWidth, formatToken, padAligned } from "../utils/markdown.js";

interface MarkdownTableProps {
  token: Tokens.Table;
}

function renderCell(tokens: Tokens.TableCell["tokens"]): string {
  return (tokens ?? [])
    .map((token) => formatToken(token))
    .join("")
    .trim();
}

const MarkdownTable: FC<MarkdownTableProps> = ({ token }) => {
  const rendered = useMemo(() => {
    const headers = token.header.map((cell) => renderCell(cell.tokens));
    const rows = token.rows.map((row) =>
      row.map((cell) => renderCell(cell.tokens)),
    );
    const terminalWidth = process.stdout.columns ?? 80;

    const columnWidths = headers.map((header, index) => {
      const rowWidths = rows.map((row) => displayWidth(row[index] ?? ""));
      return Math.max(displayWidth(header), ...rowWidths, 3);
    });

    const totalWidth =
      columnWidths.reduce((sum, width) => sum + width, 0) +
      columnWidths.length * 3 +
      1;

    if (totalWidth > terminalWidth - 4) {
      return rows
        .map((row, rowIndex) => {
          const lines = row.map((cell, cellIndex) => {
            const label = headers[cellIndex] || `Column ${cellIndex + 1}`;
            return `${label}: ${cell}`;
          });

          if (rowIndex === 0) {
            return lines.join("\n");
          }

          return ["─".repeat(Math.max(20, terminalWidth - 6)), ...lines].join(
            "\n",
          );
        })
        .join("\n");
    }

    const border = (
      left: string,
      middle: string,
      join: string,
      right: string,
    ) =>
      `${left}${columnWidths
        .map((width) => middle.repeat(width + 2))
        .join(join)}${right}`;

    const renderRow = (cells: string[], isHeader: boolean) => {
      return `│ ${cells
        .map((cell, index) => {
          const align = isHeader ? "center" : (token.align[index] ?? "left");
          return padAligned(
            cell,
            displayWidth(cell),
            columnWidths[index]!,
            align,
          );
        })
        .join(" │ ")} │`;
    };

    return [
      border("┌", "─", "┬", "┐"),
      renderRow(headers, true),
      border("├", "─", "┼", "┤"),
      ...rows.map((row) => renderRow(row, false)),
      border("└", "─", "┴", "┘"),
    ].join("\n");
  }, [token]);

  return (
    <Box>
      <Text>{rendered}</Text>
    </Box>
  );
};

export default MarkdownTable;
