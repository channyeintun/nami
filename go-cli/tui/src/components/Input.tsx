import React, { type FC, useEffect, useMemo, useState } from "react";
import { Box, Text, useInput } from "ink";
import type { PromptController } from "../hooks/usePromptHistory.js";

interface InputProps {
  prompt: PromptController;
  mode: string;
  isLoading: boolean;
  onSubmit: () => void;
  onModeToggle: () => void;
  onCancel: () => void;
  disabled?: boolean;
}

const INPUT_HINT =
  "Enter send | Shift+Enter newline | Arrows move | Tab mode | Esc cancel";
const DISABLED_HINT = "Engine busy | Esc cancel";

function formatModeLabel(mode: string): string {
  return mode === "plan" ? "PLAN" : mode.toUpperCase();
}

function getModeColor(mode: string): "blue" | "green" | "yellow" {
  if (mode === "plan") {
    return "blue";
  }

  if (mode === "fast") {
    return "green";
  }

  return "yellow";
}

function getPromptTextColumns(terminalColumns: number): number {
  return Math.max(8, terminalColumns - 7);
}

function getWrappedLineSegments(value: string, columns: number): string[] {
  const wrapWidth = Math.max(1, columns - 1);
  const logicalLines = value.split("\n");
  const segments: string[] = [];

  for (const line of logicalLines) {
    if (line.length === 0) {
      segments.push("");
      continue;
    }

    for (let offset = 0; offset < line.length; offset += wrapWidth) {
      segments.push(line.slice(offset, offset + wrapWidth));
    }
  }

  return segments;
}

function renderInputLines(
  value: string,
  cursorOffset: number,
  columns: number,
): string[] {
  const wrapWidth = Math.max(1, columns - 1);
  const logicalLines = value.split("\n");
  const renderedLines: string[] = [];
  let lineStartOffset = 0;

  logicalLines.forEach((line, logicalLineIndex) => {
    if (line.length === 0) {
      const isCursorHere = cursorOffset === lineStartOffset;
      renderedLines.push(isCursorHere ? "█" : " ");
    } else {
      for (let start = 0; start < line.length; start += wrapWidth) {
        const end = Math.min(line.length, start + wrapWidth);
        const segmentStart = lineStartOffset + start;
        const segmentEnd = lineStartOffset + end;
        const nextStart = segmentEnd;
        const isLastWrappedSegment = end === line.length;
        const isCursorInside =
          (cursorOffset >= segmentStart && cursorOffset < segmentEnd) ||
          (cursorOffset === segmentEnd && isLastWrappedSegment);

        if (!isCursorInside) {
          renderedLines.push(line.slice(start, end));
          continue;
        }

        const cursorColumn = cursorOffset - segmentStart;
        const rendered =
          line.slice(start, start + cursorColumn) +
          "█" +
          line.slice(start + cursorColumn, end);
        renderedLines.push(rendered);

        if (
          cursorOffset === segmentEnd &&
          !isLastWrappedSegment &&
          nextStart === cursorOffset
        ) {
          // The cursor is exactly on a visual wrap boundary, so render it
          // at the start of the next wrapped line instead of after the last char.
          renderedLines[renderedLines.length - 1] = line.slice(start, end);
        }
      }

      if (
        cursorOffset === lineStartOffset + line.length &&
        line.length % wrapWidth === 0
      ) {
        renderedLines.push("█");
      }
    }

    lineStartOffset += line.length;
    if (logicalLineIndex < logicalLines.length - 1) {
      lineStartOffset += 1;
    }
  });

  return renderedLines.length > 0 ? renderedLines : ["█"];
}

