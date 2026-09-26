/**
 * Clipboard bridge — intercepts OSC 52 clipboard writes on stdout and
 * forwards them to the native system clipboard via `pbcopy` (macOS).
 *
 * Silvery's SelectionFeature auto-copies selected text via OSC 52 on
 * mouseup.  Terminals that support OSC 52 (Ghostty, iTerm2, Kitty,
 * WezTerm) handle this natively.  Terminal.app and some others don't,
 * so this bridge ensures the clipboard is always populated.
 *
 * Call `installClipboardBridge()` once at startup, before silvery's
 * `createApp().run()` — the monkey-patch captures all subsequent writes.
 */

import { spawn } from "node:child_process";

const OSC52_REGEX = /\x1b\]52;c;([A-Za-z0-9+/=]+)\x07/;

function resolveWindowsPowerShell(): string {
  const override =
    process.env.NAMI_POWERSHELL?.trim() ||
    process.env.NAMI_WINDOWS_SHELL?.trim();
  if (override) {
    return override;
  }

  return "powershell.exe";
}

function writeToNativeClipboard(text: string): void {
  try {
    let proc;
    if (process.platform === "darwin") {
      proc = spawn("pbcopy", [], {
        stdio: ["pipe", "ignore", "ignore"],
      });
    } else if (process.platform === "win32") {
      proc = spawn(
        resolveWindowsPowerShell(),
        [
          "-NoProfile",
          "-NonInteractive",
          "-Command",
          "$text = [Console]::In.ReadToEnd(); Set-Clipboard -Value $text",
        ],
        {
          stdio: ["pipe", "ignore", "ignore"],
          windowsHide: true,
        },
      );
    } else {
      return;
    }

    // A missing binary is reported asynchronously as an 'error' event, which
    // the surrounding try cannot catch. Left unhandled it becomes an uncaught
    // exception, and silvery ends the app on those.
    proc.on("error", () => {
      // Native clipboard unavailable — the terminal may still honor OSC 52.
    });
    proc.stdin.on("error", () => {
      // Ignore clipboard pipe shutdown races.
    });
    proc.stdin.write(text);
    proc.stdin.end();
  } catch {
    // Native clipboard bridge unavailable — no-op.
  }
}

export function installClipboardBridge(): void {
  const originalWrite = process.stdout.write.bind(process.stdout);

  process.stdout.write = function patchedWrite(
    chunk: Uint8Array | string,
    ...rest: unknown[]
  ): boolean {
    const str = typeof chunk === "string" ? chunk : chunk.toString();
    const match = OSC52_REGEX.exec(str);
    if (match?.[1]) {
      const text = Buffer.from(match[1], "base64").toString("utf-8");
      if (text.length > 0) {
        writeToNativeClipboard(text);
      }
    }
    return (originalWrite as Function).call(process.stdout, chunk, ...rest);
  } as typeof process.stdout.write;
}
