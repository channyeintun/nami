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
