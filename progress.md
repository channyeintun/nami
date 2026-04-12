# Fix Progress

Tracking fixes per plan.md.

---

## Task 1 — Create GitHub Copilot integration plan and tracker

**Files**: `plan.md`, `progress.md`

Created a new implementation plan for GitHub Copilot support in `gocode` before
making any code changes. The plan covers the minimal end-to-end path:

- persist Copilot credentials in config
- add a Go port of the device-code OAuth flow
- register a `github-copilot` provider preset
- inject Copilot-specific HTTP headers into the OpenAI-compatible client
- add a `/connect` slash command that logs in and switches to a Copilot model

Verification and execution constraints were recorded up front: no tests, format
after each completed task, and commit after each completed task.

Implementation completed in the same task:

- added persisted GitHub Copilot auth fields to `gocode/internal/config/config.go`
- added a new `gocode/internal/api/github_copilot.go` helper with:
  - device-code login start
  - device-code polling
  - Copilot token refresh
  - base URL derivation from the Copilot token
  - required Copilot static and dynamic headers
- registered a new `github-copilot` OpenAI-compatible provider preset
- updated the OpenAI-compatible client to send Copilot-specific headers only for
  the Copilot provider
- updated engine client creation to load, refresh, and persist Copilot
  credentials automatically
- added `/connect` in `gocode/cmd/gocode/slash_commands.go`
  - defaults to GitHub Copilot
  - supports optional enterprise domain via `/connect github-copilot <domain>`
  - prints the verification URL and device code into the TUI transcript
  - attempts to open the browser automatically
  - persists credentials and switches the active model to `github-copilot/gpt-4o`
- updated `/help` to show the new `/connect` command

Verification completed:

- ran `gofmt -w` on all changed Go files
- ran `go build ./...`
- ran `make release-local` in `gocode/tui`

## Task 2 — Set GitHub Copilot model defaults to GPT-5.4 and Haiku 4.5

**Files**: `gocode/internal/api/github_copilot.go`, `gocode/internal/config/config.go`, `gocode/internal/api/provider_config.go`, `gocode/cmd/gocode/slash_commands.go`, `gocode/cmd/gocode/subagent_runtime.go`, `progress.md`

Updated the GitHub Copilot integration so:

- the main Copilot model default is now `github-copilot/gpt-5.4`
- `/connect` persists `github-copilot/claude-haiku-4.5` as the subagent model
- Copilot subagents no longer inherit the main model; they resolve a dedicated
  child client using the saved subagent model

This keeps the primary interactive session on GPT-5.4 while routing subagents to
Claude Haiku 4.5 automatically when the active provider is GitHub Copilot.

## Task 3 — Include time, pwd, OS, and branch in environment prompt context

**Files**: `gocode/internal/agent/context_inject.go`, `progress.md`

Expanded the environment block injected into the system prompt so the model now
sees these fields explicitly on every turn:

- current time in RFC3339 format
- present working directory as `pwd`
- OS name
- architecture
- current git branch with a fallback when not on a branch

`gocode` already included git status, recent commits, and a working-directory
listing, so this change keeps the existing context and makes the high-signal
environment details explicit and easier for the model to use reliably.

## Task 4 — Document GitHub Copilot /connect usage in the README

**Files**: `README.md`, `gocode/README.md`, `progress.md`

Added a dedicated GitHub Copilot setup section to both user-facing README files.
The docs now explain:

- that GitHub Copilot uses `/connect` instead of a static API key
- what happens during the device-login flow
- how to use GitHub Enterprise with `/connect github-copilot <domain>`
- that the main model becomes `github-copilot/gpt-5.4`
- that the subagent model becomes `github-copilot/claude-haiku-4.5`
- that future launches can use the saved Copilot connection directly

The slash-command table was also updated to include `/connect`.

## Task 5 — Route GitHub Copilot models to the correct API protocol

