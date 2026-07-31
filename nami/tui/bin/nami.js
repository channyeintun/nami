#!/usr/bin/env node

import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { existsSync } from "node:fs";
import { spawnSync } from "node:child_process";

const __dirname = dirname(fileURLToPath(import.meta.url));
const engineBinaryName =
  process.platform === "win32" ? "nami-engine.exe" : "nami-engine";

function isFilesystemCandidate(candidate) {
  return candidate.includes("/") || candidate.includes("\\");
}

// Resolve the Go engine from installed and source-build layouts before PATH.
const candidates = [
  join(__dirname, engineBinaryName),
  join(__dirname, "..", "engine", engineBinaryName),
  engineBinaryName,
  "nami-engine",
];
const enginePath =
  candidates.find((candidate) =>
    isFilesystemCandidate(candidate) ? existsSync(candidate) : true,
  ) ?? "nami-engine";

// Set env so the TUI picks it up
process.env["NAMI_ENGINE_PATH"] ??= enginePath;

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
    process.env["NAMI_MODEL"] = args[++i];
  } else if (args[i] === "--mode" && args[i + 1]) {
    process.env["NAMI_MODE"] = args[++i];
  } else if (args[i] === "--auto-mode") {
    process.env["NAMI_AUTO_MODE"] = "true";
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

// Launch the packed TUI entrypoint.
await import("../dist/index.mjs");
