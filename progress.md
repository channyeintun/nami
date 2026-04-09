# go-cli — Implementation Progress

## Project Setup
- [x] Go module initialized (go1.26.1, `github.com/channyeintun/go-cli`)
- [x] Full directory structure created
- [x] Cobra dependency added
- [x] `.gitignore` configured
- [x] Build + vet passing clean

---

## Week 1–2: MVP Core

### `internal/api/` — LLM Client + Streaming
- [x] `client.go` — LLMClient interface, ModelRequest, ModelEvent, Usage types
- [x] `provider_config.go` — 9 provider presets (Anthropic, OpenAI, Gemini, DeepSeek, Qwen, GLM, Mistral, Groq, Ollama)
- [x] `retry.go` — APIError classification, exponential backoff, RetryWithBackoff
- [x] `anthropic.go` — Anthropic Messages API streaming client
- [x] `openai_compat.go` — OpenAI-compatible streaming client
- [x] `gemini.go` — Gemini native streaming client
- [x] `ollama.go` — Ollama local model client

### `internal/agent/` — Query Engine
- [x] `query_stream.go` — iter.Seq2-based QueryStream, QueryDeps, QueryState, 5-phase runIteration skeleton
- [x] `modes.go` — ExecutionMode (plan/fast), ExecutionProfile with ProfileForMode
- [x] `token_budget.go` — ContinuationTracker with diminishing returns logic
- [x] `context_inject.go` — SystemContext (session-stable) + TurnContext (per-turn refresh)
- [ ] `loop.go` — Wire real model calls into the 5-phase iteration
- [ ] `planner.go` — Plan creation + enforcement before writes

### `internal/tools/` — Tool Execution
- [x] `interface.go` — Tool interface, PermissionLevel, ToolInput/ToolOutput
- [x] `registry.go` — Tool registry with Get/List/Definitions
- [x] `orchestration.go` — Dynamic concurrency classification, PartitionBatches, ExecuteBatch
- [x] `budgeting.go` — ResultBudget, ApplyBudget with disk spillover
- [x] `bash.go` — Bash tool with security validation
- [x] `file_read.go` — File read tool
- [x] `file_write.go` — File write tool
- [x] `file_edit.go` — File edit tool
- [x] `glob.go` — Glob tool
- [x] `grep.go` — Ripgrep wrapper tool
- [x] `web_search.go` — Web search tool (DuckDuckGo-backed with domain filters)
- [x] `web_fetch.go` — Web fetch tool (URL validation, HTTPS upgrade, redirect limits, HTML→markdown, in-memory cache)
- [ ] `git.go` — Git operations tool
- [ ] `streaming_executor.go` — Overlap tool execution with model generation

### `internal/utils/`
- [x] `tokens.go` — Token estimation (~4 chars/token)
- [x] `messages.go` — Message normalization (consolidate consecutive, strip whitespace)

---

## Week 3: Security & Awareness

### `internal/permissions/`
- [x] `gating.go` — Rule-based permission context (allow/deny/ask), Decision check
- [x] `bash_rules.go` — ZSH dangerous commands blocklist, destructive command patterns, read-only classifier
- [ ] Wire permissions into tool executor

### `internal/agent/`
- [x] `context_inject.go` — Two-layer injection implemented
- [ ] Wire context injection into query loop (per-turn refresh)

### `internal/cost/`
- [x] `tracker.go` — Per-model token/cost/duration tracking, thread-safe Snapshot
- [ ] Wire into API client (record after every call)
- [ ] Wire into tool executor (record tool duration)

---

## Week 4–5: Compaction

### `internal/compact/`
- [x] `tokens.go` — Thresholds (autocompact 13k buffer, warning 20k, circuit breaker 3)
- [x] `pipeline.go` — Pipeline skeleton with 3-strategy cascade
- [x] `tool_truncate.go` — Strategy A: tool result truncation (microcompact)
- [x] `summarize.go` — 9-section compaction prompt template
- [ ] Strategy B implementation: call LLM/local model for summarization
- [ ] Strategy C implementation: partial compaction scoped to recent messages
- [ ] `sliding_window.go` — Sliding window strategy
- [ ] Auto-compaction trigger logic wired into query loop
- [ ] Tests for each strategy

---

## Week 6: Interface & Configuration

### `internal/ipc/`
- [x] `protocol.go` — StreamEvent (18 event types), ClientMessage (6 message types), all typed payloads
- [x] `bridge.go` — NDJSON reader/writer, EmitEvent, EmitReady, EmitError

### `cmd/go-cli/`
- [x] `main.go` — Cobra entrypoint, `--stdio`/`--model`/`--mode` flags, NDJSON event loop
- [ ] Wire query engine into the event loop (replace stub response)
- [ ] Slash command dispatch (`/plan`, `/fast`, `/compact`, `/model`, `/cost`, `/resume`)

