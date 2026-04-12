import { useCallback, useState } from "react";

function clampOffset(value: string, offset: number): number {
  return Math.max(0, Math.min(offset, value.length));
}

function replaceRange(
  value: string,
  start: number,
  end: number,
  replacement: string,
) {
  const nextValue = value.slice(0, start) + replacement + value.slice(end);

  return {
    value: nextValue,
    cursorOffset: start + replacement.length,
  };
}

function findLinePosition(value: string, cursorOffset: number) {
  const lines = value.split("\n");
  const starts: number[] = [];
  let nextStart = 0;

  for (const line of lines) {
    starts.push(nextStart);
    nextStart += line.length + 1;
  }

  for (let index = 0; index < lines.length; index += 1) {
    const line = lines[index] ?? "";
    const start = starts[index] ?? 0;
    const end = start + line.length;

    if (cursorOffset <= end || index === lines.length - 1) {
      return {
        lines,
        starts,
        lineIndex: index,
        column: cursorOffset - start,
      };
    }
  }

  return {
    lines,
    starts,
    lineIndex: 0,
    column: 0,
  };
}

interface WrappedSegment {
  start: number;
  end: number;
  logicalLineIndex: number;
  text: string;
}

function normalizeWrapWidth(columns: number): number {
  return Math.max(1, columns - 1);
}

function buildWrappedSegments(
  value: string,
  columns: number,
): WrappedSegment[] {
  const wrapWidth = normalizeWrapWidth(columns);
  const logicalLines = value.split("\n");
  const segments: WrappedSegment[] = [];
  let lineStartOffset = 0;

  logicalLines.forEach((line, logicalLineIndex) => {
    if (line.length === 0) {
      segments.push({
        start: lineStartOffset,
        end: lineStartOffset,
        logicalLineIndex,
        text: "",
      });
    } else {
      for (
        let startInLine = 0;
        startInLine < line.length;
        startInLine += wrapWidth
      ) {
        const endInLine = Math.min(line.length, startInLine + wrapWidth);
        segments.push({
          start: lineStartOffset + startInLine,
          end: lineStartOffset + endInLine,
          logicalLineIndex,
          text: line.slice(startInLine, endInLine),
        });
      }
    }

    lineStartOffset += line.length;
    if (logicalLineIndex < logicalLines.length - 1) {
      lineStartOffset += 1;
    }
  });

  return segments;
}

function findWrappedCursorPosition(
  value: string,
  cursorOffset: number,
  columns: number,
) {
  const segments = buildWrappedSegments(value, columns);

  if (segments.length === 0) {
    return {
      segments,
      segmentIndex: -1,
      column: 0,
    };
  }

  for (let index = 0; index < segments.length; index += 1) {
    const segment = segments[index];
    if (!segment) {
      continue;
    }

    const next = segments[index + 1];
    const isLastSegmentOfLine =
      next === undefined || next.logicalLineIndex !== segment.logicalLineIndex;
    const isWithinSegment =
      (cursorOffset >= segment.start && cursorOffset < segment.end) ||
      (cursorOffset === segment.end && isLastSegmentOfLine);

    if (isWithinSegment) {
      return {
        segments,
        segmentIndex: index,
        column: cursorOffset - segment.start,
      };
    }
  }

  const lastSegment = segments[segments.length - 1]!;
  return {
    segments,
    segmentIndex: segments.length - 1,
    column: lastSegment.end - lastSegment.start,
  };
}

function clampWrappedColumn(segment: WrappedSegment, column: number): number {
  return segment.start + Math.min(column, segment.end - segment.start);
}

function findPreviousWordStart(value: string, cursorOffset: number): number {
  let offset = cursorOffset;

  while (offset > 0 && /\s/.test(value[offset - 1] ?? "")) {
    offset -= 1;
  }

  while (offset > 0 && !/\s/.test(value[offset - 1] ?? "")) {
    offset -= 1;
  }

  return offset;
}

function findNextWordEnd(value: string, cursorOffset: number): number {
  let offset = cursorOffset;

  while (offset < value.length && !/\s/.test(value[offset] ?? "")) {
    offset += 1;
  }

  while (offset < value.length && /\s/.test(value[offset] ?? "")) {
    offset += 1;
  }

  return offset;
}

export interface PromptController {
  value: string;
  cursorOffset: number;
  setValue: (value: string) => void;
  setCursorOffset: (offset: number) => void;
  insertImageReference: (id: number) => void;
  submit: () => string;
  navigateUp: () => void;
  navigateDown: () => void;
  insertText: (text: string) => void;
  insertNewline: () => void;
  backspace: () => void;
  deleteForward: () => void;
  deleteWordBackward: () => void;
  deleteWordForward: () => void;
  moveLeft: () => void;
  moveRight: () => void;
  moveWordLeft: () => void;
  moveWordRight: () => void;
  moveUp: () => void;
  moveDown: () => void;
  moveVisualUp: (columns: number) => boolean;
  moveVisualDown: (columns: number) => boolean;
  moveLineStart: () => void;
  moveLineEnd: () => void;
  clear: () => void;
}

