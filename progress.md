# Fix Progress

Tracking fixes per plan.md.

---

## Phase 1: Critical Bugs

### Task 1 — Fix Gemini `FunctionResponse.Name` ✅

- **File:** `internal/api/gemini.go`
- Built a `toolCallID → toolName` map in `buildGeminiContents` by scanning all
  assistant messages, passed it through `convertGeminiMessage` and
  `geminiFunctionResponsePart` so Gemini receives the actual function name
  instead of the opaque call ID.

### Task 2 — Fix Race Condition in Title Generation Goroutine ✅

- **Files:** `internal/api/client.go`, `cmd/gocode/main.go`
- Added `api.DeepCopyMessages()` that deep-copies `ToolCalls`, `Images`, and
  `ToolResult` for each message.
- Title goroutine now calls `api.DeepCopyMessages(messages)` instead of a
  shallow `append` slice copy.

### Task 3 — Fix Race Condition on `globalFileHistory` ✅

- **File:** `internal/tools/file_history.go`
- Converted the bare `*FileHistory` global into an anonymous struct protected
  by `sync.RWMutex`; `SetGlobalFileHistory` holds a write lock,
  `GetGlobalFileHistory` holds a read lock.

### Task 4 — Add Timeouts to `http.Client` ✅

- **Files:** `internal/api/client.go`, `anthropic.go`, `gemini.go`, `ollama.go`,
  `openai_compat.go`
- Added `newHTTPClient()` helper returning `&http.Client{Timeout: 5 * time.Minute}`.
- All four API constructors now call `newHTTPClient()` instead of bare
  `&http.Client{}`.

---

## Phase 2: Medium Severity Bugs and Security

### Task 5 — Fix Child Agent Cost Double-Counting ✅

- **File:** `internal/cost/tracker.go`
- `RecordChildAgentSnapshot` now adds only the child's _own_ cost/tokens
  (total minus its own nested-child subtotals) to the `ChildAgent*` fields,
  preventing double-counting with what `mergeSnapshotLocked` already adds.

### Task 6 — Consolidate Duplicate Bash Security Rules ✅

- **Files:** new `internal/bashsecurity/rules.go`, `internal/tools/bash.go`,
  `internal/permissions/bash_rules.go`
- Created `internal/bashsecurity` package as the single canonical source for
  all bash security patterns and validator functions (import-cycle-free).
- `tools/bash.go` and `permissions/bash_rules.go` now delegate to
  `bashsecurity.ValidateBashSecurity` and `bashsecurity.CheckDestructive`.
- Duplicate regex vars removed from both packages.

### Task 7 — Resolve Symlinks in `resolveToolPath` ✅

- **File:** `internal/tools/path_resolution.go`
- After the normal path-escape check, calls `filepath.EvalSymlinks()` when the
  path already exists and re-validates the real path against `baseDir`.
  Non-existent paths (e.g. `create_file`) are skipped silently.

### Task 8 — Validate `base_url` Against Known Provider Domains ✅

- **Files:** `internal/api/client.go`, `anthropic.go`, `gemini.go`, `ollama.go`,
  `openai_compat.go`
- Added `warnCustomBaseURL()` helper that prints a stderr warning whenever the
  configured `base_url` differs from the provider default.
- All four constructors call `warnCustomBaseURL` after the URL is resolved.
- Warning is suppressed when `GOCODE_ALLOW_CUSTOM_BASE_URL=1` is set.

### Task 9 — Validate Permission Mode on Load ✅

- **File:** `internal/config/config.go`
- `Load()` now validates `cfg.PermissionMode` after all overrides are applied.
  Unknown values print a warning to stderr and fall back to `"default"`.

---

## Phase 3: Code Quality

### Task 10 — Remove `max()` Redefinition and `minInt` Helper ✅

