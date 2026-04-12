# Gocode Improvement Plan (pi-mono Reference)

Derived from studying `pi-mono/packages/ai/src/providers/google-shared.ts`,
`google.ts`, `google-gemini-cli.ts`, and `transform-messages.ts`.

---

## Task 26 — Use `parametersJsonSchema` for Gemini tool declarations

**File**: `gocode/internal/api/gemini.go`

**Problem**: `geminiFunctionDeclaration.Parameters` serialises as `"parameters"`, which maps to the legacy OpenAPI 3.03 Schema field. That field does not support `anyOf`, `oneOf`, `const`, `$defs`, or full `$schema` metadata. The correct field for native Gemini models is `parametersJsonSchema`, which accepts a full JSON Schema.

**Fix**: Rename the struct field to `ParametersJsonSchema` with tag `json:"parametersJsonSchema,omitempty"`. Sanitisation can be simplified because Gemini accepts full JSON Schema natively when `parametersJsonSchema` is used.

---

## Task 27 — Merge consecutive tool result turns into one user Content

**File**: `gocode/internal/api/gemini.go`

**Problem**: When multiple tool calls are executed in parallel, each `RoleTool` message is converted to a separate `geminiContent{role:"user"}`. Gemini requires all function responses belonging to one model turn to be in a **single** user `Content` (multiple `functionResponse` parts in the same `parts` array). Sending them in separate turns triggers a validation error.

**Fix**: In `buildGeminiContents`, after computing a `functionResponse` part, check whether `contents[len-1]` already has `role:"user"` and its parts contain only `functionResponse` items. If so, append to that content instead of pushing a new one. This mirrors pi-mono's merge logic:

```ts
const lastContent = contents[contents.length - 1];
if (
  lastContent?.role === "user" &&
  lastContent.parts?.some((p) => p.functionResponse)
) {
  lastContent.parts.push(functionResponsePart);
} else {
  contents.push({ role: "user", parts: [functionResponsePart] });
}
```

---

## Task 28 — Use `"error"` key for error tool results

**File**: `gocode/internal/api/gemini.go`

**Problem**: `geminiFunctionResponsePart` puts `"is_error": true` alongside `"output"` in the response map for error results. The Gemini-native convention (from SDK docs and pi-mono) is to use the **key** `"error"` for error results and `"output"` for success results, with no extra `is_error` boolean.

**Fix**: In `geminiFunctionResponsePart`, when `result.IsError`, write `response["error"] = textResult` (omit `is_error`).

---

## Task 29 — Inject synthetic error results for orphaned tool calls

**File**: `gocode/internal/api/gemini.go`

**Problem**: If an agent turn is interrupted (error, abort, or crash) after a model emits tool calls but before all results are returned, the history will have a model turn with `functionCall` parts followed immediately by another model turn or a new user message. Gemini rejects this as an invalid conversation structure.

**Fix**: In `buildGeminiContents`, track which tool calls each model turn emits. When a new model turn or non-result user turn is encountered, inject synthetic `functionResponse` parts (`{error: "No result provided"}`) for any unmatched tool call IDs. Port the pattern from pi-mono's `transformMessages` pass-2 orphan recovery.

---

## Task 30 — Scope thought signature sentinel to Gemini 3+ models

**File**: `gocode/internal/api/gemini.go`

**Problem**: `ensureGeminiActiveLoopThoughtSignatures` injects `"skip_thought_signature_validator"` on every function call part that lacks a real signature — regardless of model version. This sentinel is only meaningful (and needed) for **Gemini 3+**, where the thought signature is mandatory. For Gemini 2.x, injecting it is unnecessary noise and might cause unexpected behaviour on future API revisions.

**Fix**: Add a `geminiMajorVersion(modelID string) int` helper (parses `gemini-(\d+)` prefix). Pass `modelID` into `ensureGeminiActiveLoopThoughtSignatures` and only apply the sentinel when `major >= 3`.

---

## Task 31 — Parse Retry-After / X-RateLimit-Reset headers

**File**: `gocode/internal/api/gemini.go` (and optionally `retry.go`)

**Problem**: When Gemini returns 429, the current retry logic ignores `Retry-After` and `X-RateLimit-Reset` headers and falls back to fixed exponential backoff. This can be both too slow (wait 16 s when the server says 1 s) and too fast (exhaust retries when the server says wait 60 s).

**Fix**: In `openStream`'s retry closure, after reading a non-2xx body:

1. Check `Retry-After` header (seconds or HTTP date).
2. Fall back to `X-RateLimit-Reset` (unix timestamp seconds).
3. Fall back to `X-RateLimit-Reset-After` (seconds).
4. Scan body text for patterns like `"Please retry in Xs"` or `"retryDelay":"34.074s"`.

If a server delay is found, use it as the sleep duration (capped at `maxRetryDelay` = 60 s). If the server delay exceeds the cap, fail immediately rather than waiting.

---

## Task 32 — Retry on empty SSE stream

**File**: `gocode/internal/api/gemini.go`

**Problem**: Gemini occasionally returns a 200 OK HTTP response with an empty SSE body (no `data:` lines). The current code silently emits a stop event with an empty stop reason, which surfaces as an empty model response to the user.

**Fix**: In `Stream`, count the total events received. If the stream closes with zero events, retry the request (up to `maxEmptyRetries = 2`) with short backoff (500 ms, 1 s) before propagating the empty-response error. Mirror pi-mono's `MAX_EMPTY_STREAM_RETRIES = 2` pattern.

---

## Task 33 — Rebuild and reinstall

**Commands**:

```
cd gocode && gofmt -w ./... && go build ./...
cd tui && make release-local
install -m 755 release/gocode ~/.local/bin/gocode
install -m 755 release/gocode-engine ~/.local/bin/gocode-engine
```

---

## Summary table

| Task | File(s)         | Type                                  |
| ---- | --------------- | ------------------------------------- |
| 26   | `api/gemini.go` | Bug fix — wrong schema field          |
| 27   | `api/gemini.go` | Bug fix — tool result merging         |
| 28   | `api/gemini.go` | Bug fix — error key in response       |
| 29   | `api/gemini.go` | Robustness — orphan injection         |
| 30   | `api/gemini.go` | Correctness — version-scoped sentinel |
| 31   | `api/gemini.go` | Robustness — server retry delay       |
| 32   | `api/gemini.go` | Robustness — empty stream retry       |
| 33   | build / install | Build — reinstall binary              |
