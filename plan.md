# Bubble Tea TUI Migration Plan

## Goal

Migrate Nami from the current Silvery/React TUI plus Go engine split-process architecture to a single Go executable with a Bubble Tea TUI.

The normal user path should become:

```text
nami
  -> Bubble Tea TUI
  -> in-process Go engine
```

The result should remove the runtime dependency on Node.js, Bun, or Deno for normal TUI usage while preserving the engine's typed event/message contract for tests, automation, and future integrations.

## Current Architecture

Today, Nami has two runtime halves:

- `nami/cmd/nami-engine/main.go` loads config and starts either `engine.RunStdioEngine` or `launchTUI`.
- `launchTUI` requires `node`, resolves `nami/tui/dist/index.js`, and passes `NAMI_ENGINE_PATH`, model, mode, and auto-mode through environment variables.
- `nami/tui/src/hooks/useEngine.ts` starts the Go binary as a child process with `--stdio`.
- `nami/internal/ipc/protocol.go` defines the protocol:
  - Go -> TUI: `ipc.StreamEvent`
  - TUI -> Go: `ipc.ClientMessage`
- `nami/internal/ipc/bridge.go` serializes the protocol as NDJSON over stdin/stdout.
- `nami/internal/ipc/router.go` buffers client messages and routes cancellation.
- `nami/tui/src/hooks/useEvents.ts` is the current UI reducer.
- `nami/tui/src/App.tsx` owns the main screen state, prompt queue, overlays, transcript search, background task polling, and event dispatch.
- `nami/tui/Makefile`, `nami/install.sh`, and `nami/install.ps1` package and install three artifacts: wrapper, `nami.js`, and `nami-engine`.

## Target Architecture

Keep the protocol types, remove the subprocess requirement.

```text
cmd/nami
  |
  +-- internal/tui
  |     Bubble Tea program
  |     owns terminal input, layout, rendering, and UI state
  |
  +-- internal/engine
        same agent loop, tools, sessions, permissions, artifacts, MCP
        emits ipc.StreamEvent values into the TUI
        receives ipc.ClientMessage values from the TUI
```

The stdio engine path should remain available:

```text
nami --stdio
  -> NDJSON engine mode
```

That preserves automation and gives the new Bubble Tea TUI a stable migration fallback.

## Dependency Decision

Use the latest stable Charm v2 modules at the time implementation begins. As of 2026-06-08, the current targets are:

- `charm.land/bubbletea/v2@v2.0.7` for the TUI runtime.
- `charm.land/bubbles/v2@v2.1.0` for reusable components.
- `charm.land/lipgloss/v2@v2.0.3` for layout and styling.

Initial install command:

```bash
go get charm.land/bubbletea/v2@v2.0.7 charm.land/bubbles/v2@v2.1.0 charm.land/lipgloss/v2@v2.0.3
go mod tidy
```

Use Bubbles as the default component layer. Bubble Tea should own the runtime loop, message model, terminal features, and renderer. Lip Gloss should own styles and layout primitives. Bubbles should own standard interactive components unless a Nami workflow needs custom behavior.

Expected Bubbles components:

- `textarea` for the prompt composer, with `DynamicHeight` evaluated for the current prompt area.
- `viewport` for the transcript and long dialog bodies.
- `spinner` for inline activity states.
- `key` and `help` for consistent key bindings and footer hints.
- `list` for model, reasoning, resume, rewind, and slash-command selection surfaces when it fits.
- `table` only for structured data where column alignment matters.
- `progress` only if native progress rendering is better than the current textual progress rows.

Build custom components only for Nami-specific behavior such as transcript rows, tool output summaries, artifact review, permission prompts, background task dashboards, image-reference tracking, and prompt queue merging.

Keep `CGO_ENABLED=0` release builds unless a selected dependency forces otherwise.

References:

- Bubble Tea README: https://github.com/charmbracelet/bubbletea
- Bubble Tea v2 upgrade guide: https://github.com/charmbracelet/bubbletea/blob/main/UPGRADE_GUIDE_V2.md
- Bubbles releases: https://github.com/charmbracelet/bubbles/releases
- Bubbles package docs: https://pkg.go.dev/charm.land/bubbles/v2
- Lip Gloss releases: https://github.com/charmbracelet/lipgloss/releases

