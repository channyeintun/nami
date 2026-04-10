# Progress

## Working Rules

- Follow [plan.md](/Users/channyeintun/Documents/go-code/plan.md) as the execution baseline.
- Reference `sourcecode/` first for every feature or behavior change.
- Do not add tests.
- After each completed task: update this file, run formatting, and create a git commit.

## Current Status

| Phase                                      | Status      | Notes                                                                                                                                                                                     |
| ------------------------------------------ | ----------- | ----------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| 1. Layout and prompt foundation            | in progress | Cursor-aware editing, multiline entry, wrapped-line navigation, and a fuller prompt footer are in place. Clipboard image paste is blocked by the current text-only `user_input` protocol. |
| 2. Permission UX parity                    | not started | Waiting for Phase 1 completion.                                                                                                                                                           |
| 3. Markdown and syntax highlighting parity | not started | Waiting for Phase 2 completion.                                                                                                                                                           |
| 4. Transcript/message-row parity           | not started | Waiting for Phase 3 completion.                                                                                                                                                           |
| 5a. Status line parity                     | not started | Waiting for Phase 4 completion.                                                                                                                                                           |
| 5b. Prompt footer parity                   | not started | Waiting for Phase 5a completion.                                                                                                                                                          |
| 6. Protocol follow-up                      | not started | Only if parity requires engine changes.                                                                                                                                                   |

## Task Log

### 2026-04-10

- Completed: reset `progress.md` back to the current parity plan only after stale unrelated history reappeared.
- Completed: referenced `sourcecode/hooks/useTextInput.ts`, `sourcecode/hooks/useArrowKeyHistory.tsx`, `sourcecode/components/TextInput.tsx`, and `sourcecode/utils/Cursor.ts` before continuing Phase 1 prompt work.
- Completed: landed the first Phase 1 slice with cursor-aware editing, multiline input via Shift+Enter or Meta+Enter, word and line movement, and a bordered prompt container.
- Completed: added wrapped-line aware prompt rendering and vertical cursor movement based on the current terminal width.
- Completed: added a fuller prompt-adjacent footer with mode, activity, wrapped-input state, and shortcut hints, based on upstream `PromptInputFooter` and `PromptInputFooterLeftSide` structure.
- Remaining in Phase 1: clipboard image paste support.
- Blocker: image paste requires protocol expansion because `go-cli/tui` currently sends only text in `user_input` and the Go IPC payload mirrors that text-only shape.
