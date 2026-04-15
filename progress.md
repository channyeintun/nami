# Progress

## Current

- Post-review regression fixes are complete; `ModelChanged` remains intentionally unchanged.

## Completed

- Phase 1 display cleanup completed.
	- Added a shared TUI helper for stripping provider prefixes.
	- Switched StatusBar, ModelSelectionPrompt, ResumeSelectionPrompt, StreamingAssistantMessage, and AssistantTextMessage to the shared helper.
- Phase 2 provider inference updates completed.
	- Added provider hints to curated model presets.
	- Carried provider hints through the model-selection IPC flow.
	- Kept GitHub Copilot as the routing authority when the active provider is Copilot.
- Phase 5 subagent model selection completed.
	- Added a /subagent slash command that reuses the existing model picker.
	- Stored the active subagent model as session-scoped state and persisted it in session metadata.
	- Reset the subagent model on new sessions and restored it on resumed sessions.
	- Surfaced the current subagent model in status output and /subagent help text.
- Root binary cleanup completed.
	- Removed the tracked local build artifact at chan/chan.
	- Added an ignore rule so future local Go builds do not show up in git status.
- Phase 4 protocol cleanup partially completed.
	- `ModelSelectionRequested.CurrentModel` now sends model-only display text.
	- `ModelChangedPayload.Model` remains `provider/model` because the TUI still uses the full value.
- Post-review regression fixes completed.
	- `/model default` now preserves the configured provider/model instead of re-inferring a backend.
	- `/subagent` selections now apply in non-Copilot sessions, and non-Copilot sessions default to the active model unless explicitly overridden.
	- The TUI model picker custom-entry mode no longer exits after the first keystroke.
	- App toast rendering again preserves toast variants and descriptions.

## Next

- Optional future cleanup: only revisit `ModelChangedPayload.Model` if context-window logic moves server-side.