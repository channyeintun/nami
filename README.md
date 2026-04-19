# Nami

An agentic coding CLI powered by LLMs. Think, plan, and execute code changes from your terminal.

Nami combines a terminal UI, a Go-based execution engine, first-class artifacts, and bounded child agents so you can inspect code, plan work, edit safely, and verify results without leaving the terminal.

![Agentic Coding CLI Architecture](./docs/architecture.webp)

## Why Nami

- **Agentic terminal workflow** — chat with your codebase, run tools, and edit files in one place.
- **Two operating modes** — use **plan** mode for review-first workflows or **fast** mode for direct execution.
- **First-class artifacts** — implementation plans, task lists, walkthroughs, diff previews, and search reports persist as reviewable outputs.
- **Bounded child agents** — delegate exploration, code search, or terminal-heavy work to specialized subagents.
- **Permission gating** — risky or sensitive actions require explicit approval.
- **Multi-provider model support** — works with Anthropic, OpenAI, Google, DeepSeek, Groq, Mistral, Ollama, and GitHub Copilot.

## Architecture & Vision

Nami is built on three core pillars:

1. **TUI (Silvery)** — interactive terminal UX with streaming output, tool transcripts, progress, background task visibility, and artifact panels.
2. **Go Engine** — high-performance backend for the agent loop, tool execution, provider integration, session persistence, and permission gating.
3. **Artifacts** — durable structured outputs that can be reviewed, revised, and resumed across turns.

## Architecture Docs

- [Lean Retrieval Architecture](./docs/lean-retrieval-architecture.md)
- [Silvery Guide for Nami](./docs/silvery-guide.md)

## Quick Start

### Prerequisites

- macOS, Linux, or Windows 11
- One supported JavaScript runtime to run the `nami` launcher: Node.js, Bun, or Deno. The Windows installer can bootstrap a local Node.js runtime automatically if none is already available.
- One configured model provider: Anthropic, OpenAI, Google, DeepSeek, Groq, Mistral, Ollama, or GitHub Copilot
- Go 1.26+ only if building from source or rebuilding `nami-engine`

### Install

#### macOS / Linux (one command)

```bash
curl -fsSL https://raw.githubusercontent.com/channyeintun/nami/main/nami/install.sh | sh
```

This downloads prebuilt `nami` and `nami-engine` release assets from GitHub Releases. It does **not** build from source.

Current releases install a launcher shim, a portable `nami.js` bundle, and the Go engine.

You need one of these runtimes on your `PATH` to run the installed launcher:

- `node`
- `bun`
- `deno`

The installer chooses a writable directory automatically:

- `/usr/local/bin` if writable
- `~/.local/bin` otherwise

After install, verify:

```bash
command -v nami
```

If needed, add the install dir to your `PATH`:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

#### Windows (PowerShell)

```powershell
Set-ExecutionPolicy -Scope Process Bypass -Force; irm https://raw.githubusercontent.com/channyeintun/nami/main/nami/install.ps1 | iex
```

This runs in your current PowerShell session, downloads the Windows release archive, installs `nami.cmd`, `nami.js`, and `nami-engine.exe`, and adds the install directory to your user `PATH`.

If `node`, `bun`, or `deno` is already on your `PATH`, the installer reuses it. If not, it downloads a local Node.js runtime automatically and wires `nami` to use it.

Current Windows releases install into:

- `%LOCALAPPDATA%\Programs\nami\bin`

If the installer had to bootstrap Node.js, it stores it here:

- `%LOCALAPPDATA%\Programs\nami\runtime\node`

After install in the same PowerShell window, verify:

```powershell
nami --help
```

#### Manual install

On Windows, download `nami-windows-amd64.zip` or `nami-windows-arm64.zip` from GitHub Releases, extract it, then copy these files into a directory on your `PATH`:

- `nami.cmd`
- `nami.js`
- `nami-engine.exe`

You also need one supported runtime on your `PATH`: `node`, `bun`, or `deno`.

If you already have local Unix launcher assets and engine binaries:

```bash
sudo install -m 755 nami /usr/local/bin/nami
sudo install -m 755 nami.js /usr/local/bin/nami.js
sudo install -m 755 nami-engine /usr/local/bin/nami-engine
```

Without `sudo`:

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 nami "$HOME/.local/bin/nami"
install -m 755 nami.js "$HOME/.local/bin/nami.js"
install -m 755 nami-engine "$HOME/.local/bin/nami-engine"
export PATH="$HOME/.local/bin:$PATH"
```

If installing from a local clone:

```bash
cd nami/tui
make release-local
mkdir -p "$HOME/.local/bin"
install -m 755 release/nami "$HOME/.local/bin/nami"
install -m 755 release/nami.js "$HOME/.local/bin/nami.js"
install -m 755 release/nami-engine "$HOME/.local/bin/nami-engine"
export PATH="$HOME/.local/bin:$PATH"
```

To build Windows release assets from source:

```bash
cd nami/tui
make release
```

The release target now emits Windows archives and direct assets alongside the macOS and Linux artifacts.

## Setup

### API key providers

Example:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```

Supported providers:

