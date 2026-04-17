# Prompt Caching Plan

Scope: based on `reference/prompt-caching.txt` and the Claude Code reference under `reference/claudecode/`.

This plan intentionally excludes test work. The goal here is to close the prompt-caching architecture gaps in Chan, not to add or run tests.

## What Claude Code is doing that matters

- Keeps a large, stable prefix hot: system instructions, tool definitions, and durable project context stay fixed while the conversation grows at the bottom.
- Treats prompt caching as provider-side KV caching, not local string memoization.
- Preserves cache-critical request parameters across forked work such as side questions, session-memory extraction, and subagents.
- Uses summarized handoffs instead of forwarding large raw outputs between stages when a short brief is enough.
- Avoids mutating the cached prefix with ephemeral state.
- Tracks cache creation and cache read tokens as a first-class operational metric.

## What Chan is missing today

### 1. Chan optimizes prompt assembly locally, but not Anthropic prompt caching on the wire

Chan has a local `PromptAssemblyCache`, but the Anthropic request path still sends a plain string system prompt and plain message blocks. There is no cache-control segmentation in the outbound request, so Chan is not explicitly shaping the request for Anthropic prompt caching.

### 2. Chan still mutates the system prompt every turn

The current system prompt composition includes turn-variant sections such as:

- a literal per-turn RFC3339 current time in `chan/internal/agent/context_inject.go`
- memory prompt derived from the current user prompt and recalls
- session memory snapshot
- working directory and git context
- live retrieval snippets
- attempt log history

That means the most cache-friendly part of the request is much smaller than it should be. Claude Code keeps this kind of volatile information out of the static prefix whenever possible.

Claude Code's reference implementation is stricter here than Chan today:

- normal date context is memoized once for the conversation in `reference/claudecode/context.ts`, not regenerated for every request
- explicit system-prompt cache breaking is isolated behind `systemPromptInjection`, also in `reference/claudecode/context.ts`, and treated as an exceptional path rather than normal prompt assembly

Chan should adopt the same discipline. A fresh `Current time:` line in the prompt every turn is a direct cache miss generator.

### 3. Child-agent flows are not cache-safe forks of the parent turn

Chan subagents start with a fresh system prompt and fresh message list. That is good for isolation, but it throws away the parent request prefix that Claude Code deliberately reuses for cache hits. Session-memory and side-task flows need a shared cache-safe request shape, not just isolated execution.

### 4. Compaction is continuity-oriented, but not cache-oriented

Chan compaction rewrites conversation state into a summary message for continuation. That preserves continuity, but it does not follow the cache-safe forking pattern described in the reference material, where compaction work is appended as a new instruction on top of the existing cached prefix.

### 5. Cache-invalidating inputs are not treated as a disciplined contract

Claude Code is strict about not changing tools, model, or other cache-key inputs mid-session unless it accepts the miss. Chan does not currently expose a dedicated cache-safe request contract for:

- tool definition ordering and serialization stability
- model-switch handling when prompt caching matters
- stable identifiers for any temp-path-like values that can leak into prompts
- separating durable prompt state from ephemeral debugging or runtime state

### 6. Cache metrics are collected, but not elevated into an optimization loop

Chan already records cache read and cache creation tokens in usage and cost tracking, but there is no explicit cache-efficiency surface that tells us whether prompt-shaping changes are working or regressing.

## Fix Plan

## Phase 1: Split stable prefix from volatile suffix

Goal: stop rebuilding high-churn state into the system prompt.

- Define a strict prompt contract with two layers:
  - stable prefix: base instructions, capability rules, tool definitions, durable repo/user memory that does not change during the session
  - volatile suffix: current user ask, live retrieval, attempt log deltas, transient working context, and turn-specific reminders
- Refactor system prompt assembly so only stable content remains in the system field for Anthropic-cached sessions.
- Remove the per-turn RFC3339 `Current time:` prompt line from the cacheable prefix. If date awareness is still useful, use a session-stable day snapshot or append a time reminder only on time-sensitive requests.
- Move turn-scoped context that currently lives in `composeSystemPrompt` into regular conversation messages or tool/result summaries appended after the cached prefix.
- Keep session memory only if it is truly stable for the session slice; otherwise inject it as a later message instead of mutating the prefix.

## Phase 2: Implement real Anthropic cache-aware request shaping

Goal: make Chan intentionally compatible with provider-side prompt caching instead of only local memoization.

- Extend the Anthropic request builder to support structured system/message blocks where cache boundaries can be controlled explicitly.
- Add a cache-aware prompt segment model in Chan so the outbound request can mark cacheable sections deliberately.
- Preserve deterministic serialization for tools and prompt blocks so equivalent requests stay byte-stable.
- Keep the current local `PromptAssemblyCache` only as a CPU optimization; do not treat it as the main caching mechanism.

## Phase 3: Add cache-safe fork parameters for subagents and side tasks

