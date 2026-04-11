# Explanation-Driven Enhancement Plan

## Goal

Replace the closed parity-era roadmap with a new roadmap driven by the architecture patterns in `sourcecode-explanation/` and confirmed with targeted reads from `sourcecode/`. Improve only the areas that materially raise `gocode` quality for local coding work:

- subagents, but not swarm/team orchestration
- tool depth and concurrent execution
- project memory
- compaction and prompt-budget behavior
- TUI/UI
- milliseconds-level developer experience

## Reference Basis

Primary reference:

- `sourcecode-explanation/book/ch05-agent-loop.md`
- `sourcecode-explanation/book/ch06-tools.md`
- `sourcecode-explanation/book/ch07-concurrency.md`
- `sourcecode-explanation/book/ch08-sub-agents.md`
- `sourcecode-explanation/book/ch11-memory.md`
- `sourcecode-explanation/book/ch13-terminal-ui.md`
- `sourcecode-explanation/book/ch17-performance.md`

Targeted source cross-checks:

- `sourcecode/Tool.ts`
- `sourcecode/tools.ts`
- `sourcecode/query.ts`
- `sourcecode/tasks.ts`
- `sourcecode/main.tsx`

Current implementation seams reviewed for planning:

- `gocode/internal/agent/loop.go`
- `gocode/internal/agent/memory_files.go`
- `gocode/internal/agent/token_budget.go`
- `gocode/internal/tools/interface.go`
- `gocode/internal/tools/registry.go`
- `gocode/internal/tools/orchestration.go`
- `gocode/internal/tools/streaming_executor.go`
- `gocode/internal/compact/pipeline.go`
- `gocode/internal/session/store.go`
- `gocode/tui/src/components/ArtifactView.tsx`
- `gocode/tui/src/components/Input.tsx`
- `gocode/tui/src/hooks/useEvents.ts`

## Non-Negotiable Guardrails

- Keep artifacts first-class. No roadmap item may demote implementation plans, task lists, walkthroughs, diff previews, search reports, or tool-log artifacts into transcript-only features.
- Do not add MCP, swarm/team orchestration, remote execution, browser automation, or other product lines that do not belong to the current `gocode` scope.
- Prefer extending the existing local engine and TUI architecture over introducing parallel subsystems.
- Keep subagents local-first and artifact-safe. In the first version, child agents return reports to the parent rather than mutating parent session artifacts directly.
- This roadmap is for planning and sequencing only. It does not authorize implementation shortcuts around permissions, budgeting, or artifact review.

## Current Baseline

- `gocode` already has strong artifact groundwork: reviewable implementation plans, task-list and walkthrough artifacts, routed diff and search artifacts, and artifact-aware TUI panels.
- Tool execution is no longer naive: the engine already supports per-call concurrency classification (`ConcurrencySerial`/`ConcurrencyParallel`), batch execution with configurable semaphore (default 10 concurrent), and a streaming executor that preserves ordered result delivery.
- Compaction exists and works: tool-result truncation (Stage A), full summarization (Stage B), and partial recent-window compaction (Stage C) are already implemented.
- `ContinuationTracker` in `token_budget.go` already implements budget-aware continuation stop conditions (90% budget threshold, diminishing-returns detection after 3+ continuations).
- 16 registered tools plus aliases, with 2+ additional tools implemented but not yet wired into the registry (`file_diff_preview.go`, `file_history.go`).
- `internal/skills/` exists but is not addressed in the current roadmap.
- The largest gaps are now architectural rather than cosmetic: there is no subagent runtime, no persistent memory and recall system, no cache-aware prompt budgeting, limited tool breadth compared with the reference architecture, and no serious latency instrumentation.

## Phase 1: Measure and Protect the Runtime

**Purpose:** establish hard data and guardrails before adding new agent depth.

### Scope

- Add a structured timing system with named checkpoints for the critical user-visible latencies:
  - **boot-to-ready**: process start → prompt ready (target: measure current, then improve)
  - **prompt-to-first-token**: user submit → first streamed model token
  - **prompt-to-first-tool-result**: user submit → first tool output rendered
  - **prompt-to-artifact-focus**: user submit → artifact panel highlighted
  - **compaction-duration**: manual or auto compaction wall-clock time