| Provider       | Environment variable     |
| -------------- | ------------------------ |
| Anthropic      | `ANTHROPIC_API_KEY`      |
| OpenAI         | `OPENAI_API_KEY`         |
| Google         | `GEMINI_API_KEY`         |
| DeepSeek       | `DEEPSEEK_API_KEY`       |
| Groq           | `GROQ_API_KEY`           |
| Mistral        | `MISTRAL_API_KEY`        |
| Ollama         | none — runs locally      |
| GitHub Copilot | use `/connect` in Nami   |

### GitHub Copilot setup

GitHub Copilot uses a device-login flow instead of a static API key.

Start Nami, then run:

```text
/connect
```

Nami will:

- print the GitHub verification URL and device code
- try to open the verification page automatically
- wait for authorization to complete
- save credentials in the platform config file (`~/.config/nami/config.json` on macOS/Linux)
- switch the main model to `github-copilot/gpt-5.4`
- set the subagent model to `github-copilot/claude-haiku-4.5`

For GitHub Enterprise:

```text
/connect github-copilot your-company.example
```

## Usage

Start the CLI:

```bash
nami
```

Then type what you want, for example:

- `summarize this repository`
- `find dead code and propose a cleanup plan`
- `add a new flag to this CLI`
- `debug why this test is flaky`

### Common flags

```bash
nami --model openai/gpt-4o
nami --model ollama/gemma3
nami --model ollama/gemma4:e4b
nami --mode fast
nami --auto-mode
nami --help
```

### MCP management

Nami now includes a small MCP management CLI similar to Claude Code's core flow.

```bash
nami mcp add my-server -- npx my-mcp-server
nami mcp add --transport http sentry https://mcp.sentry.dev/mcp
nami mcp add-json docs '{"transport":"stdio","command":"uvx","args":["docs-mcp"]}'
nami mcp list
nami mcp get sentry
nami mcp remove sentry
```

Supported scopes:

- `project` writes repo-local MCP config to `.nami/mcp.json`
- `user` writes user MCP config to the platform config directory (`~/.config/nami/config.json` on macOS/Linux)

Notes:

- `add` supports `stdio`, `http`, `sse`, and `ws` transports
- `--env KEY=value` applies to `stdio` servers
- `--header 'Key: Value'` applies to `http`, `sse`, and `ws` servers
- `list` and `get` attempt real MCP connections, so listing a repo-scoped `stdio` server will spawn it briefly to inspect health and capabilities

### Slash commands

| Command               | Description                                    |
| --------------------- | ---------------------------------------------- |
| `/connect`            | Connect GitHub Copilot with device login       |
| `/plan`               | Switch to plan mode                            |
| `/fast`               | Switch to fast mode                            |
| `/model [name]`       | Show or switch the active model                |
| `/reasoning [level]`  | Show or set reasoning effort [low|medium|high|xhigh]            |
| `/compact`            | Compact conversation to save context           |
| `/resume [id]`        | Resume a previous session                      |
| `/clear`              | Clear the conversation and start fresh         |
| `/status`             | Show current session and MCP server status     |
| `/sessions`           | List recent sessions                           |
| `/debug [subcommand]` | Enable debug logging or inspect its path       |
| `/help`               | Show slash-command help                        |

## First-Class Outputs

Artifacts are central to Nami's workflow.

- **Implementation plans** are saved as review artifacts before execution.
- **Task lists** track multi-step progress across turns.
- **Walkthroughs** summarize completed work and validation.
- Large `web_fetch` and `git diff` outputs are routed into dedicated artifacts so the transcript stays concise.

Artifacts are meant to be reopened, revised, and resumed — not just dumped text.

## Permission System

When Nami wants to run a command or change files, it can ask for approval.

```text
╭─ Permission Required ──────────────────────╮
│ bash: git status                           │
│ Risk: execute                              │
│                                            │
│ [y] Allow  [n] Deny  [a] Always Allow      │
│ [s] Allow Safe (This Session)              │
╰────────────────────────────────────────────╯
```

| Key | Action                                                                |
| --- | --------------------------------------------------------------------- |
| `y` | Allow this one command                                                |
| `n` | Deny this command                                                     |
| `a` | Always allow this exact command                                       |
| `s` | Allow future non-destructive, non-sensitive requests for this session |

Use the `--auto-mode` flag at startup to automatically enable "Allow Safe" for the entire session.

Destructive commands and sensitive edits such as `.env`, lockfiles, `.git`, or workspace settings still require explicit approval.

## Tooling

Nami exposes a broad local-tool runtime, including:

