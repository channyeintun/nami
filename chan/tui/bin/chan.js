#!/usr/bin/env bun

import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { existsSync } from "node:fs";
import { spawnSync } from "node:child_process";

const __dirname = dirname(fileURLToPath(import.meta.url));

// Resolve the Go engine from installed and source-build layouts before PATH.
const candidates = [
  join(__dirname, "chan-engine"),
  join(__dirname, "..", "engine", "chan-engine"),
  "chan-engine",
];
const enginePath =
  candidates.find((candidate) =>
    candidate.includes("/") ? existsSync(candidate) : true,
  ) ?? "chan-engine";

// Set env so the TUI picks it up
process.env["CHAN_ENGINE_PATH"] ??= enginePath;

// Forward CLI args as env overrides
const args = process.argv.slice(2);
if (args[0] === "debug-view") {
  const result = spawnSync(enginePath, args, {
    stdio: "inherit",
    env: process.env,
  });
  if (result.error) {
    throw result.error;
  }
  process.exit(result.status ?? 0);
}

for (let i = 0; i < args.length; i++) {
  if ((args[i] === "--model" || args[i] === "-m") && args[i + 1]) {
    process.env["CHAN_MODEL"] = args[++i];
  } else if (args[i] === "--mode" && args[i + 1]) {
    process.env["CHAN_MODE"] = args[++i];
  } else if (args[i] === "--auto-mode") {
    process.env["CHAN_AUTO_MODE"] = "true";
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

// Launch the TUI
await import("../dist/index.js");
