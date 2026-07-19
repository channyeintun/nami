# Silvery Guide For Nami

This is a project-specific guide for how `nami` currently uses Silvery in the TUI, plus the integration details and pitfalls discovered during the migration and follow-up UX work.

It is not a general Silvery tutorial. It is a practical note for working on `nami/tui`.

## Current Setup

### Runtime shape

- The TUI entrypoint is `nami/tui/src/index.tsx`.
- The app is wrapped in `ThemeProvider` from `silvery`.
- The current default theme is `presetTheme("nord")`.
- The main app root uses `Screen` so the layout fills the terminal and responds correctly to resize events.

### Package source

- `nami/tui` depends on the registry `silvery` package with version `^0.21.1`.
- `nami/tui/package-lock.json` resolves `silvery` at `0.21.1`, with `@silvery/ansi`, `@silvery/color`, and `@silvery/commander` from the registry at compatible `0.21.x` versions.
- The old local wrapper package has been removed.

## What Works Well

### 1. Use `Screen` as the shell root

For fullscreen terminal apps, `Screen` is the right top-level primitive.

Why:

- It owns terminal-sized layout cleanly.
- It behaves better than ad hoc top-level `Box` sizing.
- It keeps transcript, prompt, footer, and modal regions bounded instead of letting children grow unpredictably.

Current example:

- `nami/tui/src/App.tsx` uses `Screen` as the top-level container.

### 2. Use `ListView` for transcript history

For the main conversation surface, `ListView` is better than a `Box` with passive overflow scrolling.

Why:

- It gives a proper scroll/navigation model for long transcripts.
- It handles large item lists more predictably.
- Cursor-based navigation and live-tail behavior fit transcript rendering better than plain scroll boxes.

Current example:

- `nami/tui/src/components/StreamOutput.tsx`

Important detail:

- `overflowIndicator` adds the built-in `▲N` and `▼N` arrows. If those arrows are not desired, leave `overflowIndicator` unset.

### 3. Prefer semantic theme tokens over raw colors

Silvery’s theming model works best when app code uses semantic tokens like:

- `$primary`
- `$accent`
- `$muted`
- `$warning`
- `$error`
- `$success`
- `$border`

Why:

- The UI becomes easier to retheme.
- Component intent stays clear.
- Hardcoded ANSI colors become unnecessary in most visible UI chrome.

Current examples:

- `nami/tui/src/components/Input.tsx`
- `nami/tui/src/components/StatusBar.tsx`
- `nami/tui/src/components/TranscriptSearchPrompt.tsx`

### 4. Use `Spinner` directly for inline activity

Silvery’s `Spinner` works well for short inline activity indicators in the TUI.

Supported built-in types:

- `dots`
- `line`
- `arc`
- `bounce`

Current usage notes:

- The prompt-area spinner above the composer currently uses `arc`.
- The label above the prompt is normalized to `Working` instead of exposing raw `Thinking` state.

Current examples:

- `nami/tui/src/components/Input.tsx`
- `nami/tui/src/components/messages/StreamingAssistantMessage.tsx`
- `nami/tui/src/components/messages/AssistantThinkingMessage.tsx`

### 5. Use `usePaste` from `silvery/runtime`

Paste handling works correctly through `usePaste`.

Why:

- It fits Silvery’s runtime event model.
- It was the correct replacement during the Ink-to-Silvery migration.

Current example:

- `nami/tui/src/components/Input.tsx`

### 6. Use `key.text ?? input` for text insertion

When processing keyboard input, use `key.text ?? input` for inserted text.

Why:

- Silvery’s normalized `input` alone can flatten some shifted characters and punctuation.
- `key.text` preserves the actual typed character when available.

This was important to keep characters like `?` and other shifted punctuation working correctly in the composer and prompt dialogs.

Current examples:

- `nami/tui/src/components/Input.tsx`
- `nami/tui/src/components/TranscriptSearchPrompt.tsx`

## Practical Patterns In Nami

### Prompt area

