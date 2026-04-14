# Progress

## Active Task

- Completed: lean retrieval architecture implementation for the agent harness.

## Notes

- Added a session-scoped attempt log and wired failed tool-attempt recording into the query loop.
- Added live retrieval with anchor extraction, candidate scoring, live snippet reads, prompt injection, and retrieval telemetry.
- Narrowed durable memory framing toward preferences and conventions instead of repo facts.
- Shared retrieval token budgeting with context-pressure handling and wired attempt-log creation from the engine session directory.
- Verified the Go module builds successfully with `go build ./...`.