import React, { type FC, useEffect, useMemo, useRef, useState } from "react";
import {
  Box,
  ListView,
  Text,
  useBoxRect,
  useFocusManager,
  useInput,
} from "silvery";
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

const ModelSelectionPrompt: FC<ModelSelectionPromptProps> = ({
  selection,
  onSelect,
  onCancel,
}) => {
  const [terminalRows, setTerminalRows] = useState(process.stdout.rows ?? 24);
  const focusManager = useFocusManager();
  const initialIndex = useMemo(() => {
    const activeIndex = selection.options.findIndex((option) => option.active);
    return activeIndex >= 0 ? activeIndex : 0;
  }, [selection.options]);
  const [selectedIndex, setSelectedIndex] = useState(initialIndex);
  const [customMode, setCustomMode] = useState(false);
  const [customValue, setCustomValue] = useState("");
  const [cursorOffset, setCursorOffset] = useState(0);
  const selectedIndexRef = useRef(initialIndex);
  const customModeRef = useRef(false);
  const customValueRef = useRef("");
  const cursorOffsetRef = useRef(0);

  selectedIndexRef.current = selectedIndex;
  customModeRef.current = customMode;
  customValueRef.current = customValue;
  cursorOffsetRef.current = cursorOffset;

  useEffect(() => {
    setSelectedIndex(initialIndex);
    setCustomMode(false);
    setCustomValue("");
    setCursorOffset(0);
    selectedIndexRef.current = initialIndex;
    customModeRef.current = false;
    customValueRef.current = "";
    cursorOffsetRef.current = 0;
  }, [initialIndex, selection.requestId]);

  useEffect(() => {
    if (customMode) {
      focusManager.blur();
    }
  }, [customMode, focusManager]);

  useEffect(() => {
    const handleResize = () => {
      setTerminalRows(process.stdout.rows ?? 24);
    };

    handleResize();
    process.stdout.on("resize", handleResize);

    return () => {
      process.stdout.off("resize", handleResize);
    };
  }, []);

  const compactLayout = terminalRows < 22;

  const updateSelectedIndex = (
    updater: number | ((current: number) => number),
  ) => {
    const next =
      typeof updater === "function"
        ? updater(selectedIndexRef.current)
        : updater;
    selectedIndexRef.current = next;
    setSelectedIndex(next);
  };

  const updateCustomMode = (next: boolean) => {
    customModeRef.current = next;
    setCustomMode(next);
  };

  const updateCustomValue = (
    updater: string | ((current: string) => string),
  ) => {
    const next =
      typeof updater === "function" ? updater(customValueRef.current) : updater;
    customValueRef.current = next;
    setCustomValue(next);
  };

  const updateCursorOffset = (
    updater: number | ((current: number) => number),
  ) => {
    const next =
      typeof updater === "function"
        ? updater(cursorOffsetRef.current)
        : updater;
    cursorOffsetRef.current = next;
    setCursorOffset(next);
  };

  useInput((input, key) => {
    const text = key.text ?? input;
    const isEscape = key.escape || input === "\u001b" || text === "\u001b";

    if (customModeRef.current) {
      if (isEscape) {
        updateCustomMode(false);
        return;
      }
      if (key.return) {
        const value = customValueRef.current.trim();
        if (value.length > 0) {
          onSelect(value);
        }
        return;
      }
      if (key.leftArrow || (key.ctrl && input === "b")) {
        updateCursorOffset((current) => Math.max(0, current - 1));
        return;
      }
      if (key.rightArrow || (key.ctrl && input === "f")) {
        updateCursorOffset((current) =>
          Math.min(customValueRef.current.length, current + 1),
        );
        return;
      }
      if (key.home || (key.ctrl && input === "a")) {
        updateCursorOffset(0);
        return;
      }
      if (key.end || (key.ctrl && input === "e")) {
        updateCursorOffset(customValueRef.current.length);
        return;
      }
      if (key.backspace || (key.ctrl && input === "h")) {
        if (cursorOffsetRef.current === 0) {
          return;
        }
        updateCustomValue((current) =>
          replaceRange(
            current,
            cursorOffsetRef.current - 1,
            cursorOffsetRef.current,
            "",
          ),
        );
        updateCursorOffset((current) => Math.max(0, current - 1));
        return;
      }
      if (key.delete) {
        updateCustomValue((current) =>
          replaceRange(
            current,
            cursorOffsetRef.current,
            cursorOffsetRef.current + 1,
            "",
          ),
        );
        return;
      }
      if (key.ctrl && input === "u") {
        updateCustomValue("");
        updateCursorOffset(0);
        return;
      }
      if (text && !key.ctrl && !key.meta) {
        updateCustomValue((current) =>
          replaceRange(
            current,
            cursorOffsetRef.current,
            cursorOffsetRef.current,
            text,
          ),
        );
        updateCursorOffset((current) => current + text.length);
      }
      return;
    }

    if (isEscape) {
      onCancel();
      return;
    }

    const shortcut = input?.toLowerCase();
    if (shortcut === "q") {
      onCancel();
    }
  });

  const handleListSelect = (index: number) => {
    const selectedOption = selection.options[index];
    if (!selectedOption) {
      return;
    }
    if (selectedOption.isCustom) {
      focusManager.blur();
      updateCustomMode(true);
      updateCursorOffset(customValueRef.current.length);
      return;
    }
    if (selectedOption.model) {
      onSelect(selectedOption.model, selectedOption.provider ?? undefined);
    }
  };

  return (
    <Box
      flexDirection="column"
      flexGrow={1}
      flexShrink={1}
      minWidth={0}
      minHeight={0}
      backgroundColor="$popover-bg"
      borderStyle="double"
      borderColor="$inputborder"
      overflow="hidden"
      paddingX={2}
      paddingY={compactLayout ? 0 : 1}
    >
      <Box flexDirection="column" flexShrink={0} minWidth={0}>
        <Text bold color="$primary">
          Select Model
        </Text>
        <Box marginTop={compactLayout ? 0 : 1} flexDirection="column" minWidth={0}>
          {!compactLayout ? <Text>Choose the active model for the session.</Text> : null}
          <Text color="$muted">
            Current: {formatCurrentModel(selection.currentModel)}
          </Text>
        </Box>
      </Box>

      <ModelSelectionList
        options={selection.options}
        selectedIndex={selectedIndex}
        active={!customMode}
        compact={compactLayout && !customMode}
        onCursor={updateSelectedIndex}
        onSelectIndex={handleListSelect}
      />
      {customMode ? (
        <Box
          marginTop={compactLayout ? 0 : 1}
          paddingX={1}
          paddingY={1}
          backgroundColor="$surface-bg"
          borderStyle="round"
          borderColor="$focusborder"
          flexDirection="column"
          flexShrink={0}
        >
          <Text color="$primary">Custom model</Text>
          <Text color="$muted">
            {compactLayout
              ? "Model id only. No provider prefix."
              : "Enter a model id only. Provider prefixes are not accepted."}
          </Text>
          <Text>{renderEditableValue(customValue, cursorOffset)}</Text>
        </Box>
      ) : null}
      <Box marginTop={compactLayout ? 0 : 1} flexDirection="column" flexShrink={0}>
        <Text color="$fg">
          {customMode ? (
            <>
              <Text color="$primary" bold>
                Enter
              </Text>{" "}
              apply ·{" "}
              <Text color="$primary" bold>
                Esc
              </Text>{" "}
              return to list
            </>
          ) : (
            <>
              <Text color="$primary" bold>
                Enter
              </Text>{" "}
              choose ·{" "}
              {compactLayout ? null : (
                <>
                  <Text color="$primary" bold>
                    Up/Down
                  </Text>{" "}
                  change selection ·{" "}
                </>
              )}
              <Text color="$primary" bold>
                Esc
              </Text>{" "}
              or{" "}
              <Text color="$primary" bold>
                Q
              </Text>{" "}
              cancel
            </>
          )}
        </Text>
      </Box>
    </Box>
  );
};

