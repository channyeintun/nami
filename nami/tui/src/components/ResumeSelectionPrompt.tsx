import React, { type FC, useEffect, useMemo, useRef, useState } from "react";
import { Box, ListView, Text, useBoxRect, useInput } from "silvery";
import type { UIResumeSelection } from "../hooks/useEvents.js";
import { stripProviderPrefix } from "../utils/formatModel.js";

interface ResumeSelectionPromptProps {
  selection: UIResumeSelection;
  onSelect: (sessionId: string) => void;
  onCancel: () => void;
}

const ResumeSelectionPrompt: FC<ResumeSelectionPromptProps> = ({
  selection,
  onSelect,
  onCancel,
}) => {
  const [searchValue, setSearchValue] = useState("");
  const [searchCursorOffset, setSearchCursorOffset] = useState(0);
  const [searchCursorVisible, setSearchCursorVisible] = useState(true);
  const filteredSessions = useMemo(
    () => filterResumeSessions(selection.sessions, searchValue),
    [searchValue, selection.sessions],
  );
  const [selectedIndex, setSelectedIndex] = useState(0);
  const selectedIndexRef = useRef(0);
  const searchValueRef = useRef("");
  const searchCursorOffsetRef = useRef(0);

  selectedIndexRef.current = selectedIndex;
  searchValueRef.current = searchValue;
  searchCursorOffsetRef.current = searchCursorOffset;

  useEffect(() => {
    selectedIndexRef.current = 0;
    setSelectedIndex(0);
  }, [searchValue, selection.requestId]);

  useEffect(() => {
    searchValueRef.current = "";
    searchCursorOffsetRef.current = 0;
    setSearchValue("");
    setSearchCursorOffset(0);
  }, [selection.requestId]);

  useEffect(() => {
    const timer = setInterval(() => {
      setSearchCursorVisible((current) => !current);
    }, 530);

    return () => {
      clearInterval(timer);
    };
  }, []);

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

    if (key.escape) {
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
    const selected = filteredSessions[index];
    if (selected) {
      onSelect(selected.sessionId);
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
      paddingY={1}
    >
      <Box flexDirection="column" flexShrink={0} minWidth={0}>
        <Text bold color="$primary">
          Resume Session
        </Text>
        <Box marginTop={1} flexDirection="column" minWidth={0}>
          <Text>Choose a session to resume.</Text>
          <Text color="$muted">
            {selection.sessions.length} available session
            {selection.sessions.length === 1 ? "" : "s"}
          </Text>
        </Box>
      </Box>

      <Box
        marginTop={1}
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

      {filteredSessions.length > 0 ? (
        <ResumeSessionList
          sessions={filteredSessions}
          selectedIndex={selectedIndex}
          onCursor={setSelectedIndex}
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
          <Text color="$muted">No sessions match the current filter.</Text>
        </Box>
      )}
      <Box marginTop={1} flexDirection="column" flexShrink={0}>
        <Text color="$fg">
          <Text color="$primary" bold>
            Enter
          </Text>{" "}
          resume ·{" "}
          <Text color="$primary" bold>
            Up/Down
          </Text>{" "}
          change selection ·{" "}
          <Text color="$primary" bold>
            Esc
          </Text>{" "}
          cancel
        </Text>
      </Box>
    </Box>
  );
};

export default ResumeSelectionPrompt;

interface ResumeSessionListProps {
  sessions: UIResumeSelection["sessions"];
  selectedIndex: number;
  onCursor: (index: number) => void;
  onSelectIndex: (index: number) => void;
}

const ResumeSessionList: FC<ResumeSessionListProps> = ({
  sessions,
  selectedIndex,
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
        items={sessions}
        height={viewportHeight}
        nav
        cursorKey={selectedIndex}
        onCursor={onCursor}
        onSelect={onSelectIndex}
        active
        estimateHeight={2}
        overflowIndicator
        getKey={(session) => session.sessionId}
        renderItem={(session, _index, meta) => {
          const isSelected = meta.isCursor;
          const timestamp = formatUpdatedAt(session.updatedAt);

          return (
            <Box
              key={session.sessionId}
              flexDirection="column"
              backgroundColor={isSelected ? "$selectionbg" : undefined}
              paddingX={1}
              marginBottom={1}
              minWidth={0}
            >
              <Text color={isSelected ? "$selection" : "$fg"} bold={isSelected}>
                {isSelected ? "›" : " "} {session.sessionId.slice(0, 8)}{" "}
                {timestamp}
              </Text>
              <Text color={isSelected ? "$selection" : "$muted"}>
                {session.title}
                {session.model
                  ? `  ·  ${stripProviderPrefix(session.model) ?? session.model}`
                  : ""}
                {`  ·  $${session.totalCostUsd.toFixed(4)}`}
              </Text>
            </Box>
          );
        }}
      />
    </Box>
  );
};

function formatUpdatedAt(value: string | null): string {
  if (!value) {
    return "unknown time";
  }

  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) {
    return value;
  }

  const year = parsed.getFullYear();
  const month = String(parsed.getMonth() + 1).padStart(2, "0");
  const day = String(parsed.getDate()).padStart(2, "0");
  const hours = String(parsed.getHours()).padStart(2, "0");
  const minutes = String(parsed.getMinutes()).padStart(2, "0");
  return `${year}-${month}-${day} ${hours}:${minutes}`;
}

function filterResumeSessions(
  sessions: UIResumeSelection["sessions"],
  query: string,
): UIResumeSelection["sessions"] {
  const normalizedQuery = query.trim().toLowerCase();
  if (!normalizedQuery) {
    return sessions;
  }

  return sessions.filter((session) => {
    const haystack = [
      session.sessionId,
      session.title,
      session.updatedAt,
      session.model,
      session.totalCostUsd.toFixed(4),
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
