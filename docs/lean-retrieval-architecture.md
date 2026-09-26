# Lean Retrieval Architecture

This document describes the lean retrieval architecture now implemented in `nami`.

It replaces the old idea of storing durable code summaries with a repo-first retrieval path that selects fresh, live context per turn.

## Summary

Nami now uses five cooperating context layers:

1. **Working context** — current prompt, recent tool output, active session state, git state.
2. **Live retrieval graph** — a session-scoped, in-memory graph over repository structure.
3. **Preference memory** — durable user/project guidance for non-derivable conventions only.
4. **Session memory** — structured per-session continuity state extracted from the transcript.
5. **Compaction and attempt log** — transcript shrinking plus short-lived failure history to avoid repeated mistakes.

The codebase remains the source of truth. Retrieval reads live files from disk and injects excerpts with provenance instead of relying on cached code summaries. Session memory and compaction complement retrieval; they do not replace it.

## Why this architecture

The old memory-heavy shape risks wasting tokens on stale or low-value code facts.

The new design optimizes for:

- exact anchors over vague recall
- live repository reads over cached summaries
- structural expansion over embedding-first search
- token budget discipline
- fast invalidation when files change
- session-local retry prevention
- continuity preservation when context pressure rises

## Core design rules

1. **Repo first.** Source files, tests, tool output, and git state outrank any stored note.
2. **No durable code summaries.** Persistent memory is for preferences and conventions, not repo facts.
3. **Session-scoped graph.** Structural retrieval state lives in memory for the active session only.
4. **Pressure aware.** Retrieval shrinks or skips when prompt pressure rises.
5. **Continuity preserved separately.** Session memory and compaction keep working state without turning durable memory into a repo cache.
6. **Cheap invalidation.** Touched files are re-parsed when their mod time changes.

## Main components

### 1. Query state owns a session graph

`QueryState` now carries a session-scoped `RetrievalGraph`.

- `nami/internal/agent/query_stream.go`
- `NewQueryState(...)` initializes `Graph: NewRetrievalGraph(ctx.CurrentDir)`

This means the graph persists across turns within one session instead of being rebuilt from scratch every turn.

### 2. Per-turn live retrieval

The retrieval pass runs during the query loop before model invocation.

- `nami/internal/agent/loop_retrieval.go`
- `runLiveRetrieval(...)`, called from `runLiveRetrievalStage` in `nami/internal/agent/iteration_pipeline.go`

Per turn it does this:

1. gather recent tool output
2. extract exact anchors from user prompt, git status, and tool output
3. score candidate files through the retrieval graph
4. read top snippets live from disk
5. inject a `<live_context>` section into the prompt

The injected content is file-backed and attributed:

```xml
<live_context>
<file path="/abs/path/to/file.go" source="exact anchor">
...
</file>
</live_context>
```

### 3. Retrieval graph

The graph is implemented in:

- `nami/internal/agent/retrieval_graph.go`

It is a session-scoped in-memory index over repository structure.

#### Node types

- file
- symbol
- test
- error signature
- tool-result artifact
- preference record

#### Edge types

- file contains symbol
- file imports file
- symbol references symbol
- test covers file/symbol
- tool output mentions file/symbol
- diff/staging overlay
- session touched file
- preference applies

#### Supported languages

The graph lazily parses multiple languages with lightweight regex-based structure extraction:

- Go
- TypeScript / JavaScript
- Python
- Rust
- Ruby
- Java
- C / C++

It extracts symbols, import-like edges, and common test relationships.

## Retrieval flow

### Anchor extraction

`nami/internal/agent/retrieval.go`

Anchors come from exact signals, especially:

- file paths in the prompt or tool output
- symbol names recognized from known graph nodes
- error signatures from failing commands/tool output
- modified files from git status
- files touched earlier in the session

### Graph scoring

The scorer prefers deterministic, high-signal context.

Strong signals include:

- exact file anchors
- staged or modified files
- recent session-touched files
- current error context
- structural 1-hop neighbors

If the first hop is sparse, retrieval expands a second hop with a penalty instead of exploding context size.

### Live snippet reads

After ranking, Nami reads only the top candidate files live from disk and injects small excerpts.

Candidates are limited to files inside the project: the git root above the working directory, or the working directory itself outside a repository, compared with symlinks resolved. Anchors come from untrusted text such as tool output, and retrieval reads without the permission checks a `read_file` call gets, so a path that leads outside the project, whether absolute, through `../`, or through a link in the project, is never read.

Key limits in `retrieval.go`:

- soft retrieval budget: about `3000` tokens
- max scored candidates: `24`
- max injected snippets: `4`
- max snippet bytes per file: `2000`

## Durable memory is narrowed to preferences

Durable memory still exists, but its role is narrower.

- `nami/internal/agent/memory_files.go`

`AGENTS.md` remains instruction-like context.

`MEMORY.md` recall is now framed as **preferences and conventions** only:

- workflow constraints
- style decisions
- non-derivable project guidance

The prompt text explicitly tells the model to treat these as selective guidance and to verify code facts against the live repository.

That is the key architectural shift: durable memory no longer acts as the main source for code understanding.

## Session memory sits beside retrieval

Session memory is a separate layer from durable preference memory and separate from retrieval.

- `nami/internal/engine/session_memory.go`
- `nami/internal/agent/session_memory.go`

Purpose:

- preserve current task state across compaction
- keep active files, workflow, errors, decisions, and worklog in structured form
- carry forward continuity without treating the transcript itself as the only working memory

Important properties:

