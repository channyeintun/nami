# Progress

## Working Rules

- Follow [plan.md](/Users/channyeintun/Documents/go-code/plan.md) as the execution baseline.
- Reference `sourcecode/` first for every feature or behavior change.
- Do not add tests.
- After each completed task: update this file, run formatting, and create a git commit.

## Current Status

| Phase                      | Status      | Notes                                                                                                                                                                                                          |
| -------------------------- | ----------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 6. Protocol follow-up      | completed   | Permission amendment/feedback text, raw 5h/7d Anthropic rate-limit windows, configurable footer cost-threshold notices, and block-oriented assistant message rendering are now wired through the IPC/TUI path. |
| 7. Deferred infrastructure | not started | Virtual transcript list requires scroll/fullscreen primitives the TUI does not yet have.                                                                                                                       |

## Task Log

### 2026-04-10

- Completed: Phases 1–5b (layout/prompt foundation, permission UX, markdown/syntax highlighting, transcript/message-row, status line, prompt footer) and the first Phase 6 slice (session metadata, live context usage, structured permission metadata).
- See git history for detailed per-task entries.

### 2026-04-11

- Completed: Phase 6 permission amendment/feedback text parity slice. The permission prompt now accepts an optional note, the IPC payload carries it, denials include it in the rejection reason, and approved tool executions append it to the emitted tool result so the model can see the user's note on the next turn.
- Completed: Phase 6 rate-limit status-line slice. Anthropic response headers now emit raw 5-hour and 7-day utilization windows through the engine protocol, and the TUI status bar shows those percentages when the provider returns them.
- Completed: Phase 6 cost-threshold notice slice. The CLI now exposes a configurable session cost warning threshold, passes it into the TUI, and shows a footer warning once tracked spend crosses that threshold.
- Completed: Phase 6 block-oriented assistant message slice. The TUI now stores assistant turns as ordered thinking/text blocks, preserves that structure for completed turns, and renders the live stream from the same block model instead of separate flat text buffers.