const Input: FC<InputProps> = ({
  prompt,
  mode,
  isLoading,
  onSubmit,
  onModeToggle,
  onCancel,
  disabled,
}) => {
  const [terminalColumns, setTerminalColumns] = useState(
    process.stdout.columns ?? 80,
  );

  useEffect(() => {
    const handleResize = () => {
      setTerminalColumns(process.stdout.columns ?? 80);
    };

    handleResize();
    process.stdout.on("resize", handleResize);

    return () => {
      process.stdout.off("resize", handleResize);
    };
  }, []);

  const promptTextColumns = useMemo(
    () => getPromptTextColumns(terminalColumns),
    [terminalColumns],
  );

  useInput((input, key) => {
    if (key.escape) {
      onCancel();
      return;
    }
    if (disabled) return;

    if (key.tab) {
      onModeToggle();
      return;
    }
    if (key.return) {
      if (key.shift || key.meta) {
        prompt.insertNewline();
        return;
      }

      onSubmit();
      return;
    }
    if (key.upArrow) {
      if (!prompt.moveVisualUp(promptTextColumns)) {
        prompt.navigateUp();
      }

      return;
    }
    if (key.downArrow) {
      if (!prompt.moveVisualDown(promptTextColumns)) {
        prompt.navigateDown();
      }

      return;
    }
    if (key.leftArrow) {
      if (key.ctrl || key.meta) {
        prompt.moveWordLeft();
      } else {
        prompt.moveLeft();
      }

      return;
    }
    if (key.rightArrow) {
      if (key.ctrl || key.meta) {
        prompt.moveWordRight();
      } else {
        prompt.moveRight();
      }

      return;
    }
    if (key.home || (key.ctrl && input === "a")) {
      prompt.moveLineStart();
      return;
    }
    if (key.end || (key.ctrl && input === "e")) {
      prompt.moveLineEnd();
      return;
    }
    if (key.backspace) {
      if (key.ctrl || key.meta) {
        prompt.deleteWordBackward();
      } else {
        prompt.backspace();
      }

      return;
    }
    if (key.delete) {
      if (key.ctrl || key.meta) {
        prompt.deleteWordForward();
      } else {
        prompt.deleteForward();
      }

      return;
    }
    if (key.ctrl) {
      switch (input) {
        case "b":
          prompt.moveLeft();
          return;
        case "f":
          prompt.moveRight();
          return;
        case "h":
          prompt.backspace();
          return;
        case "n":
          prompt.navigateDown();
          return;
        case "p":
          prompt.navigateUp();
          return;
        case "u":
          prompt.clear();
          return;
        case "w":
          prompt.deleteWordBackward();
          return;
        default:
          break;
      }
    }
    if (input) {
      prompt.insertText(input);
      return;
    }
  });

  const showPlaceholder = prompt.value.length === 0;
  const hint = disabled ? DISABLED_HINT : INPUT_HINT;
  const renderedLines = useMemo(
    () =>
      renderInputLines(prompt.value, prompt.cursorOffset, promptTextColumns),
    [prompt.cursorOffset, prompt.value, promptTextColumns],
  );
  const wrappedLineCount = useMemo(
    () => getWrappedLineSegments(prompt.value, promptTextColumns).length,
    [prompt.value, promptTextColumns],
  );
  const footerLayout = terminalColumns < 96 ? "column" : "row";
  const activityLabel = isLoading ? "running" : disabled ? "blocked" : "ready";
  const showWrappedIndicator = !showPlaceholder && wrappedLineCount > 1;

  return (
    <Box
      flexDirection="column"
      borderStyle="round"
      borderColor="cyan"
      paddingX={1}
    >
      <Box flexDirection="column">
        {showPlaceholder ? (
          <Box>
            <Text color="cyan" bold>
              {"> "}
            </Text>
            <Text color="gray">Ask go-cli to inspect, plan, or edit code</Text>
            <Text color="gray">{"█"}</Text>
          </Box>
        ) : (
          renderedLines.map((line, index) => (
            <Box key={index}>
              <Text color={index === 0 ? "cyan" : "gray"} bold={index === 0}>
                {index === 0 ? "> " : "  "}
              </Text>
              <Text>{line.length > 0 ? line : " "}</Text>
            </Box>
          ))
        )}
      </Box>
      <Box
        paddingLeft={2}
        marginTop={1}
        flexDirection={footerLayout}
        justifyContent="space-between"
      >
        <Text dimColor>
          <Text color={getModeColor(mode)} bold>
            {formatModeLabel(mode)}
          </Text>
          {"  "}
          <Text>{activityLabel}</Text>
          {showWrappedIndicator ? `  wrapped:${wrappedLineCount}` : ""}
        </Text>
        <Text dimColor>{hint}</Text>
      </Box>
    </Box>
  );
};

export default Input;
