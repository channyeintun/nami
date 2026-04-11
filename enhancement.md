# Explanation-Driven Enhancement Opportunities

This file replaces the old mixed backlog. It is now a focused gap analysis for the next `gocode` workstream.

The primary reference is `sourcecode-explanation/`. Targeted reads from `sourcecode/` are used only to confirm the underlying architecture patterns without turning this file into a source audit.

## Hard Exclusions

Do not reopen these categories in the current roadmap:

- MCP
- swarm or team orchestration
- remote execution
- browser-first features
- unrelated product lines that do not strengthen the existing local coding loop

## Artifact Safety Requirement

Every opportunity below must preserve the existing artifact model.

- Parent sessions remain the authority for implementation-plan, task-list, walkthrough, diff-preview, search-report, and tool-log artifacts.
- Memory storage must stay separate from session artifacts.
- Tool expansion must preserve result budgeting, artifact spillover, and focused artifact events.
- Compaction improvements must not demote durable outputs into transcript-only summaries.

## Highest-Leverage Opportunities

### 1. Artifact-Safe Subagent Foundation

**Reference signal**

- `sourcecode-explanation/book/ch08-sub-agents.md`
- `sourcecode/Tool.ts`
- `sourcecode/tools.ts`
- `sourcecode/tasks.ts`

**Current baseline**

- `gocode/internal/agent/loop.go` already has proactive compaction, 3-attempt model recovery (`invokeModelWithRecovery`), batch tool execution via `deps.ExecuteToolBatch()`, continuation tracking with budget-aware stop conditions, and dynamic capability downgrade. This is a strong foundation for hosting child agents.

**Current gap**

- Despite the loop's sophistication, it has no delegation path — no `Agent` tool in the registry, no child context model, no child permission isolation, and no child cancellation or result-lifecycle model.

**Best opportunity**

- Add a bounded `Agent` tool with only `general-purpose` and `explore` in the first iteration.
- Support both blocking and background execution.
- Give child agents scoped tool pools and scoped permission modes.
- Return a child report or background handle to the parent and keep parent artifacts parent-owned.

**Why this beats the old roadmap**

- It is the single largest capability jump still missing from the current local architecture.
- It improves research, search, and setup turns without importing swarm, team, or remote complexity.

**What to defer**

- team agents
- swarm messaging
- remote execution
- worktree isolation in the first release

### 2. Deeper Local Tooling and Smarter Concurrency

**Reference signal**

- `sourcecode-explanation/book/ch06-tools.md`
- `sourcecode-explanation/book/ch07-concurrency.md`
- `sourcecode/Tool.ts`
- `sourcecode/tools.ts`
- `sourcecode/query.ts`

**Current baseline**

- `gocode/internal/tools/interface.go` already supports per-call `Concurrency(input)` returning `ConcurrencySerial` or `ConcurrencyParallel`.
- `gocode/internal/tools/orchestration.go` and `gocode/internal/tools/streaming_executor.go` already batch and stream ordered results with configurable semaphore (default 10 max concurrent).
- `gocode/internal/tools/registry.go` exposes 16 registered tools plus aliases (`bash`, `list_dir`, `file_read`, `file_write`, `file_edit`, `multi_replace_file_content`, `glob`, `grep`, `web_search`, `web_fetch`, `git`, `command_status`, `send_command_input`, `save_implementation_plan`, `upsert_task_list`, `save_walkthrough`).
- `gocode/internal/tools/budgeting.go` already exists for result budgeting.

**Current gap**

- limited semantic validation before execution
- shallow concurrency classification for complex shell inputs
- unregistered tools already implemented but not exposed: `file_diff_preview.go` and `file_history.go` exist in the tools directory but are not wired into the registry — these are free improvements
- no `Think` scratchpad tool (zero-cost, high-value reasoning aid from the reference system)
- no code navigation tools beyond grep (symbol search, go-to-definition)
- a smaller active tool family than the architecture it is modeled against

**Best opportunity**

- Add semantic validation to the tool contract.
- Make `bash` scheduling more input-aware.
- Expand the local tool families that fit the current product: code navigation, repository inspection, safer batch edits, and terminal follow-up.
- Grow the tool surface deliberately rather than chasing a raw count target.

**Why this beats the old roadmap**

- It increases useful work per turn immediately.
- It also makes future subagents more valuable because delegated agents inherit a stronger local toolset.

### 3. Project Memory With Selective Recall

**Reference signal**

- `sourcecode-explanation/book/ch11-memory.md`
- `sourcecode/query.ts` memory-prefetch and attachment patterns

**Current gap**

- `gocode/internal/agent/memory_files.go` only loads `AGENTS.md` and `AGENTS.local.md` instruction files.
- `gocode/internal/session/store.go` persists transcripts but does not promote durable learnings across sessions.

**Best opportunity**