| Tool                             | Description                                           |
| -------------------------------- | ----------------------------------------------------- |
| `agent`                          | Spawn bounded child agents                            |
| `agent_status` / `agent_stop`    | Inspect or stop background child agents               |
| `bash`                           | Run shell commands                                    |
| `think`                          | Scratchpad reasoning with no side effects             |
| `read_file` / `file_write`       | Read or overwrite files                               |
| `replace_string_in_file`         | Exact in-place replacement                            |
| `multi_replace_string_in_file`   | Batch exact replacements                              |
| `apply_patch`                    | Multi-file or structural text edits                   |
| `create_file`                    | Create a new file                                     |
| `file_search` / `grep_search`    | Find files or search contents                         |
| `go_definition` / `go_references`| Parser-backed Go code navigation                      |
| `read_project_structure`         | Inspect the directory tree                            |
| `project_overview`               | Summarize repository structure                        |
| `dependency_overview`            | Summarize manifest dependencies                       |
| `web_search` / `web_fetch`       | Web research tools                                    |
| `git`                            | Read-only git operations                              |
| `list_commands` / `command_status` | Inspect background shell sessions                   |
| `file_history`                   | Snapshot and inspect tracked file history             |
| `mcp__<server>__<tool>`          | Dynamically discovered MCP tools from configured servers |

### Child agent modes

The `agent` tool supports three bounded modes:

| Mode              | Best for                                                   |
| ----------------- | ---------------------------------------------------------- |
| `Explore`         | Broad read-only codebase search and architecture research  |
| `general-purpose` | Delegated work that doesn't fit a specialized mode         |
| `verification`    | Builds, tests, and validation without file edits           |

## Configuration

Config file:

```text
~/.config/nami/config.json
```

Example:

```json
{
  "model": "anthropic/claude-sonnet-4-20250514",
  "default_mode": "plan"
}
```

Environment variables override config:

| Variable               | Description                                      |
| ---------------------- | ------------------------------------------------ |
| `NAMI_MODEL`           | Model to use                                     |
| `NAMI_API_KEY`         | API key override                                 |
| `NAMI_BASE_URL`        | Custom API base URL                              |
| `NAMI_DEBUG`           | Enable runtime debug logging                     |
| `NAMI_PERMISSION_MODE` | `default`, `autoApprove`, or `bypassPermissions` |
| `NAMI_AUTO_MODE`      | Set to `true` to auto-approve non-destructive tools |

If you use GitHub Copilot, config may also persist Copilot credentials and a `subagent_model`.

### MCP servers

Nami can load external MCP servers at startup from either `~/.config/nami/config.json` or `.nami/mcp.json` in the current workspace. The workspace file is merged on top of the user config for the current session, so team-local MCP settings can live in the repo without replacing your personal global setup.

Example user config:

```json
{
  "model": "anthropic/claude-sonnet-4-20250514",
  "default_mode": "plan",
  "mcp": {
    "servers": {
      "github": {
        "transport": "stdio",
        "command": "github-mcp-server",
        "args": ["stdio"],
        "env": {
          "GITHUB_TOKEN": "$GITHUB_TOKEN"
        },
        "enabled": true,
        "trust": false,
        "exclude_tools": []
      },
      "docs": {
        "transport": "http",
        "url": "http://127.0.0.1:8787/mcp",
        "headers": {
          "Authorization": "Bearer $DOCS_MCP_TOKEN"
        },
        "enabled": true,
        "trust": true,
        "tool_permissions": {
          "search": "read"
        }
      }
    }
  }
}
```

Example workspace override in `.nami/mcp.json`:

```json
{
  "servers": {
    "browser": {
      "transport": "ws",
      "url": "ws://127.0.0.1:9000/mcp",
      "enabled": true
    }
  }
}
```

Supported transport values are `stdio`, `sse`, `http`, and `ws`.

Permission behavior for MCP tools is conservative by default:

- untrusted servers default to execute-style approval
- trusted servers can map individual tools to `read`, `write`, or `execute`
- `exclude_tools` hides discovered tools from the model

Discovered MCP tools are exposed with stable names like `mcp__github__search_issues`. Run `/status` to see which servers connected, which failed, and how many tools each server exported.

### Debug logging

Launch with debug capture:

```bash
NAMI_DEBUG=1 nami
```

Or enable it inside the TUI:

```text
/debug
```

Debug logs are written to:

```text
~/.config/nami/sessions/<session-id>/debug.log
```

Inspect manually:

```bash
nami debug-view --file ~/.config/nami/sessions/<session-id>/debug.log
tail -F ~/.config/nami/sessions/<session-id>/debug.log | jq .
```

## Repository Layout

```text
nami/    Go engine, CLI, TUI launcher, install script
web/     Project website and docs page assets
docs/    Architecture and integration guides
reference/  Reference material and external notes
```

## Internal Architecture

```text
┌──────────────────────────────┐
│  nami (JS launcher)          │  ← Terminal UI
│    Renders TUI, handles I/O  │
│         │ stdin/stdout NDJSON│
│  ┌──────▼─────────────────┐  │
│  │ nami-engine (Go)       │  │  ← LLM client, tools, agent loop
│  │  Streams events out    │  │
│  │  Reads commands in     │  │
│  └────────────────────────┘  │
└──────────────────────────────┘
```

The launcher shim, `nami.js`, and `nami-engine` should live in the same directory, or `nami-engine` must be in `PATH`.

## Building from Source

Requires: Go 1.26+, Bun 1.0+ for local builds

```bash
cd nami/tui
bun install --frozen-lockfile
bun run setup
bun run start

make release-local
make release
make install
```

`make release` writes GitHub-release-ready artifacts under `nami/tui/release/`.

## License

See [LICENSE](./LICENSE).
