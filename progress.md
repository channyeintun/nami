# Progress

## Working Rules

- Follow `plan.md` as the active execution baseline.
- Use `sourcecode-explanation/` as the primary reference and targeted `sourcecode/` reads to confirm architecture decisions.
- Do not add tests.
- Do not plan MCP, swarm or team orchestration, remote execution, or unrelated product lines.
- Preserve first-class artifacts across every future phase.
- After each completed task: update this file, run formatting, and create a git commit.

## Current Status

| Workstream                          | Status    | Scope | Notes                                                                                      |
| ----------------------------------- | --------- | ----- | ------------------------------------------------------------------------------------------ |
| Planning refresh                    | completed | S     | 2026-04-12 explanation-driven roadmap replaced stale parity-era planning docs.             |
| Phase 1 runtime measurement         | in progress | S   | Checkpoint logging and artifact ownership contract landed; threshold validation remains.   |
| Phase 2 tool depth                  | planned   | L     | Register existing tools, add Think tool, semantic validation, input-aware concurrency.     |
| Phase 3 subagents                   | planned   | XL    | Parent-child delegation, fresh context model, permission isolation, sidechain transcripts. |
| Phase 4 memory                      | planned   | L     | Four-type taxonomy, MEMORY.md index, async recall, staleness warnings.                    |
| Phase 5 compaction and cache        | planned   | M     | Output slot reservation, prompt memoization, provider-gated cache stability.               |
| Phase 6 UI and developer experience | planned   | M     | Data-driven: API preconnect, measured Ink optimizations, subagent/memory UI surfaces.      |

## Task Log

### 2026-04-12

- Completed: reviewed `sourcecode-explanation` chapters 5, 6, 7, 8, 11, 13, and 17 as the primary design reference for the next roadmap.
- Completed: cross-checked targeted slices in `sourcecode/Tool.ts`, `sourcecode/tools.ts`, `sourcecode/query.ts`, `sourcecode/tasks.ts`, and `sourcecode/main.tsx` to confirm the reference architecture patterns.
- Completed: audited the current `gocode` seams in the agent loop, tool system, compaction pipeline, session storage, and TUI so the new roadmap reflects the actual codebase rather than the previous backlog.
- Updated: replaced the stale artifact-only and parity-era planning docs with a new roadmap centered on subagents, tools and concurrency, memory, compaction and cache behavior, UI, and milliseconds-level developer experience.
- Note: this was a planning-only task. No implementation work was performed.
- Completed: added a session-local checkpoint logger that writes structured JSONL timing records to `timings.ndjson` under each session directory.
- Completed: instrumented engine startup, per-query first-token latency, first tool-result latency, first artifact-focus latency, and compaction duration without changing runtime behavior.
- Completed: made session artifact ownership explicit in the artifact manager via normalized ownership metadata (`owner_scope`, `owner_id`, `owner_authority`) while preserving `session_id` compatibility for existing lookups.
- Completed: routed tool-log artifact persistence through the session upsert path so implementation plans, task lists, walkthroughs, search reports, diff previews, and tool logs all share the same parent-session ownership contract.
- Note: Phase 1 is now in progress rather than planned; the remaining work is continuation and budgeting threshold validation against real timing data.

## Next Planning Baseline

See `plan.md` for full phase ordering rationale and cross-cutting concerns (cost tracking, provider abstraction, skills evaluation).