Goal: let delegated work reuse the parent prefix when the workflow allows it.

- Introduce a `CacheSafeParams`-style structure for Chan that captures the cache-critical request inputs for a parent turn.
- Thread those params into cache-sensitive child flows such as:
  - subagent launches that only need an appended instruction
  - session-memory extraction/refinement
  - planning or summarization helpers
  - compact/overflow handling helpers
- Preserve isolation for mutable runtime state, but keep cache-critical prompt inputs identical unless the child explicitly needs a different model or tool set.
- When a child flow does require a different model or tool list, mark it as a deliberate cache miss instead of silently drifting.

## Phase 4: Make subagent handoffs smaller and more cache-friendly

Goal: reduce dynamic suffix growth caused by oversized delegation payloads.

- Add a summarized-brief handoff path for workflows that currently pass more raw detail than necessary.
- Prefer short parent-to-child briefs plus targeted file/tool access over embedding large raw transcripts in prompts.
- Add explicit prompt-building rules for planner/explorer style flows so the child receives the minimum context needed to act.
- Keep raw artifacts available outside the prompt path when the full detail is still useful for inspection.

## Phase 5: Rework compaction around cache-safe continuation

Goal: preserve continuity without unnecessarily discarding reusable prefix structure.

- Separate compaction-for-continuity from compaction-for-cache so the system can make deliberate tradeoffs instead of one rewrite path.
- Add a cache-safe compaction flow that performs summarization as an appended instruction over the existing conversation state, then resumes with a preserved boundary where possible.
- Keep the current summary-based continuation as a fallback when the cache-safe path is not possible or would exceed limits.
- Record which compaction mode ran so later tuning can compare continuity gains against cache-hit losses.

## Phase 6: Enforce cache-stability rules in session behavior

Goal: stop accidental cache busting.

- Freeze tool definition order for the lifetime of a session once the first model turn is sent.
- Detect and log model switches that invalidate a hot cache.
- Audit any prompt content that can include timestamps, random IDs, temp paths, or live debug state; either stabilize it or move it out of the prefix.
- Specifically eliminate always-fresh timestamps from normal prompt assembly, including the current per-turn environment time in `chan/internal/agent/context_inject.go`.
- Audit prompt-visible metadata such as durable-memory `updated_at` fields and other bookkeeping values; omit them from recalled prompt sections unless they are semantically required for the task.
- Keep ephemeral runtime hints out of the system prompt unless intentionally used to break cache.
- If Chan needs a cache-break mechanism for debugging or emergency invalidation, model it as an explicit opt-in path similar to Claude Code's `systemPromptInjection`, not as routine prompt mutation.

## Phase 7: Add cache-efficiency observability

Goal: make prompt caching measurable during normal use.

- Surface cache creation tokens, cache read tokens, and computed cache efficiency in session status/cost reporting.
- Add timing or cost-summary output that makes cache-hit regressions obvious after architecture changes.
- Split reporting by main agent vs child-agent usage so it is clear which workflows are keeping the cache hot and which are not.
- Use these metrics to tune prompt placement, compaction mode choice, and child-flow design.

## Recommended execution order

1. Phase 1: stable vs volatile split
2. Phase 2: Anthropic cache-aware request shaping
3. Phase 3: cache-safe fork parameters
4. Phase 6: cache-stability rules
5. Phase 4: smaller subagent handoffs
6. Phase 5: cache-safe compaction improvements
7. Phase 7: observability cleanup and tuning

## Claude Code reference mapping

- Stable vs volatile prompt boundaries:
  - `reference/prompt-caching.txt`
  - `reference/claudecode/context.ts`
- Anthropic cache discipline and cache-breaking exceptions:
  - `reference/prompt-caching.txt`
  - `reference/claudecode/context.ts`
- Date snapshot vs per-request timestamp handling:
  - `reference/claudecode/context.ts`
- Cache-safe forked work for child flows:
  - `reference/claudecode/utils/forkedAgent.ts`
  - `reference/claudecode/cli/print.ts`
- Session-memory extraction using cache-reusing helper flows:
  - `reference/claudecode/services/SessionMemory/sessionMemory.ts`
- Stable identifiers to avoid accidental cache busting:
  - `reference/claudecode/utils/tempfile.ts`
- Compaction and preserved-boundary continuation concepts:
  - `reference/prompt-caching.txt`
  - `reference/claudecode/README.md`

Use these references as behavioral anchors during implementation. The target is not literal code parity with Claude Code; the target is the same caching discipline adapted to Chan's Go engine and session model.

## Expected outcome

If this plan is implemented well, Chan should move from partial accidental prefix reuse to deliberate prompt caching discipline:

- larger reusable static prefix
- fewer cache misses from turn-to-turn prompt mutation
- better cache reuse across subagents and helper flows
- lower Anthropic input cost on long sessions
- clearer visibility into whether cache behavior is improving or regressing