**Files**: `gocode/internal/api/openai_responses.go`, `gocode/internal/api/anthropic.go`, `gocode/internal/api/github_copilot.go`, `gocode/internal/api/openai_compat.go`, `gocode/cmd/gocode/engine.go`, `progress.md`

Fixed the post-connect GitHub Copilot runtime failure where a normal prompt would
fail with `OpenAI-compatible request failed` even though `/connect` had already
completed successfully.

Root cause:

- `gocode` was sending every GitHub Copilot model through the OpenAI-compatible
  `/chat/completions` path
- Copilot does not use one protocol for every model family
- the selected main model `github-copilot/gpt-5.4` expects the OpenAI
  Responses API
- the selected subagent model `github-copilot/claude-haiku-4.5` expects the
  Anthropic Messages API with Copilot bearer-auth headers

Implementation completed:

- added a new `openai_responses.go` client that streams Copilot/OpenAI
  Responses events from `/responses`
- added Copilot model-family detection helpers in
  `gocode/internal/api/github_copilot.go`
- updated engine client creation so GitHub Copilot now routes by model:
  - GPT-5 and `o*` families use the Responses client
  - Claude families use the Anthropic Messages client
  - legacy Copilot-compatible models can still fall back to chat completions
- updated the Anthropic client so Copilot Claude models use bearer auth and
  Copilot headers instead of Anthropic API-key auth
- improved network-level provider errors so the underlying transport failure is
  included in the surfaced message instead of only showing a generic provider
  label

Verification completed:

- ran `gofmt -w` on all changed Go files
- ran `go build ./...`
- ran `make release-local` in `gocode/tui`

## Task 6 — Fix GitHub Copilot business host derivation

**Files**: `gocode/internal/api/github_copilot.go`, `progress.md`

Fixed a GitHub Copilot enterprise/base-URL bug exposed by business accounts.

Root cause:

- Copilot access tokens include a `proxy-ep=` field such as
  `proxy.business.githubcopilot.com`
- our base-URL derivation removed the `proxy.` prefix entirely and produced
  `https://business.githubcopilot.com`
- that host does not exist, so Responses requests failed with DNS lookup errors

Implementation completed:

- updated `GetGitHubCopilotBaseURL` to convert `proxy.` to `api.` instead of
  trimming it away
- this now resolves business Copilot tokens to hosts like
  `https://api.business.githubcopilot.com`, matching the reference

Verification completed:

- ran `gofmt -w` on the changed Go file
- ran `go build ./...`
- ran `make release-local` in `gocode/tui`

## Task 7 — Sanitize OpenAI-style tool schemas for alias-based command tools

**Files**: `gocode/internal/api/openai_responses.go`, `gocode/internal/api/openai_compat.go`, `progress.md`

Fixed a tool-registration failure exposed by the `send_command_input` command
tool when running on GitHub Copilot's OpenAI Responses path.

Root cause:

- several runtime tools use top-level `anyOf` or `allOf` in their JSON Schema to
  express camelCase and snake_case aliases
- the OpenAI Responses tool validator rejects top-level combinators and expects
  a plain object schema at the root
- `send_command_input` hit this first because its schema requires both command
  id and input aliases through a top-level `allOf`

Implementation completed:

- updated the OpenAI Responses tool builder to sanitize tool schemas before
  sending them to the provider
- applied the same sanitization to the OpenAI-compatible chat-completions tool
  builder so the same schema shape does not break other OpenAI-style providers
- reused the existing alias-flattening sanitizer that already converts top-level
  `anyOf` and `allOf` alias patterns into a provider-safe object schema with
  concrete required fields

Verification completed:

- ran `gofmt -w` on the changed Go files
- ran `go build ./...`
- ran `make release-local` in `gocode/tui`

## Task 8 — Add a dedicated OpenAI-style tool schema guard

**Files**: `gocode/internal/api/openai_schema.go`, `gocode/internal/api/openai_responses.go`, `gocode/internal/api/openai_compat.go`, `progress.md`

