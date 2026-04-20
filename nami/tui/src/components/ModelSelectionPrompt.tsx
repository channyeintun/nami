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
  const [searchValue, setSearchValue] = useState("");
  const [searchCursorOffset, setSearchCursorOffset] = useState(0);
  const [searchCursorVisible, setSearchCursorVisible] = useState(true);
  const filteredOptions = useMemo(
    () => filterModelSelectionOptions(selection.options, searchValue),
    [searchValue, selection.options],
  );
  const initialIndex = useMemo(() => {
    const activeIndex = filteredOptions.findIndex((option) => option.active);
    return activeIndex >= 0 ? activeIndex : 0;
  }, [filteredOptions]);
  const [selectedIndex, setSelectedIndex] = useState(initialIndex);
  const [customMode, setCustomMode] = useState(false);
  const [customValue, setCustomValue] = useState("");
  const [cursorOffset, setCursorOffset] = useState(0);
  const selectedIndexRef = useRef(initialIndex);
  const customModeRef = useRef(false);
  const customValueRef = useRef("");
  const cursorOffsetRef = useRef(0);
  const searchValueRef = useRef("");
  const searchCursorOffsetRef = useRef(0);

  selectedIndexRef.current = selectedIndex;
  customModeRef.current = customMode;
  customValueRef.current = customValue;
  cursorOffsetRef.current = cursorOffset;
  searchValueRef.current = searchValue;
  searchCursorOffsetRef.current = searchCursorOffset;

  useEffect(() => {
    selectedIndexRef.current = initialIndex;
    setSelectedIndex(initialIndex);
  }, [initialIndex]);

  useEffect(() => {
    setCustomMode(false);
    setCustomValue("");
    setCursorOffset(0);
    setSearchValue("");
    setSearchCursorOffset(0);
    customModeRef.current = false;
    customValueRef.current = "";
    cursorOffsetRef.current = 0;
    searchValueRef.current = "";
    searchCursorOffsetRef.current = 0;
  }, [selection.requestId]);

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

  useEffect(() => {
    const timer = setInterval(() => {
      setSearchCursorVisible((current) => !current);
    }, 530);

    return () => {
      clearInterval(timer);
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

  const updateSearchValue = (
    updater: string | ((current: string) => string),
  ) => {
    const next =
      typeof updater === "function" ? updater(searchValueRef.current) : updater;
    searchValueRef.current = next;
    setSearchValue(next);
  };

  const updateSearchCursorOffset = (
    updater: number | ((current: number) => number),
  ) => {
    const next =
      typeof updater === "function"
        ? updater(searchCursorOffsetRef.current)
        : updater;
    searchCursorOffsetRef.current = next;
    setSearchCursorOffset(next);
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

    if (key.leftArrow || (key.ctrl && input === "b")) {
      updateSearchCursorOffset((current) => Math.max(0, current - 1));
      return;
    }
    if (key.rightArrow || (key.ctrl && input === "f")) {
      updateSearchCursorOffset((current) =>
        Math.min(searchValueRef.current.length, current + 1),
      );
      return;
    }
    if (key.home || (key.ctrl && input === "a")) {
      updateSearchCursorOffset(0);
      return;
    }
    if (key.end || (key.ctrl && input === "e")) {
      updateSearchCursorOffset(searchValueRef.current.length);
      return;
    }
    if (key.backspace || (key.ctrl && input === "h")) {
      if (searchCursorOffsetRef.current === 0) {
        return;
      }
      updateSearchValue((current) =>
        replaceRange(
          current,
          searchCursorOffsetRef.current - 1,
          searchCursorOffsetRef.current,
          "",
        ),
      );
      updateSearchCursorOffset((current) => Math.max(0, current - 1));
      return;
    }
    if (key.delete) {
      updateSearchValue((current) =>
        replaceRange(
          current,
          searchCursorOffsetRef.current,
          searchCursorOffsetRef.current + 1,
          "",
        ),
      );
      return;
    }
    if (key.ctrl && input === "u") {
      updateSearchValue("");
      updateSearchCursorOffset(0);
      return;
    }
    if (
      text &&
      !key.ctrl &&
      !key.meta &&
      !key.return &&
      !key.upArrow &&
      !key.downArrow
    ) {
      updateSearchValue((current) =>
        replaceRange(
          current,
          searchCursorOffsetRef.current,
          searchCursorOffsetRef.current,
          text,
        ),
      );
      updateSearchCursorOffset((current) => current + text.length);
    }
  });

  const handleListSelect = (index: number) => {
    const selectedOption = filteredOptions[index];
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
          {selection.title ?? "Select Model"}
        </Text>
        <Box marginTop={compactLayout ? 0 : 1} flexDirection="column" minWidth={0}>
          {!compactLayout && (selection.description ?? DEFAULT_MODEL_SELECTION_DESCRIPTION) ? (
            <Text>
              {selection.description ?? DEFAULT_MODEL_SELECTION_DESCRIPTION}
            </Text>
          ) : null}
          {selection.currentModel ? (
            <Text color="$muted">
              Current: {formatCurrentModel(selection.currentModel)}
            </Text>
          ) : null}
        </Box>
      </Box>

      <Box
        marginTop={compactLayout ? 0 : 1}
        flexDirection="column"
        flexShrink={0}
      >
        <Text color={searchValue.length > 0 ? "$fg" : "$muted"}>
          {renderSearchValue(
            searchValue,
            searchCursorOffset,
            searchCursorVisible,
          )}
        </Text>
      </Box>

      {filteredOptions.length > 0 ? (
        <ModelSelectionList
          options={filteredOptions}
          selectedIndex={selectedIndex}
          active={!customMode}
          compact={compactLayout && !customMode}
          onCursor={updateSelectedIndex}
          onSelectIndex={handleListSelect}
        />
      ) : (
        <Box
          marginTop={1}
          flexDirection="column"
          flexGrow={1}
          flexShrink={1}
          justifyContent="center"
          minHeight={0}
        >
          <Text color="$muted">No options match the current filter.</Text>
        </Box>
      )}
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
              ? "Model id or provider/model."
              : "Enter a model id or provider/model to pick a specific provider."}
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
              cancel
            </>
          )}
        </Text>
      </Box>
    </Box>
  );
};

