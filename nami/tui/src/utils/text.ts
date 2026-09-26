import { displayWidth } from "silvery";

const TAB_STOP_COLUMNS = 4;

// silvery measures a tab as zero columns wide but does not draw it that way,
// so a line holding tabs is laid out narrower than it renders and gets
// clipped: text after a tab disappears, and gofmt-indented code loses whole
// statements. Text from the model, tools and the user is expanded to spaces
// at 4-column tab stops before it is rendered. ANSI styling in a line does
// not count toward its columns.
export function expandTabs(text: string): string {
  if (!text.includes("\t")) {
    return text;
  }

  return text
    .split("\n")
    .map((line) => {
      if (!line.includes("\t")) {
        return line;
      }

      const [first = "", ...rest] = line.split("\t");
      let expanded = first;
      let column = displayWidth(first);
      for (const piece of rest) {
        const padding = TAB_STOP_COLUMNS - (column % TAB_STOP_COLUMNS);
        expanded += " ".repeat(padding) + piece;
        column += padding + displayWidth(piece);
      }
      return expanded;
    })
    .join("\n");
}
