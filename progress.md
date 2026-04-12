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
