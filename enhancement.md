# Enhancement Opportunities

Code review of progress against `plan.md` phases 6–8 and post-parity stabilization.

## Status as of 2026-04-11

All items in this review have now been addressed or superseded by later changes.

- Resolved in code: tool-result truncation, planner lifecycle hooks, background output loss reporting, PTY-backed background commands, background command cleanup, background reader shutdown cancellation, file-tool path traversal defense, artifact ID sanitization, protocol parse warnings, image paste warnings, Esc cancellation during permission waits, bash substitution narrowing, TUI limit documentation, and compat alias schema alignment.
- Superseded by current code: the permission prompt no longer replaces the live transcript area, and the markdown token cache now promotes hits on access so eviction behaves as LRU.

Keep the historical findings below only as an audit trail.

---

## P0 — Bugs / Correctness

### 1. Tool result truncation is a no-op

**File:** `gocode/internal/compact/tool_truncate.go`

`TruncateToolResults` keys `lastSeen` by `ToolCallID`, which is unique per invocation. The guard `if i == lastSeen[msg.ToolResult.ToolCallID]` therefore always matches, so no tool result is ever truncated. The function should key by **tool name** (not call ID) to preserve only the most recent result of each compactable tool type.

### 2. Planner lifecycle hooks are dead code

**File:** `gocode/internal/agent/planner.go`

`BeginTurn()` and `FinalizeTurn()` both return `(nil, nil)` and are never called from the agent loop. `progress.md` claims "updates draft/final implementation-plan artifacts at the start/end of plan-mode turns" but this is handled entirely by the explicit `save_implementation_plan` tool instead. The dead stubs should either be removed or implemented to seed/finalize plan artifacts automatically as the progress notes describe.

### 3. Background command output buffer offset can drift

**File:** `gocode/internal/tools/background_commands.go` (lines 200–210)

After trimming the front of the buffer, `readOffset` is adjusted with `readOffset -= trim`. If the write burst that triggers trimming is large enough to overshoot the previous `readOffset`, the else branch resets to 0, which silently skips already-buffered output that hasn't been read yet. A safer approach is to clamp to 0 and document the skip so callers know output was lost.

---

## P1 — Plan Deviations

### 4. Background commands use pipes instead of PTYs

**File:** `gocode/internal/tools/background_commands.go`

`plan.md` specifies "pseudo-terminals (PTYs)" for background command orchestration. The current implementation uses `cmd.StdinPipe()` / `cmd.StdoutPipe()` / `cmd.StderrPipe()`. This means interactive programs that rely on terminal capabilities (raw mode, control characters, line editing) will not work correctly. Consider using `github.com/creack/pty` to allocate a real PTY.

### 5. Background command map is never pruned

**File:** `gocode/internal/tools/background_commands.go`

Completed entries in the `backgroundCommands` map persist for the lifetime of the process. Long sessions with many background commands will accumulate stale entries, leaking the `exec.Cmd`, stdin pipe, and output buffer for each. Add a reaper (TTL or explicit cleanup after final status read).

### 6. Background output goroutines lack cancellation

**File:** `gocode/internal/tools/background_commands.go`

The two `streamBackgroundOutput` goroutines per command run `io.Copy` without a context or deadline. If a process hangs without producing output, those goroutines block indefinitely. Pass a context to enable cancellation from the agent loop.

---

## P2 — Security / Defense-in-Depth

### 7. File tools lack path-traversal validation

**Files:** `file_edit.go`, `file_write.go`, `multi_replace_file_content.go`, `file_read.go`, `list_dir.go`

When a relative path is provided, these tools join it to `os.Getwd()` via `filepath.Join` without verifying the resolved path stays within the project root. A model-generated path containing `../../` could reach outside the working directory. The permission layer mitigates most risk, but an explicit `filepath.Rel` + prefix check would add defense-in-depth.

### 8. Artifact store ID not sanitized

**File:** `gocode/internal/artifacts/store.go`

`Save()` accepts an external `req.ID` and uses it in `filepath.Join(baseDir, kind, id)`. A crafted ID like `../../etc` could escape the store directory. Sanitize the ID (reject `/` and `..` components) or use the generated hex ID exclusively.

---

## P3 — TUI Behavior / UX

### 9. Permission prompt hides live assistant content

**File:** `gocode/tui/src/App.tsx`

When a `permission_request` event arrives mid-stream, the permission modal replaces the main content area. The user loses visibility of the partial assistant response. `plan.md` post-parity item 2 states: "Keep the live assistant status visible across tool execution and permission waits." Render the permission prompt as an overlay or inline element that preserves the visible stream output.

### 10. Esc during permission wait denies instead of cancelling

**File:** `gocode/tui/src/App.tsx`, `gocode/tui/src/components/Input.tsx`

Pressing Esc while a permission prompt is shown sends a denial for that specific tool, not a cancel of the active turn. `plan.md` post-parity item 3 states: "Make Esc interruption reliable while a turn is active." Consider adding a secondary key (e.g., Ctrl+C) or changing Esc to cancel the entire turn from the permission state.

### 11. Silent protocol parse failures

**File:** `gocode/tui/src/protocol/codec.ts`

`parseEvent()` returns `null` on JSON parse errors without logging. Malformed engine output is silently dropped, making protocol bugs hard to diagnose. Emit a debug-level warning to stderr.

### 12. Silent image paste failures

**File:** `gocode/tui/src/utils/imagePaste.ts`

`readImageFile()` catches all errors and returns `null`. Users have no indication that an image wasn't loaded. Surface a brief warning to the user.

---

## P4 — Code Quality / Cleanup

### 13. DangerousSubstitution regex overly broad

**File:** `gocode/internal/permissions/bash_rules.go`

The pattern blocks all `$()` and `${}` expansions, which prevents common legitimate shell idioms like `echo "$(date)"` or `${VAR:-default}`. If the intent is to block only dangerous expansions, narrow the pattern or whitelist common safe forms.

### 14. Token cache eviction is not LRU

**File:** `gocode/tui/src/utils/markdown.ts`

The token cache evicts using `Map.keys().next()` (insertion order), not least-recently-used. Under high reuse of certain entries, frequently accessed items can be evicted while stale ones remain. Use an LRU structure or promote on access.

### 15. Hardcoded magic numbers without documentation

**Files:** `StreamOutput.tsx`, `ToolProgress.tsx`, `Input.tsx`

Constants like `MAX_TRANSCRIPT_BLOCKS = 200`, `TRANSCRIPT_CAP_STEP = 50`, 320-char output truncation, and 6-line limits have no comments explaining their rationale. Add brief inline documentation to aid future maintenance.

### 16. Compat alias schema/parameter mismatch

**File:** `gocode/internal/tools/compat_aliases.go`

The `ReadFileAliasTool` schema declares `path` as the only required parameter, but `Execute()` also accepts `filePath` and `file_path`. A model calling with `filePath` would pass schema validation only if the schema lists it. Align the schema with accepted parameter names.