Tightened the OpenAI/Copilot tool-schema path so provider compatibility no
longer depends on reusing the Gemini schema sanitizer by accident.

Implementation completed:

- added a dedicated `sanitizeOpenAIToolSchema` helper for OpenAI-style function
  calling endpoints
- the helper now enforces a top-level object schema and strips unsupported root
  combinators such as `oneOf`, `anyOf`, `allOf`, `not`, and `enum`
- kept the alias-flattening behavior by sanitizing first and then applying the
  OpenAI-specific root-schema contract
- updated both OpenAI Responses and OpenAI-compatible chat tool builders to use
  the dedicated sanitizer instead of the Gemini-named helper directly

Verification completed:

- ran `gofmt -w` on the changed Go files
- ran `go build ./...`
- ran `make release-local` in `gocode/tui`

## Task 9 — Add GPT-5.4 reasoning-effort selection

**Files**: `gocode/internal/config/config.go`, `gocode/internal/api/client.go`, `gocode/internal/api/openai_reasoning.go`, `gocode/internal/agent/query_stream.go`, `gocode/internal/agent/loop.go`, `gocode/internal/api/openai_responses.go`, `gocode/cmd/gocode/engine.go`, `gocode/cmd/gocode/subagent_runtime.go`, `gocode/cmd/gocode/slash_commands.go`, `progress.md`

Added a user-selectable reasoning-effort setting for OpenAI Responses models so
GitHub Copilot GPT-5.4 can be used with the same low / medium / high / xhigh
effort levels exposed in VS Code.

Implementation completed:

- added persisted `reasoning_effort` config support plus the
  `GOCODE_REASONING_EFFORT` environment override
- added model-aware OpenAI reasoning helpers that:
  - validate `low`, `medium`, `high`, and `xhigh`
  - clamp `xhigh` down to `high` on models that do not support it
  - default supported OpenAI reasoning models to `medium`
- threaded the configured reasoning effort through the query request path
- updated the OpenAI Responses client to send `reasoning.effort` and automatic
  reasoning summaries on supported models
- preserved the existing `ultrathink` prompt behavior by raising the current
  turn's effort on supported Responses models instead of dropping the request
- added `/reasoning [low|medium|high|xhigh|default]`
- updated `/status` to display the active reasoning level
- updated `/connect` so GitHub Copilot initializes with `medium` reasoning if
  no explicit setting has been saved yet

Verification completed:

- ran `gofmt -w` on the changed Go files
- ran `go build ./...`
- ran `make release-local` in `gocode/tui`

## Task 10 — Fix Responses turns that end with thinking-only output

**Files**: `gocode/internal/api/openai_responses.go`, `gocode/tui/src/hooks/useEvents.ts`, `progress.md`

Fixed a Copilot GPT-5.4 review/runtime issue where the UI could appear to stop
after showing only `Thinking` text following tool use.

Root cause:

- the OpenAI Responses adapter was streaming reasoning-summary text into the TUI
  correctly
- but it did not recover assistant message content from
  `response.output_item.done` when the item type was `message`
- if Copilot delivered the final answer only at that stage, the turn completed
  with reasoning visible but no assistant text deltas emitted
- the TUI then preserved a thinking-only assistant message, which looked like a
  stalled or truncated response

Implementation completed:

- updated the Responses stream parser to:
  - track streamed text deltas
  - recover final assistant message content from `response.output_item.done`
  - emit only any missing text suffix when the provider sends the full message
    at completion time
  - mark reasoning-summary output correctly
- updated the TUI completion logic so a thinking-only stream is not treated as a
  normal final assistant answer; it now falls back to the empty-response marker
  unless real text was produced

Verification completed:

- ran `gofmt -w` on the changed Go file
- ran `go build ./...`
- ran `bunx tsc` in `gocode/tui`
- ran `make release-local` in `gocode/tui`

## Task 11 — Handle Responses incomplete completions