The prompt composer is custom-rendered rather than delegated to a built-in text input.

Why this approach is currently used:

- The app wants explicit control over wrapped prompt rendering.
- The block cursor is rendered manually.
- Prompt markers and multi-line wrapping behavior are part of the UX.

Current example:

- `nami/tui/src/components/Input.tsx`

### Transcript item layout

Conversation rows are composed from small, explicit components instead of one generic message renderer.

Current examples:

- `nami/tui/src/components/messages/UserTextMessage.tsx`
- `nami/tui/src/components/messages/AssistantTextMessage.tsx`
- `nami/tui/src/components/messages/StreamingAssistantMessage.tsx`
- `nami/tui/src/components/MessageRow.tsx`

This makes it easier to change marker behavior, labels, metadata, and inline spinner placement without destabilizing the whole transcript surface.

### Scroll ownership matters

Tall panes and prompts should own their own scroll region.

What was learned:

- Parent overflow wrappers are not always enough when a single child is taller than the visible pane.
- Permission prompts and artifact review prompts behaved better when their internal component owned the scroll behavior.

This matters any time a lower panel can grow larger than the available viewport.

## Gotchas

### 1. `ThemeProvider` is the right place for theme selection

The theme should be applied by wrapping the app in `ThemeProvider`.

Do not try to pass theme data through `Screen`.

### 2. `presetTheme()` is safer than theme lookup APIs that return unions

In the Silvery type surface, some theme lookup helpers can return a union that is not typed as a concrete `Theme`.

What worked reliably:

- `presetTheme("nord")`

This avoids the `Theme | ColorPalette` typing problem that showed up during integration.

### 3. The registry package layout matters

The installed `silvery` package exposes built `dist` files, not source files.

So when checking types or behavior in the installed package:

- inspect `node_modules/silvery/dist/*.d.mts` for types
- inspect `node_modules/silvery/dist/*.mjs` for implementation details

Do not assume the same source tree layout as `reference/silvery`.

### 4. `ListView` height should come from layout context

`ListView` needs a concrete height. In fullscreen transcript regions, using layout measurements from Silvery is the correct approach.

Current example:

- `nami/tui/src/components/StreamOutput.tsx` uses `useBoxRect()` and derives `viewportHeight` from it.

### 5. Not every raw model status should be shown directly to users

Internal activity states like `Thinking` can be technically accurate but not always desirable in the composer chrome.

What `nami` currently does:

- Transcript internals can still distinguish thinking blocks.
- The spinner above the prompt normalizes that label to `Working`.

This keeps the prompt area simpler without removing detailed transcript state entirely.

## Good Defaults For Future TUI Work

When adding or namiging Silvery UI in `nami`, prefer this order of decisions:

1. Start from `Screen` and bounded layout regions.
2. Use `ListView` for long, interactive vertical surfaces.
3. Use semantic theme tokens instead of raw colors.
4. Use `Spinner` inline for lightweight activity.
5. Use `key.text ?? input` when the intent is text insertion.
6. Let the component that visually scrolls own the scroll behavior.

## Good Files To Read First

If you need to work on the TUI again, these are the most useful files to inspect first:

- `nami/tui/src/App.tsx`
- `nami/tui/src/index.tsx`
- `nami/tui/src/components/Input.tsx`
- `nami/tui/src/components/StreamOutput.tsx`
- `nami/tui/src/components/MessageRow.tsx`
- `nami/tui/src/components/messages/StreamingAssistantMessage.tsx`
- `nami/tui/node_modules/silvery/dist/index.d.mts`
- `reference/silvery/docs/api/spinner.md`
- `reference/silvery/docs/guides/theming.md`

## Summary

The biggest practical Silvery lessons in `nami` so far are:

- treat `Screen` as the fullscreen shell
- treat `ListView` as the transcript primitive
- use semantic theme tokens everywhere possible
- rely on `usePaste` and `key.text ?? input` for correct input behavior
- keep scroll ownership local to the pane that actually needs it
- prefer `presetTheme()` for stable theme setup in this repo
