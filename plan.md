# Provider & Model Architecture Plan

## Current State

Chan stores models internally as `provider/model` (e.g. `anthropic/claude-sonnet-4-20250514`).
This format flows through config, session persistence, IPC events, and the engine runtime.
The TUI strips the provider prefix before display, so users never see it.

`/model` now rejects `provider/` prefixes and only accepts bare model names.
The engine infers the provider from the model name via substring matching in `resolveModelSelection()`.

GitHub Copilot is the default provider. `/connect` writes `github-copilot/<model>` into config and sets up OAuth credentials. Copilot routes Claude-family models to the Anthropic Messages API and GPT-family models to the OpenAI Responses API.

## Reference Patterns

**OpenCode**: Uses `providerID/modelID` in config. Providers are first-class objects that own their models. Provider is explicit in config but the model picker groups by provider and the user picks a model. Models carry a `providerID` back-reference. Model metadata (capabilities, pricing, limits) comes from a central registry (`models.dev`).

**VSCode Copilot Chat**: All models come through Copilot's proxy API. Provider is invisible. Users see model names categorized by tier (Standard/Premium/Custom). A `family` string drives capability branching. An "Auto" pseudo-model delegates to the best backend.

## Design Principles

1. **Users pick models, not providers.** The `/model` picker shows model names. Provider is an implementation detail.
2. **Provider is determined by context.** When connected to GitHub Copilot, all models route through Copilot. Otherwise, provider is inferred from the model name or set via config/env.
3. **Config stays `provider/model`.** The engine needs unambiguous routing. Config and session state keep the full `provider/model` format. This is never shown to users.
4. **Display is model-only.** Status bar, model picker, session list, and all user-facing text show only the model name.

## Current Issues

### 1. Provider inference is fragile
`resolveModelSelection()` uses substring matching — `strings.Contains(lower, "claude")` routes to anthropic.

### 2. `/connect` overwrites global config
Running `/connect` writes the provider into `config.json` permanently, changing the default model even for future sessions. This is intentional but worth noting.

### 3. Subagent model is GitHub-Copilot-only
`cfg.SubagentModel` is only used when the parent provider is `github-copilot`. For all other providers, subagents reuse the parent client.

## Plan

### Phase 1: Clean up display layer (TUI-only)

**Goal**: Single source of truth for stripping provider prefixes in the TUI.

- [x] Extract `stripProviderPrefix()` into a shared utility (e.g. `utils/formatModel.ts`)
- [x] Use it in `StatusBar.tsx`, `ModelSelectionPrompt.tsx`, `ResumeSelectionPrompt.tsx`, `StreamingAssistantMessage.tsx`, and `AssistantTextMessage.tsx`
- [x] Remove the duplicate implementations

### Phase 2: Strengthen provider inference (Go engine)

**Goal**: Curated presets carry their own provider hint so inference isn't solely substring-based.

- [x] Add a `Provider` field to `modelSelectionPreset` struct
- [x] Populate it for each curated preset:
  - `claude-sonnet-4.6` → `anthropic`
  - `claude-opus-4.6` → `anthropic`
  - `claude-haiku-4.5` → `anthropic`
  - `gpt-5.4` → `openai`
  - `gpt-5.4-mini` → `openai`
  - `gemini-3.0-flash` → `gemini`
  - `gemini-3.1-pro` → `gemini`
- [x] When the user selects a curated preset, pass the provider hint through the IPC response so the engine doesn't need to re-infer it
- [x] For custom model input, keep `resolveModelSelection()` as the fallback inference

### Phase 3: Respect active provider context

**Goal**: When connected to GitHub Copilot, model names route through Copilot unless overridden.

The current behavior is mostly correct because:
- `/connect` sets `cfg.Model` to `github-copilot/<model>`
- `resolveModelSelection()` checks if the current provider is `github-copilot` and inherits it
- `newLLMClient("github-copilot", model, cfg)` routes Claude→Anthropic API, GPT→OpenAI Responses API through Copilot's proxy

No changes needed for `/connect` flow. The provider context already propagates naturally.

For **non-Copilot users** (raw API keys), the inference chain is:
1. Curated preset provider hint (Phase 2) — most reliable
2. `resolveModelSelection()` substring matching — fallback for custom input
3. Current provider fallback — if no pattern matches

This ordering is correct and doesn't need restructuring.

### Phase 4: Protocol cleanup (optional, low priority)

**Goal**: Reduce `provider/model` transit through IPC where it's unnecessary.

- [ ] `ModelChangedPayload.Model` currently sends `provider/model` — TUI always strips it. Could send model-only, but the TUI also uses the full string for context window inference. **Keep as-is** unless context window logic moves server-side.
- [x] `ModelSelectionRequestedPayload.CurrentModel` sends `provider/model` — only used for display (immediately stripped). Could strip server-side. Low priority.

### Phase 5: `/subagent` model selection

**Goal**: Let users interactively switch the subagent model mid-session, using the same picker as `/model`.

- Default subagent model is `claude-haiku-4.5`
- [x] Add `/subagent` slash command that opens the same model selection list as `/model`
- [x] When the user picks a model, persist it as session-scoped subagent state
- [x] Display current subagent model in the `/subagent` help text
- [x] Subagent model resets to `claude-haiku-4.5` on new sessions unless overridden in config

### Out of scope

- **`models.dev` integration**: OpenCode fetches model metadata from a central registry. Useful long-term but not needed now — Chan's curated list is small and hand-maintained.
- **Auto model / model routing**: VSCode Copilot's "Auto" model that picks the best backend. Not needed for Chan's direct model selection.
- **Provider management UI**: OpenCode has a "Connect Provider" dialog. Chan uses `/connect` for Copilot and env vars for everything else. No change needed.

## Summary

The architecture is sound: `provider/model` internally, model-only user-facing. The main feature gaps are closed. Phase 1 removed duplicate display utils, Phase 2 added provider hints for curated presets, Phase 3 confirmed the existing `/connect` flow is already correct, and Phase 5 added `/subagent` selection using the same model list as `/model`, with `claude-haiku-4.5` as the default. Phase 4 is mostly optional polish; the display-only `CurrentModel` path is now cleaned up, while `ModelChanged` intentionally remains unchanged.