- **Files:** `internal/tools/file_read.go`, `internal/tools/file_diff_preview.go`,
  `internal/agent/memory_files.go`, `internal/tools/web_search.go`,
  `internal/agent/output_budget.go`, `internal/agent/memory_files.go`,
  `internal/tools/apply_patch.go`, `internal/tools/project_overview.go`
- Removed all custom `max()`, `minInt()`, and `min()` helper functions.
- Replaced all call sites with the Go 1.21+ builtin `min()`/`max()`.

### Task 11 — Expand Bash Security Validation ✅

- **File:** `internal/bashsecurity/rules.go` (via Task 6)
- Added `EvalExec` pattern blocking `eval`, `exec`, `source`, `.` (dot-source),
  and backtick command substitution.
- `ValidateBashSecurity` checks this pattern and returns a descriptive error.

---

## Phase 4: Structural Improvements

### Task 12 — Extract Engine Loop from `main.go` ✅

- **Files:** `cmd/gocode/main.go` (trimmed to ~147 lines), new files:
  - `cmd/gocode/engine.go` (~1065 lines) — `runStdioEngine`, model helpers, stream emitters, plan review gate
  - `cmd/gocode/tool_executor.go` (~686 lines) — `executeToolCalls`, permission helpers, tool param utilities
  - `cmd/gocode/slash_commands.go` (~495 lines) — `handleSlashCommand`, format helpers, `gitDiff`
  - `cmd/gocode/session_helpers.go` (~368 lines) — artifact emitters, compaction, `persistSessionState`
- Pure file reorganization; no logic changes. Build verified after split.

### Task 13 — Add Foundational Tests ⏭️

- Skipped per project policy.

### Task 14 — Reduce Simple-Task Retry Thrash and Search Alias Errors ✅

- **Files:** `gocode/cmd/gocode/tool_executor.go`, `gocode/internal/agent/loop.go`, `gocode/cmd/gocode/engine.go`
- Normalized invented tool names like `google:search`/`google_search` into the native `web_search` tool and mapped `queries[0]` to `query` so local-model compatibility errors stop derailing turns.
- When the model asks a routine clarification for a concrete implementation request, the engine now emits a visible recoverable status and retries once with a stronger directive that forbids unnecessary web search for basic scaffold/syntax tasks.
- Tightened the default system prompt so simple self-contained requests are handled by direct file edits instead of pointless browsing or clarification loops.

### Task 15 — Audit Subagent Implementation ✅

- **Files reviewed:** `gocode/cmd/gocode/subagent_runtime.go`, `gocode/cmd/gocode/subagent_background.go`, `gocode/internal/tools/agent.go`, `gocode/internal/hooks/types.go`, `gocode/internal/tools/registry.go`, plus `vscode-copilot-chat/src/extension/agents/vscode-node/*` and `vscode-copilot-chat/src/extension/intents/node/toolCallingLoop.ts`
- Confirmed a high-severity bug: `makeSubagentRunner(...)` captures the startup `client` and `activeModelID` once, so child agents do not follow later lazy model initialization or `/model` switches.
- Confirmed a hook-model mismatch versus VS Code Copilot Chat: child agents reuse generic `session_start` / `stop` hooks instead of dedicated subagent hook types, so parent hooks can unintentionally affect child-agent startup and completion.
- No code fixes applied in this task; findings recorded for follow-up implementation.

### Task 16 — Fix Subagent Runtime State and Hook Isolation ✅

- **Files:** `gocode/cmd/gocode/model_state.go`, `gocode/cmd/gocode/engine.go`, `gocode/cmd/gocode/subagent_runtime.go`, `gocode/internal/hooks/types.go`
- Added a shared active-model state so the `agent` tool reads the current model client and model id at invocation time instead of using startup snapshots.
- Main engine now refreshes that state after lazy client initialization and after slash-command model/session changes, so child agents follow `/model`, restore, and delayed startup recovery.
- Split child lifecycle hooks into dedicated `subagent_start`, `subagent_stop`, and `subagent_stop_failure` hook types so top-level session hooks no longer leak into child-agent runs.

