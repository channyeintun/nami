#!/usr/bin/env bun
/**
 * Standalone entry point for bun build --compile.
 * Resolves the Go engine binary next to itself and launches the Silvery TUI.
 */
import { dirname, join } from "node:path";
import { existsSync } from "node:fs";
import { createTerminalProfile, ThemeProvider } from "silvery";
import { createApp } from "silvery/runtime";
import { createTheme } from "silvery/theme";
import React from "react";
import App from "./App.js";
import { installClipboardBridge } from "./utils/clipboardBridge.js";

installClipboardBridge();

// Resolve engine: same directory as this binary, then PATH
const selfDir = dirname(process.execPath);
const candidates = [
  join(selfDir, "nami-engine"),
  join(selfDir, "engine", "nami-engine"),
  "nami-engine",
];
const enginePath =
  process.env["NAMI_ENGINE_PATH"] ??
  candidates.find((p) => existsSync(p)) ??
  "nami-engine";

let model = "anthropic/claude-sonnet-5";
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
const profile = createTerminalProfile();

const args = process.argv.slice(2);
for (let i = 0; i < args.length; i++) {
  if ((args[i] === "--model" || args[i] === "-m") && args[i + 1]) {
    model = args[++i]!;
  } else if (args[i] === "--mode" && args[i + 1]) {
    mode = args[++i]!;
  } else if (args[i] === "--auto-mode") {
    autoMode = true;
  } else if (args[i] === "--help" || args[i] === "-h") {
    console.log(`Usage: nami [options]

Options:
  --model, -m <provider/model>  Model to use (default: anthropic/claude-sonnet-5)
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
    profile,
    alternateScreen: true,
    kitty: profile.caps.kittyKeyboard,
    focusReporting: true,
    textSizing: "auto",
    widthDetection: "auto",
  },
);
await handle.waitUntilExit();