- Implementation approach: a lightweight `Checkpoint` recorder in Go (not OpenTelemetry — too heavy for a CLI tool). Each checkpoint is a named `(label, time.Time)` pair. Aggregate per-turn and per-session timings are logged to structured JSON for post-hoc analysis.
- Define an artifact ownership contract for tool outputs, future subagents, and compaction or memory outputs.
- Add per-turn aggregate tool-result budgeting so wider concurrency and future subagents cannot flood the transcript or bypass artifact spill logic. (`budgeting.go` already exists — extend it rather than creating a parallel system.)
- Validate that the existing `ContinuationTracker` budget thresholds are sensible under real workloads; adjust if data shows otherwise.

### Exit Criteria

- Every later phase can be evaluated against real latency numbers instead of guesses.
- Artifact spill and focus behavior remain stable under heavier concurrent tool load.
- The repo has a single source of truth for runtime guardrails before subagents or memory are introduced.

## Phase 2: Deepen the Local Tool System

**Purpose:** close the biggest execution gap first without importing unrelated feature sets.

### Scope

- **Immediate wins: register existing tools.** `file_diff_preview.go` and `file_history.go` are already implemented but not in the registry. Evaluate and wire them up first.
- Extend the `Tool` contract with semantic validation so tools can reject invalid or low-value calls before permission resolution and execution (modeled after ch06's 14-step pipeline: validate before permission, not after).
- Make concurrency classification richer for `bash` by inspecting the command string (ch07 pattern: parse subcommands, return `ConcurrencyParallel` only if every subcommand is in a read-only set like `ls`, `cat`, `grep`, `find`).
- Add specific new tools in focused categories:
  - **`Think`**: zero-cost scratchpad/reasoning tool — the model writes to it but it has no side effects. High-value, trivial to implement.
  - **Code navigation**: symbol search and/or go-to-definition beyond what `grep` provides (evaluate feasibility with Go's `go/packages` or LSP).
  - **Structured repository inspection**: e.g., project-structure summary, dependency-graph tool.
  - **Terminal follow-up**: complement existing `command_status` and `send_command_input` with richer process lifecycle management if Phase 1 timing data shows it matters.
- Keep ordered result delivery, permission gates, tool-result budgeting, and artifact spill paths as non-negotiable invariants.

### Notes

- Do not chase a raw `40+ tools` number just for parity optics.
- The first target is a coherent local toolset that materially improves coding workflows without dead weight.
- Evaluate whether `internal/skills/` needs integration work alongside the tool expansion.
- If the local tool surface grows large enough, deferred schema loading becomes worth planning; it is not the first step.

### Exit Criteria

- The tool surface is materially broader than the current local-only set.
- Tool validation and concurrency decisions are input-aware where it matters.
- Larger tool batches do not regress artifact routing or transcript clarity.

## Phase 3: Introduce Artifact-Safe Subagents

**Purpose:** add delegation without importing swarm, remote, or team complexity.

### Scope

- Add a single `Agent` tool for bounded delegation with a parent-child lifecycle modeled after ch08's `runAgent()` 15-step lifecycle.
- Start with two agent types only:
  - `general-purpose` (full tool access minus artifact-mutation tools)
  - `explore` (read-only tool pool: `file_read`, `glob`, `grep`, `list_dir`, `git`, `bash` read-only subset)
- Support both blocking (sync) and background (async) execution.

**Architectural decisions required:**

- **Context model**: fork (clone parent history + filter incomplete tool_calls) vs. fresh (empty context + task description). Start with fresh for v1 — simpler, avoids token waste on irrelevant parent history. Fork mode is a later optimization for continuation-style delegation.
- **Permission isolation**: child agents get a scoped permission wrapper (modeled after ch08's layered `getAppState()` pattern). Background children use auto-deny for interactive prompts to prevent deadlock. Sync children can bubble permission prompts to the parent.
- **Transcript storage**: child transcripts are stored as sidechain NDJSON files under the session directory (not injected into parent transcript). Parent receives only the final report as a tool result.
- **Result delivery**: the `Agent` tool returns a structured result containing: child status (completed/failed/cancelled), summary text, and optionally a list of files read/modified. In v1, child agents do not directly update the parent session's task-list or implementation-plan artifacts.
- **Cancellation**: sync children share the parent's abort controller. Async children get an independent abort controller with a timeout (configurable, default 5 minutes).

### Notes

- No team agents.
- No swarm messaging.
- No remote or worktree execution in the initial scope.
- The first implementation should prove delegation quality and transcript clarity before adding more agent types.

### Cost Note

- Each child agent consumes its own API call budget. The parent's cost tracker must aggregate child costs. Display child cost as a sub-item in the cost summary.

### Exit Criteria

- The model can delegate research, search, and setup work without polluting the parent turn.
- Background children never deadlock on permission prompts.
- Artifact ownership remains explicit and stable.

## Phase 4: Build a Real Project Memory System

**Purpose:** move from instruction-file loading to durable, selective project memory.

### Scope

- Add project-scoped memory storage outside the artifact store, following the four-type taxonomy from ch11:
  - `user` (role, expertise, preferences)
  - `feedback` (corrections + confirmations with reason and trigger)
  - `project` (ongoing context, absolute dates)
  - `reference` (bookmarks, external URLs)
- Derivability test for what to store: "Can this be re-derived from the current project state?" If yes, don't store it.
- Always load a lightweight `MEMORY.md` index (200-line / 25KB limit) and keep full memory files on demand.
- Add an async memory recall side-query so only the most relevant memories enter the next turn.
- Add staleness warnings for older memories so the model treats them as observations to verify, not immutable facts (today/yesterday: no warning; older: age caveat injected).
- Reuse existing file tools for the write path — create `.md` file with YAML frontmatter, update index entry in `MEMORY.md`. No custom memory tool surface.

**Disk layout:**

- Per-project: `~/.gocode/projects/{project-slug}/memory/MEMORY.md` index + individual `.md` files.
- User-level: `~/.gocode/memory/` for cross-project user-type memories.

**Memory recall routing:**

- The side-query needs a fast, cheap model to select relevant memories. Evaluate routing options: (a) use the configured model with a short max-tokens cap, (b) add a "lightweight model" config slot for side-queries, or (c) use heuristic keyword matching as a v1 fallback if no cheap model is available (e.g., Ollama users without a fast secondary model).

### Notes

- `AGENTS.md` and `AGENTS.local.md` remain instruction files, not project memory.
- Memory must stay separate from artifacts so long-term observations do not pollute session deliverables.
- Defer the background extraction safety net (fork agent scans for missed memories after each query) to a later iteration — it's expensive and can be added incrementally.

### Cost Note

- Memory side-queries add per-turn API cost. Budget for 1 lightweight call per turn. Track and surface this cost separately.

### Exit Criteria

- `gocode` can remember durable project guidance across sessions.
- Memory recall stays selective and bounded.
- Old memories surface with age-aware caveats instead of false authority.

## Phase 5: Upgrade Compaction and Prompt Budgeting

**Purpose:** reclaim context and cost headroom without weakening current compaction behavior.

### Scope

**Universal improvements (all providers):**

- Keep the existing three-stage compaction pipeline (tool truncate → summarize → partial) and add tighter output slot reservation with escalation only on truncation (ch17 pattern: default 8K `max_output_tokens`, escalate to 64K on truncation — saves 12-28% usable context).
- Add section-level memoization for system prompt assembly — avoid rebuilding identical prompt sections each turn.
- Build on the existing `ContinuationTracker` in `token_budget.go` to coordinate continuation budgeting with compaction decisions and memory recall.
- Make compaction, tool-result budgeting, continuation tracking, and future memory recall cooperate via a shared context-pressure policy rather than each managing pressure in isolation.
- Use the existing `compact-summary` artifact kind only if it improves reviewability; do not duplicate transcript content by default.

**Provider-specific improvements (behind provider abstraction):**

- Reorder prompt construction for cache stability: stable sections first (identity, tools, code style), volatile sections later (date, memory, history). This benefits Anthropic API and compatible OpenRouter routes. No-op for non-caching providers.
- Implement sticky latches for prompt fields that change mid-conversation (e.g., date string, memory index) — only when the provider supports prompt caching.
- These optimizations must be gated behind a `SupportsCaching() bool` provider method so they activate correctly.

### Exit Criteria

- Sessions keep more usable context before compaction fires.
- Repeated turns avoid unnecessary prompt rebuild churn.
- Compaction remains explainable, bounded, and compatible with subagents and memory recall.
- Provider-specific optimizations activate only for capable providers.

## Phase 6: Tighten the TUI and Milliseconds-Level Developer Experience

**Purpose:** turn the runtime and interface improvements into felt speed.

### Scope

- Use Phase 1 timing data to prioritize the highest-impact bottlenecks. Likely candidates:
  - **startup fast paths**: API preconnect via HEAD request during init (~100-200ms saving per ch17), parallel keychain/config reads
  - **transcript rendering**: profile Ink render cycles on long sessions before assuming Ink is the bottleneck. ch13 describes a custom renderer with packed `Int32Array` cells and double buffering — these patterns inform *what to measure* (frame times, cell count, damage rectangles) but are *not a build target* since gocode uses Ink
  - **prompt editor interaction**: keystroke-to-render latency, paste handling, multiline editing responsiveness
  - **search and index performance**: for large repositories, evaluate whether async indexing with partial queryability (ch17's yield-to-event-loop pattern) would help
  - **new UI surfaces**: subagent activity indicator, memory recall visibility — only after engine contracts are stable
- Keep artifact surfaces primary when presenting structured work.
- Do not import the custom renderer architecture from ch13. Ink is sufficient unless measurement proves otherwise.

### Candidate UI Improvements (prioritized by likely impact)

1. API preconnect warmup (cheap, measurable, universal)
2. Faster transcript paging and search on long sessions
3. More actionable latency and status feedback in the footer and status line
4. Subagent status surfaces (depends on Phase 3)
5. Memory recall visibility without transcript noise (depends on Phase 4)
6. Richer prompt editing and keybinding ergonomics (measure first)

### Exit Criteria

- The slowest visible interactions are instrumented and intentionally improved.
- UI work stays anchored to measured bottlenecks instead of imitation.
- Artifact presentation remains stronger, not weaker, after the new surfaces land.

## Cross-Cutting Concerns

- **Cost tracking**: Phases 3, 4, and 5 all increase API costs (subagent calls, memory side-queries, larger context windows). The existing cost tracker must be extended to aggregate child-agent costs and memory-recall costs. Surface these in the cost summary.
- **Provider abstraction**: Several ch17 optimizations (prompt caching, output slot reservation) are Anthropic-specific. All provider-dependent features must be gated behind provider capability checks.
- **Skills**: `internal/skills/` exists. Evaluate during Phase 2 whether skills need better integration with the expanded tool system and future subagents.

## Recommended Execution Order

1. Phase 1: runtime measurement and guardrails
2. Phase 2: tool depth and concurrency
3. Phase 3: subagents
4. Phase 4: memory
5. Phase 5: compaction and prompt budgeting
6. Phase 6: TUI and milliseconds-level developer experience

This order is deliberate. Better measurement and stronger tool execution reduce risk for every later phase. Subagents become substantially more valuable once the tool system is deeper. Memory becomes more useful once parent and child agents can both consume it. UI work should reflect measured bottlenecks from the earlier phases, not guesswork.

## Success Signals

- Tool batches finish faster without reducing determinism.
- The first subagent release improves search and research turns without confusing artifact ownership.
- Memory recall changes behavior across sessions in ways the user can notice and trust.
- Compaction fires later and with less disruption.
- Boot-to-ready and first-response latency are tracked and improved as first-class engineering metrics.
