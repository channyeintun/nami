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

const usage = `Usage: nami [options]
       nami mcp <add|add-json|list|get|remove> [args]
       nami debug-view --file <debug.log>

Options:
  --model, -m <provider/model>  Model to use (default: anthropic/claude-sonnet-5)
  --mode <plan|fast>            Execution mode (default: plan)
  --auto-mode                   Auto-approve non-destructive tool calls
  --help, -h                    Show this help`;

// Options that take a value, and the environment variable each one sets.
const valueOptions = new Map([
  ["--model", "NAMI_MODEL"],
  ["-m", "NAMI_MODEL"],
  ["--mode", "NAMI_MODE"],
]);

function exitWithUsageError(message) {
  console.error(`nami: ${message}\nRun "nami --help" for usage.`);
  process.exit(2);
}

// Forward CLI args as env overrides. A mistyped or incomplete option stops
// here: ignoring it would start a session with settings the user did not ask
// for.
function applyOptions(args) {
  for (let i = 0; i < args.length; i++) {
    const arg = args[i];
    if (arg === "--help" || arg === "-h") {
      console.log(usage);
      process.exit(0);
    }
    if (arg === "--auto-mode") {
      process.env["NAMI_AUTO_MODE"] = "true";
      continue;
    }

    // Long options also take their value as "--model=name".
    const equals = arg.startsWith("--") ? arg.indexOf("=") : -1;
    const name = equals === -1 ? arg : arg.slice(0, equals);
    const envName = valueOptions.get(name);
    if (!envName) {
      exitWithUsageError(
        arg.startsWith("-") ? `unknown option ${arg}` : `unexpected argument ${arg}`,
      );
    }
    const value = equals === -1 ? args[++i] : arg.slice(equals + 1);
    if (!value || value.startsWith("-")) {
      exitWithUsageError(`${name} needs a value`);
    }
    if (name === "--mode" && value !== "plan" && value !== "fast") {
      exitWithUsageError(`--mode must be plan or fast, not ${value}`);
    }
    process.env[envName] = value;
  }
}

// Silvery, the TUI renderer, needs Node.js 24 or newer; older releases fail
// deep inside it with a SyntaxError. The release bundle cannot even be parsed
// by them, so the bin/nami and nami.cmd wrappers check first; this covers
// running bin/nami.js directly. Bun and Deno report their own Node
// compatibility versions, so only real Node is checked.
function exitIfNodeTooOld() {
  const major = Number(process.versions.node.split(".")[0]);
  if (process.versions.bun || process.versions.deno || major >= 24) {
    return;
  }
  console.error(
    `nami requires Node.js 24 or newer, but this is ${process.version}. Upgrade Node.js, or run nami with Bun or Deno.`,
  );
  process.exit(1);
}

const args = process.argv.slice(2);
if (engineSubcommands.has(args[0])) {
  runEngine(args);
} else {
  applyOptions(args);
  exitIfNodeTooOld();
  // Launch the packed TUI entrypoint.
  await import("../dist/index.mjs");
}