### Task 17 — Install Latest Local Build ✅

- **Method:** Followed the local-clone install flow from `README.md` via `gocode/tui/Makefile`.
- Rebuilt the local release with `cd gocode/tui && make release-local`.
- Installed updated `gocode` and `gocode-engine` into `~/.local/bin` using `install -m 755`.
- Verified the installed launcher resolves from `~/.local/bin/gocode` and `gocode --help` runs successfully.

### Task 18 — Sanitize Gemini Tool Schemas ✅

- **File:** `gocode/internal/api/gemini.go`
- Added a Gemini-specific schema sanitizer in `buildGeminiTools(...)` so function declarations no longer forward raw tool JSON Schema directly.
- The sanitizer strips unsupported metadata keys, filters `required` to declared properties, removes object-only keywords from non-object nodes, and flattens the alias-style `anyOf` / `allOf` required groups used by `gocode` tools into Gemini-safe top-level `required` fields.
- Preferred canonical required fields are selected with a stable heuristic (`path`/`input`/snake_case before camel/PascalCase), so Gemini sees a smaller compatible parameter surface while runtime compatibility aliases remain accepted by the tool executor.
- Verified with `gofmt -w internal/api/gemini.go && go build ./...`.

### Task 19 — Install Latest Local Build After Gemini Fix ✅

- **Method:** Re-ran the documented local-clone install flow from `gocode/README.md`.
- Rebuilt the current release with `cd gocode/tui && make release-local`.
- Installed updated `gocode` and `gocode-engine` into `~/.local/bin` using `install -m 755`.
- Verified `gocode` resolves from `~/.local/bin/gocode` and `gocode --help` runs successfully.

### Task 20 — Execute Gemini Tool Calls Independent of Stop Reason ✅

- **File:** `gocode/internal/agent/loop.go`
- Changed the main agent loop to execute a tool batch whenever the model emitted tool calls, instead of requiring `stopReason == "tool_use"`.
- This fixes Gemini turns where the model returns `functionCall` parts together with `finishReason: STOP`, which previously surfaced as a completed turn with no assistant text and no file operation.
- Verified with `gofmt -w internal/agent/loop.go && go build ./...`.

### Task 21 — Install Latest Local Build After Gemini Tool-Stop Fix ✅

- **Method:** Re-ran the documented local-clone install flow from `gocode/README.md` after Task 20.
- Rebuilt the current release with `cd gocode/tui && make release-local`.
- Installed updated `gocode` and `gocode-engine` into `~/.local/bin` using `install -m 755`.
- Verified `gocode` resolves from `~/.local/bin/gocode` and `gocode --help` runs successfully.

### Task 22 — Preserve Gemini Thought Signatures on Tool Calls ✅

- **Files:** `gocode/internal/api/client.go`, `gocode/internal/api/gemini.go`
- Extended the shared `api.ToolCall` struct with `thought_signature` storage so Gemini tool-call metadata survives transcript persistence and replay.
- The Gemini stream adapter now captures `thoughtSignature` from function-call parts, and `convertGeminiMessage(...)` reattaches that exact signature to the same function-call part when sending the next request.
- This fixes Gemini validation failures like missing `thought_signature` on `functionCall` parts during tool loops.
- Verified with `gofmt -w internal/api/client.go internal/api/gemini.go && go build ./...`.

### Task 23 — Install Latest Local Build After Gemini Thought-Signature Fix ✅

- **Method:** Re-ran the documented local-clone install flow from `gocode/README.md` after Task 22.
- Rebuilt the current release with `cd gocode/tui && make release-local`.
- Installed updated `gocode` and `gocode-engine` into `~/.local/bin` using `install -m 755`.
- Verified `gocode` resolves from `~/.local/bin/gocode` and `gocode --help` runs successfully.