export default ModelSelectionPrompt;

const DEFAULT_MODEL_SELECTION_DESCRIPTION =
  "Choose the active model, a curated preset, or a provider default for the session.";

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
  const label = option.isCustom ? "Custom model" : option.label;
  return `${prefix} ${label}${option.active ? " current" : ""}`;
}

function formatCurrentModel(model: string | null): string {
  return stripProviderPrefix(model) ?? "unknown model";
}

function filterModelSelectionOptions(
  options: UIModelSelectionOption[],
  query: string,
): UIModelSelectionOption[] {
  const normalizedQuery = query.trim().toLowerCase();
  if (!normalizedQuery) {
    return options;
  }

  return options.filter((option) => {
    const haystack = [
      option.label,
      option.model,
      option.provider,
      option.description,
      option.isCustom ? "custom model" : null,
    ]
      .filter((value): value is string => typeof value === "string")
      .join(" ")
      .toLowerCase();

    return haystack.includes(normalizedQuery);
  });
}

function renderEditableValue(
  value: string,
  cursorOffset: number,
  cursorVisible = true,
): string {
  const clampedOffset = Math.max(0, Math.min(value.length, cursorOffset));
  const cursor = cursorVisible ? "█" : " ";
  return value.slice(0, clampedOffset) + cursor + value.slice(clampedOffset);
}

function renderSearchValue(
  value: string,
  cursorOffset: number,
  cursorVisible: boolean,
): string {
  if (value.length === 0) {
    return `Search${cursorVisible ? "█" : " "}`;
  }
  return renderEditableValue(value, cursorOffset, cursorVisible);
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
