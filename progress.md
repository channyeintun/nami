# Fix Progress

Tracking fixes per plan.md.

---

## Task 26 — Use `parametersJsonSchema` for Gemini tool declarations

**File**: `gocode/internal/api/gemini.go`

Renamed `geminiFunctionDeclaration.Parameters` → `ParametersJsonSchema` with tag
`json:"parametersJsonSchema,omitempty"`. This makes Gemini accept full JSON Schema
(anyOf, oneOf, const, \$defs) for tool input schemas instead of the restricted
OpenAPI 3.03 `parameters` field.

---

## Task 27 — Merge consecutive tool result turns into one user Content

**File**: `gocode/internal/api/gemini.go`

Added `geminiContentIsOnlyFunctionResponses` helper and modified
`buildGeminiContents` to merge adjacent user turns that contain only
`functionResponse` parts. Parallel tool calls now emit a single
`{role:"user", parts:[fr1, fr2, fr3]}` instead of three separate user turns.
Gemini requires all function responses for a turn to be in one Content entry.

---

## Task 28 — Use `"error"` key for error tool results

**File**: `gocode/internal/api/gemini.go`

`geminiFunctionResponsePart` now uses `response["error"]` when `result.IsError`,
instead of `response["output"] + "is_error": true`. This matches the Gemini API
convention (`{output: text}` for success, `{error: text}` for errors) used by
pi-mono and the official SDK documentation.

---

## Task 29 — Inject synthetic error results for orphaned tool calls

**File**: `gocode/internal/api/gemini.go`

`buildGeminiContents` now tracks pending tool calls from each model turn. When a
new model turn or a user text turn arrives before all tool call IDs have been
answered by `ToolResult` messages, synthetic `{error: "No result provided"}`
function response parts are injected into the history. This prevents Gemini from
receiving a malformed conversation where a `functionCall` has no following
`functionResponse`.

---

## Task 30 — Scope thought signature sentinel to Gemini 3+ models

**File**: `gocode/internal/api/gemini.go`

Added `geminiMajorVersion(modelID string) int` helper (parses `gemini[-live]-<N>`
prefix). `ensureGeminiActiveLoopThoughtSignatures` now takes a `modelID` parameter
and only injects `"skip_thought_signature_validator"` when the major version is >= 3.
On Gemini 2.x, thought signatures are optional so the sentinel is suppressed.

---

## Task 31 — Parse Retry-After / X-RateLimit-Reset headers

**Files**: `gocode/internal/api/gemini.go`, `gocode/internal/api/retry.go`

Added `geminiRetryAfterDelay(resp, body)` that checks `Retry-After`,
`X-RateLimit-Reset`, `X-RateLimit-Reset-After` headers and body patterns
(`"Please retry in Xs"`, `"retryDelay":"Xs"`) in priority order.
Added `RetryAfter time.Duration` field to `APIError`; `RetryWithBackoff` now uses
it when set. Delays exceeding `geminiMaxRetryAfter` (60 s) cause immediate failure
rather than a long wait.

---

## Task 32 — Retry on empty SSE stream

**File**: `gocode/internal/api/gemini.go`

`Stream` now retries up to `geminiMaxEmptyRetries = 2` times when Gemini returns
a 200 OK with an empty SSE body (zero events). Backoff starts at 500 ms and
doubles per attempt. If all retries are exhausted with an empty body, an
`ErrOverloaded` error is propagated instead of silently yielding nothing.

---

## Task 33 — Rebuild and reinstall

Ran `make release-local` in `gocode/tui`, then installed both binaries:

- `~/.local/bin/gocode` (TUI launcher)
- `~/.local/bin/gocode-engine` (Go backend)

`gocode --help` confirms the updated binary resolves correctly.

---

## Task 34 — Actionable error messages for plan-mode write blocks

**File**: `gocode/internal/agent/planner.go`

Improved the two error messages returned by `Planner.ValidateTool` when a write
tool is blocked in plan mode:

- **No plan yet**: now explicitly says _"call save_implementation_plan with a
  complete implementation plan before modifying any files"_ instead of the vague
  "finish the implementation plan before modifying files".
- **Plan saved but awaiting review**: now says _"awaiting user review — do not
  call write tools until the user approves the plan and the mode switches to
  fast"_ so the model knows not to retry.

These messages are returned as `isError: true` tool results that the model reads
directly, so making them prescriptive prevents the model from looping.

---

## Task 35 — Fix hang after plan approval + remove mandatory write gate

**Files**: `gocode/cmd/gocode/engine.go`, `gocode/internal/agent/modes.go`

### Bug 1: hang after plan approval

After the user approved an implementation plan, the engine called
`persistCurrentMessages()` and then hit `break` — returning to idle and waiting
for new user input. Nothing ran.

**Fix**: when `reviewResult.Decision == "approved"`, inject a user message
`"Plan approved. Implement it now."` and `continue` the inner loop so the agent
immediately runs a fast-mode execution turn.

### Bug 2: plan mode blocking writes on trivial tasks

`RequirePlanBeforeWrite: true` in the plan-mode profile caused `Planner.ValidateTool`
to block **all** write tools until a formal `save_implementation_plan` artifact
existed — even for a one-liner like "create hello.txt with Hello world!".

**Fix**: set `RequirePlanBeforeWrite: false`. Plan mode now means "think more
carefully" (slower model, full verbosity, plan panel visible) without gating every
write behind a mandatory plan artifact. `save_implementation_plan` remains
available for complex multi-step tasks where the user genuinely wants to review
before execution.

---

## Task 36 — Emit turn_complete when QueryStream exits due to continuation/turn limits

**File**: `gocode/internal/agent/query_stream.go`

**Root cause**: `ContinuationTracker`'s "diminishing returns" check fires after 3+
model turns where each turn's `CandidatesTokenCount` is < 500 tokens. With Gemini
models, each individual tool-call turn (ReadFile, EditFile, Bash, etc.) produces
very few output tokens (just the function call declaration), so 3 sequential tool
calls are enough to trigger `ShouldStop = true`. The `QueryStream` loop then exits
without `state.StopRequested = true`, meaning no `turn_complete` event is ever
emitted. The TUI receives no `turn_complete` so `isStreaming` stays `true` and the
"⠙ Working" spinner never stops.

**Fix**: After the `for state.ShouldContinue()` loop exits, check `state.StopRequested`.
If it's `false` (loop ended due to continuation budget / max-turn limit rather than
a clean `turn_complete` from `runIteration`), emit a `turn_complete` event with
`stop_reason: "stop"` so the TUI transitions from "Working" to idle.

---