export default ModelSelectionPrompt;

interface ModelSelectionListProps {
  options: UIModelSelectionOption[];
  selectedIndex: number;
  active: boolean;
  compact: boolean;
  onCursor: (index: number | ((current: number) => number)) => void;
  onSelectIndex: (index: number) => void;
}

const ModelSelectionList: FC<ModelSelectionListProps> = ({
  options,
  selectedIndex,
  active,
  compact,
  onCursor,
  onSelectIndex,
}) => {
  const { height: rectHeight } = useBoxRect();
  const viewportHeight = Math.max(1, rectHeight);

  return (
    <Box
      marginTop={1}
      flexDirection="column"
      flexGrow={1}
      flexShrink={1}
      minHeight={0}
      minWidth={0}
      overflow="hidden"
    >
      <ListView
        items={options}
        height={viewportHeight}
        nav
        cursorKey={selectedIndex}
        onCursor={(index) => onCursor(index)}
        onSelect={onSelectIndex}
        active={active}
        estimateHeight={compact ? 1 : 2}
        overflowIndicator
        getKey={(option, index) => `${option.label}-${index}`}
        renderItem={(option, index, meta) => {
          const isSelected = meta.isCursor;

          return (
            <Box
              key={`${option.label}-${index}`}
              flexDirection="column"
              backgroundColor={isSelected ? "$selectionbg" : undefined}
              paddingX={1}
              marginBottom={compact ? 0 : 1}
              minWidth={0}
            >
              <Text color={isSelected ? "$selection" : "$fg"} bold={isSelected}>
                {compact
                  ? formatCompactModelLine(option, isSelected)
                  : `${isSelected ? "›" : " "} ${option.label}`}
              </Text>
              {!compact ? (
                <Text color={isSelected ? "$selection" : "$muted"}>
                  {formatModelLine(option)}
                  {option.description ? `  ·  ${option.description}` : ""}
                </Text>
              ) : null}
            </Box>
          );
        }}
      />
    </Box>
  );
};

function formatModelLine(option: UIModelSelectionOption): string {
  if (option.isCustom) {
    return "Press Enter to type your own model";
  }
  return stripProviderPrefix(option.model) ?? "Unknown model";
}

function formatCompactModelLine(
  option: UIModelSelectionOption,
  isSelected: boolean,
): string {
  const prefix = isSelected ? ">" : " ";
  const label = option.isCustom
    ? "Custom model"
    : stripProviderPrefix(option.model) ?? option.label;
  return `${prefix} ${label}${option.active ? " current" : ""}`;
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