**Files**: `gocode/internal/api/openai_responses.go`, `progress.md`

Fixed another OpenAI Responses termination path that could still surface as
`(Model returned an empty response)` during review-style prompts.

Root cause:

- the Responses adapter handled `response.completed`
- but it ignored `response.incomplete`, which the reference implementations also
  treat as a terminal event
- when Copilot ended a turn with `response.incomplete`, `gocode` reached EOF
  without emitting a model stop event
- the agent loop then normalized the empty stop reason to `end_turn`, and the
  TUI rendered the empty-response fallback

Implementation completed:

- updated the Responses event handler to treat `response.incomplete` as a normal
  terminal event alongside `response.completed`
- this ensures usage and stop-reason handling still run and prevents the empty
  synthetic assistant message caused by a missing terminal event

Verification completed:

- ran `gofmt -w` on the changed Go file
- ran `go build ./...`
- ran `make release-local` in `gocode/tui`

---

## Task 12 — Align GitHub Copilot login and capability discovery with the references

**Files**: `plan.md`, `gocode/internal/api/github_copilot.go`, `gocode/internal/api/client.go`, `gocode/cmd/gocode/engine.go`, `gocode/cmd/gocode/slash_commands.go`, `progress.md`

Switched this phase from incremental Copilot bug fixes to a reference-alignment
pass against `opencode` and `pi-mono`, focusing on the remaining provider-level
 behaviors that were still missing from `gocode`.

Implementation completed:

- updated `plan.md` so the active work reflects the broader GitHub Copilot
  parity pass rather than the earlier minimal `/connect` scope
- improved GitHub Copilot device-flow polling to track the reference-style
  interval multipliers and surface a clearer timeout when repeated `slow_down`
  responses suggest clock drift
- added GitHub Copilot model-policy enablement helpers and updated `/connect`
  to best-effort enable the connected Copilot models after login instead of
  stopping at raw token acquisition
- added runtime GitHub Copilot `/models` discovery with a short timeout and a
  24-hour in-memory cache so model metadata can be reused without hitting the
  endpoint on every client creation
- added dynamic Copilot capability resolution so the engine now derives tool
  use, vision, reasoning support, JSON mode, and token limits from Copilot's
  model metadata when available
- wrapped Copilot clients with the resolved capabilities so both the main
  session and Copilot subagents use the same discovered behavior without
  changing the rest of the query loop

Verification completed:

- ran `gofmt -w` on the changed Go files
- ran `go build ./...`
- ran `make release-local` in `gocode/tui`

## Task 13 — Fix Responses parser missing terminal events and text recovery

**Files**: `gocode/internal/api/openai_responses.go`, `progress.md`

Fixed the `(Model returned an empty response)` failure that occurred when asking
Copilot GPT-5.4 to review commits or other prompts where the final answer was
produced but never surfaced to the UI.

Root causes (three separate gaps in the Responses stream parser):

1. **Missing `response.done` terminal event** — the Codex/Responses API can send
   `response.done` as an alias for `response.completed`. Pi-mono's codex
   provider handles it; gocode silently dropped it via `default: return nil`,
   so no `ModelEventStop` was ever emitted for those turns.

2. **Missing `response.output_text.done` handler** — this event carries the
   final full text of a text output part, sent after all delta events. If deltas
   were incomplete or skipped, this was the second chance to capture the text.
   gocode dropped it silently.

3. **No last-resort text recovery from the `response.completed` body** — the
   completed event includes the full `response.output` array with all message
   content. If all earlier text events were missed, this was the final fallback.
   gocode only extracted `status` and `usage`, ignoring the output entirely.

Implementation completed:

- added a `sawContentText` tracking flag to the stream state so the terminal
  handler knows whether any real content text was emitted during the stream
- set the flag in all existing text-emission paths: `output_text.delta`,
  `refusal.delta`, and `emitMessageSuffix`
- added `response.output_text.done` handler that emits any text not already
  covered by delta events
