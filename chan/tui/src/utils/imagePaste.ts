import { execFile } from "node:child_process";
import { readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { basename, extname, join } from "node:path";

export interface PastedImageData {
  data: string;
  mediaType: string;
  filename?: string;
  sourcePath?: string;
}

export interface ParsedPasteParts {
  text: string;
  images: PastedImageData[];
  warnings: string[];
}

interface ReadImageFileResult {
  image: PastedImageData | null;
  warning: string | null;
}

function execFileAsync(file: string, args: string[]): Promise<void> {
  return new Promise((resolve, reject) => {
    execFile(file, args, (error) => {
      if (error) {
        reject(error);
        return;
      }

      resolve();
    });
  });
}

function isAbsoluteImagePath(value: string): boolean {
  if (!value) {
    return false;
  }

  const lower = value.toLowerCase();
  const hasImageExt = [".png", ".jpg", ".jpeg", ".gif", ".webp"].some((ext) =>
    lower.endsWith(ext),
  );
  if (!hasImageExt) {
    return false;
  }

  return value.startsWith("/") || /^[a-zA-Z]:\\/.test(value);
}

function decodeEscapedPath(value: string): string {
  return value.replace(/\\ /g, " ").replace(/^file:\/\//, "");
}

function mediaTypeFromFilename(filename: string): string {
  switch (extname(filename).toLowerCase()) {
    case ".jpg":
    case ".jpeg":
      return "image/jpeg";
    case ".gif":
      return "image/gif";
    case ".webp":
      return "image/webp";
    default:
      return "image/png";
  }
}

async function readImageFile(path: string): Promise<ReadImageFileResult> {
  try {
    const buffer = await readFile(path);
    return {
      image: {
        data: buffer.toString("base64"),
        mediaType: mediaTypeFromFilename(path),
        filename: basename(path),
        sourcePath: path,
      },
      warning: null,
    };
  } catch (error) {
    const reason = error instanceof Error ? error.message : "unknown error";
    return {
      image: null,
      warning: `Failed to load pasted image path ${path}: ${reason}`,
    };
  }
}

async function readClipboardImageOnMac(): Promise<PastedImageData | null> {
  if (process.platform !== "darwin") {
    return null;
  }

  const outputPath = join(
    tmpdir(),
    `gocode-clipboard-${process.pid}-${Date.now()}.png`,
  );
  const script = [
    "set png_data to the clipboard as «class PNGf»",
    `set fp to open for access POSIX file \"${outputPath}\" with write permission`,
    "write png_data to fp",
    "close access fp",
  ];

  try {
    await execFileAsync(
      "osascript",
      script.flatMap((line) => ["-e", line]),
    );
    const buffer = await readFile(outputPath);
    return {
      data: buffer.toString("base64"),
      mediaType: "image/png",
      filename: "clipboard.png",
    };
  } catch {
    return null;
  } finally {
    void rm(outputPath, { force: true });
  }
}

function extractImageDataUrls(text: string): ParsedPasteParts {
  const images: PastedImageData[] = [];
  const stripped = text.replace(
    /data:(image\/[a-zA-Z0-9.+-]+);base64,([A-Za-z0-9+/=\r\n]+)/g,
    (_match, mediaType: string, base64Data: string) => {
      images.push({
        data: base64Data.replace(/\s+/g, ""),
        mediaType,
      });
      return "";
    },
  );

  return {
    text: stripped.trim(),
    images,
    warnings: [],
  };
}

export async function parsePasteParts(text: string): Promise<ParsedPasteParts> {
  const trimmed = text.trim();
  if (trimmed.length === 0) {
    const clipboardImage = await readClipboardImageOnMac();
    return {
      text: "",
      images: clipboardImage ? [clipboardImage] : [],
      warnings: [],
    };
  }

  const dataUrlParts = extractImageDataUrls(text);
  if (dataUrlParts.images.length > 0) {
    return dataUrlParts;
  }

  const parts = text
    .split(/ (?=\/|[a-zA-Z]:\\|file:\/\/)/)
    .flatMap((part) => part.split("\n"))
    .map((part) => part.trim())
    .filter(Boolean);

  const images: PastedImageData[] = [];
  const warnings: string[] = [];
  const textParts: string[] = [];

  for (const part of parts) {
    const decoded = decodeEscapedPath(part);
    if (isAbsoluteImagePath(decoded)) {
      const result = await readImageFile(decoded);
      if (result.image) {
        images.push(result.image);
        continue;
      }
      if (result.warning) {
        warnings.push(result.warning);
      }
    }

    textParts.push(part);
  }

  return {
    text: textParts.join("\n"),
    images,
    warnings,
  };
}

export function parseImageReferenceIds(value: string): Set<number> {
  const ids = new Set<number>();
  const matches = value.matchAll(/\[Image #(\d+)\]/g);

  for (const match of matches) {
    const raw = match[1];
    if (!raw) {
      continue;
    }

    const id = Number.parseInt(raw, 10);
    if (Number.isFinite(id)) {
      ids.add(id);
    }
  }

  return ids;
}
