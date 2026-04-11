# Progress

## Working Rules

- Follow `plan.md` as the active execution baseline.
- Use `sourcecode-explanation/` as the primary reference and targeted `sourcecode/` reads to confirm architecture decisions.
- Do not add tests.
- Do not plan MCP, swarm or team orchestration, remote execution, or unrelated product lines.
- Preserve first-class artifacts across every future phase.
- After each completed task: update this file, run formatting, and create a git commit.

## Current Status

| Workstream                          | Status    | Scope | Notes                                                                                                                                                                                                                                                                                                                    |
| ----------------------------------- | --------- | ----- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ |
| Planning refresh                    | completed | S     | 2026-04-12 explanation-driven roadmap replaced stale parity-era planning docs.                                                                                                                                                                                                                                           |
| Phase 1 runtime measurement         | completed | S     | Checkpoint logging, artifact ownership, aggregate tool-result budgeting, and continuation stop telemetry are in place.                                                                                                                                                                                                   |
| Phase 2 tool depth                  | completed | L     | File history, semantic validation, input-aware bash concurrency, Think, Go-aware navigation, repository and dependency overview, and full background-command lifecycle tooling are landed; existing skill selection and prompt injection make the current tool surface sufficient to close the phase.                    |
| Phase 3 subagents                   | completed | XL    | Fresh-context explore, permission-isolated general-purpose child agents, explicit tool allowlists, and full background child launch/status/stop lifecycle are landed; artifact-safe result delivery and non-interactive child permissions are sufficient to close the phase.                                             |
| Phase 4 memory                      | completed | L     | Project and user MEMORY.md loading, staleness cues, durable write guidance, bounded side-query recall with fallback, structured entry parsing, validation, note loading, and separate memory-recall cost surfacing are wired in; the memory workflow is sufficient to close the phase.                                   |
| Phase 5 compaction and cache        | completed | M     | Session-local prompt memoization, adaptive output slot reservation, shared context-pressure policy, provider-gated cache-stable prompt ordering, continuation-aware compaction/output-escalation coordination, and reviewable compact-summary artifacts are landed; the phase is complete for the intended provider set. |
| Phase 6 UI and developer experience | completed | M     | Async preconnect warmup, timing visibility, transcript navigation, artifact/background visibility, and queue-aware prompt chrome are landed; the current measured UI bottlenecks are improved enough to close the phase.                                                                                                 |

## Completion Dashboard

This section is the canonical phase tracker. A phase is only complete when its `Remaining to Finish` list is empty and its exit criteria are all satisfied.

### Phase 2: Tool Depth

**Status:** completed

**Landed**

- Registered `file_diff_preview`, `file_history`, and `file_history_rewind`.
- Added semantic validation and input-aware bash concurrency classification.
- Added `think`, `symbol_search`, `project_overview`, `dependency_overview`, `go_definition`, and `go_references`.
- Completed background command lifecycle tooling with `list_commands`, `command_status`, `send_command_input`, `stop_command`, and `forget_command`.

**Remaining to Finish**

- None.

**Exit Criteria Check**

- [x] Tool surface is materially broader than the original local-only set.
- [x] Tool validation and concurrency decisions are input-aware where it matters.
- [x] Larger tool batches and the expanded tool surface are considered complete enough that no additional Phase 2 follow-up is required.

### Phase 3: Subagents

**Status:** completed

**Landed**

- Added sync `agent` execution with fresh-context `explore` subagents.
- Added broader `general-purpose` child agents with artifact mutation tools excluded.
- Added async launch plus `agent_status` and `agent_stop` lifecycle tools.
- Added background-agent panel, transcript notices, live update events, and child-cost surfacing.

**Remaining to Finish**

- None.

**Exit Criteria Check**

- [x] The model can delegate research, search, and setup work without polluting the parent turn.
- [x] Background children do not deadlock on permission prompts under the current auto-deny policy.
- [x] Artifact ownership and child lifecycle behavior are stable enough that no additional Phase 3 hardening is required.

### Phase 4: Memory

**Status:** completed

**Landed**

- Added project and user `MEMORY.md` loading with tighter caps and staleness cues.
- Added prompt guidance for durable memory writes using existing file tools.
- Added bounded heuristic recall plus model-backed side-query recall.
- Added structured `MEMORY.md` entry parsing, validation, note loading, telemetry, and separate memory-recall cost tracking.

**Remaining to Finish**

- None.

**Exit Criteria Check**

- [x] `gocode` can remember durable project guidance across sessions.
- [x] Memory recall is selective and bounded.
- [x] Old memories surface with age-aware caveats instead of false authority.
- [x] Memory workflow is considered complete enough that no additional Phase 4 follow-up is required.

### Phase 5: Compaction and Cache

**Status:** completed

**Landed**

- Added session-local memoization for prompt sections.
- Added adaptive output budgeting with escalation after truncation.
- Added shared context-pressure policy across compaction, recall, and continuation.
- Added provider-gated cache-stable prompt ordering and continuation-aware pressure coordination.

**Remaining to Finish**

- None.

**Exit Criteria Check**