- added `response.done` to the terminal event case alongside
  `response.completed` and `response.incomplete`
- expanded the `openAIResponsesCompletedEvent` struct to include the response
  `Output` array
- added last-resort text extraction in the terminal handler: when no content
  text was emitted and no tool calls were seen, scan the response output for
  message items and emit their text before the stop event

Verification completed:

- ran `gofmt -w` on the changed Go file
- ran `go build ./...`
- ran `make release-local` in `gocode/tui`

## Task 14 — Guarantee stop event and show thinking-only turns

**Files**: `gocode/internal/api/openai_responses.go`, `gocode/tui/src/hooks/useEvents.ts`, `progress.md`

Fixed a persistent `(Model returned an empty response)` failure on Copilot
GPT-5.4 review prompts even after the earlier terminal-event fix.

Root causes (three remaining gaps):

1. **No stop event on clean stream EOF** — if the SSE stream closed without a
   terminal event (`response.completed`, `response.done`), no `ModelEventStop`
   was ever emitted. The loop finished silently with whatever was accumulated,
   and the turn completed with an empty result.

2. **Thinking-only turns discarded by the TUI** — `assistantBlocksHaveText`
   required `kind === "text"`. If the model produced only reasoning/thinking
   content (common with GPT-5.4 on review tasks), those blocks had
   `kind: "thinking"` and were replaced with the empty-response placeholder.

3. **`[DONE]` sentinel not handled** — if Copilot sends the standard OpenAI
   `[DONE]` SSE sentinel, the JSON parser would crash and propagate an error.

Implementation completed:

- added a safety net at the end of the Stream iterator: if `readSSE` returns
  without error and no stop was emitted, emit a synthetic `end_turn` stop so
  the agent loop always receives a proper termination
- handled the `[DONE]` sentinel by emitting a stop event instead of trying to
  parse it as JSON
- updated the TUI turn_complete handler so when no text blocks exist but
  thinking blocks do, the thinking blocks are preserved and shown instead of
  being replaced with the empty-response placeholder

Verification completed:

- ran `gofmt -w` on the changed Go file
- ran `go build ./...`
- ran `make release-local` in `gocode/tui`

---

## Task 15 — AOP Debugger monitoring tool

**Files**: `gocode/internal/debuglog/logger.go`, `gocode/internal/debuglog/sse_reader.go`, `gocode/internal/debuglog/goroutine.go`, `gocode/internal/debuglog/bridge_proxy.go`, `gocode/internal/api/sse_debug.go`, `gocode/cmd/gocode/debug_proxy.go`, `gocode/cmd/gocode/engine.go`, `gocode/internal/api/openai_responses.go`, `gocode/internal/api/anthropic.go`, `gocode/internal/api/gemini.go`, `progress.md`

Implemented a zero-source-change runtime debugger activated by `GOCODE_DEBUG=1`.
All logs go to a JSONL `debug.log` file in the session directory.

Components:

- **debuglog/logger.go** — core JSONL writer with timestamp, goroutine ID,
  category, event name, arbitrary fields. 50 MB cap. Conditional on `Enabled`.
- **debuglog/sse_reader.go** — `SSEReaderProxy` wrapping `io.Reader` to capture
  raw SSE bytes from all provider streams.
- **debuglog/goroutine.go** — `LogGoroutineCount()` and `LogGoroutineSnapshot()`
  using `runtime.Stack` and `runtime.NumGoroutine()`.
- **debuglog/bridge_proxy.go** — `IPCWriter` and `IPCReader` wrapping the
  Bridge's stdin/stdout to capture all inbound/outbound NDJSON IPC traffic.
- **api/sse_debug.go** — `sseBodyWithDebug()` helper that wraps resp.Body when
  debug is enabled.
- **cmd/gocode/debug_proxy.go** — `debugClientProxy` wrapping `api.LLMClient`
  to log every `Stream()` request, every `ModelEvent` yielded, and warmup calls.

Wiring:

