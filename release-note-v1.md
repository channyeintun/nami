# Release Note V1

Date: 2026-04-12

Scope: completion of the explanation-driven roadmap across runtime measurement, tool depth, subagents, memory, compaction and cache, and terminal UX, plus the immediate remediation pass from `review-v1.md`.

## Highlights

- Completed roadmap Phases 1 through 6 with the new progress dashboard now reflecting the full milestone as shipped.
- Expanded the local tool surface substantially, including file-history restore flows, semantic validation, parser-backed Go navigation, repository/dependency overviews, background command lifecycle tools, and explicit scratchpad support.
- Added bounded child-agent delegation with fresh-context explore and general-purpose modes, async lifecycle controls, live TUI panels, transcript notices, and child-cost visibility.
- Added durable `MEMORY.md` loading, structured memory recall, side-query selection, validation, staleness cues, and explicit write guidance for project and user memory.
- Improved prompt assembly and long-session behavior with section memoization, adaptive output budgeting, provider-aware cache ordering, shared context-pressure heuristics, and reviewable compact-summary artifacts.
- Upgraded the terminal UI with startup warmup visibility, turn-latency checkpoints, transcript search, artifact focus surfaces, background agent/command panels, prompt metrics, and queued-prompt visibility.

## Shipped Changes

### Runtime and Tools

- Added session timing checkpoints and continuation stop telemetry.
- Added `file_diff_preview`, `file_history`, `file_history_rewind`, `think`, `symbol_search`, `project_overview`, `dependency_overview`, `go_definition`, and `go_references`.
- Completed the background command lifecycle with `list_commands`, `command_status`, `send_command_input`, `stop_command`, and `forget_command`.
- Added schema-backed tool validation and stronger bash concurrency classification.

### Subagents

- Added synchronous `explore` child agents and broader `general-purpose` child agents with explicit tool allowlists.
- Added async launch plus `agent_status` and `agent_stop`.
- Surfaced background child lifecycle, transcript updates, and delegated cost tracking in the TUI.

### Memory

- Added project and user `MEMORY.md` index loading with caps and age-aware caveats.
- Added bounded heuristic recall plus model-backed side-query recall.
- Added canonical entry parsing, note loading, validation warnings, and separate memory-recall cost tracking.

### Compaction and Cache

- Added prompt-section memoization and adaptive output reservation.
- Added shared context-pressure policy across compaction, recall, and continuation.
- Added provider-gated cache-stable prompt ordering and continuation-aware pressure coordination.

### Terminal UX

- Added async API warmup and visible turn-latency checkpoints.
- Added transcript search, stronger artifact focus visibility, background command and agent panels, and queued prompt chrome.
- Improved prompt editing ergonomics with restored-history cursor placement and live footer metrics.

## Reliability and Safety Fixes

The follow-up remediation pass from `review-v1.md` is included in this milestone:

- Restricted session-wide `Allow Safe` behavior to read-only tools and non-destructive shell commands instead of auto-approving all non-`bash` writes.
- Replaced hardcoded `/bin/zsh` execution with supported local-shell fallback resolution.
- Surfaced partial failures from `file_history_rewind` instead of silently reporting success.
- Extended schema validation to enforce `anyOf` and `allOf` required-field contracts.
- Surfaced unreadable session-artifact warnings during resume and lookup instead of silently dropping them.
- Corrected the final Phase 6 dashboard status so the top-level tracker and completion dashboard agree.

## User-Visible Notes

- The permission prompt label now reads `Allow Safe (This Session)` to match the runtime behavior.
- Resume can now surface recoverable warnings if an existing session artifact is unreadable.
- Shell-backed tools are more portable across Linux environments that do not ship zsh.

## Validation

- `bun run build` passed for the TUI.
- `go build ./cmd/gocode` passed for the Go engine.
