#!/usr/bin/env bun
/**
 * Standalone entry point for bun build --compile.
 * Resolves the Go engine binary next to itself and launches the Ink TUI.
 */
import { dirname, join } from "node:path";
import { existsSync } from "node:fs";
import { render } from "ink";
import React from "react";
import App from "./App.js";

// Resolve engine: same directory as this binary, then PATH
const selfDir = dirname(process.execPath);
const candidates = [
  join(selfDir, "gocode-engine"),
  join(selfDir, "engine", "gocode-engine"),
  "gocode-engine",
];
const enginePath =
  process.env["GOCODE_ENGINE_PATH"] ??
  candidates.find((p) => existsSync(p)) ??
  "gocode-engine";

let model = "anthropic/claude-sonnet-4-20250514";
let mode = "plan";

const args = process.argv.slice(2);
for (let i = 0; i < args.length; i++) {
  if ((args[i] === "--model" || args[i] === "-m") && args[i + 1]) {
    model = args[++i]!;
  } else if (args[i] === "--mode" && args[i + 1]) {
    mode = args[++i]!;
  } else if (args[i] === "--help" || args[i] === "-h") {
    console.log(`Usage: gocode [options]

Options:
  --model, -m <provider/model>  Model to use (default: anthropic/claude-sonnet-4-20250514)
  --mode <plan|fast>            Execution mode (default: plan)
  --help, -h                    Show this help`);
    process.exit(0);
  }
}

render(
  React.createElement(App, { enginePath, model, mode }),
);
