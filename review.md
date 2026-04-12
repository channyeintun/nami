# Review

Date: 2026-04-12

Scope: post-ship code review of the enhancement work summarized in `release-note-v2.md`, focused on critical issues, real bugs, and materially missing follow-up work.

Status update: the findings below were addressed in a follow-up implementation pass on 2026-04-12. This file remains the historical audit record for what was found.

## Findings

### 1. High: `agent_stop` bypasses child stop-hook policy

- Files: `gocode/cmd/gocode/subagent_background.go`, `gocode/cmd/gocode/subagent_runtime.go`
- Evidence: the background stop path calls `bg.cancel()` immediately in `stopBackgroundAgent`, while child stop-hook evaluation only happens inside the child loop via `BeforeStop` in `executeSubagent`.
- Failure mode: a user-triggered `agent_stop` can cancel the child context before the child reaches the hook-controlled stop path, so a stop hook that is supposed to block early completion never gets a chance to veto the stop.
- Impact: the most important Phase 4 policy feature is ineffective for the highest-value stop scenario, which is explicit user cancellation of a background child.
- Suggested direction: change `agent_stop` into a cooperative stop request that the child loop observes first, then add an explicit force-cancel fallback only if policy allows or the child does not respond.

### 2. Medium: parent turns still do not honor stop hooks

- Files: `gocode/internal/agent/query_stream.go`, `gocode/cmd/gocode/main.go`, `gocode/internal/hooks/types.go`
- Evidence: the shared query loop now exposes `BeforeStop`, but the top-level runtime `QueryDeps` in `main.go` does not populate it; only child runs wire stop hooks.
- Failure mode: hook authors can prevent a child from stopping early, but they cannot apply the same policy to the main agent turn.
- Impact: the lifecycle model is still inconsistent between parent and child despite the roadmap now reading as fully complete.
- Suggested direction: wire `HookStop` and `HookStopFailure` through the main runtime so the same stop policy contract applies to both parent and child execution.

### 3. Medium: TypeScript post-edit diagnostics can pull in transient package-runner behavior

- File: `gocode/internal/tools/post_edit_diagnostics.go`
- Evidence: `runTypeScriptDiagnostics` chooses `bunx` or `npx` whenever those launchers exist on the machine.
- Failure mode: a file write can trigger package-runner resolution for `tsc` even when the repository does not have a local TypeScript toolchain pinned.
- Impact: post-edit diagnostics can unexpectedly depend on global tooling state or transient package downloads, which weakens the local-safety and determinism bar that the file-tool work was trying to improve.
- Suggested direction: only run TypeScript diagnostics when a local checker is already present, such as a repo-local `node_modules/.bin/tsc`, an explicitly configured script, or a known pinned workspace tool.

## No Other Critical Findings

- I did not find additional critical bugs in the shipped file-tool semantics split, edit-failure taxonomy, patch-grade edit path, invocation lineage transport, or TUI metadata rendering.
- The review above is a focused post-ship audit, not a replacement for future runtime or integration testing.
