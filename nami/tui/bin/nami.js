#!/usr/bin/env node

import { fileURLToPath } from "node:url";
import { dirname, join } from "node:path";
import { existsSync } from "node:fs";
import { spawn } from "node:child_process";

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
const resolvedEnginePath =
  candidates.find((candidate) =>
    isFilesystemCandidate(candidate) ? existsSync(candidate) : true,
  ) ?? "nami-engine";

// Set env so the TUI picks it up. An explicit override applies to the engine
// subcommands below as well.
process.env["NAMI_ENGINE_PATH"] ??= resolvedEnginePath;
const enginePath = process.env["NAMI_ENGINE_PATH"];

// Subcommands the Go engine implements itself instead of the TUI.
const engineSubcommands = new Set(["debug-view", "mcp", "timing-summary"]);

// Run the engine in the foreground and exit with its status. Signals sent to
// the launcher are passed on, so stopping nami does not orphan the engine.
function runEngine(engineArgs) {
  const child = spawn(enginePath, engineArgs, { stdio: "inherit" });
  const forwardedSignals = ["SIGINT", "SIGTERM", "SIGHUP"];
  const forward = (signal) => child.kill(signal);
  for (const signal of forwardedSignals) {
    process.on(signal, forward);
  }

  child.on("error", (error) => {
    console.error(`nami: could not run ${enginePath}: ${error.message}`);
    process.exit(1);
  });
  child.on("exit", (code, signal) => {
    for (const forwardedSignal of forwardedSignals) {
      process.off(forwardedSignal, forward);
    }
    if (signal) {
      // End the same way, so the caller sees the signal and not a status.
      process.kill(process.pid, signal);
      return;
    }
    process.exit(code ?? 1);
  });
}

// Forward CLI args as env overrides
function applyOptions(args) {
  for (let i = 0; i < args.length; i++) {
    if ((args[i] === "--model" || args[i] === "-m") && args[i + 1]) {
      process.env["NAMI_MODEL"] = args[++i];
    } else if (args[i] === "--mode" && args[i + 1]) {
      process.env["NAMI_MODE"] = args[++i];
    } else if (args[i] === "--auto-mode") {
      process.env["NAMI_AUTO_MODE"] = "true";
    } else if (args[i] === "--help" || args[i] === "-h") {
      console.log(`Usage: nami [options]
       nami mcp <add|add-json|list|get|remove> [args]
       nami debug-view --file <debug.log>

Options:
  --model, -m <provider/model>  Model to use (default: anthropic/claude-sonnet-5)
  --mode <plan|fast>            Execution mode (default: plan)
  --auto-mode                   Auto-approve non-destructive tool calls
  --help, -h                    Show this help`);
      process.exit(0);
    }
  }
}

const args = process.argv.slice(2);
if (engineSubcommands.has(args[0])) {
  runEngine(args);
} else {
  applyOptions(args);
  // Launch the packed TUI entrypoint.
  await import("../dist/index.mjs");
}
