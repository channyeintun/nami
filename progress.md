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
- 2026-06-08: Improved the early transcript renderer by aggregating streaming assistant tokens and formatting common engine events.
- 2026-06-08: Added Bubbles `key` and `help` support for footer key hints and help toggling.
- 2026-06-08: Polished early shell layout by resizing around expanded help, error rows, and smaller terminal heights.
- 2026-06-08: Started Phase 3 by extracting early TUI state and engine-event handling into `state.go` and `reducer.go`.
- 2026-06-08: Extended reducer state and the status bar for mode, model, context usage, cost, and rate-limit updates.
- 2026-06-08: Extended reducer state for artifacts, artifact review requests, background commands, and background agents.
- 2026-06-08: Extended reducer state for permission requests, ask-user-question prompts, and model/reasoning/resume/rewind selection requests.
- 2026-06-08: Added reducer support for conversation hydration and rebuilt early transcript lines from hydrated messages, progress, and tool calls.
- 2026-06-08: Extended reducer state for memory recall, retrieval, compaction, timing, and session update/restore/rewind events.
- 2026-06-08: Started Phase 4 by replacing plain transcript strings with structured transcript entries and a dedicated renderer.
- 2026-06-08: Added prompt history navigation and basic tab completion for ready-time slash commands.
- 2026-06-08: Added basic transcript search state plus page scrolling and follow-tail controls.
- 2026-06-08: Improved structured transcript row kinds for tool, progress, artifact, background, and error events.
- 2026-06-08: Started Phase 5 by rendering pending permission, question, selection, and artifact-review request state in a dialog band.
- 2026-06-08: Wired keyboard responses for permission dialogs and artifact-review dialogs.
- 2026-06-08: Wired Bubbles list selection dialogs for model, reasoning, resume, and rewind requests with enter/escape responses.
- 2026-06-08: Wired basic ask-user-question responses using recommended or first options, with cancel support.
- 2026-06-08: Improved ask-user-question dialog rendering by previewing the default answers that `y` will send.
- 2026-06-08: Started Phase 6 by handling Bubble Tea paste messages and appending pasted text into the prompt composer.
- 2026-06-08: Added image reference parsing for pasted image data URLs and absolute local image paths, sending referenced images with user input.
- 2026-06-08: Added lightweight markdown/code rendering for transcript rows, including fenced code block styling.
- 2026-06-08: Started Phase 7 by replacing the `cmd/nami-engine` Node launcher path with the in-process Bubble Tea TUI while preserving `--stdio`.
- 2026-06-08: Updated the Unix installer to install a single `nami` binary and removed JS runtime checks from that path.
- 2026-06-08: Updated the Windows installer and release Makefiles to install and package a single Go `nami` executable.
- 2026-06-08: Updated README and website install/architecture docs for the Bubble Tea single-executable packaging model.
- 2026-06-08: Removed the legacy Silvery/React TUI source tree and stale npm package files now that release packaging uses the Go executable.
- 2026-06-08: Fixed release packaging to build the full Cobra CLI package as `nami`, preserving `--stdio`, and bumped the source-build requirement to Go 1.26.4 after `govulncheck` flagged standard-library vulnerabilities in Go 1.26.3.
- 2026-06-08: Completed automated final checks: `go build ./...`, `make release`, archive single-binary inspection, `nami --help` without Node on `PATH`, stdio `shutdown` smoke, and `govulncheck`.

## Next Task

Start Phase 7 by collapsing packaging and CLI:

- run a manual interactive Bubble Tea TUI smoke in a real terminal and resolve any remaining parity gaps
- keep the no-tests constraint while porting reducer logic

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
- improve transcript rendering beyond raw event summaries
- add footer key hints with Bubbles `key` and `help`
- handle resize and prompt layout polish
- create reducer state structs for ready status, transcript, active turn, and errors
- move event handling out of the root model into a reducer package/file
- extend reducer state to cover model, mode, context, cost, and rate limits
- extend reducer state to cover artifacts and background tasks
- extend reducer state to cover selection dialogs and permission/question prompts
- extend reducer state for conversation hydration and persisted transcript entries
- extend reducer state for memory, retrieval, compaction, timing, and session restore/rewind events
- replace the early transcript text renderer with structured transcript rows
- add prompt history and slash-command completion basics
- add transcript search and scroll/follow-tail behavior
- improve core transcript rows for tool/progress/artifact entries
- start Phase 5 dialog surfaces for permission, question, selection, and artifact review prompts
- wire keyboard responses for permission and artifact review dialogs
- wire basic selection dialog responses for model, reasoning, resume, and rewind requests
- wire basic ask-user-question responses
- improve dialog answer editing and multi-option flows
- start Phase 6 paste, clipboard, image, and markdown support
- add image reference parsing for pasted data URLs and file paths
- add markdown/code rendering for transcript rows
- start Phase 7 packaging and CLI collapse toward the single `nami` executable
- update installers and release/build scripts to install one Go executable
- update Windows installer and release/build scripts to install one Go executable
- update docs and website install instructions for the single executable
- remove legacy JS TUI sources and npm package metadata
- run final release, stdio, vulnerability, and no-JS-runtime verification
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
