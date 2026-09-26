import { useCallback, useState } from "react";
import { graphemeWidth } from "silvery";
import { tabWidthAt } from "../utils/text.js";

function clampOffset(value: string, offset: number): number {
  return Math.max(0, Math.min(offset, value.length));
}

const graphemeSegmenter = new Intl.Segmenter(undefined, {
  granularity: "grapheme",
});

// Offsets are UTF-16 indices, so stepping by one can land between the halves
// of a surrogate pair or inside a cluster such as a flag, an emoji with a skin
// tone, or a letter with a combining accent. Single-step moves and deletes go
// by whole grapheme clusters instead.
function previousGraphemeOffset(value: string, offset: number): number {
  if (offset <= 0) {
    return 0;
  }
  const segment = graphemeSegmenter.segment(value).containing(offset - 1);
  return segment ? segment.index : offset - 1;
}

function nextGraphemeOffset(value: string, offset: number): number {
  if (offset >= value.length) {
    return value.length;
  }
  const segment = graphemeSegmenter.segment(value).containing(offset);
  return segment ? segment.index + segment.segment.length : offset + 1;
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

// One visual row of the prompt. Input draws exactly these rows, and the
// Up/Down keys move between them, so the two always agree.
export interface WrappedSegment {
  start: number;
  end: number;
  logicalLineIndex: number;
  text: string;
  // Only a line's last row has a cursor position after its final character;
  // on any other row that position is the start of the next row.
  isLastOfLine: boolean;
}

// Leave one column for the block cursor so a cursor drawn after the last
// character of a full row does not spill onto a phantom row.
function normalizeWrapWidth(columns: number): number {
  return Math.max(1, columns - 1);
}

// Printable ASCII takes one column per UTF-16 unit, so such a line can be cut
// by length. Anything else is measured per grapheme the way silvery lays it
// out: wide characters wrap before the edge instead of overflowing the row,
// no surrogate pair or cluster is split across rows, and a tab takes the
// columns expandTabs gives it when the row is drawn.
const PRINTABLE_ASCII = /^[\x20-\x7e]*$/;

function graphemeColumns(grapheme: string, column: number): number {
  return grapheme === "\t" ? tabWidthAt(column) : graphemeWidth(grapheme);
}

function wrapLine(line: string, wrapWidth: number): Array<[number, number]> {
  const ranges: Array<[number, number]> = [];
  if (PRINTABLE_ASCII.test(line)) {
    for (let start = 0; start < line.length; start += wrapWidth) {
      ranges.push([start, Math.min(line.length, start + wrapWidth)]);
    }
    return ranges;
  }

  let rowStart = 0;
  let column = 0;
  for (const { segment: grapheme, index } of graphemeSegmenter.segment(line)) {
    let width = graphemeColumns(grapheme, column);
    if (index > rowStart && column + width > wrapWidth) {
      ranges.push([rowStart, index]);
      rowStart = index;
      column = 0;
      width = graphemeColumns(grapheme, column);
    }
    column += width;
  }
  ranges.push([rowStart, line.length]);
  return ranges;
}

export function buildWrappedSegments(
  value: string,
  columns: number,
): WrappedSegment[] {
  const wrapWidth = normalizeWrapWidth(columns);
  const logicalLines = value.split("\n");
  const segments: WrappedSegment[] = [];
  let lineStartOffset = 0;

  logicalLines.forEach((line, logicalLineIndex) => {
    const ranges: Array<[number, number]> =
      line.length === 0 ? [[0, 0]] : wrapLine(line, wrapWidth);
    ranges.forEach(([start, end], rangeIndex) => {
      segments.push({
        start: lineStartOffset + start,
        end: lineStartOffset + end,
        logicalLineIndex,
        text: line.slice(start, end),
        isLastOfLine: rangeIndex === ranges.length - 1,
      });
    });

    lineStartOffset += line.length;
    if (logicalLineIndex < logicalLines.length - 1) {
      lineStartOffset += 1;
    }
  });

  return segments;
}

// The row the cursor is drawn on: a cursor on a wrap boundary belongs to the
// start of the next row, except after the final character of a line.
export function findCursorSegmentIndex(
  segments: WrappedSegment[],
  cursorOffset: number,
): number {
  const index = segments.findIndex(
    (segment) =>
      (cursorOffset >= segment.start && cursorOffset < segment.end) ||
      (cursorOffset === segment.end && segment.isLastOfLine),
  );
  return index >= 0 ? index : segments.length - 1;
}

// Columns from the start of a row to `offset` within it.
function columnsBefore(segment: WrappedSegment, offset: number): number {
  let column = 0;
  const prefix = segment.text.slice(0, offset - segment.start);
  for (const { segment: grapheme } of graphemeSegmenter.segment(prefix)) {
    column += graphemeColumns(grapheme, column);
  }
  return column;
}

// The offset in a row nearest `column` without passing it.
function offsetAtColumn(segment: WrappedSegment, column: number): number {
  let current = 0;
  let lastGraphemeStart = segment.start;
  for (const { segment: grapheme, index } of graphemeSegmenter.segment(
    segment.text,
  )) {
    const width = graphemeColumns(grapheme, current);
    if (current + width > column) {
      return segment.start + index;
    }
    current += width;
    lastGraphemeStart = segment.start + index;
  }
  return segment.isLastOfLine ? segment.end : lastGraphemeStart;
}

function findWrappedCursorPosition(
  value: string,
  cursorOffset: number,
  columns: number,
) {
  const segments = buildWrappedSegments(value, columns);
  const segmentIndex = findCursorSegmentIndex(segments, cursorOffset);
  const segment = segments[segmentIndex];

  return {
    segments,
    segmentIndex,
    column: segment
      ? columnsBefore(segment, Math.min(cursorOffset, segment.end))
      : 0,
  };
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
  submit: (overrideText?: string) => void;
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
  moveUpOrRecallPrevious: (columns: number) => void;
  moveDownOrRecallNext: (columns: number) => void;
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

function recallPreviousEntry(current: PromptHistoryState): PromptHistoryState {
  if (current.entries.length === 0 || current.index >= current.entries.length) {
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
}

function recallNextEntry(current: PromptHistoryState): PromptHistoryState {
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
}

// The cursor moved one visual row up (-1) or down (1), keeping its column, or
// null when there is no such row.
function moveToAdjacentRow(
  current: PromptHistoryState,
  columns: number,
  direction: -1 | 1,
): PromptHistoryState | null {
  const position = findWrappedCursorPosition(
    current.value,
    current.cursorOffset,
    columns,
  );
  const target = position.segments[position.segmentIndex + direction];
  if (!target) {
    return null;
  }

  return {
    ...current,
    cursorOffset: offsetAtColumn(target, position.column),
  };
}

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

  // Clears the prompt and records the text in history. It returns nothing:
  // the updater may run after this call, so callers read the text to send
  // from the prompt value themselves.
  const submit = useCallback((overrideText?: string) => {
    setState((current) => {
      const nextValue = (overrideText ?? current.value).trim();
      if (!nextValue) {
        return current;
      }

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
  }, []);

  const navigateUp = useCallback(() => {
    setState(recallPreviousEntry);
  }, []);

  const navigateDown = useCallback(() => {
    setState(recallNextEntry);
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
        previousGraphemeOffset(current.value, current.cursorOffset),
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
        nextGraphemeOffset(current.value, current.cursorOffset),
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
      cursorOffset: previousGraphemeOffset(current.value, current.cursorOffset),
    }));
  }, []);

  const moveRight = useCallback(() => {
    setState((current) => ({
      ...current,
      cursorOffset: nextGraphemeOffset(current.value, current.cursorOffset),
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

  // Up and Down move between the prompt's visual rows and step through
  // history from its first or last row. The choice is made inside the state
  // update: React may run an updater after setState has returned, so a flag
  // set inside it cannot tell the caller whether the cursor moved.
  const moveUpOrRecallPrevious = useCallback((columns: number) => {
    setState(
      (current) =>
        moveToAdjacentRow(current, columns, -1) ?? recallPreviousEntry(current),
    );
  }, []);

  const moveDownOrRecallNext = useCallback((columns: number) => {
    setState(
      (current) =>
        moveToAdjacentRow(current, columns, 1) ?? recallNextEntry(current),
    );
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
    moveUpOrRecallPrevious,
    moveDownOrRecallNext,
    moveLineStart,
    moveLineEnd,
    clear,
  };
}
