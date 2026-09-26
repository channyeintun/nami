import { displayWidth } from "silvery";

export const graphemeSegmenter = new Intl.Segmenter(undefined, {
  granularity: "grapheme",
});

// Offsets are UTF-16 indices, so stepping by one can land between the halves
// of a surrogate pair or inside a cluster such as a flag, an emoji with a skin
// tone, or a letter with a combining accent. Cursor steps and single-character
// deletes go by whole grapheme clusters instead.
export function previousGraphemeOffset(value: string, offset: number): number {
  if (offset <= 0) {
    return 0;
  }
  const segment = graphemeSegmenter.segment(value).containing(offset - 1);
  return segment ? segment.index : offset - 1;
}

export function nextGraphemeOffset(value: string, offset: number): number {
  if (offset >= value.length) {
    return value.length;
  }
  const segment = graphemeSegmenter.segment(value).containing(offset);
  return segment ? segment.index + segment.segment.length : offset + 1;
}

// What Backspace leaves of a single-line text field.
export function withoutLastCharacter(value: string): string {
  return value.slice(0, previousGraphemeOffset(value, value.length));
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
