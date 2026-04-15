# Progress

## 2026-04-15

- Fixed the repeated TUI out-of-memory crash caused by oversized child-agent summaries being duplicated into live tool results, background-agent updates, and replayed UI state.
- Added `plan.md` with the recovered MCP integration implementation plan, covering phased delivery, config layering, runtime manager design, permission mapping, and verification scope.
- Refined the MCP plan to use explicit per-server `stdio`, `sse`, `http`, and `ws` transport choices, aligned with the preferred Claude Code-style config model.
- Locked the MCP plan decisions: Chan stays MCP-client-only, `.chan/mcp.json` is the project override path, permission mapping is entirely config-driven, Phase 1 stays startup-loaded, discovered tools export by default, and the config schema uses a discriminated transport union.
- Added MCP config layering for Chan with user-level `mcp.servers`, repo-local `.chan/mcp.json` overrides, transport-specific validation, env expansion, and trusted per-tool permission mapping.
- Added the MCP runtime manager, SDK session wrapper, and transport implementations for `stdio`, `sse`, `http`, and `ws`, including startup discovery, graceful per-server failure handling, and clean shutdown.
- Registered discovered MCP tools into the main tool registry with stable `mcp__<server>__<tool>` names, routed execution through the existing tool runner, surfaced MCP-specific permission targets, and extended `/status` plus the README with MCP server visibility and configuration docs.
- Capped live child-agent payloads in the Go engine and hardened the TUI replay path for oversized historical agent payloads.
- Validated with `go build ./...`, rebuilt the TUI release bundle, and installed the updated `chan` and `chan-engine` into `~/.local/bin`.
- Changed plan mode to `Ultrathink`: removed write blocking, updated the plan-mode system prompt, and updated the `/plan` command description so explicit create/modify requests are allowed in plan mode.