- [x] Sessions keep more usable context before compaction fires.
- [x] Repeated turns avoid unnecessary prompt rebuild churn.
- [x] Compaction remains bounded and compatible with subagents and memory recall.
- [x] Provider-specific optimizations are complete enough for the intended supported providers.

### Phase 6: UI and Developer Experience

**Status:** in progress

**Landed**

- Added async API preconnect warmup and session timing records.
- Surfaced turn timing in the footer and status line.
- Added transcript search and better long-session navigation.
- Added memory recall visibility, child-agent status/cost visibility, and background-command panels, notices, summaries, and cleanup behavior.
- Added queue-aware prompt chrome so blocked/running turns now surface queued follow-up prompts and the current footer block reason in always-visible status/footer text.

**Remaining to Finish**

- None.

**Exit Criteria Check**

- [x] The slowest visible interactions are instrumented.
- [x] The highest-priority measured UI bottlenecks have been improved enough to call the phase complete.
- [x] Artifact presentation remains stronger, not weaker, after the new UI surfaces.

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
- Completed: promoted the existing diff-preview helper into a read-only `file_diff_preview` tool so the agent can preview compact file diffs against another file or inline proposed content without performing a write.
- Completed: registered `file_diff_preview` in the runtime registry, system prompt, README, and subagent allowlists so the previously unexposed immediate-win tool is now available across the runtime surface.
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
- Completed: enriched `list_commands` with activity timestamps and unread output previews so follow-up command inspection can start from the most relevant retained process instead of probing each command id blindly.
- Completed: enriched the shared background-command result payload so `command_status`, `send_command_input`, and `stop_command` now return command text, cwd, and timing context alongside unread output and exit state.
- Completed: added an execute-gated `forget_command` tool so completed or stopped background commands can be removed from retention explicitly, returning their final metadata plus any unread output instead of waiting for timed cleanup.
- Completed: added a read-only `go_definition` tool that parses Go source files directly and returns precise file, line, column, package, kind, and signature information for matching declarations.
- Completed: registered `go_definition` in the runtime prompt and README so the agent now has a parser-backed Go navigation primitive beyond regex search.
- Completed: added a read-only `go_references` tool that walks parsed Go ASTs and returns identifier reference locations, usage kinds, and source-line context, with optional inclusion of declaration sites.
- Completed: registered `go_references` in the runtime prompt and README so the agent now has a companion Go-aware reference finder alongside `go_definition`.
- Completed: added a read-only `dependency_overview` tool that summarizes dependencies from common manifests such as `go.mod`, `package.json`, `pyproject.toml`, `requirements.txt`, `Cargo.toml`, and `Gemfile` so the agent can inspect project dependencies without stitching together raw file reads.
- Completed: registered `dependency_overview` in the runtime prompt, README, and subagent allowlists so both parent and child agents can use the same structured dependency inspection primitive.
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
- Completed: added child-agent cost and token totals to the shared `agent` result model and `background_agent_updated` payload so delegated runs now preserve their own spend data instead of only mutating the parent aggregate tracker.
- Completed: surfaced background child-agent cost summaries in the TUI panel so completed and failed delegated runs show their token and dollar footprint alongside transcript and result-file hints.
- Completed: added dedicated child-agent subtotal counters to the session cost tracker and `cost_update` payload so delegated work can be surfaced separately from the aggregate session total.
- Completed: surfaced child-agent spend in the main TUI status bar alongside the existing memory-recall breakdown, making background delegation costs visible even when the Background Agents panel is collapsed out of view.
- Completed: extended the prompt memory loader to discover user-global and project-scoped `MEMORY.md` indexes from the config tree alongside existing `AGENTS.md` instruction files.
- Completed: applied tighter `MEMORY.md` index caps (200 lines / 25KB) and separated durable memory indexes from hard instructions in the formatted system-prompt section.
- Completed: added age-aware staleness warnings for older loaded memory indexes so memories older than yesterday are presented as context to verify rather than unconditional facts.
- Completed: added prompt-level memory write guidance with concrete project and user memory paths plus index-update instructions so durable memories can be written through existing file tools without a custom memory tool.
- Completed: added canonical memory filename, frontmatter, and MEMORY.md index-entry scaffolding to the prompt guidance so future memory writes can follow a consistent on-disk format.
- Completed: started Phase 5 by adding session-local memoization for base, skill, memory, context, and final system-prompt sections so identical prompt components are reused across turns instead of being rebuilt every iteration.
- Completed: added adaptive output budgeting so normal turns start with a smaller reserved output cap and only escalate toward the provider ceiling after a `max_tokens` stop, reclaiming prompt headroom without removing continuation behavior.
- Completed: added a shared context-pressure policy so proactive compaction and memory recall now consult the same pressure decision instead of independently guessing when to compact or skip extra memory context.
- Completed: added a `SupportsCaching` provider capability seam and switched prompt assembly to use a cache-stable section order only for cache-capable providers, keeping volatile sections later in the prompt when the provider can benefit from stable prefixes.
- Completed: tightened the shared context-pressure policy so continuation-heavy turns compact earlier and delay output-budget escalation when the prompt is already under pressure, keeping continuations, compaction, and memory recall aligned instead of competing for headroom.
- Completed: started Phase 6 by adding async API preconnect warmup during engine startup, reusing each provider client's existing HTTP transport and writing session timing records for warmup outcomes without blocking ready-to-query startup.
- Completed: surfaced streamed turn-latency checkpoints in the TUI footer and status line so first-token, first-tool-result, artifact-focus, and total turn timings are visible during and after a turn instead of staying buried in session timing logs.
- Completed: removed low-value ready-state status copy such as `Engine ready (protocol v1)` so the UI now relies on user-relevant readiness and activity signals instead of filling the screen with protocol details.
- Completed: added a local transcript search mode in the Ink TUI, activated with `Ctrl+G`, so long sessions can jump between matching transcript blocks without issuing a new model/tool request.
- Completed: taught transcript paging to recenter around selected search hits and surface visible match markers plus a dedicated search prompt, extending the existing PageUp/PageDown history window into actual long-session search navigation.
- Completed: taught the TUI to parse `bash` background launches plus `list_commands`, `command_status`, `send_command_input`, and `stop_command` results into retained background-command state instead of leaving command lifecycle updates buried in raw JSON tool output.
- Completed: added a compact Background Commands panel to the Ink TUI so active and recent retained shell commands show status, cwd, timing, failure metadata, and unread or latest output previews during long sessions.
- Completed: added transcript and status-line lifecycle notices for background shell commands so background launches, completions, failures, and newly unread output surface in the main session flow instead of only in the retained commands panel.
- Completed: surfaced a compact background-command summary in the main status bar so active runs, unread output, and failures remain visible even when the retained commands panel is out of view.
- Completed: distinguished intentional `stop_command` results from actual command failures in the TUI so stopped commands now retain a stable `stopped` status across notices, the retained commands panel, and the status bar.
- Completed: taught the TUI to treat `forget_command` as immediate retained-command removal so forgotten background commands disappear from the panel and status summary as soon as the cleanup tool succeeds.
- Completed: surfaced artifact counts, focused artifact summaries, and pending review context in the main status bar, and added explicit artifact-focus status-line updates so long sessions make artifact navigation progress visible without scanning the full transcript.
- Completed: prioritized the currently focused non-plan artifact in the preview area and added a visible focus marker so artifact navigation remains obvious without scanning multiple artifact cards.
- Completed: improved prompt editing ergonomics by keeping the cursor at the end of recalled history entries and surfacing live prompt metrics (chars, lines, line/column) in the footer during multiline editing.
- Completed: surfaced queued follow-up prompt counts in the status/footer chrome and added explicit footer block reasons (`booting`, `search open`, `turn active`, `engine error`) so deferred work and blocked input states stay visible without scanning the lower prompt area.
- Completed: closed Phase 2 after confirming the existing `internal/skills/` pipeline already loads global and project skills, auto-selects relevant entries, and injects them into prompt assembly, so no additional skills integration or parity-chasing tooling is needed for the phase goal.
- Completed: closed Phase 3 after confirming child sessions stay bounded by explicit tool allowlists, artifact mutation remains excluded from child execution, and non-auto-approved child write/execute requests degrade to denial instead of interactive permission deadlocks.
- Completed: closed Phase 4 after confirming durable memory writes already have explicit on-disk guidance, memory recall stays bounded through side-query selection plus safe fallback behavior, and recalled memory remains separate from artifact ownership and review flows.
- Completed: closed Phase 5 after confirming prompt assembly already memoizes stable sections, provider-specific cache ordering is explicitly gated by model capabilities, and compaction remains reviewable through existing compact-summary artifact handling rather than needing a separate sticky-latch feature first.
- Completed: emitted structured `memory_recalled` telemetry from the query loop using existing MEMORY.md recall metadata so each turn can report which durable notes were selected without polluting the transcript.
- Completed: surfaced low-noise per-turn memory recall summaries in the TUI footer, showing recalled note titles and recall source while keeping full recall content out of the main conversation flow.
- Completed: switched MEMORY.md index injection from whole-file dumping to bounded heuristic recall so only a small set of lines relevant to the current request enters the prompt by default.
- Completed: added a bounded model-backed memory side-query that selects relevant MEMORY.md index entries before each turn and falls back to the existing heuristic recall path when the side-query fails or returns nothing.
- Completed: parsed canonical MEMORY.md entries into structured filename/title/type records, switched recall candidates to use that structured metadata, and loaded referenced memory note excerpts on demand for the entries that were actually recalled.
- Completed: split memory side-query usage into its own tracker counters, extended the `cost_update` payload with memory-recall totals, and surfaced the breakdown in the TUI status bar while keeping aggregate session cost unchanged.
- Completed: tightened MEMORY.md validation so malformed canonical entries and unsafe or missing note references are flagged explicitly, skipped from structured recall candidates, and surfaced as warnings in recalled memory context instead of silently degrading.

## Next Planning Baseline

See `plan.md` for full phase ordering rationale and cross-cutting concerns (cost tracking, provider abstraction, skills evaluation).
