# Plan

## Goal

Reduce TUI distraction by aligning the default interaction flow more closely with the ClaudeCode reference in `sourcecode/`.

## Reference Notes

- `sourcecode/hooks/useCancelRequest.ts`: background work is surfaced through lightweight notifications instead of a persistent panel.
- `sourcecode/hooks/useTypeahead.tsx`: thinking is not pushed into the transcript by default; the UI points to a keyboard shortcut instead.
- `sourcecode/components/Spinner.tsx`: active work stays compact, centered around a narrow status line rather than expanded panels.
- `sourcecode/components/FileEditToolUpdatedMessage.tsx`: file edits can be condensed to a short summary instead of always expanding the diff inline.

## Tasks

1. Remove persistent background agent and background command panels from the main TUI surface while preserving status visibility.
2. Hide streaming thinking content by default and add an explicit keyboard shortcut affordance to reveal or hide it.
3. Collapse inline file diff previews in the transcript to concise mutation summaries.
4. Regenerate the TUI build output, update progress tracking, and commit the completed task.

## Follow-up

1. Replace low-contrast footer mode colors with high-contrast badges for `FAST` and `PLAN`.
2. Remove inherited dim styling from the footer mode badge and switch to darker backgrounds.
3. Replace colored footer mode badges with a neutral no-background tag treatment.
4. Remove mode-specific color styling from the top status bar.
