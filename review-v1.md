# Review V1

Reviewed range: `ec77892..09caaa2`

Scope: line-by-line audit of the landed roadmap work for fake/mock/todo leftovers, critical bugs, regressions, and high-impact improvements.

Status: Findings 1-6 were addressed by the follow-up remediation pass on 2026-04-12. This file is kept as the original review record.

## Findings

### High

1. Session-wide allow-all currently bypasses write safety for every non-`bash` tool.
   - Files: `gocode/internal/permissions/gating.go:69`, `gocode/cmd/gocode/main.go:1116-1117`
   - Why this matters: the permission state is documented as “allow all non-destructive commands for this session,” but the implementation only checks destructiveness for `bash`. For every other tool, `SessionAllowAll` returns `DecisionAllow` immediately, which includes write-capable tools like `file_write`, `file_edit`, `multi_replace_file_content`, `file_history_rewind`, and the session artifact tools.
   - User impact: after pressing “Allow This Session” once, later destructive non-shell edits can run without any further prompt, which is a much broader permission grant than the UI and README imply.
   - Suggested fix: treat `SessionAllowAll` as a shortcut only for read-only tools plus explicitly classified safe execute tools, or add tool-level destructiveness classification for non-`bash` tools before auto-allowing them.

2. The shell execution path hardcodes `/bin/zsh`, which breaks the advertised Linux support on systems without zsh.
   - Files: `gocode/internal/tools/bash.go:179`, `gocode/internal/tools/background_commands.go:90`
   - Why this matters: the CLI is documented as supporting macOS and Linux, but both foreground `bash` execution and background command launch require `/bin/zsh` to exist.
   - User impact: on common Linux environments that ship `/bin/bash` but not `/bin/zsh`, one of the core tools fails outright, including all background command lifecycle features added in this workstream.
   - Suggested fix: resolve the shell from `$SHELL` with a safe fallback chain such as `/bin/bash` then `/bin/zsh`, and keep the security checks aligned with the chosen shell.

### Medium

3. `file_history_rewind` can leave the workspace partially restored while still reporting success.
   - Files: `gocode/internal/tools/file_history.go:128-163`, `gocode/internal/tools/file_history_tools.go:145-149`
   - Why this matters: `Rewind` silently `continue`s on failed delete, read, mkdir, and write operations. The caller only receives a restored count, with no failed file list or aggregate error.
   - User impact: a rewind can leave the workspace in a mixed state and the tool output still looks like a clean success message.
   - Suggested fix: return structured partial-failure information such as restored/failed paths, or fail the tool when any requested restore step cannot be completed.

4. The new schema validation layer does not actually understand `anyOf`/`allOf`, so several new tool contracts are only validated at execution time.
   - Files: `gocode/internal/tools/validation.go:26-57`
   - Examples of affected schemas: `gocode/internal/tools/command_status.go:46-48`, `gocode/internal/tools/stop_command.go:46-48`, `gocode/internal/tools/forget_command.go:35-37`, `gocode/internal/tools/list_dir.go:45-47`, `gocode/internal/tools/send_command_input.go:54-64`, `gocode/internal/tools/multi_replace_file_content.go:59-140`
   - Why this matters: the roadmap claims malformed tool calls are rejected before permission prompts and execution, but alias-based required fields are skipped by the validator because it only reads top-level `required`.
   - User impact: malformed calls can still reach permission handling or execution, which weakens the new validation guarantee and creates unnecessary prompts for broken requests.
   - Suggested fix: implement recursive schema handling for `anyOf` and `allOf`, at least for required-field validation, before relying on schema validation as a pre-permission gate.

5. Session artifact loading and lookup silently discard read/load failures.
   - Files: `gocode/internal/artifacts/manager.go:99-108`, `gocode/internal/artifacts/manager.go:124-127`
   - Why this matters: `LoadSessionArtifacts` and `FindSessionArtifact` both skip artifact load failures with `continue`, without surfacing the failure to the caller.
   - User impact: resume can silently hide broken artifacts, and upsert-style flows can miss an existing artifact and create a duplicate replacement instead of updating it.
   - Suggested fix: return partial results plus error details, or at minimum log/report skipped artifact IDs so the session can explain why artifacts disappeared.

### Low

6. The progress tracker is internally inconsistent after the phase-closing commits.
   - File: `progress.md:116`
   - Why this matters: the top-level status table marks Phase 6 complete, `Remaining to Finish` is `None`, and all exit criteria are checked, but the Phase 6 dashboard section still says `Status: in progress`.
   - User impact: the completion dashboard can no longer be treated as the canonical tracker, which was the exact purpose of the dashboard rewrite.
   - Suggested fix: update the Phase 6 dashboard status to `completed` so the table and dashboard agree.

## Impactful Enhancements

1. Add structured partial-failure reporting to `file_history_rewind` and artifact loading.
   - Rationale: both flows currently degrade silently on per-item failures; returning `restored`, `failed`, and `skipped` lists would make the new safety/recovery features much more trustworthy.

2. Unify background-command wait parameters and naming.
   - Rationale: the new lifecycle tools mix `WaitDurationSeconds` and `WaitMs`, which makes the surface harder to learn and easier for the model to call incorrectly.

3. Clarify the semantics of “Allow This Session” in both the UI and runtime.
   - Rationale: even if the permission behavior is tightened, the prompt copy should explicitly say what is and is not being auto-approved so the user’s mental model matches the engine.

## Verified Non-Issues

- No fake/mock/stub/TODO leftovers were found in the reviewed code path. The only `placeholder` hits were ordinary variable names or comments, not unfinished implementation markers.
- The memory side-query path does have a heuristic fallback: when no recalled lines are returned, `formatRelevantMemoryIndexContent(...)` falls back to `selectRelevantMemoryLines(...)`.
