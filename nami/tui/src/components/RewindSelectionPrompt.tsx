import React, { type FC, useEffect, useState } from "react";
import {
  Box,
  ListView,
  MeasuredBox,
  ModalDialog,
  Text,
  useInput,
} from "silvery";
import type { UIRewindSelection } from "../hooks/useEvents.js";

interface RewindSelectionPromptProps {
  selection: UIRewindSelection;
  onSelect: (messageIndex: number) => void;
  onCancel: () => void;
}

const RewindSelectionPrompt: FC<RewindSelectionPromptProps> = ({
  selection,
  onSelect,
  onCancel,
}) => {
  const [selectedIndex, setSelectedIndex] = useState(
    Math.max(selection.turns.length - 1, 0),
  );
  const [terminalRows, setTerminalRows] = useState(process.stdout.rows ?? 24);
  const [terminalColumns, setTerminalColumns] = useState(
    process.stdout.columns ?? 80,
  );

  useEffect(() => {
    setSelectedIndex(Math.max(selection.turns.length - 1, 0));
  }, [selection.requestId, selection.turns.length]);

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

  useInput((input, key) => {
    if (key.escape) {
      onCancel();
      return;
    }

    const shortcut = input?.toLowerCase();
    if (!shortcut) {
      return;
    }

    if (shortcut === "q") {
      onCancel();
    }
  });

  const handleListSelect = (index: number) => {
    const selected = selection.turns[index];
    if (selected) {
      onSelect(selected.messageIndex);
    }
  };

  const dialogWidth =
    terminalColumns > 52
      ? Math.min(96, terminalColumns - 4)
      : Math.max(24, terminalColumns - 2);
  const dialogHeight =
    terminalRows > 16
      ? Math.min(24, terminalRows - 4)
      : Math.max(10, terminalRows - 2);

  return (
    <ModalDialog
      title="Rewind Conversation"
      width={dialogWidth}
      height={dialogHeight}
      borderStyle="single"
      borderColor="$inputborder"
      footer="Enter rewind · Up/Down change selection · Esc or Q cancel"
    >
      <Box
        flexDirection="column"
        flexGrow={1}
        flexShrink={1}
        minWidth={0}
        minHeight={0}
      >
        <Box flexDirection="column" flexShrink={0} minWidth={0}>
          <Text>
            Choose the user turn to keep. Later messages will be dropped.
          </Text>
          <Text color="$muted">
            {selection.turns.length} available turn
            {selection.turns.length === 1 ? "" : "s"}
          </Text>
        </Box>
        <RewindTurnList
          turns={selection.turns}
          selectedIndex={selectedIndex}
          onCursor={setSelectedIndex}
          onSelectIndex={handleListSelect}
        />
      </Box>
    </ModalDialog>
  );
};

export default RewindSelectionPrompt;

interface RewindTurnListProps {
  turns: UIRewindSelection["turns"];
  selectedIndex: number;
  onCursor: (index: number) => void;
  onSelectIndex: (index: number) => void;
}

const RewindTurnList: FC<RewindTurnListProps> = ({
  turns,
  selectedIndex,
  onCursor,
  onSelectIndex,
}) => {
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
          items={turns}
          height={Math.max(1, height)}
          nav
          cursorKey={selectedIndex}
          onCursor={onCursor}
          onSelect={onSelectIndex}
          active
          estimateHeight={2}
          overflowIndicator
          getKey={(turn) => `${turn.turnNumber}-${turn.messageIndex}`}
          renderItem={(turn, _index, meta) => {
            const isSelected = meta.isCursor;

            return (
              <Box
                key={`${turn.turnNumber}-${turn.messageIndex}`}
                flexDirection="column"
                backgroundColor={isSelected ? "$selectionbg" : undefined}
                paddingX={1}
                marginBottom={1}
                minWidth={0}
              >
                <Text color={isSelected ? "$selection" : "$fg"} bold={isSelected}>
                  {isSelected ? "›" : " "} Turn {turn.turnNumber}
                </Text>
                <Text color={isSelected ? "$selection" : "$muted"}>
                  {turn.preview}
                </Text>
              </Box>
            );
          }}
        />
      )}
    </MeasuredBox>
  );
};
