# UI Enhance Plan

Scope: `chan/tui` UI only. Do not add tests.

Execution rules:

- Follow these tasks in order.
- After each completed task: update `progress.md`, run formatting for touched code, and create a git commit.
- For code tasks, validate the affected build before committing.
- After all tasks are complete, install the latest local binaries to `~/.local/bin` with `install -m 755`.

## Task 1. Create plan and align tracking

- Create `ui-enhance-plan.md`.
- Record the new plan in `progress.md`.

Completion criteria:

- This plan exists in the repo.
- `progress.md` notes the UI enhancement work kickoff.

## Task 2. Switch the TUI to Sonokai and explicit DOM mouse handling

- Update the TUI bootstrap to build the theme with `createTheme().preset("sonokai").build()`.
- Replace the current minimal boot path with the Silvery app composition needed for explicit DOM-event mouse support while preserving fullscreen, kitty keyboard, focus reporting, and selection behavior.

Completion criteria:

- The TUI uses the Sonokai theme.
- The runtime explicitly enables the DOM-event mouse path.
- The TUI TypeScript build passes.

## Task 3. Hide search-tool result bodies in chat

- Suppress visible tool response bodies for file-search and grep-style search tools in the transcript.
- Keep the tool row header and status readable.
- Preserve clear empty-result messaging when there are no matches.

Completion criteria:

- Search tool rows no longer dump matched paths or grep output into chat.
- Empty-result cases still report cleanly.
- The TUI TypeScript build passes.

## Task 4. Rebuild and install the latest local binaries

- Produce fresh local release artifacts for `chan` and `chan-engine`.
- Install both binaries to `~/.local/bin` with `install -m 755`.
- Record the rebuild/install in `progress.md`.

Completion criteria:

- `chan/tui/release/chan` and `chan/tui/release/chan-engine` are rebuilt from the updated sources.
- `~/.local/bin/chan` and `~/.local/bin/chan-engine` are updated from the latest local release build.