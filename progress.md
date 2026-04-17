# Read Tool Implementation Progress

## References

- Reviewed chan's current `read_file` implementation.
- Reviewed reference implementations from opencode, Claude Code, and VS Code Copilot Chat during planning.

## Task Status

1. Create progress tracker
   - Status: completed
   - Notes: Added this file to track task-by-task execution and commits.

2. Refactor `read_file` API
   - Status: completed
   - Notes: `read_file` now uses only `filePath` + `offset` + `limit`, applies bounded default reads, clips long lines, caps output bytes, and emits canonical continuation hints.

3. Add reread dedup state
   - Status: completed
   - Notes: Added session-scoped unchanged-slice suppression keyed by path, offset, limit, size, and modification time, and wired it into engine startup plus `read_file`.

4. Invalidate cache on writes
   - Status: completed
   - Notes: Added shared invalidation after successful create, write, edit, patch, delete, and rewind mutations.

5. Tighten prompt guidance
   - Status: completed
   - Notes: Strengthened the tool description and engine prompt guidance for canonical bounded reads, and added lightweight session-scoped read metrics for tuning.

6. Format and verify changes
   - Status: completed
   - Notes: Ran `gofmt` on all touched Go files, completed repeated `go build ./...` verification passes, and confirmed there are no relevant editor diagnostics.

## Completion

- All items from `patch-plan.md` are implemented.
- No tests were added.

## Follow-up fixes

- Fixed the `read_file` schema/normalization mismatch so validation accepts the actual supported path aliases.
- Fixed `read_file` parameter validation so unexpected params fail fast instead of being silently ignored.
- Fixed reread dedup bookkeeping so slices are only remembered after inline delivery survives output budgeting.
- Tightened the compatibility alias path so it uses the same fail-fast validation and canonical `filePath` forwarding.

---

# Provider UX Implementation Progress

## References

- Reviewed `plan.md` for the target multi-provider UX.
- Reviewed `reference/opencode` provider onboarding, auth-method, and provider selection flows.
- Reviewed current `chan` startup, `/connect`, `/model`, and TUI model picker behavior.

## Task Status

1. Stabilize startup and align the default provider
   - Status: completed
   - Notes: Fixed the typed-nil client crash during warmup, changed the shipped default to `github-copilot/gpt-5.4`, rebuilt the launcher and engine, and reinstalled the local binaries.

2. Add provider discovery snapshot
   - Status: completed
   - Notes: Added a shared provider snapshot helper in `internal/commands` that resolves the selected provider, orders built-in providers, classifies auth sources, and marks providers as usable or setup-required from the current config and environment.

3. Add `/providers` command
   - Status: completed
   - Notes: Added a new `/providers` slash command that uses the shared provider snapshot and reports the active selection, first usable provider, per-provider auth source, setup state, and next action when setup is required.

4. Improve startup and session status UX
   - Status: completed
   - Notes: Startup now uses provider discovery to fall back from an unusable default to the first safe provider with credentials, emits a clear notice when it switches, avoids optimistic auto-fallback to Ollama, and shows provider state in `/status`.

5. Generalize `/connect`
   - Status: completed
   - Notes: Reworked `/connect` into a generic provider entry point with overview, help, and status modes; kept GitHub Copilot on device auth; added API-key and local-provider onboarding guidance; and made ready providers switch the active session immediately through the same command.

6. Redesign model picker around provider state
   - Status: completed
   - Notes: The model picker now builds its options from provider discovery instead of a flat curated preset list, preserves a current custom selection when needed, pushes usable providers to the top, and surfaces provider readiness directly in the option labels and descriptions.

7. Persist recent successful model selections
   - Status: in progress

8. Final rebuild and install
   - Status: not started
