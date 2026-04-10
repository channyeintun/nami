import { stripVTControlCharacters } from "node:util";
import { highlight, supportsLanguage } from "cli-highlight";
import { marked, type Token, type Tokens } from "marked";

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

export function displayWidth(text: string): number {
  return Array.from(stripAnsi(text)).length;
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
  depth: number,
  orderedIndex: number | null,
): string {
  const indent = "  ".repeat(depth);
  const marker = orderedIndex === null ? "-" : `${orderedIndex}.`;
  const rendered = (token.tokens ?? [])
    .map((child) => formatToken(child, depth + 1))
    .join("")
    .trimEnd();

  if (!rendered) {
    return `${indent}${marker}${EOL}`;
  }

  const lines = rendered.split(EOL);
  const formattedLines = lines.map((line, index) => {
    if (index === 0) {
      return `${indent}${marker} ${line}`;
    }

    return line.length > 0 ? `${indent}  ${line}` : line;
  });

  return `${formattedLines.join(EOL)}${EOL}`;
}

export function formatToken(token: Token, listDepth = 0): string {
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
    case "list":
      return (token as Tokens.List).items
        .map((item: Tokens.ListItem, index: number) =>
          formatListItem(
            item,
            listDepth,
            (token as Tokens.List).ordered
              ? Number((token as Tokens.List).start || 1) + index
              : null,
          ),
        )
        .join("");
    case "list_item":
      return formatListItem(token as Tokens.ListItem, listDepth, null);
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
    const content = currentText.replace(/\n+$/, "").trim();
    if (content.length > 0) {
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
