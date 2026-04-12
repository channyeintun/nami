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