interface PromptHistoryState {
  value: string;
  entries: string[];
  index: number;
  draft: string;
  cursorOffset: number;
}

const MAX_HISTORY_ENTRIES = 50;

const initialState: PromptHistoryState = {
  value: "",
  entries: [],
  index: 0,
  draft: "",
  cursorOffset: 0,
};

export function usePromptHistory(): PromptController {
  const [state, setState] = useState<PromptHistoryState>(initialState);

  const updateEditedValue = useCallback(
    (
      updater: (current: PromptHistoryState) => {
        value: string;
        cursorOffset: number;
      },
    ) => {
      setState((current) => {
        const next = updater(current);

        return {
          ...current,
          value: next.value,
          cursorOffset: clampOffset(next.value, next.cursorOffset),
          index: 0,
          draft: "",
        };
      });
    },
    [],
  );

  const setValue = useCallback((value: string) => {
    setState((current) => ({
      ...current,
      value,
      cursorOffset: value.length,
      index: 0,
      draft: "",
    }));
  }, []);

  const setCursorOffset = useCallback((offset: number) => {
    setState((current) => ({
      ...current,
      cursorOffset: clampOffset(current.value, offset),
    }));
  }, []);

  const submit = useCallback((): string => {
    let submitted = "";

    setState((current) => {
      const nextValue = current.value.trim();
      if (!nextValue) {
        return current;
      }

      submitted = nextValue;
      return {
        value: "",
        entries: [
          nextValue,
          ...current.entries.filter((entry) => entry !== nextValue),
        ].slice(0, MAX_HISTORY_ENTRIES),
        index: 0,
        draft: "",
        cursorOffset: 0,
      };
    });

    return submitted;
  }, []);

  const navigateUp = useCallback(() => {
    setState((current) => {
      if (
        current.entries.length === 0 ||
        current.index >= current.entries.length
      ) {
        return current;
      }

      const nextIndex = current.index + 1;
      const nextValue = current.entries[nextIndex - 1] ?? current.value;
      return {
        ...current,
        index: nextIndex,
        draft: current.index === 0 ? current.value : current.draft,
        value: nextValue,
        cursorOffset: nextValue.length,
      };
    });
  }, []);

  const navigateDown = useCallback(() => {
    setState((current) => {
      if (current.index === 0) {
        return current;
      }

      if (current.index === 1) {
        return {
          ...current,
          index: 0,
          value: current.draft,
          draft: "",
          cursorOffset: current.draft.length,
        };
      }

      const nextIndex = current.index - 1;
      const nextValue = current.entries[nextIndex - 1] ?? "";

      return {
        ...current,
        index: nextIndex,
        value: nextValue,
        cursorOffset: nextValue.length,
      };
    });
  }, []);

  const insertText = useCallback(
    (text: string) => {
      if (text.length === 0) {
        return;
      }

      updateEditedValue((current) =>
        replaceRange(
          current.value,
          current.cursorOffset,
          current.cursorOffset,
          text,
        ),
      );
    },
    [updateEditedValue],
  );

  const insertNewline = useCallback(() => {
    updateEditedValue((current) =>
      replaceRange(
        current.value,
        current.cursorOffset,
        current.cursorOffset,
        "\n",
      ),
    );
  }, [updateEditedValue]);

  const insertImageReference = useCallback(
    (id: number) => {
      updateEditedValue((current) => {
        const before = current.value.slice(0, current.cursorOffset);
        const after = current.value.slice(current.cursorOffset);
        const reference = `${/\s$/.test(before) || before.length === 0 ? "" : " "}[Image #${id}]${/^\s/.test(after) || after.length === 0 ? "" : " "}`;

        return replaceRange(
          current.value,
          current.cursorOffset,
          current.cursorOffset,
          reference,
        );
      });
    },
    [updateEditedValue],
  );

  const backspace = useCallback(() => {
    updateEditedValue((current) => {
      if (current.cursorOffset === 0) {
        return {
          value: current.value,
          cursorOffset: current.cursorOffset,
        };
      }

      return replaceRange(
        current.value,
        current.cursorOffset - 1,
        current.cursorOffset,
        "",
      );
    });
  }, [updateEditedValue]);

  const deleteForward = useCallback(() => {
    updateEditedValue((current) => {
      if (current.cursorOffset >= current.value.length) {
        return {
          value: current.value,
          cursorOffset: current.cursorOffset,
        };
      }

      return replaceRange(
        current.value,
        current.cursorOffset,
        current.cursorOffset + 1,
        "",
      );
    });
  }, [updateEditedValue]);

  const deleteWordBackward = useCallback(() => {
    updateEditedValue((current) => {
      const nextOffset = findPreviousWordStart(
        current.value,
        current.cursorOffset,
      );

      return replaceRange(current.value, nextOffset, current.cursorOffset, "");
    });
  }, [updateEditedValue]);

  const deleteWordForward = useCallback(() => {
    updateEditedValue((current) => {
      const nextOffset = findNextWordEnd(current.value, current.cursorOffset);

      return replaceRange(current.value, current.cursorOffset, nextOffset, "");
    });
  }, [updateEditedValue]);

  const moveLeft = useCallback(() => {
    setState((current) => ({
      ...current,
      cursorOffset: clampOffset(current.value, current.cursorOffset - 1),
    }));
  }, []);

  const moveRight = useCallback(() => {
    setState((current) => ({
      ...current,
      cursorOffset: clampOffset(current.value, current.cursorOffset + 1),
    }));
  }, []);

  const moveWordLeft = useCallback(() => {
    setState((current) => ({
      ...current,
      cursorOffset: findPreviousWordStart(current.value, current.cursorOffset),
    }));
  }, []);

  const moveWordRight = useCallback(() => {
    setState((current) => ({
      ...current,
      cursorOffset: findNextWordEnd(current.value, current.cursorOffset),
    }));
  }, []);

  const moveLineStart = useCallback(() => {
    setState((current) => {
      const lineStart = current.value.lastIndexOf(
        "\n",
        current.cursorOffset - 1,
      );

      return {
        ...current,
        cursorOffset: lineStart === -1 ? 0 : lineStart + 1,
      };
    });
  }, []);

  const moveLineEnd = useCallback(() => {
    setState((current) => {
      const lineEnd = current.value.indexOf("\n", current.cursorOffset);

      return {
        ...current,
        cursorOffset: lineEnd === -1 ? current.value.length : lineEnd,
      };
    });
  }, []);

  const moveUp = useCallback(() => {
    setState((current) => {
      const position = findLinePosition(current.value, current.cursorOffset);
      if (position.lineIndex === 0) {
        return current;
      }

      const previousIndex = position.lineIndex - 1;
      const previousStart = position.starts[previousIndex] ?? 0;
      const previousLine = position.lines[previousIndex] ?? "";

      return {
        ...current,
        cursorOffset:
          previousStart + Math.min(position.column, previousLine.length),
      };
    });
  }, []);

  const moveDown = useCallback(() => {
    setState((current) => {
      const position = findLinePosition(current.value, current.cursorOffset);
      if (position.lineIndex >= position.lines.length - 1) {
        return current;
      }

      const nextIndex = position.lineIndex + 1;
      const nextStart = position.starts[nextIndex] ?? current.value.length;
      const nextLine = position.lines[nextIndex] ?? "";

      return {
        ...current,
        cursorOffset: nextStart + Math.min(position.column, nextLine.length),
      };
    });
  }, []);

  const moveVisualUp = useCallback((columns: number): boolean => {
    let moved = false;

    setState((current) => {
      const position = findWrappedCursorPosition(
        current.value,
        current.cursorOffset,
        columns,
      );

      if (position.segmentIndex <= 0) {
        return current;
      }

      const targetSegment = position.segments[position.segmentIndex - 1];
      if (!targetSegment) {
        return current;
      }

      moved = true;
      return {
        ...current,
        cursorOffset: clampWrappedColumn(targetSegment, position.column),
      };
    });

    return moved;
  }, []);

  const moveVisualDown = useCallback((columns: number): boolean => {
    let moved = false;

    setState((current) => {
      const position = findWrappedCursorPosition(
        current.value,
        current.cursorOffset,
        columns,
      );

      if (
        position.segmentIndex === -1 ||
        position.segmentIndex >= position.segments.length - 1
      ) {
        return current;
      }

      const targetSegment = position.segments[position.segmentIndex + 1];
      if (!targetSegment) {
        return current;
      }

      moved = true;
      return {
        ...current,
        cursorOffset: clampWrappedColumn(targetSegment, position.column),
      };
    });

    return moved;
  }, []);

  const clear = useCallback(() => {
    setState((current) => ({
      ...current,
      value: "",
      cursorOffset: 0,
      index: 0,
      draft: "",
    }));
  }, []);

  return {
    value: state.value,
    cursorOffset: state.cursorOffset,
    setValue,
    setCursorOffset,
    insertImageReference,
    submit,
    navigateUp,
    navigateDown,
    insertText,
    insertNewline,
    backspace,
    deleteForward,
    deleteWordBackward,
    deleteWordForward,
    moveLeft,
    moveRight,
    moveWordLeft,
    moveWordRight,
    moveUp,
    moveDown,
    moveVisualUp,
    moveVisualDown,
    moveLineStart,
    moveLineEnd,
    clear,
  };
}