## Migration Principles

- Preserve engine behavior first. The TUI migration should not rewrite model providers, tool execution, session storage, permission policy, artifact storage, or MCP behavior.
- Treat `ipc/protocol.go` as the source of truth for cross-layer messages.
- Port `useEvents.ts` into a small, testable Go reducer before rebuilding every visual component.
- Prefer Bubbles components before writing custom interactive widgets.
- Keep the current Silvery TUI available until the Bubble Tea TUI covers core workflows.
- Prefer simple package boundaries and explicit error returns.
- Keep `--stdio` working throughout the migration.

## Proposed Package Layout

```text
nami/cmd/nami/
  main.go

nami/internal/engine/
  embedded.go          # in-process engine entrypoint
  stdio.go             # stdio adapter, or thin wrapper around existing code

nami/internal/ipc/
  protocol.go          # existing typed protocol
  bridge.go            # existing NDJSON bridge
  channel_bridge.go    # in-process channel transport
  router.go            # generalized to read from a message source

nami/internal/tui/
  app.go               # tea.Program setup
  model.go             # root Bubble Tea model
  update.go            # key handling and event dispatch
  view.go              # root layout
  engine_client.go     # sends ClientMessage, receives StreamEvent
  reducer.go           # Go port of useEvents.ts
  state.go             # UI state structs
  keymap.go            # keyboard bindings
  styles.go            # lipgloss styles
  prompt.go            # composer, prompt history, image references
  transcript.go        # transcript model and rendering
  artifacts.go         # artifact/plan rendering
  dialogs.go           # permission/model/reasoning/rewind/resume/question
  background_tasks.go  # background command/agent dashboard
  clipboard.go         # paste and image paste helpers
  markdown.go          # markdown/code rendering adapter
```

This can be adjusted as code lands. The important boundary is that `internal/tui` owns UI state and rendering, while `internal/engine` owns agent execution.

## Phase 1: Stabilize The Engine Boundary

Tasks:

- Extract most of `engine.RunStdioEngine` into an internal runner that accepts:
  - an event sink for `ipc.StreamEvent`
  - a client message source for `ipc.ClientMessage`
  - the existing config and context
- Keep `RunStdioEngine(ctx, cfg)` as a wrapper around the NDJSON bridge.
- Add `RunEmbeddedEngine(ctx, cfg, messages, events)` or an equivalent in-process API.
- Generalize `ipc.MessageRouter` so it reads from an interface instead of directly from `*ipc.Bridge`.
- Add a channel-backed IPC implementation for embedded mode.
- Ensure cancellation still works:
  - Bubble Tea sends `ipc.MsgCancel`
  - router calls the active cancel function
  - no stale cancellation leaks into later turns
- Ensure shutdown still works:
  - Bubble Tea sends `ipc.MsgShutdown`
  - engine returns cleanly
  - all background commands for the session are shut down

Acceptance criteria:

- `nami --stdio` behaves the same as before.
- A small test can start the embedded engine, receive `ready`, send `shutdown`, and exit without goroutine leaks.
- Existing engine tests still pass.

## Phase 2: Add A Minimal Bubble Tea Shell

Tasks:

- Create a Bubble Tea entrypoint that starts the embedded engine in a goroutine.
- Convert engine events into `tea.Msg` values.
- Convert user actions into `ipc.ClientMessage` values.
- Render a minimal full-screen layout:
  - status line
  - transcript `viewport`
  - prompt `textarea`
  - error line
- Wire core keys:
  - `ctrl+c` / escape cancellation behavior
  - enter to submit
  - shift-enter or alt-enter for newline
  - tab to toggle mode
- Handle terminal resize with Bubble Tea window size messages.
- Set alt-screen/full-window behavior through Bubble Tea v2 `tea.View` fields.
- Use Bubbles `textarea`, `viewport`, `spinner`, `key`, and `help` before introducing custom primitives.

Acceptance criteria:

- `go run ./cmd/nami` starts the Bubble Tea TUI.
- The TUI receives the engine `ready` event.
- A plain prompt can be sent to the engine.
- Streaming assistant output appears in the transcript.
- `ctrl+c` cancels an active turn or exits cleanly when idle.

