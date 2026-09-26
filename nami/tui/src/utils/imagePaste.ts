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

// osascript and PowerShell answer in well under this, but a clipboard owner
// that never responds would otherwise leave the paste pending forever with
// the helper process still running.
const CLIPBOARD_COMMAND_TIMEOUT_MS = 10_000;

function execFileAsync(file: string, args: string[]): Promise<string> {
  return new Promise((resolve, reject) => {
    execFile(
      file,
      args,
      { windowsHide: true, timeout: CLIPBOARD_COMMAND_TIMEOUT_MS },
      (error, stdout) => {
        if (error) {
          reject(error);
          return;
        }

        resolve(stdout.toString());
      },
    );
  });
}

// Nothing awaits the cleanup, and an unhandled rejection ends the whole app,
// so a failed removal is dropped here. The image has been read by then; the
// cost of a failure is a stray file in the OS temp directory.
function removeTemporaryFile(path: string): void {
  rm(path, { force: true }).catch(() => undefined);
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

  return (
    value.startsWith("/") ||
    value.startsWith("\\\\") ||
    /^[a-zA-Z]:(\\|\/)/.test(value)
  );
}

function decodeEscapedPath(value: string): string {
  let decoded = value.replace(/\\ /g, " ");
  if (!decoded.startsWith("file://")) {
    return decoded;
  }

  try {
    const fileUrl = new URL(decoded);
    const pathname = decodeURIComponent(fileUrl.pathname);
    if (fileUrl.host) {
      return `\\\\${fileUrl.host}${pathname.replace(/\//g, "\\")}`;
    }
    decoded = pathname;
  } catch {
    decoded = decoded.replace(/^file:\/\//, "");
  }

  if (/^\/[a-zA-Z]:\//.test(decoded)) {
    return decoded.slice(1);
  }

  return decoded;
}

function resolveWindowsPowerShell(): string {
  const override =
    process.env.NAMI_POWERSHELL?.trim() ||
    process.env.NAMI_WINDOWS_SHELL?.trim();
  if (override) {
    return override;
  }

  return "powershell.exe";
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

  try {
    const clipboardPath = (
      await execFileAsync("osascript", [
        "-e",
        "get POSIX path of (the clipboard as «class furl»)",
      ])
    ).trim();

    if (clipboardPath.length > 0) {
      if (!isAbsoluteImagePath(clipboardPath)) {
        return null;
      }

      const result = await readImageFile(clipboardPath);
      if (result.image) {
        return result.image;
      }
    }
  } catch {
    // Fall through to raw image clipboard extraction when no file path exists.
  }

  const outputPath = join(
    tmpdir(),
    `nami-clipboard-${process.pid}-${Date.now()}.png`,
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
    removeTemporaryFile(outputPath);
  }
}

async function readClipboardImageOnWindows(): Promise<PastedImageData | null> {
  if (process.platform !== "win32") {
    return null;
  }

  const outputPath = join(
    tmpdir(),
    `nami-clipboard-${process.pid}-${Date.now()}.png`,
  );
  const escapedOutputPath = outputPath.replace(/'/g, "''");
  const script = [
    "Add-Type -AssemblyName System.Windows.Forms | Out-Null",
    "Add-Type -AssemblyName System.Drawing | Out-Null",
    "if ([System.Windows.Forms.Clipboard]::ContainsFileDropList()) {",
    "  $paths = [System.Windows.Forms.Clipboard]::GetFileDropList()",
    "  if ($paths.Count -gt 0) { [Console]::Out.Write($paths[0]); exit 0 }",
    "}",
    "if (-not [System.Windows.Forms.Clipboard]::ContainsImage()) { exit 1 }",
    "$image = [System.Windows.Forms.Clipboard]::GetImage()",
    "if ($null -eq $image) { exit 1 }",
    `$image.Save('${escapedOutputPath}', [System.Drawing.Imaging.ImageFormat]::Png)`,
    "$image.Dispose()",
    `[Console]::Out.Write('${escapedOutputPath}')`,
  ].join("; ");

  try {
    const clipboardPath = (
      await execFileAsync(resolveWindowsPowerShell(), [
        "-NoProfile",
        "-NonInteractive",
        "-Sta",
        "-Command",
        script,
      ])
    ).trim();

    if (!isAbsoluteImagePath(clipboardPath)) {
      return null;
    }

    const result = await readImageFile(clipboardPath);
    if (!result.image) {
      return null;
    }

    if (clipboardPath === outputPath) {
      return {
        ...result.image,
        filename: "clipboard.png",
      };
    }

    return result.image;
  } catch {
    return null;
  } finally {
    removeTemporaryFile(outputPath);
  }
}

async function readClipboardImage(): Promise<PastedImageData | null> {
  if (process.platform === "darwin") {
    return readClipboardImageOnMac();
  }
  if (process.platform === "win32") {
    return readClipboardImageOnWindows();
  }
  return null;
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
  // Terminals deliver pasted line breaks as CRLF or a bare CR as well as LF,
  // and the prompt editor only understands LF.
  const normalized = text.replace(/\r\n?/g, "\n");
  if (normalized.trim().length === 0) {
    const clipboardImage = await readClipboardImage();
    return {
      text: "",
      images: clipboardImage ? [clipboardImage] : [],
      warnings: [],
    };
  }

  const dataUrlParts = extractImageDataUrls(normalized);
  if (dataUrlParts.images.length > 0) {
    return dataUrlParts;
  }

  // Dropping files onto the terminal pastes their paths separated by spaces
  // (a space inside a path arrives escaped as "\ ") or by line breaks, so
  // split before every absolute path as well as at each line.
  const parts = normalized
    .split(/ (?=\/|\\\\|[a-zA-Z]:[\\/]|file:\/\/)/)
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

  if (images.length === 0) {
    // Nothing was attached, so this is an ordinary text paste. Keep it exactly
    // as copied: the trimmed parts would drop indentation and blank lines.
    return { text: normalized, images, warnings };
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