- session-scoped, not cross-session
- derived from the live transcript, not from cached code summaries
- deduplicated against durable memory and recent transcript content
- refreshed after significant progress, tool activity, or compaction

The important boundary is:

- **retrieval** answers "what live repo context should be loaded now?"
- **session memory** answers "what current task state must survive transcript pressure?"

These are related but not interchangeable.

## Session attempt log

Nami also keeps a short-lived session attempt log.

Purpose:

- record failed commands and error signatures
- surface relevant failures back into the prompt
- prevent repeated retry loops in the same task

This is session-local state, not durable memory.

It is especially useful when the model keeps retrying the same failing edit or command with stale assumptions.

## Context pressure and shared budget

`nami/internal/agent/context_pressure.go`

Live retrieval shares the same pressure gate used by memory recall.

When context gets hot:

- memory recall can be skipped
- live retrieval can also be skipped
- retrieval budget can be reduced
- compaction remains the higher-priority safety valve

This keeps retrieval and compaction from competing independently for tokens.

## Compaction is now continuity-aware

Compaction is no longer just a blunt summary fallback.

- `nami/internal/compact/pipeline.go`
- `nami/internal/compact/summarize.go`
- `nami/internal/engine/session_helpers.go`

The pipeline now has multiple cooperating steps:

1. tool-result truncation / microcompaction
2. full summarization when needed
3. recent-window partial compaction when that preserves more fidelity

Compaction also coordinates with session memory:

- session memory is refreshed around compaction events
- compaction prompts are told what session memory already preserves
- summaries can avoid repeating facts already carried in session memory

This changes the role of compaction. It is no longer only a token emergency valve. It is also a continuity-preserving layer that works with session memory under pressure.

## Invalidation model

The graph is designed to be cheap to refresh.

### File invalidation

When a file changes:

- its graph nodes/edges are invalidated
- the next access re-parses the file lazily

### Git overlay invalidation

When git status changes:

- diff/staging edges are rebuilt without rebuilding the whole graph

### Session scope

The graph is in memory only for the active session.

No disk persistence. No durable code cache. No cross-session graph state.

## Telemetry

The context system emits explicit runtime telemetry.

Defined in:

- `nami/internal/ipc/protocol.go`
- `nami/internal/timing/analysis.go`

Key events:

- `retrieval_used`
  - snippet count
  - tokens used
  - anchor count
  - edges expanded
  - skipped flag
- `attempt_log_surfaced`
  - entry count
  - tokens used
  - whether attempt-log content was injected
- `attempt_repeated`
  - emitted when a new tool failure matches a prior logged failure

Key compaction/session-memory signals:

- whether session memory was present
- whether session memory was fresh at compaction time
- whether microcompaction fired
- how many tokens compaction saved

This telemetry is surfaced to the TUI and timing analysis so retrieval and continuity behavior stay visible, not hidden.

## Relationship to the web docs diagrams

The architecture section in `web/docs.html` reflects this design at two levels.

It shows two complementary views:

1. **Core Harness** — TUI, Go engine, retrieval, memory, planner, tools, and LLM infrastructure.
2. **Per-Turn Retrieval Pipeline** — retrieval-specific flow: input signals → anchor extraction → session graph → scoring/budgeting → LLM, with invalidation and pressure gating.

Related assets:

- `web/docs.html`

The important conceptual mapping is:

- **Prompt / Git Diff / Tool Logs** feed anchor extraction
- **Session Graph** persists across turns
- **Scoring** ranks context under a token budget
- **Invalidation** refreshes graph structure after changes
- **Pressure Gate** reduces or skips retrieval under context pressure
- **Session Memory** preserves structured working state outside the retrieval diagram
- **Compaction** preserves continuity when transcript pressure still exceeds budget

## What this architecture is not

This system is intentionally **not**:

- a vector database
- an embedding-first retrieval system
- a durable store of code summaries
- a cross-session episodic task memory system

It is a lightweight, repo-first retrieval layer inside a broader context system that also includes session memory and compaction.

## Key source files

- `nami/internal/agent/query_stream.go` — session state owns the graph
- `nami/internal/agent/loop_retrieval.go` — runs retrieval
- `nami/internal/agent/loop_events.go` — emits retrieval and attempt-log telemetry
- `nami/internal/agent/retrieval.go` — anchor extraction, scoring entrypoint, live snippet reads
- `nami/internal/agent/retrieval_graph.go` — graph structure, parsing, invalidation, scoring expansion
- `nami/internal/agent/context_pressure.go` — pressure gating and retrieval budget
- `nami/internal/agent/memory_files.go` — preference-framed durable memory recall
- `nami/internal/agent/session_memory.go` — prompt-facing session-memory snapshot formatting
- `nami/internal/engine/session_memory.go` — session-memory extraction and refresh policy
- `nami/internal/compact/pipeline.go` — microcompaction, summarization, partial compaction
- `nami/internal/compact/summarize.go` — compaction prompts and session-memory coordination
- `nami/internal/ipc/protocol.go` — retrieval and attempt-log telemetry events
- `nami/internal/timing/analysis.go` — timing summary and compaction/session-memory analysis

## Final takeaway

Nami’s context architecture now works by combining:

- live repo reads
- exact anchors
- a session-scoped structural graph
- preference-only durable memory
- session-scoped structured session memory
- continuity-aware compaction
- session-local retry prevention
- token-aware prompt assembly

The result is lower-trust durable memory, higher-trust live context, explicit continuity state, and a retrieval path that stays fast, cheap to invalidate, and aligned with the current repository state.