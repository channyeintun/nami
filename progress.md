# Progress

## Working Rules

- Follow `plan.md` as the active execution baseline.
- Use `sourcecode-explanation/` as the primary reference and targeted `sourcecode/` reads to confirm architecture decisions.
- Do not add tests.
- Do not plan MCP, swarm or team orchestration, remote execution, or unrelated product lines.
- Preserve first-class artifacts across every future phase.
- After each completed task: update this file, run formatting, and create a git commit.

## Current Status

| Workstream                          | Status      | Scope | Notes                                                                                                                                                                                                                                                                |
| ----------------------------------- | ----------- | ----- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| Planning refresh                    | completed   | S     | 2026-04-12 explanation-driven roadmap replaced stale parity-era planning docs.                                                                                                                                                                                       |
| Phase 1 runtime measurement         | completed   | S     | Checkpoint logging, artifact ownership, aggregate tool-result budgeting, and continuation stop telemetry are in place.                                                                                                                                               |
| Phase 2 tool depth                  | in progress | L     | File-history tools, semantic validation, input-aware bash concurrency, Think, stronger Go-aware navigation, repository overview tooling, and fuller background command lifecycle tools landed; follow-up tooling remains.                                            |
| Phase 3 subagents                   | in progress | XL    | Fresh-context explore, permission-isolated general-purpose child agents, and full background child launch/status/stop lifecycle are landed.                                                                                                                          |
| Phase 4 memory                      | in progress | L     | Project and user MEMORY.md index loading, staleness cues, write-path scaffolding, model-backed side-query recall, structured MEMORY.md entry parsing, explicit MEMORY.md validation, on-demand note loading, and separate memory-recall cost surfacing are wired in. |
| Phase 5 compaction and cache        | planned     | M     | Output slot reservation, prompt memoization, provider-gated cache stability.                                                                                                                                                                                         |
| Phase 6 UI and developer experience | planned     | M     | Data-driven: API preconnect, measured Ink optimizations, subagent/memory UI surfaces.                                                                                                                                                                                |

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
- Completed: added an aggregate per-turn inline tool-result budget so parallel tool batches cannot flood a single turn even when each individual tool result stays under its own cap.
- Completed: extended turn timing metadata with tool-result count, inline-char usage, spill count, and aggregate-budget spill count so future threshold tuning has real session data to work from.
- Completed: initialized the continuation tracker with the active max-token budget so the 90% budget stop condition now actually applies during multi-iteration turns.
- Completed: added continuation stop telemetry to turn timing metadata, including continuation count, budget used, budget ceiling, and the concrete stop reason (`budget_exhausted` or `diminishing_returns`).
- Note: there were still no local `timings.ndjson` samples to analyze, but Phase 1 now has the required runtime instrumentation and guardrails to validate thresholds from subsequent real sessions.
- Completed: added `file_history` and `file_history_rewind` tool wrappers on top of the existing session file-history runtime so snapshots, diff stats, and rewind are now available through the agent tool surface.
- Completed: registered the new file-history tools in the runtime registry and updated the model prompt plus README tool list so the exposed tool names stay in sync.
- Completed: added a schema-backed semantic validation layer that rejects malformed tool calls before tool-start events, plan-mode checks, permission prompts, and execution.
- Completed: added tool-specific semantic validators for `bash` and `git` on top of the shared validator hook so obviously low-value or incomplete calls fail early with clear errors.
- Completed: replaced the old regex-based `bash` concurrency check with top-level command-chain parsing so `&&`, `||`, `;`, pipelines, and env-prefixed read-only commands are classified more accurately.
- Completed: kept concurrency classification conservative by forcing serial execution for shell-heavy constructs such as redirection, subshells, command substitution, backgrounding, unmatched quotes, or other ambiguous syntax.
- Completed: added a zero-side-effect `think` tool so the model can use an explicit scratchpad without shelling out or touching the filesystem.
- Completed: registered `think` in the runtime tool list and updated the system prompt plus README so the exposed tool names stay synchronized.
- Completed: added a read-only `symbol_search` tool that finds likely symbol definitions across common source files and returns file paths, line numbers, symbol kinds, and matched lines.
- Completed: registered `symbol_search` in the runtime prompt and README so the model has a code-navigation primitive beyond plain `grep` without introducing a heavyweight dependency.
- Completed: added a read-only `project_overview` tool that summarizes repository structure, manifest files, dominant languages, notable entry files, and top-level sections in one call.
- Completed: registered `project_overview` in the runtime prompt and README so the model can gather a repository snapshot without stitching together repeated `list_dir`, `glob`, and `grep` calls.
- Completed: added an execute-gated `stop_command` tool so the agent can explicitly terminate a background shell command and retrieve its final unread output and exit status.
- Completed: extended the background command manager with a dedicated stop path and registered `stop_command` in the runtime prompt and README to close the background-command lifecycle loop.
- Completed: added a read-only `list_commands` tool so the agent can enumerate active or recently retained background commands without guessing command ids.
- Completed: extended the background command manager with stable command summaries and registered `list_commands` in the runtime prompt and README to complete inspection of the background command lifecycle.
- Completed: added a read-only `go_definition` tool that parses Go source files directly and returns precise file, line, column, package, kind, and signature information for matching declarations.
- Completed: registered `go_definition` in the runtime prompt and README so the agent now has a parser-backed Go navigation primitive beyond regex search.
- Completed: added a read-only `go_references` tool that walks parsed Go ASTs and returns identifier reference locations, usage kinds, and source-line context, with optional inclusion of declaration sites.
- Completed: registered `go_references` in the runtime prompt and README so the agent now has a companion Go-aware reference finder alongside `go_definition`.
- Completed: started Phase 3 with a runtime-backed `agent` tool that launches a synchronous `explore` child agent in a fresh child session and returns its final report plus sidecar transcript path.
- Completed: kept the first subagent slice artifact-safe by restricting the child tool pool to read-only exploration tools, suppressing child UI/tool event leakage, and persisting child transcripts in their own session directories.
- Completed: widened the `agent` tool to support a synchronous `general-purpose` child agent with a broader tool pool while keeping artifact mutation tools excluded from child sessions.
- Completed: enforced cloned, non-interactive permission policy inside child sessions so write and execute calls only run when the inherited policy already auto-approves them; otherwise they are denied inside the child transcript instead of prompting.
- Completed: added async child-agent launch via `agent.run_in_background`, returning a durable `agent_id`, child transcript path, and result-file path without blocking the parent turn.
- Completed: added a read-only `agent_status` tool plus a background child-agent manager so async child runs can be polled for `running`, `completed`, or `failed` status and return their final report on completion.
- Completed: added an execute-gated `agent_stop` tool and background cancellation path so async child agents can be moved from `running` to `cancelling` and settle into `cancelled` status without killing the parent session.
- Completed: taught the Ink TUI to parse `agent`, `agent_status`, and `agent_stop` tool results into a dedicated background-agent state list instead of leaving child lifecycle updates buried in raw JSON tool output.
- Completed: added a compact Background Agents panel plus human-readable agent tool summaries so active and recent child runs are visible directly in the terminal UI while the parent session continues.
- Completed: added a dedicated `background_agent_updated` IPC event so the engine now pushes background child launch, completion, failure, cancellation, and stop-request state changes directly to the TUI.
- Completed: wired the TUI reducer to merge live background-agent events with existing tool-result-derived state, so the Background Agents panel updates even when no follow-up `agent_status` poll is run.
- Completed: added transcript-level background-agent notices for launch, stop, completion, cancellation, and failure so important child-agent state transitions are visible in the main conversation flow instead of only in the side panel.
- Completed: upgraded the Background Agents panel to separate active and recent child runs, show compact status counts, and surface transcript/result/update hints so the panel is useful without digging through raw session files.
- Completed: capped retained background-agent entries in TUI state so long sessions do not accumulate an unbounded child-agent list after repeated background runs.
- Completed: extended the prompt memory loader to discover user-global and project-scoped `MEMORY.md` indexes from the config tree alongside existing `AGENTS.md` instruction files.
- Completed: applied tighter `MEMORY.md` index caps (200 lines / 25KB) and separated durable memory indexes from hard instructions in the formatted system-prompt section.
- Completed: added age-aware staleness warnings for older loaded memory indexes so memories older than yesterday are presented as context to verify rather than unconditional facts.
- Completed: added prompt-level memory write guidance with concrete project and user memory paths plus index-update instructions so durable memories can be written through existing file tools without a custom memory tool.
- Completed: added canonical memory filename, frontmatter, and MEMORY.md index-entry scaffolding to the prompt guidance so future memory writes can follow a consistent on-disk format.
- Completed: switched MEMORY.md index injection from whole-file dumping to bounded heuristic recall so only a small set of lines relevant to the current request enters the prompt by default.
- Completed: added a bounded model-backed memory side-query that selects relevant MEMORY.md index entries before each turn and falls back to the existing heuristic recall path when the side-query fails or returns nothing.
- Completed: parsed canonical MEMORY.md entries into structured filename/title/type records, switched recall candidates to use that structured metadata, and loaded referenced memory note excerpts on demand for the entries that were actually recalled.
- Completed: split memory side-query usage into its own tracker counters, extended the `cost_update` payload with memory-recall totals, and surfaced the breakdown in the TUI status bar while keeping aggregate session cost unchanged.
- Completed: tightened MEMORY.md validation so malformed canonical entries and unsafe or missing note references are flagged explicitly, skipped from structured recall candidates, and surfaced as warnings in recalled memory context instead of silently degrading.

## Next Planning Baseline

See `plan.md` for full phase ordering rationale and cross-cutting concerns (cost tracking, provider abstraction, skills evaluation).