## Phase 3: Port The UI Reducer

Tasks:

- Port the state shape from `nami/tui/src/hooks/useEvents.ts` into Go structs.
- Implement `ApplyEvent(state, event)` as mostly pure reducer logic.
- Preserve these state areas:
  - ready status and slash commands
  - messages and transcript entries
  - live assistant blocks
  - progress entries
  - tool calls and tool statuses
  - permission request state
  - ask-user-question state
  - model, reasoning, rewind, and resume selection state
  - artifact list, focused artifact, and artifact review state
  - context window, cost, rate limits, and compact state
  - background command and background agent state
  - session id/title restore/update state
- Add reducer tests that replay representative `ipc.StreamEvent` sequences.

Acceptance criteria:

- Reducer tests cover ready, streaming, tool lifecycle, permission flow, artifacts, background task updates, and turn completion.
- Event handling does not depend on terminal rendering.

## Phase 4: Port Core Surfaces

Tasks:

- Status bar:
  - ready state
  - mode
  - model and reasoning effort
  - session title/id
  - context usage
  - cost and rate limit summaries
  - artifact and background task indicators
- Transcript:
  - user messages
  - assistant messages
  - thinking blocks with visibility toggle
  - streaming assistant message
  - tool start/progress/result/error blocks
  - progress notices
  - system/error/notice messages
  - queued prompts
  - transcript search
  - follow-tail behavior until the user scrolls
- Prompt:
  - prompt history navigation
  - multi-line editing
  - slash command preview and completion
  - prompt queue merging while a turn is active
  - image reference markers
  - paste warning display
- Footer:
  - concise key hints
  - expanded hints on `?`

Implementation notes:

- Use `viewport` or an equivalent cached transcript renderer for long transcript history.
- Avoid rebuilding expensive markdown or tool output views on every key press when possible.
- Keep the current prompt keymap unless a key cannot be represented reliably by Bubble Tea.

Acceptance criteria:

- The Bubble Tea TUI can complete a normal chat turn.
- Long transcripts scroll correctly.
- Search can jump through transcript matches.
- Queued prompts behave like the current TUI.
- Thinking visibility, artifact visibility, reasoning cycling, and background task toggles work.

## Phase 5: Port Dialogs And Blocking Flows

Tasks:

- Permission prompt:
  - allow
  - deny
  - always allow
  - allow all session
  - optional feedback
  - cancel turn
- Ask-user-question prompt:
  - single-select
  - multi-select
  - freeform
  - decline/cancel
- Model selection prompt.
- Reasoning selection prompt.
- Rewind selection prompt.
- Resume selection prompt.
- Artifact review prompt:
  - approve
  - revise with feedback
  - cancel
- Background tasks dialog:
  - list background commands and agents
  - inspect
  - stop
  - swarm dashboard polling while open

Acceptance criteria:

- Every `ClientMessageType` currently sent by the React TUI has a Bubble Tea equivalent.
- Blocking prompts disable normal composer input.
- Responses include the same payload fields used by the engine today.

## Phase 6: Paste, Clipboard, Images, And Markdown

Tasks:

- Reimplement `nami/tui/src/utils/imagePaste.ts` behavior in Go:
  - parse image data URLs
  - parse pasted absolute image paths
  - read image files as base64 payloads
  - macOS clipboard image support through `osascript`
  - Windows clipboard image support through PowerShell
  - Linux support for file paths and data URLs first; native clipboard image support can be a follow-up if needed
- Reimplement image reference tracking:
  - insert `[Image #N]`
  - retain only referenced pasted images
  - send `ipc.ImageInputPayload` with the submitted prompt
- Preserve text paste behavior.
- Decide markdown renderer:
  - use a Go renderer such as Glamour if it covers Nami's transcript needs
  - otherwise port the current lightweight markdown rendering rules
- Replace `cli-highlight` with a Go syntax highlighter only if needed.
- Decide whether native clipboard bridge behavior is still required:
  - Bubble Tea v2 has native clipboard support in supported terminals
  - keep OS-specific fallback only where testing shows it is needed

