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

## Next Task

Start Phase 1 by stabilizing the engine boundary:

- inspect `engine.RunStdioEngine`, `ipc.Bridge`, and `ipc.MessageRouter`
- introduce a small message-source/event-sink boundary for stdio and future embedded mode
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
