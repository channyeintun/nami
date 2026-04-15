# Progress

## 2026-04-16

- Researched context and memory handling across the reference tools under `reference/` excluding Silvery, compared them against Chan's implementation, and documented ratings, strengths, weaknesses, and recommended follow-up work in `docs/reference-context-memory-comparison.md`.
- Added an explicit Chan self-rating to the context and memory comparison report so the scorecard applies the same standard to Chan as to the reference tools.
- Reconsidered Chan's memory-recall scoring to treat deterministic recall as a strength rather than a primary weakness, updated the report accordingly, and created `enhancement-plan.md` focused on session-memory extraction and advanced continuity-aware compaction.
- Implemented the first enhancement slice: added a session-memory artifact with prompt injection, automatic session-memory refresh after significant turns, and richer compaction telemetry carrying token savings, microcompaction, and session-memory presence.
- Made session memory incremental and freshness-aware by merging prior extracted state into each refresh, adding update metadata, deduplicating and capping working-memory sections, and requiring fresh session memory before earlier proactive compaction kicks in.
- Added rollout controls for the new context features by introducing config and env flags for session-memory extraction and microcompaction, gating both code paths, and surfacing their runtime state in `/status`.
- Extended compaction timing telemetry with session-memory availability and freshness metadata, token savings, and microcompaction details, and made manual `/compact` refresh the session-memory artifact immediately after compaction.
- Reworked session-memory extraction to follow Claude Code more closely by gating initialization and refresh on token and tool-call thresholds, switching to a richer structured template with workflow and worklog sections, and deduplicating extracted notes against durable memory before prompt injection.
- Expanded continuity-aware compaction heuristics to account for fresh session memory, pending tool chains, retry loops, and recent file focus, and upgraded microcompaction so truncated tool results retain file or command identity instead of collapsing to a generic marker.
- Added a non-test trace-tuning path by introducing a `timing-summary` CLI command over `timings.ndjson`, so real sessions can be inspected for compaction strategy mix, session-memory freshness at compaction time, token savings, and follow-up threshold recommendations.
- Tightened the quality of the new context-memory runtime by fixing over-eager session-memory refreshes on tool-heavy turns, narrowing retry-loop detection to repeated failures instead of any two errors, propagating real session titles instead of the generic artifact title, and filtering extracted notes against the recent live transcript as well as durable memory.
- Added the enhancement planning and reference-comparison markdown documents to the repository so the implementation history, ratings, and rollout plan are tracked alongside the code changes.
- Closed the two remaining gaps for Claude Code parity: (1) added LLM-mediated session memory refinement so extraction captures reasoning and intent beyond what pure heuristics can collect, gated by a token-delta threshold to control cost; (2) made compaction session-memory-aware by injecting already-preserved session memory content into the compaction prompt so the summarizer produces a complementary summary instead of repeating facts.
- Tightened all system prompts across the codebase for extreme concision: compaction templates, session memory refinement, main system prompt, plan mode, subagent variants, capability fallback, memory/skill injection preambles, and the compaction session-memory hint. Sacrificed grammar for precision throughout.

## 2026-04-15

- Fixed the repeated TUI out-of-memory crash caused by oversized child-agent summaries being duplicated into live tool results, background-agent updates, and replayed UI state.
- Completed Phase 1 MCP client integration for Chan: layered user and workspace config, explicit `stdio`/`sse`/`http`/`ws` transport support, runtime server management, dynamic `mcp__<server>__<tool>` registration, config-driven permission mapping, and MCP status visibility in `/status` and the README.
- Validated the MCP work with `go build ./...`, rebuilt and installed the latest `chan` and `chan-engine` into `~/.local/bin`, and ran a live stdio smoke check against the Go MCP SDK hello server.
- Changed plan mode to `Ultrathink`: removed write blocking, updated the plan-mode system prompt, and updated the `/plan` command description so explicit create/modify requests are allowed in plan mode.