import React, { type FC, useEffect, useMemo, useState } from "react";
import { Box, Text, useInput } from "silvery";
import type {
  UIModelSelection,
  UIModelSelectionOption,
} from "../hooks/useEvents.js";
import { stripProviderPrefix } from "../utils/formatModel.js";

interface ModelSelectionPromptProps {
  selection: UIModelSelection;
  onSelect: (modelId: string, provider?: string) => void;
  onCancel: () => void;
}

const VISIBLE_WINDOW = 8;

const ModelSelectionPrompt: FC<ModelSelectionPromptProps> = ({
  selection,
  onSelect,
  onCancel,
}) => {
  const initialIndex = useMemo(() => {
    const activeIndex = selection.options.findIndex((option) => option.active);
    return activeIndex >= 0 ? activeIndex : 0;
  }, [selection.options]);
  const [selectedIndex, setSelectedIndex] = useState(initialIndex);
  const [customMode, setCustomMode] = useState(false);
  const [customValue, setCustomValue] = useState("");
  const [cursorOffset, setCursorOffset] = useState(0);

  useEffect(() => {
    setSelectedIndex(initialIndex);
    setCustomMode(false);
    setCustomValue("");
    setCursorOffset(0);
  }, [initialIndex, selection.requestId]);

  useInput((input, key) => {
    const text = key.text ?? input;
    const selectedOption = selection.options[selectedIndex];

    if (customMode) {
      if (key.escape) {
        setCustomMode(false);
        return;
      }
      if (key.return) {
        const value = customValue.trim();
        if (value.length > 0) {
          onSelect(value);
        }
        return;
      }
      if (key.leftArrow || (key.ctrl && input === "b")) {
        setCursorOffset((current) => Math.max(0, current - 1));
        return;
      }
      if (key.rightArrow || (key.ctrl && input === "f")) {
        setCursorOffset((current) => Math.min(customValue.length, current + 1));
        return;
      }
      if (key.home || (key.ctrl && input === "a")) {
        setCursorOffset(0);
        return;
      }
      if (key.end || (key.ctrl && input === "e")) {
        setCursorOffset(customValue.length);
        return;
      }
      if (key.backspace || (key.ctrl && input === "h")) {
        if (cursorOffset === 0) {
          return;
        }
        setCustomValue((current) =>
          replaceRange(current, cursorOffset - 1, cursorOffset, ""),
        );
        setCursorOffset((current) => Math.max(0, current - 1));
        return;
      }
      if (key.delete) {
        setCustomValue((current) =>
          replaceRange(current, cursorOffset, cursorOffset + 1, ""),
        );
        return;
      }
      if (key.ctrl && input === "u") {
        setCustomValue("");
        setCursorOffset(0);
        return;
      }
      if (text && !key.ctrl && !key.meta) {
        setCustomValue((current) =>
          replaceRange(current, cursorOffset, cursorOffset, text),
        );
        setCursorOffset((current) => current + text.length);
      }
      return;
    }

    if (key.escape) {
      onCancel();
      return;
    }

    if (key.upArrow) {
      setSelectedIndex((current) =>
        current === 0 ? selection.options.length - 1 : current - 1,
      );
      return;
    }

    if (key.downArrow) {
      setSelectedIndex((current) => (current + 1) % selection.options.length);
      return;
    }

    if (key.return) {
      if (!selectedOption) {
        return;
      }
      if (selectedOption.isCustom) {
        setCustomMode(true);
        setCursorOffset(customValue.length);
        return;
      }
      if (selectedOption.model) {
        onSelect(selectedOption.model, selectedOption.provider ?? undefined);
      }
      return;
    }

    const shortcut = input?.toLowerCase();
    if (shortcut === "q") {
      onCancel();
    }
  });

  const startIndex = useMemo(() => {
    if (selection.options.length <= VISIBLE_WINDOW) {
      return 0;
    }
    const centered = selectedIndex - Math.floor(VISIBLE_WINDOW / 2);
    return Math.max(
      0,
      Math.min(centered, selection.options.length - VISIBLE_WINDOW),
    );
  }, [selectedIndex, selection.options.length]);

  const visibleOptions = selection.options.slice(
    startIndex,
    startIndex + VISIBLE_WINDOW,
  );

  return (
    <Box
      flexDirection="column"
      flexGrow={1}
      flexShrink={1}
      minHeight={0}
      borderStyle="round"
      borderColor="cyan"
      overflow="scroll"
      paddingX={1}
    >
      <Text bold color="cyan">
        Select Model
      </Text>
      <Box marginTop={1} flexDirection="column">
        <Text>Choose the active model for the session.</Text>
        <Text color="gray">
          Current: {formatCurrentModel(selection.currentModel)}
        </Text>
      </Box>
      <Box marginTop={1} flexDirection="column">
        {visibleOptions.map((option, index) => {
          const actualIndex = startIndex + index;
          const isSelected = actualIndex === selectedIndex;

          return (
            <Box
              key={`${option.label}-${actualIndex}`}
              flexDirection="column"
              marginBottom={1}
            >
              <Text color={isSelected ? "cyan" : "white"} bold={isSelected}>
                {isSelected ? "›" : " "} {option.label}
                {option.active ? <Text color="green"> current</Text> : null}
              </Text>
              <Text color="gray">
                {formatModelLine(option)}
                {option.description ? `  ·  ${option.description}` : ""}
              </Text>
            </Box>
          );
        })}
      </Box>
      {customMode ? (
        <Box
          marginTop={1}
          paddingX={1}
          borderStyle="round"
          borderColor="cyan"
          flexDirection="column"
        >
          <Text color="cyan">Custom model</Text>
          <Text color="gray">
            Enter a model id only. Provider prefixes are not accepted.
          </Text>
          <Text>{renderEditableValue(customValue, cursorOffset)}</Text>
        </Box>
      ) : null}
      <Box marginTop={1} flexDirection="column">
        <Text dimColor>
          {customMode
            ? "Enter apply · Esc return to list"
            : "Enter choose · Up/Down change selection · Esc or Q cancel"}
        </Text>
      </Box>
    </Box>
  );
};

export default ModelSelectionPrompt;

function formatModelLine(option: UIModelSelectionOption): string {
  if (option.isCustom) {
    return "Type your own model selection";
  }
  return stripProviderPrefix(option.model) ?? "Unknown model";
}

function formatCurrentModel(model: string | null): string {
  return stripProviderPrefix(model) ?? "unknown model";
}

function renderEditableValue(value: string, cursorOffset: number): string {
  const clampedOffset = Math.max(0, Math.min(value.length, cursorOffset));
  return value.slice(0, clampedOffset) + "█" + value.slice(clampedOffset);
}

function replaceRange(
  value: string,
  start: number,
  end: number,
  replacement: string,
): string {
  const safeStart = Math.max(0, Math.min(value.length, start));
  const safeEnd = Math.max(safeStart, Math.min(value.length, end));
  return value.slice(0, safeStart) + replacement + value.slice(safeEnd);
}
