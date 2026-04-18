#!/usr/bin/env bun
/**
 * Standalone entry point for bun build --compile.
 * Resolves the Go engine binary next to itself and launches the Silvery TUI.
 */
import { dirname, join } from "node:path";
import { existsSync } from "node:fs";
import { detectTerminalCaps, ThemeProvider } from "silvery";
import { createApp } from "silvery/runtime";
import { createTheme } from "silvery/theme";
import React from "react";
import App from "./App.js";
import { installClipboardBridge } from "./utils/clipboardBridge.js";

installClipboardBridge();

// Resolve engine: same directory as this binary, then PATH
const selfDir = dirname(process.execPath);
const candidates = [
  join(selfDir, "chan-engine"),
  join(selfDir, "engine", "chan-engine"),
  "chan-engine",
];
const enginePath =
  process.env["CHAN_ENGINE_PATH"] ??
  candidates.find((p) => existsSync(p)) ??
  "chan-engine";

let model = "github-copilot/gpt-5.4";
let mode = "plan";
let autoMode = false;
const theme = createTheme()
  .preset("sonokai")
  .color("background", "#2C2E34")
  .color("foreground", "#E2E2E3")
  .color("cursorColor", "#E2E2E3")
  .color("cursorText", "#2C2E34")
  .color("selectionBackground", "#4A4C53")
  .color("selectionForeground", "#E2E2E3")
  .build();
const caps = detectTerminalCaps();

const args = process.argv.slice(2);
for (let i = 0; i < args.length; i++) {
  if ((args[i] === "--model" || args[i] === "-m") && args[i + 1]) {
    model = args[++i]!;
  } else if (args[i] === "--mode" && args[i + 1]) {
    mode = args[++i]!;
  } else if (args[i] === "--auto-mode") {
    autoMode = true;
  } else if (args[i] === "--help" || args[i] === "-h") {
    console.log(`Usage: chan [options]

Options:
  --model, -m <provider/model>  Model to use (default: github-copilot/gpt-5.4)
  --mode <plan|fast>            Execution mode (default: plan)
  --auto-mode                   Auto-approve non-destructive tool calls
  --help, -h                    Show this help`);
    process.exit(0);
  }
}

const app = createApp(() => () => ({}));
const handle = await app.run(
  <ThemeProvider theme={theme}>
    <App enginePath={enginePath} model={model} mode={mode} autoMode={autoMode} />
  </ThemeProvider>,
  {
    caps,
    alternateScreen: true,
    kitty: caps.kittyKeyboard,
    mouse: true,
    focusReporting: true,
    selection: true,
    textSizing: "auto",
    widthDetection: "auto",
  },
);
await handle.waitUntilExit();
