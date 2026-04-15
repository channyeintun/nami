# Progress

## 2026-04-15

- Fixed the repeated TUI out-of-memory crash caused by oversized child-agent summaries being duplicated into live tool results, background-agent updates, and replayed UI state.
- Capped live child-agent payloads in the Go engine and hardened the TUI replay path for oversized historical agent payloads.
- Validated with `go build ./...`, rebuilt the TUI release bundle, and installed the updated `chan` and `chan-engine` into `~/.local/bin`.