- Add a project-scoped memory directory with a lightweight `MEMORY.md` index.
- Follow the four-type taxonomy: `user`, `feedback`, `project`, `reference`.
- Add async recall so only the most relevant memories enter the next turn.
- Add staleness warnings so old memories are treated as observations to verify.
- Reuse existing file tools for the memory write path.

**Why this beats the old roadmap**

- It fixes repeated user re-explanation across sessions.
- It raises agent quality without inventing new user-facing surface area.

### 4. Cache-Aware Compaction and Prompt Budgeting

**Reference signal**

- `sourcecode-explanation/book/ch05-agent-loop.md`
- `sourcecode-explanation/book/ch17-performance.md`
- `sourcecode/query.ts`

**Current baseline**

- `gocode/internal/compact/pipeline.go` already has tool truncation, summarization, and partial compaction.
- `gocode/internal/compact/tool_truncate.go` already preserves the newest result per compactable tool type.

**Current gap**

- output slot reservation is not tuned for context efficiency
- prompt sections are not organized for cache stability
- there is no section-level memoization for prompt assembly
- tool budgeting, compaction, and future memory recall are not yet coordinated as one context strategy

**Best opportunity**

- tighten output reservation and escalate only on truncation (universally useful across all providers)
- memoize stable system-prompt sections to avoid rebuilding identical content each turn (universally useful)
- make prompt construction cache-aware by placing stable sections before volatile sections — applicable to Anthropic API and compatible OpenRouter routes; no-op for providers without prompt caching (Ollama, etc.)
- build on the existing `ContinuationTracker` in `token_budget.go` which already monitors budget usage and diminishing-returns stop conditions
- unify result budgeting, compaction, continuation tracking, and future memory recall into a shared context-pressure policy

**Provider caveat**

- Prompt cache stability patterns from the reference system (`__DYNAMIC_BOUNDARY__`, sticky latches, session date memoization) are Anthropic-specific. These should be implemented behind a provider abstraction so they activate only when the current model supports prompt caching.

**Why this beats the old roadmap**

- It reclaims context and cost headroom without adding new product scope.
- It protects future subagent and memory work from avoidable prompt bloat.
- Building on `ContinuationTracker` avoids reinventing budget-awareness.

### 5. Measured UI and Milliseconds-Level Developer Experience

**Reference signal**

- `sourcecode-explanation/book/ch13-terminal-ui.md` (informs *what to measure*, not *how to build* — the reference uses a custom DOM + React reconciler with `Int32Array` packed cells and double-buffered frames, which is incompatible with Ink)
- `sourcecode-explanation/book/ch17-performance.md` (startup checkpoints, API preconnect, module-level I/O parallelism, 26-bit bitmap pre-filter for fuzzy search)
- `sourcecode/main.tsx`

**Current baseline**

- `gocode/tui/src/components/ArtifactView.tsx` already renders full markdown artifacts.
- `gocode/tui/src/components/Input.tsx`, `StreamOutput.tsx`, `PromptFooter.tsx`, and `useEvents.ts` already provide a strong Ink-based interaction model.

**Current gap**

- no startup, query, or render profiler — currently no hard data on where time is spent
- no API preconnect or other deliberate warmup path (ch17 shows ~100-200ms savings from HEAD-request warmup)
- long-session transcript and search performance are still mostly heuristic
- no dedicated subagent or memory status surfaces yet

**Best opportunity**

- instrument first, then optimize: boot-to-ready, prompt-submit-to-first-token, prompt-submit-to-first-tool-result, manual compaction duration
- add API preconnect during init (warmup HEAD request pattern from ch17)
- targeted Ink performance work based on measured bottlenecks — do not import custom renderer patterns from ch13 unless Ink proves to be the bottleneck
- add UI surfaces for subagents and memory only after the engine contracts are stable

**Why this beats the old roadmap**

- It aligns the UI with the user's stated priority: specific milliseconds-level developer experience.
- It keeps performance work tied to data instead of parity imitation.
- It avoids the trap of chasing the reference system's custom renderer when Ink may be sufficient.

## Unaddressed but Worth Noting

- **Skills system**: `gocode/internal/skills/` exists but is not covered by any opportunity above. Evaluate during Phase 2 (tools) whether skills need expansion or better integration with subagents.
- **Cost tracking**: Subagents, memory side-queries, and background extraction all increase API costs. The existing cost tracker should be extended in the relevant phases.

## Already Strong and Not the Next Bottleneck

Do not reopen these as the primary roadmap unless regressions appear:

- first-class artifacts and plan review flow
- ordered concurrent tool batching
- streaming tool execution with order-preserving yield
- current three-stage compaction pipeline (tool truncate → summarize → partial)
- Bun-based TUI workflow
- artifact-aware transcript and panel rendering
- continuation tracking with budget-aware stop conditions (`ContinuationTracker`)
