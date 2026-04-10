# Progress

## Working Rules

- Follow [plan.md](/Users/channyeintun/Documents/go-code/plan.md) as the execution baseline.
- Reference `sourcecode/` first for every feature or behavior change.
- Do not add tests.
- After each completed task: update this file, run formatting, and create a git commit.

## Current Status

| Phase                      | Status    | Notes                                                                                                                                                                                                                                                                                                   |
| -------------------------- | --------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 6. Protocol follow-up      | completed | Permission amendment/feedback text, raw 5h/7d Anthropic rate-limit windows, configurable footer cost-threshold notices, and block-oriented assistant message rendering are now wired through the IPC/TUI path.                                                                                          |
| 7. Deferred infrastructure | completed | The capped transcript now supports PageUp/PageDown paging plus Home/End jumps in stock Ink, which is sufficient for this parity pass. Full scroll/fullscreen primitives for a true virtualized list remain explicitly deferred because the upstream implementation relies on custom renderer internals. |

## Task Log

### 2026-04-10

- Completed: Phases 1–5b (layout/prompt foundation, permission UX, markdown/syntax highlighting, transcript/message-row, status line, prompt footer) and the first Phase 6 slice (session metadata, live context usage, structured permission metadata).
- See git history for detailed per-task entries.

### 2026-04-11

- Completed: Phase 6 permission amendment/feedback text parity slice. The permission prompt now accepts an optional note, the IPC payload carries it, denials include it in the rejection reason, and approved tool executions append it to the emitted tool result so the model can see the user's note on the next turn.
- Completed: Phase 6 rate-limit status-line slice. Anthropic response headers now emit raw 5-hour and 7-day utilization windows through the engine protocol, and the TUI status bar shows those percentages when the provider returns them.
- Completed: Phase 6 cost-threshold notice slice. The CLI now exposes a configurable session cost warning threshold, passes it into the TUI, and shows a footer warning once tracked spend crosses that threshold.
- Completed: Phase 6 block-oriented assistant message slice. The TUI now stores assistant turns as ordered thinking/text blocks, preserves that structure for completed turns, and renders the live stream from the same block model instead of separate flat text buffers.
- Completed: First Phase 7 transcript navigation slice. The capped transcript now supports PageUp/PageDown paging, and the footer documents those keys so long sessions can be reviewed without immediately jumping to custom fullscreen/scroll renderer work.
- Completed: Final Phase 7 resolution slice. The capped transcript now also supports Home/End jumps, and the parity plan now explicitly accepts capped transcript paging as sufficient in stock Ink while deferring true fullscreen/virtualized transcript infrastructure.
- Completed: Fixed a TUI startup readiness race that could leave the prompt blocked for fast startup paths such as local Ollama models. Engine events are now delivered directly into the UI reducer instead of relying on a single last-event snapshot, and the prompt/boot banner now use the combined engine-ready state.
- Completed: Added hidden compatibility handling for local model tool-name drift. The registry now accepts read-only aliases like `file_search` and `read_file`, and the engine also normalizes Copilot-style names such as `grep_search`, without advertising those aliases in the tool definitions.
- Completed: Fixed duplicate live thinking spinners and tightened plan-mode plan promotion. Streaming thinking now renders a single active spinner, and explain/research requests in plan mode no longer get promoted into the implementation plan artifact just because the model answered with a structured outline.
- Completed: Fixed the permission-resume/cancel streaming leak in the TUI. Approving a tool prompt no longer fabricates a fresh assistant turn before any resumed output arrives, tool permission/execution now pauses the live assistant spinner instead of looking like the model is still thinking, and pressing Esc clears the local stuck-streaming state while the cancel request is sent to the engine.
- Completed: Fixed active-turn queueing and live-turn visibility in the TUI. Prompts submitted while another turn is still active now queue locally instead of clearing the current response, the live assistant row stays visible across tool execution and permission waits, and Esc interruption now cancels active turns from the router immediately instead of waiting for the next message poll.
- Adjusted: Backed out the explicit `file_read` transcript presentation change. The “shown above” confusion is being treated as a model-behavior issue unless we explicitly decide to change how read results render in the UI.
- Completed: Matched the interrupt end-state more closely to the source flow. Pressing Esc now leaves the live assistant content visible while cancellation is pending, and a cancelled turn preserves the partial assistant response in the transcript instead of clearing it outright.
- Completed: Made the implementation plan panel transient instead of permanently pinned. A freshly produced plan now shows as a confirmation/review panel while still in plan mode, then hides after the next user response or when leaving plan mode.
