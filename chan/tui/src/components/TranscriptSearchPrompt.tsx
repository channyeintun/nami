import React, { type FC } from "react";
import { Box, Text, useInput } from "ink";

interface TranscriptSearchPromptProps {
  query: string;
  matchCount: number;
  selectedIndex: number;
  onChange: (query: string) => void;
  onNext: () => void;
  onPrevious: () => void;
  onClose: () => void;
}

const TranscriptSearchPrompt: FC<TranscriptSearchPromptProps> = ({
  query,
  matchCount,
  selectedIndex,
  onChange,
  onNext,
  onPrevious,
  onClose,
}) => {
  useInput((input, key) => {
    if (key.escape) {
      onClose();
      return;
    }

    if (key.return || key.downArrow || (key.ctrl && input === "n")) {
      onNext();
      return;
    }

    if (key.upArrow || (key.ctrl && input === "p")) {
      onPrevious();
      return;
    }

    if (key.backspace) {
      onChange(query.slice(0, -1));
      return;
    }

    if (key.delete || (key.ctrl && input === "u")) {
      onChange("");
      return;
    }

    if (!key.ctrl && !key.meta && input) {
      onChange(query + input);
    }
  });

  const status =
    query.trim().length === 0
      ? "Type to search the loaded transcript window"
      : matchCount > 0
        ? `Match ${selectedIndex + 1} of ${matchCount}`
        : "No matches";

  return (
    <Box flexDirection="column">
      <Box borderStyle="round" borderColor="cyan" paddingX={1}>
        <Text color="cyan">Transcript Search</Text>
        <Text dimColor>{"  "}</Text>
        <Text>{query.length > 0 ? query : "█"}</Text>
      </Box>
      <Box paddingLeft={1} marginTop={1}>
        <Text dimColor>
          {status} · Enter/Down next · Up previous · Backspace edit · Esc close
        </Text>
      </Box>
    </Box>
  );
};

export default TranscriptSearchPrompt;