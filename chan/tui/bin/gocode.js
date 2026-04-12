#!/usr/bin/env bun

import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { existsSync } from "node:fs";

const __dirname = dirname(fileURLToPath(import.meta.url));

// Resolve the Go engine from installed and source-build layouts before PATH.
const candidates = [
  join(__dirname, "gocode-engine"),
  join(__dirname, "..", "engine", "gocode-engine"),
  "gocode-engine",
];
const enginePath =
  candidates.find((candidate) =>
    candidate.includes("/") ? existsSync(candidate) : true,
  ) ?? "gocode-engine";

// Set env so the TUI picks it up
process.env["GOCODE_ENGINE_PATH"] ??= enginePath;

// Forward CLI args as env overrides
const args = process.argv.slice(2);
for (let i = 0; i < args.length; i++) {
  if ((args[i] === "--model" || args[i] === "-m") && args[i + 1]) {
    process.env["GOCODE_MODEL"] = args[++i];
  } else if (args[i] === "--mode" && args[i + 1]) {
    process.env["GOCODE_MODE"] = args[++i];
  } else if (args[i] === "--help" || args[i] === "-h") {
    console.log(`Usage: gocode [options]

Options:
  --model, -m <provider/model>  Model to use (default: anthropic/claude-sonnet-4-20250514)
  --mode <plan|fast>            Execution mode (default: plan)
  --help, -h                    Show this help`);
    process.exit(0);
  }
}

// Launch the TUI
await import("../dist/index.js");
