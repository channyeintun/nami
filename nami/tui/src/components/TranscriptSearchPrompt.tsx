import React, { type FC } from "react";
import { Box, Text, useInput } from "silvery";

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
    const text = key.text ?? input;

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

    if (!key.ctrl && !key.meta && text) {
      onChange(query + text);
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
      <Box borderStyle="round" borderColor="$primary" paddingX={1}>
        <Text color="$primary">Transcript Search</Text>
        <Text color="$muted">{"  "}</Text>
        <Text>{query.length > 0 ? query : "█"}</Text>
      </Box>
      <Box paddingLeft={1} marginTop={1}>
        <Text color="$muted">
          {status} · Enter/Down next · Up previous · Backspace edit · Esc close
        </Text>
      </Box>
    </Box>
  );
};

export default TranscriptSearchPrompt;
