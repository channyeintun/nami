# Bubble Tea TUI Migration Progress

## Working Rules

- Follow `plan.md` task order unless current code shape requires a smaller prerequisite.
- Do not add tests. This overrides the test-related acceptance items in `plan.md`.
- After each completed task:
  - update this file
  - run `gofmt`, `goimports`, and `go build`
  - commit the completed task with git
  - reassess this file before continuing

## Current Status

The migration is not complete.

## Completed Tasks

- 2026-06-08: Created the Bubble Tea migration plan in `plan.md`.
- 2026-06-08: Created this progress tracker and recorded the no-tests constraint.
- 2026-06-08: Started Phase 1 by adding IPC message-source/event-sink interfaces, switching `MessageRouter` to the message-source interface, and adding a channel-backed transport for future embedded mode.
- 2026-06-08: Extracted `RunStdioEngine` into a stdio wrapper around a reusable transport-driven engine runner, and moved engine emitters onto the `ipc.EventSink` boundary.
- 2026-06-08: Added `engine.RunEmbeddedEngine` so future Bubble Tea code can run the engine in-process over channels.
- 2026-06-08: Audited router shutdown and added `MessageRouter.Stop`, with the engine deferring router cleanup after startup.
- 2026-06-08: Added latest verified Charm v2 dependencies: Bubble Tea `v2.0.7`, Bubbles `v2.1.0`, and Lip Gloss `v2.0.3`.
- 2026-06-08: Added a minimal Bubble Tea shell with `viewport`, `textarea`, Lip Gloss styles, and a new `cmd/nami` entrypoint.
- 2026-06-08: Wired the Bubble Tea shell to `engine.RunEmbeddedEngine`, converting engine events into Bubble Tea messages and submitted prompts into `ipc.ClientMessage` values.
- 2026-06-08: Added active-turn tracking so `ctrl+c` sends `ipc.MsgCancel` during a turn and exits only while idle.

## Next Task

Start Phase 2 by adding a minimal Bubble Tea shell:

- improve transcript rendering beyond raw event summaries
- handle resize and prompt layout polish

Done:

- inspect `engine.RunStdioEngine`, `ipc.Bridge`, and `ipc.MessageRouter`
- introduce a small message-source/event-sink boundary for stdio and future embedded mode
- extract the current `engine.RunStdioEngine` setup into a reusable internal runner boundary
- wire the new channel-backed transport into an embedded engine entrypoint
- audit cancellation and shutdown behavior on the new transport boundary
- add Charm v2 dependencies
- create a minimal `internal/tui` Bubble Tea model
- add a `cmd/nami` entrypoint that starts the shell
- start the embedded engine from the Bubble Tea shell
- convert engine events into Bubble Tea messages
- send submitted prompts to the embedded engine
- refine cancellation behavior so `ctrl+c` cancels an active turn before exiting when idle
- keep the stdio wrapper behavior unchanged
- keep `nami --stdio` behavior unchanged

## Open Phases

- Phase 1: Stabilize the engine boundary.
- Phase 2: Add a minimal Bubble Tea shell.
- Phase 3: Port the UI reducer without adding tests.
- Phase 4: Port core surfaces.
- Phase 5: Port dialogs.
- Phase 6: Port paste, clipboard, images, and markdown.
- Phase 7: Collapse packaging and CLI to a single executable.
- Phase 8: Run verification gates without adding tests.
