# Progress

## Active Task

- Completed: lean retrieval architecture implementation for the agent harness.
- Completed: review-driven corrections for retrieval, preference recall, and telemetry wiring.
- Completed: structural edge expansion, test-covers edges, and attempt-log surfaced telemetry.

## Notes

- Added structural edge expansion (1-hop) to the retrieval pipeline: Go import edges resolve local-package imports to candidate files; test ↔ source edges associate _test.go and .test.ts files with their counterparts.
- Added attempt_log_surfaced telemetry event emitted each turn when attempt-log entries are loaded into the prompt.
- Added edges_expanded field to retrieval telemetry payload and TUI footer display.
- Added a session-scoped attempt log and wired failed tool-attempt recording into the query loop.
- Added live retrieval with anchor extraction, candidate scoring, live snippet reads, prompt injection, and retrieval telemetry.
- Narrowed durable memory framing toward preferences and conventions instead of repo facts.
- Shared retrieval token budgeting with context-pressure handling and wired attempt-log creation from the engine session directory.
- Replaced durable-memory model side-query with deterministic preference matching and stopped injecting unrecalled memory index entries.
- Normalized retrieval candidate paths, boosted error-context matches, and expanded touched-file tracking to use tool results and compatibility field names.
- Wired retrieval telemetry into the TUI footer so per-turn retrieval usage is visible instead of being dropped on the frontend.
- Verified the Go module builds successfully with `go build ./...`.