- `engine.go` checks `GOCODE_DEBUG` env, wraps stdin/stdout with IPC loggers,
  initialises debug log in the session directory, wraps client with proxy, and
  defers `debuglog.Close()`.
- All three SSE-consuming providers (openai_responses, anthropic, gemini) wrap
  `resp.Body` through `sseBodyWithDebug()` before passing to `readSSE`.

Verification: `go build ./...` passes, binary installed.

---

## Task 16 — Fix premature loop exit on tool-use turns + debug double-logging

**Files**: `gocode/internal/agent/token_budget.go`, `gocode/internal/agent/loop.go`, `gocode/cmd/gocode/engine.go`, `progress.md`

Diagnosed via `GOCODE_DEBUG=1` debug log from a GPT-5.4 code review prompt that
produced only thinking + tool_use across 3 turns with no text output.

Root causes (two bugs):

1. **Continuation tracker killed the loop prematurely** — `ContinuationTracker.Record()`
   counted tool-use turns toward the diminishing returns heuristic. Since tool
   turns have low output tokens (225, 398, 374 — all under 500), after 3
   continuations the tracker triggered "diminishing returns" and stopped the
   loop before the model could produce its final text response.
   
   Fix: `Record()` now takes `isToolTurn bool`. Tool turns still count toward
   the overall budget (for budget exhaustion), but are excluded from the
   diminishing returns window (`ContinuationCount` and `RecentTokenDeltas`).

2. **Debug proxy double-wrapping** — `ensureClientForSelection` returns the
   existing (already-proxied) client when no model switch is needed, and the
   engine re-wrapped it with a second `newDebugClientProxy`, causing every event
   to be logged twice.
   
   Fix: only wrap when `resolvedClient != client` (i.e., a new client was
   actually created).

Verification: `go build ./...` passes, binary installed.

---

## Task 17 — Hide diff-preview artifact + fix debug proxy after slash commands

**Files**: `gocode/tui/src/App.tsx`, `gocode/cmd/gocode/slash_commands.go`, `progress.md`

Two fixes based on code review feedback from the model:

1. **Diff-preview artifact shown permanently** — `selectRecentArtifacts` in
   `App.tsx` filtered out `implementation-plan` and `tool-log` but kept
   `diff-preview` artifacts. When the model ran `git diff` during a code review,
   the diff-preview artifact occupied one of the 2 visible artifact slots
   permanently, providing no user value.
   
   Fix: exclude `diff-preview` from `selectRecentArtifacts`.

2. **Debug proxy lost after `/model` and `/connect`** — slash commands assigned
   `*client = nextClient` with a raw (unwrapped) client. After this,
   `ensureClientForSelection` returned the same non-nil client unchanged, and
   the engine guard (`resolvedClient != client`) never triggered re-wrapping.
   All subsequent model traffic was unlogged under `GOCODE_DEBUG=1`.
   
   Fix: wrap `nextClient` with `newDebugClientProxy` in both `/connect` and
   `/model` paths before assigning to `*client`.

Verification: `go build ./...` passes, TUI rebuilt, both binaries installed.

---

## Task 18 — Format footer timings in human units

**Files**: `gocode/tui/src/components/PromptFooter.tsx`, `progress.md`

Updated the prompt footer timing labels so long durations are shown in familiar
human-readable units instead of raw seconds.

Examples:

- `243s` now shows as `4m 3s`
- `197s` now shows as `3m 17s`
- sub-second timings still show as `ms`
- short timings under one minute still show as whole `s`

Implementation completed:

- updated `formatLatencyMs()` in `PromptFooter.tsx`
- durations now render as `ms`, `s`, `m s`, or `h m` depending on length
- removed decimal-second formatting for long waits because it is harder to scan

Verification completed:

- ran `go build ./...`
- ran `bun run build` in `gocode/tui`
- ran `make release-local` in `gocode/tui`
- reinstalled `gocode` and `gocode-engine` to `~/.local/bin`