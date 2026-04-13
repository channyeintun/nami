# Progress

## Active Task

- Completed: reduced transcript and panel distraction in the TUI by adopting ClaudeCode-style status-first affordances.
- Completed: improved footer mode badge contrast for accessibility.
- Completed: removed dimming inheritance and replaced bright footer mode badge backgrounds.
- Completed: replaced colored footer mode badges with a neutral tag treatment.
- Completed: removed remaining mode color styling from the top status bar.

## Notes

- Planning and reference review completed.
- Removed persistent background agent and background command panels from the main surface.
- Kept background work visible through the status bar summaries and existing status notices.
- Hid streaming thinking content by default and added an explicit `Opt+T` reveal/hide shortcut.
- Collapsed inline file diff previews to concise mutation summaries so edits no longer dominate the transcript.
- Replaced low-contrast footer mode text with high-contrast badges so `FAST` and `PLAN` remain readable across terminal themes.
- The footer badge no longer inherits `dimColor`, and `PLAN`/`FAST` now use darker blue/green backgrounds for better readability.
- The footer mode marker now uses a simple bold `[MODE]` tag with no background color.
- The top status bar mode label now uses the same neutral bold `[MODE]` treatment with no mode-specific color.