Acceptance criteria:

- Text paste works in supported terminals.
- Image file path paste works on macOS, Linux, and Windows.
- Native clipboard image paste works on macOS and Windows or is explicitly documented as deferred.
- Markdown output remains readable for headings, lists, code blocks, tables, and links.

## Phase 7: Packaging And CLI Collapse

Tasks:

- Rename or add the primary command package as `nami/cmd/nami`.
- Build one binary named `nami` for normal releases.
- Keep `--stdio` on the same binary.
- Remove `launchTUI` and Node resolution from the normal path.
- Update `nami/Makefile` to build Go-only artifacts.
- Replace `nami/tui/Makefile` release flow with a Go release flow.
- Update `nami/install.sh`:
  - download one platform archive containing one `nami` binary
  - stop checking for Node, Bun, or Deno
  - install only `nami`
- Update `nami/install.ps1`:
  - stop installing portable Node.js
  - install only `nami.exe`
  - verify with `nami --help`
- Decide whether to publish compatibility assets for one release:
  - preferred final state is single binary only
  - optional transition state can include a deprecated `nami-engine` alias if existing scripts depend on it
- Update README and website docs.
- Keep `nami/tui` in the repo until Bubble Tea parity is accepted, then remove it in a final cleanup PR.

Acceptance criteria:

- Release artifacts contain one executable per OS/architecture.
- `nami` starts without Node, Bun, or Deno on PATH.
- `nami --stdio` still supports NDJSON automation.
- Installer docs no longer mention JavaScript runtime prerequisites.

## Phase 8: Verification

Automated checks:

- `go test ./...`
- `go run golang.org/x/vuln/cmd/govulncheck@latest ./...`
- `npm audit` while `nami/tui` still exists
- `go test -race ./internal/ipc ./internal/engine ./internal/tui` if race runtime cost is acceptable

Manual smoke tests:

- Fresh start, send one prompt, receive streaming response.
- Cancel a running turn.
- Run a tool that needs permission and answer the prompt.
- Toggle plan/fast mode.
- Use slash command preview and submit a slash command.
- Switch model and reasoning effort.
- Resume a previous session.
- Rewind a session.
- Trigger artifact review and answer it.
- Start/inspect/stop a background command.
- Run a child agent and inspect background agent details.
- Paste multi-line text.
- Paste an image path.
- Paste a clipboard image on macOS and Windows.
- Resize terminal from narrow to wide and back.
- Scroll a long transcript, then verify follow-tail resumes at bottom.
- Search transcript.
- Verify clean exit leaves no child process behind.

Release checks:

- Build darwin-amd64, darwin-arm64, linux-amd64, linux-arm64, windows-amd64, and windows-arm64.
- Run `nami --help` from each archive shape.
- Confirm archive contents do not include `nami.js`, wrapper shims, or `nami-engine` unless intentionally included for a transition release.

## Risks And Mitigations

- Engine/UI deadlock: use buffered channels and context-aware sends; never block the engine indefinitely on a render path.
- Re-render cost on long transcripts: cache rendered transcript blocks and invalidate only changed entries.
- Key behavior drift: create a keymap parity checklist from `Input.tsx` and verify it manually.
- Paste behavior drift: treat text paste and image paste as separate features with explicit tests.
- Markdown regressions: start with readable output, then iterate on tables/code highlighting.
- Terminal compatibility: test at least Ghostty, iTerm2, Terminal.app, Windows Terminal, and a basic Linux terminal.
- Dependency churn: pin Charm v2 modules and update intentionally.
- Packaging regression: keep `--stdio` and the old Silvery package path available until the single-binary path passes release smoke tests.

## Completion Criteria

The migration is complete when:

- Running `nami` launches the Bubble Tea TUI from one Go executable.
- The executable does not require Node.js, Bun, Deno, `nami.js`, or a separate `nami-engine` binary.
- All existing engine workflows are reachable from the Bubble Tea TUI.
- Installers and docs describe the single-binary architecture.
- `nami/tui` and JavaScript release machinery have been removed or clearly marked as legacy for one transition release.
- Go tests, vulnerability checks, and manual smoke tests pass.
