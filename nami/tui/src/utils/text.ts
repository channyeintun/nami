import { displayWidth } from "silvery";

export const graphemeSegmenter = new Intl.Segmenter(undefined, {
  granularity: "grapheme",
});

// The grapheme cluster holding the UTF-16 unit at `index`. The segmenter
// walks forward from the start of that line - a line feed always ends a
// cluster - instead of being asked through Segments#containing: Bun, one of
// the runtimes the launcher falls back to, answers that with the previous
// cluster when the one asked about starts with a surrogate pair.
function clusterAt(value: string, index: number): { start: number; end: number } {
  const lineStart = index === 0 ? 0 : value.lastIndexOf("\n", index - 1) + 1;
  for (const { index: start, segment } of graphemeSegmenter.segment(
    value.slice(lineStart),
  )) {
    const end = lineStart + start + segment.length;
    if (index < end) {
      return { start: lineStart + start, end };
    }
  }
  return { start: index, end: index + 1 };
}

// Offsets are UTF-16 indices, so stepping by one can land between the halves
// of a surrogate pair or inside a cluster such as a flag, an emoji with a skin
// tone, or a letter with a combining accent. Cursor steps and single-character
// deletes go by whole grapheme clusters instead.
export function previousGraphemeOffset(value: string, offset: number): number {
  if (offset <= 0) {
    return 0;
  }
  return clusterAt(value, offset - 1).start;
}

export function nextGraphemeOffset(value: string, offset: number): number {
  if (offset >= value.length) {
    return value.length;
  }
  return clusterAt(value, offset).end;
}

// What Backspace leaves of a single-line text field.
export function withoutLastCharacter(value: string): string {
  return value.slice(0, previousGraphemeOffset(value, value.length));
}

// Shortens text to at most maxLength UTF-16 units, ellipsis included,
// cutting only between characters so no surrogate pair or cluster is split.
export function truncateEnd(
  value: string,
  maxLength: number,
  ellipsis = "...",
): string {
  if (value.length <= maxLength) {
    return value;
  }

  const budget = Math.max(0, maxLength - ellipsis.length);
  let cut = 0;
  for (const { index, segment } of graphemeSegmenter.segment(value)) {
    const end = index + segment.length;
    if (end > budget) {
      break;
    }
    cut = end;
  }
  return `${value.slice(0, cut)}${ellipsis}`;
}

const TAB_STOP_COLUMNS = 4;

// Columns a tab takes when it starts at `column`.
export function tabWidthAt(column: number): number {
  return TAB_STOP_COLUMNS - (column % TAB_STOP_COLUMNS);
}

// silvery measures a tab as zero columns wide but does not draw it that way,
// so a line holding tabs is laid out narrower than it renders and gets
// clipped: text after a tab disappears, and gofmt-indented code loses whole
// statements. Text from the model, tools and the user is expanded to spaces
// at 4-column tab stops before it is rendered. ANSI styling in a line does
// not count toward its columns. startColumn is where the first line begins,
// for text that continues a line already on screen.
export function expandTabs(text: string, startColumn = 0): string {
  if (!text.includes("\t")) {
    return text;
  }

  return text
    .split("\n")
    .map((line, lineIndex) => {
      if (!line.includes("\t")) {
        return line;
      }

      const [first = "", ...rest] = line.split("\t");
      let expanded = first;
      let column = (lineIndex === 0 ? startColumn : 0) + displayWidth(first);
      for (const piece of rest) {
        const padding = tabWidthAt(column);
        expanded += " ".repeat(padding) + piece;
        column += padding + displayWidth(piece);
      }
      return expanded;
    })
    .join("\n");
}
