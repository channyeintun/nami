# Implementation Plan

## Objective

Evaluate whether selected GoF patterns from `golang-design-patterns` should be applied in `gocode` only where they improve robustness, maintainability, readability, or simplicity without changing existing behavior.

## Planning Decision

Design patterns are optional here, not target architecture rules. A pattern should only be introduced when it removes branching or duplication that is already making the code harder to extend or reason about. If a switch, small helper, or plain struct is still the clearest solution, keep it.

## Recommended Opportunities

| Priority | Area                                  | Pattern                 | Why it is a reasonable fit                                                                                                                                                                                                                                 | Main files                                                                                                                                                                                                      |
| -------- | ------------------------------------- | ----------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- | --------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1        | Slash command handling                | Command                 | `handleSlashCommand` is a large branch with command-specific state transitions, persistence, and UI emission. Extracting per-command handlers behind a small registry should improve readability without changing behavior.                                | `cmd/gocode/slash_commands.go`                                                                                                                                                                                  |
| 2        | LLM client construction               | Factory Method          | `newLLMClient` mixes provider selection, GitHub Copilot special cases, capability override logic, and concrete client construction. A provider factory layer should centralize that branching and reduce constructor duplication.                          | `cmd/gocode/engine.go`, `internal/api/provider_config.go`, `internal/api/anthropic.go`, `internal/api/openai_compat.go`, `internal/api/openai_responses.go`, `internal/api/gemini.go`, `internal/api/ollama.go` |
| 3        | Permission decision flow              | Chain of Responsibility | Permission evaluation already runs as an ordered series of rules. The current `Check()` is short and clear (~40 lines), so this is optional. Only proceed if new rule types make the sequential if/for blocks materially harder to follow.                 | `internal/permissions/gating.go`                                                                                                                                                                                |
| 4        | Compaction selection                  | Strategy                | The compaction pipeline already has named strategies, but selection and execution are still mostly embedded in one method. If compaction variants continue growing, promoting them to explicit strategy objects should help. This is optional, not urgent. | `internal/compact/pipeline.go`                                                                                                                                                                                  |
| 5        | Tool execution cross-cutting behavior | Decorator               | If more execution concerns accumulate around tools, such as telemetry, truncation, artifact side effects, and permission-adjacent wrapping, decorators can keep core tool logic smaller. Only apply if wrappers reduce existing duplication.               | `internal/tools/interface.go`, `internal/tools/orchestration.go`, `internal/tools/streaming_executor.go`                                                                                                        |

## Areas To Leave Alone

| Area                     | Why a pattern is not justified now                                                                                                        | Main files                                                     |
| ------------------------ | ----------------------------------------------------------------------------------------------------------------------------------------- | -------------------------------------------------------------- |
| Execution mode profiles  | Two modes and a small switch are clearer than a State or Strategy hierarchy.                                                              | `internal/agent/modes.go`                                      |
| Retry classification     | `ShouldRetry` is short and stable. Strategy objects would add ceremony without reducing much complexity.                                  | `internal/api/retry.go`                                        |
| Hook discovery           | `Runner.Run` is simple file globbing plus execution. A richer lifecycle abstraction is premature.                                         | `internal/hooks/runner.go`                                     |
| Artifact version structs | The artifact layer already has explicit versions. A Memento-style rewrite would add naming but little practical simplification right now. | `internal/artifacts/manager.go`, `internal/artifacts/store.go` |
| Tool registry basics     | `Registry` is already a clean registry implementation. Avoid wrapping it in more pattern layers unless a real extension need emerges.     | `internal/tools/registry.go`                                   |

## Proposed Implementation Order

### Phase 1: Slash Command Commandization

Scope:

- Introduce a small command handler interface and registry.
- Move each slash command branch (~13 commands: connect, plan, fast, model, reasoning, cost/usage, compact, resume, clear, help, status, sessions, diff) into focused handler functions or types.
- Replace the 8-value return tuple `(bool, string, time.Time, ExecutionMode, string, string, []Message, error)` with a structured state object that handlers receive and return.
- Keep shared helpers for persistence and event emission where that reduces duplication.

Guardrails:

- No command behavior changes.
- Keep existing command names, outputs, persistence timing, and error text semantics unless there is a clearly broken case.
- Prefer function adapters over deep object graphs.

### Phase 2: Provider Client Factory Consolidation

Scope:

- Move provider selection logic out of `newLLMClient` into a factory layer.
- Build on the existing `Presets` map and `ClientType` enum in `internal/api/provider_config.go` — extend, don't replace.
- Normalize provider preset lookup, default base URL usage, API key sourcing, and capability wrapping.
- Keep GitHub Copilot special handling explicit if needed rather than hiding it in a generic abstraction.

Guardrails:

- Preserve exact provider support and model resolution.
- Preserve current GitHub Copilot auth refresh behavior.
- Prefer a table-driven factory or constructor map over a heavy abstract-factory hierarchy.

### Phase 3: Optional Permission Check Chain

Scope:

- Only proceed if new rule types or dynamic rule insertion make the current sequential if/for blocks in `Check()` materially harder to follow.
- If done, represent deny, session allow-all, always allow, always ask, and default fallback as ordered evaluators.
- Keep ordering explicit and stable.

Guardrails:

- Behavior must remain byte-for-byte compatible for current rules where practical.
- Do not split logic across too many tiny files.
- Skip entirely if the current ~40-line method remains the clearest option.

### Phase 4: Optional Compaction Strategy Extraction

Scope:

- Only proceed if upcoming work adds another compaction path or makes the current method materially harder to follow.
- If done, keep the strategy surface very small.

Guardrails:

- Do not refactor purely for pattern purity.

### Phase 5: Optional Tool Execution Decorators

Scope:

- Only proceed if new execution wrappers would replace duplicated orchestration logic.
- Keep tool implementations directly readable.

Guardrails:

- Do not hide basic execution flow behind too many wrappers.

## Implementation Principles

- Preserve original behavior first.
- Prefer local extraction over framework-style abstraction.
- Introduce only one pattern-driven refactor at a time.
- Keep public APIs and user-facing command semantics stable.
- Do not add tests for this effort.

## Exit Criteria

- Each adopted pattern reduces branching, duplication, or coupling in a measurable way.
- No user-facing functionality or behavior regresses.
- Any area that stays simpler without a pattern remains unchanged.

## Current Status

Planning complete. No implementation has been started.
