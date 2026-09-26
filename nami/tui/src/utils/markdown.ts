import { stripVTControlCharacters } from "node:util";
import { highlight, supportsLanguage } from "cli-highlight";
import { marked, type Token, type Tokens } from "marked";
import { displayWidth as terminalDisplayWidth } from "silvery";

const EOL = "\n";
const TOKEN_CACHE_MAX = 500;
const tokenCache = new Map<string, Token[]>();
const MD_SYNTAX_RE = /[#*`|[>\-_~]|\n\n|^\d+\. |\n\d+\. /;

const ANSI = {
  boldStart: "\u001b[1m",
  boldEnd: "\u001b[22m",
  dimStart: "\u001b[2m",
  dimEnd: "\u001b[22m",
  italicStart: "\u001b[3m",
  italicEnd: "\u001b[23m",
  underlineStart: "\u001b[4m",
  underlineEnd: "\u001b[24m",
  cyanStart: "\u001b[36m",
  cyanEnd: "\u001b[39m",
  yellowStart: "\u001b[33m",
  yellowEnd: "\u001b[39m",
  grayStart: "\u001b[90m",
  grayEnd: "\u001b[39m",
};

export interface MarkdownTextBlock {
  kind: "text";
  content: string;
}

export interface MarkdownTableBlock {
  kind: "table";
  token: Tokens.Table;
}

export type MarkdownBlock = MarkdownTextBlock | MarkdownTableBlock;

let markedConfigured = false;

function wrapAnsi(text: string, start: string, end: string): string {
  if (text.length === 0) {
    return text;
  }

  return `${start}${text}${end}`;
}

function bold(text: string): string {
  return wrapAnsi(text, ANSI.boldStart, ANSI.boldEnd);
}

function dim(text: string): string {
  return wrapAnsi(text, ANSI.dimStart, ANSI.dimEnd);
}

function italic(text: string): string {
  return wrapAnsi(text, ANSI.italicStart, ANSI.italicEnd);
}

function underline(text: string): string {
  return wrapAnsi(text, ANSI.underlineStart, ANSI.underlineEnd);
}

function cyan(text: string): string {
  return wrapAnsi(text, ANSI.cyanStart, ANSI.cyanEnd);
}

function yellow(text: string): string {
  return wrapAnsi(text, ANSI.yellowStart, ANSI.yellowEnd);
}

function gray(text: string): string {
  return wrapAnsi(text, ANSI.grayStart, ANSI.grayEnd);
}

function hasMarkdownSyntax(text: string): boolean {
  return MD_SYNTAX_RE.test(text.length > 500 ? text.slice(0, 500) : text);
}

function stripPromptXMLTags(text: string): string {
  return text
    .replace(/<prompt[^>]*>/gi, "")
    .replace(/<\/prompt>/gi, "")
    .replace(/<prompt_content[^>]*>/gi, "")
    .replace(/<\/prompt_content>/gi, "");
}

export function configureMarked(): void {
  if (markedConfigured) {
    return;
  }

  markedConfigured = true;
  marked.use({
    tokenizer: {
      del() {
        return undefined;
      },
    },
  });
}

export function cachedLexer(content: string): Token[] {
  const normalized = content.replace(/\r\n/g, "\n");
  if (!hasMarkdownSyntax(normalized)) {
    return [
      {
        type: "paragraph",
        raw: normalized,
        text: normalized,
        tokens: [
          {
            type: "text",
            raw: normalized,
            text: normalized,
          },
        ],
      } as Token,
    ];
  }

  const hit = tokenCache.get(normalized);
  if (hit) {
    tokenCache.delete(normalized);
    tokenCache.set(normalized, hit);
    return hit;
  }

  configureMarked();
  const tokens = marked.lexer(normalized);
  if (tokenCache.size >= TOKEN_CACHE_MAX) {
    const firstKey = tokenCache.keys().next().value;
    if (firstKey !== undefined) {
      tokenCache.delete(firstKey);
    }
  }

  tokenCache.set(normalized, tokens);
  return tokens;
}

export function stripAnsi(text: string): string {
  return stripVTControlCharacters(text);
}

// Terminal columns as silvery lays them out: CJK and most emoji take two,
// combining marks none, and ANSI styling none. Counting code points instead
// left every table row holding such text wider than its border.
export function displayWidth(text: string): number {
  return terminalDisplayWidth(text);
}

export function padAligned(
  text: string,
  width: number,
  targetWidth: number,
  align: Tokens.TableCell["align"] | null,
): string {
  const padding = Math.max(0, targetWidth - width);

  if (align === "right") {
    return `${" ".repeat(padding)}${text}`;
  }

  if (align === "center") {
    const left = Math.floor(padding / 2);
    const right = padding - left;
    return `${" ".repeat(left)}${text}${" ".repeat(right)}`;
  }

  return `${text}${" ".repeat(padding)}`;
}

function formatInlineTokens(tokens: Token[] | undefined): string {
  return (tokens ?? []).map((token) => formatToken(token)).join("");
}

// A table that does not fit in availableWidth is laid out as one
// "header: value" line per cell instead of a grid.
export function formatTable(token: Tokens.Table, availableWidth: number): string {
  const renderCell = (cell: Tokens.TableCell) =>
    formatInlineTokens(cell.tokens).trim();
  const headers = token.header.map(renderCell);
  const rows = token.rows.map((row) => row.map(renderCell));

  const columnWidths = headers.map((header, index) => {
    const rowWidths = rows.map((row) => displayWidth(row[index] ?? ""));
    return Math.max(displayWidth(header), ...rowWidths, 3);
  });

  const totalWidth =
    columnWidths.reduce((sum, width) => sum + width, 0) +
    columnWidths.length * 3 +
    1;

  if (totalWidth > availableWidth) {
    return rows
      .map((row, rowIndex) => {
        const lines = row.map((cell, cellIndex) => {
          const label = headers[cellIndex] || `Column ${cellIndex + 1}`;
          return `${label}: ${cell}`;
        });

        if (rowIndex === 0) {
          return lines.join("\n");
        }

        return ["─".repeat(Math.max(10, availableWidth - 2)), ...lines].join(
          "\n",
        );
      })
      .join("\n");
  }

  const border = (
    left: string,
    middle: string,
    join: string,
    right: string,
  ) =>
    `${left}${columnWidths
      .map((width) => middle.repeat(width + 2))
      .join(join)}${right}`;

  const renderRow = (cells: string[], isHeader: boolean) => {
    return `│ ${cells
      .map((cell, index) => {
        const align = isHeader ? "center" : (token.align[index] ?? "left");
        return padAligned(
          cell,
          displayWidth(cell),
          columnWidths[index]!,
          align,
        );
      })
      .join(" │ ")} │`;
  };

  return [
    border("┌", "─", "┬", "┐"),
    renderRow(headers, true),
    border("├", "─", "┼", "┤"),
    ...rows.map((row) => renderRow(row, false)),
    border("└", "─", "┴", "┘"),
  ].join("\n");
}

// Columns a transcript row takes around its text (message marker, list and
// quote indents, scrollbar), so a table nested in a list item or quote - which
// has no measured box of its own - still fits once it is indented.
const NESTED_TABLE_CHROME_COLUMNS = 8;

function formatCodeBlock(token: Tokens.Code): string {
  const language = token.lang?.trim();
  const code = token.text.replace(/\n+$/, "");
  const header = language ? `${gray(`[${language}]`)}${EOL}` : "";

  if (!code) {
    return header;
  }

  if (!language || !supportsLanguage(language)) {
    return `${header}${cyan(code)}${EOL}`;
  }

  return `${header}${highlight(code, { language, ignoreIllegals: true })}${EOL}`;
}

function formatListItem(
  token: Tokens.ListItem,
  orderedIndex: number | null,
): string {
  const marker = orderedIndex === null ? "-" : `${orderedIndex}.`;
  const rendered = (token.tokens ?? [])
    // A tight item holds its text in a "text" token that ends without a line
    // break, so a nested list or code block after it would continue the line.
    .map((child) =>
      child.type === "text" ? `${formatToken(child)}${EOL}` : formatToken(child),
    )
    .join("")
    .trimEnd();

  if (!rendered) {
    return `${marker}${EOL}`;
  }

  // Every line after the first - including all lines of a nested list - is
  // indented under the item here, so each level of nesting adds exactly one
  // indent.
  const lines = rendered.split(EOL);
  const formattedLines = lines.map((line, index) => {
    if (index === 0) {
      return `${marker} ${line}`;
    }

    return line.length > 0 ? `  ${line}` : line;
  });

  return `${formattedLines.join(EOL)}${EOL}`;
}

export function formatToken(token: Token): string {
  switch (token.type) {
    case "blockquote": {
      const inner = formatInlineTokens(token.tokens)
        .split(EOL)
        .filter((line) => line.length > 0)
        .map((line) => `${dim("│")} ${italic(line)}`)
        .join(EOL);

      return `${inner}${EOL}`;
    }
    case "code":
      return `${formatCodeBlock(token as Tokens.Code)}${EOL}`;
    case "codespan":
      return cyan(`\`${token.text}\``);
    case "strong":
      return bold(formatInlineTokens(token.tokens));
    case "em":
      return italic(formatInlineTokens(token.tokens));
    case "heading": {
      const content = formatInlineTokens(token.tokens);
      if (token.depth === 1) {
        return `${underline(bold(content))}${EOL}${EOL}`;
      }

      if (token.depth === 2) {
        return `${bold(content)}${EOL}${EOL}`;
      }

      return `${yellow(bold(content))}${EOL}${EOL}`;
    }
    case "hr":
      return `${dim("─".repeat(40))}${EOL}${EOL}`;
    case "image":
      return `${token.text ? `[Image: ${token.text}] ` : ""}${token.href}`;
    case "link": {
      const label = formatInlineTokens(token.tokens).trim();
      if (!label || stripAnsi(label) === token.href) {
        return underline(cyan(token.href));
      }

      return `${underline(cyan(label))}${gray(` (${token.href})`)}`;
    }
    case "list": {
      const list = token as Tokens.List;
      // start is "" for bullet lists, and an ordered list may start at 0.
      const start = typeof list.start === "number" ? list.start : 1;
      return list.items
        .map((item: Tokens.ListItem, index: number) =>
          formatListItem(item, list.ordered ? start + index : null),
        )
        .join("");
    }
    case "list_item":
      return formatListItem(token as Tokens.ListItem, null);
    case "checkbox":
      return (token as Tokens.Checkbox).checked ? "[x] " : "[ ] ";
    case "paragraph":
      return `${formatInlineTokens(token.tokens)}${EOL}`;
    case "space":
      return EOL;
    case "br":
      return EOL;
    case "text":
      return token.tokens ? formatInlineTokens(token.tokens) : token.text;
    case "escape":
      return token.text;
    case "del":
      return `~${formatInlineTokens(token.tokens)}~`;
    case "html":
      return token.text ? `${token.text}${token.block ? EOL : ""}` : "";
    case "table":
      // renderMarkdownBlocks hands top-level tables to MarkdownTable, which
      // measures its box; only tables nested in list items or blockquotes get
      // here, and they are laid out against the terminal width instead.
      return `${formatTable(
        token as Tokens.Table,
        Math.max(
          20,
          (process.stdout.columns ?? 80) - NESTED_TABLE_CHROME_COLUMNS,
        ),
      )}${EOL}`;
    case "def":
    case "tag":
    default:
      return "";
  }
}

export function renderMarkdownBlocks(text: string): MarkdownBlock[] {
  const normalized = stripPromptXMLTags(text).replace(/\r\n/g, "\n");
  const tokens = cachedLexer(normalized);
  const blocks: MarkdownBlock[] = [];
  let currentText = "";

  const flushText = () => {
    const content = currentText.replace(/\n+$/, "");
    if (content.trim().length > 0) {
      blocks.push({ kind: "text", content });
    }
    currentText = "";
  };

  for (const token of tokens) {
    if (token.type === "table") {
      flushText();
      blocks.push({ kind: "table", token: token as Tokens.Table });
      continue;
    }

    currentText += formatToken(token);
  }

  flushText();

  if (blocks.length === 0 && normalized.trim().length > 0) {
    blocks.push({ kind: "text", content: normalized.trim() });
  }

  return blocks;
}