### `internal/config/`
- [x] `config.go` — File + env config loading, ParseModel, Save

### `internal/skills/`
- [x] `loader.go` — Two-directory discovery (~/.config/go-cli/agents/ + .agents/)
- [x] `frontmatter.go` — YAML frontmatter parser
- [ ] Wire skills into system prompt injection

### `internal/hooks/`
- [x] `types.go` — 9 hook types, Payload, Response
- [x] `runner.go` — Shell script hook executor (~/.config/go-cli/hooks/)
- [ ] Wire hooks into tool execution lifecycle
- [ ] Wire hooks into compaction lifecycle

### `internal/session/`
- [x] `store.go` — NDJSON transcript persistence, metadata save/load, ListSessions
- [ ] `restore.go` — Resume conversation/todos/model/mode state
- [ ] Wire session save into query loop

### `internal/artifacts/`
- [x] `types.go` — 10 artifact kinds, Scope (session/user), Artifact/ArtifactVersion/ArtifactRef
- [x] `service.go` — Service interface (Save/Load/List/Delete/Versions)
- [x] `store.go` — LocalStore filesystem implementation
- [ ] `manager.go` — Plan/task/walkthrough artifact lifecycle
- [ ] Wire artifacts into tool budgeting spillover
- [ ] Wire artifacts into planning mode

### `tui/` — Ink Frontend
- [x] `package.json` — React 19, Ink 7, TypeScript 6
- [x] `tsconfig.json`
- [x] `src/index.tsx` — Entry point
- [x] `src/App.tsx` — Top-level layout + event dispatch
- [x] `src/components/Input.tsx` — Text input + Tab toggle + slash commands
- [x] `src/components/StreamOutput.tsx` — Streaming text output
- [x] `src/components/StatusBar.tsx` — Mode, model, cost display
- [x] `src/components/PermissionPrompt.tsx` — y/n/a approval
- [x] `src/components/ToolProgress.tsx` — Tool execution indicator
- [x] `src/hooks/useEngine.ts` — Spawn Go child, NDJSON I/O
- [x] `src/hooks/useEvents.ts` — StreamEvent → React state
- [x] `src/protocol/types.ts` — Mirrors Go IPC types
- [x] `src/protocol/codec.ts` — NDJSON parser/serializer
- [ ] `src/components/PlanPanel.tsx` — Render implementation-plan artifact
- [ ] `src/components/ArtifactView.tsx` — Render artifact content
- [ ] `npm install` + TypeScript build verification

---

## Phase 2a: Local Model (Post-MVP)

### `internal/localmodel/`
- [x] `runner.go` — Ollama auto-detection, NewLocalModel
- [x] `router.go` — Task-based routing (compaction/scoring/title → local, reasoning → remote)
- [ ] Implement Query() method (POST to Ollama /api/generate)
- [ ] Wire into compact/summarize.go
- [ ] Wire into session title generation

---

## Phase 2b: Multi-Model Support (Post-MVP)
- [ ] Finalize LLMClient with Capabilities()
- [x] `anthropic.go` — Full streaming implementation
- [x] `openai_compat.go` — SSE parser, function calling
- [x] `gemini.go` — Native streaming, function declarations
- [x] `ollama.go` — Local chat streaming implementation
- [ ] `/model` runtime switching
- [ ] Capability-aware engine adjustments

---

## Summary

| Area | Scaffolded | Wired/Working |
|---|---|---|
| IPC Protocol | ✅ | ✅ |
| API Interfaces | ✅ | ⚠️ (Anthropic + OpenAI-compatible + Gemini + Ollama clients implemented) |
| Agent Loop | ✅ | ❌ (skeleton only) |
| Tools | ✅ (framework) | ⚠️ (bash + file read/write/edit/glob/grep implemented; remaining tools pending) |
| Compaction | ✅ (Strategy A done) | ❌ (B+C pending) |
| Permissions | ✅ | ❌ (not wired) |
| Cost Tracking | ✅ | ❌ (not wired) |
| Hooks | ✅ | ❌ (not wired) |
| Artifacts | ✅ | ❌ (not wired) |
| Session | ✅ | ❌ (not wired) |
| Config | ✅ | ✅ |
| Skills | ✅ | ❌ (not wired) |
| Local Model | ✅ | ❌ (not wired) |
| Ink TUI | ✅ | ❌ (not built/tested) |
| CLI Entrypoint | ✅ | ✅ (stub responses) |

**Current state:** All four provider clients, the Bash tool, and the file read/write/edit/glob/grep/web_search/web_fetch tools are implemented. The default tool registry now includes those built-ins. The next concrete tooling task is `git.go`, with query-loop wiring still pending behind the tool layer.
