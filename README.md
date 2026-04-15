# Chan

An agentic coding CLI powered by LLMs. Think, plan, and execute code changes from your terminal.

Chan combines a terminal UI, a Go-based execution engine, first-class artifacts, and bounded child agents so you can inspect code, plan work, edit safely, and verify results without leaving the terminal.

![Agentic Coding CLI Architecture](./docs/architecture.webp)

## Why Chan

- **Agentic terminal workflow** — chat with your codebase, run tools, edit files, and inspect diffs in one place.
- **Two operating modes** — use **plan** mode for review-first workflows or **fast** mode for direct execution.
- **First-class artifacts** — implementation plans, task lists, walkthroughs, diff previews, and search reports persist as reviewable outputs.
- **Bounded child agents** — delegate exploration, code search, or terminal-heavy work to specialized subagents.
- **Permission gating** — risky or sensitive actions require explicit approval.
- **Multi-provider model support** — works with Anthropic, OpenAI, Google, DeepSeek, Groq, Mistral, Ollama, and GitHub Copilot.

## Architecture & Vision

Chan is built on three core pillars:

1. **TUI (Silvery)** — interactive terminal UX with streaming output, grouped tool transcripts, progress, background task visibility, and artifact panels.
2. **Go Engine** — high-performance backend for the agent loop, tool execution, provider integration, session persistence, and permission gating.
3. **Artifacts** — durable structured outputs that can be reviewed, reopened, revised, and resumed across turns.

## Architecture Docs

- [Lean Retrieval Architecture](./docs/lean-retrieval-architecture.md)
- [Silvery Guide for Chan](./docs/silvery-guide.md)
- [OpenRouter + Ollama Guide](./docs/openrouter-ollama-guide.md)

## Quick Start

### Prerequisites

- macOS or Linux
- Bun 1.0+ to run the `chan` launcher
- One configured model provider: Anthropic, OpenAI, Google, DeepSeek, Groq, Mistral, Ollama, or GitHub Copilot
- Go 1.26+ only if building from source or rebuilding `chan-engine`

### Install

#### macOS / Linux (one command)

```bash
curl -fsSL https://raw.githubusercontent.com/channyeintun/chan/main/chan/install.sh | sh
```

This downloads prebuilt `chan` and `chan-engine` release assets from GitHub Releases. It does **not** build from source.

Current releases install a Bun launcher plus the Go engine, so Bun must already be installed on the target machine.

The installer chooses a writable directory automatically:

- `/usr/local/bin` if writable
- `~/.local/bin` otherwise

After install, verify:

```bash
command -v chan
```

If needed, add the install dir to your `PATH`:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

#### Manual install

If you already have local `chan` and `chan-engine` executables:

```bash
sudo install -m 755 chan /usr/local/bin/chan
sudo install -m 755 chan-engine /usr/local/bin/chan-engine
```

Without `sudo`:

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 chan "$HOME/.local/bin/chan"
install -m 755 chan-engine "$HOME/.local/bin/chan-engine"
export PATH="$HOME/.local/bin:$PATH"
```

If installing from a local clone:

```bash
cd chan/tui
make release-local
mkdir -p "$HOME/.local/bin"
install -m 755 release/chan "$HOME/.local/bin/chan"
install -m 755 release/chan-engine "$HOME/.local/bin/chan-engine"
export PATH="$HOME/.local/bin:$PATH"
```

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
| GitHub Copilot | use `/connect` in Chan   |

### GitHub Copilot setup

GitHub Copilot uses a device-login flow instead of a static API key.

Start Chan, then run:

```text
/connect
```

Chan will:

- print the GitHub verification URL and device code
- try to open the verification page automatically
- wait for authorization to complete
- save credentials in `~/.config/chan/config.json`
- switch the main model to `github-copilot/gpt-5.4`
- set the subagent model to `github-copilot/claude-haiku-4.5`

For GitHub Enterprise:

```text
/connect github-copilot your-company.example
```

## Usage

Start the CLI:

```bash
chan
```

Then type what you want, for example:

- `summarize this repository`
- `find dead code and propose a cleanup plan`
- `add a new flag to this CLI`
- `debug why this test is flaky`

### Common flags

```bash
chan --model openai/gpt-4o
chan --model ollama/gemma3
chan --model ollama/gemma4:e4b
chan --mode fast
chan --help
```

### Slash commands

| Command               | Description                                    |
| --------------------- | ---------------------------------------------- |
| `/connect`            | Connect GitHub Copilot with device login       |
| `/plan`               | Switch to plan mode                            |
| `/fast`               | Switch to fast mode                            |
| `/model [name]`       | Show or switch the active model                |
| `/reasoning [level]`  | Show or set GPT-5 reasoning effort             |
| `/compact`            | Compact conversation to save context           |
| `/resume [id]`        | Resume a previous session                      |
| `/clear`              | Clear the conversation and start fresh         |
| `/status`             | Show current session and MCP server status     |
| `/sessions`           | List recent sessions                           |
| `/diff [args]`        | Show git diff                                  |
| `/debug [subcommand]` | Enable debug logging or inspect its path       |
| `/help`               | Show slash-command help                        |

## First-Class Outputs

Artifacts are central to Chan's workflow.

- **Implementation plans** are saved as review artifacts before execution.
- **Task lists** track multi-step progress across turns.
- **Walkthroughs** summarize completed work and validation.
- Large `web_fetch` and `git diff` outputs are routed into dedicated artifacts so the transcript stays concise.

Artifacts are meant to be reopened, revised, and resumed — not just dumped text.

## Permission System

When Chan wants to run a command or change files, it can ask for approval.

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

Destructive commands and sensitive edits such as `.env`, lockfiles, `.git`, or workspace settings still require explicit approval.

## Tooling

Chan exposes a broad local-tool runtime, including:

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

The `agent` tool supports four bounded modes:

| Mode              | Best for                                                   |
| ----------------- | ---------------------------------------------------------- |
| `explore`         | Broad read-only repo research                              |
| `search`          | Focused code discovery with file/line references           |
| `execution`       | Terminal-heavy tasks such as builds, tests, and log review |
| `general-purpose` | Delegated work that doesn't fit a specialized mode         |

## Configuration

Config file:

```text
~/.config/chan/config.json
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
| `CHAN_MODEL`           | Model to use                                     |
| `CHAN_API_KEY`         | API key override                                 |
| `CHAN_BASE_URL`        | Custom API base URL                              |
| `CHAN_DEBUG`           | Enable runtime debug logging                     |
| `CHAN_PERMISSION_MODE` | `default`, `autoApprove`, or `bypassPermissions` |

If you use GitHub Copilot, config may also persist Copilot credentials and a `subagent_model`.

### MCP servers

Chan can load external MCP servers at startup from either `~/.config/chan/config.json` or `.chan/mcp.json` in the current workspace. The workspace file is merged on top of the user config for the current session, so team-local MCP settings can live in the repo without replacing your personal global setup.

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

Example workspace override in `.chan/mcp.json`:

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
CHAN_DEBUG=1 chan
```

Or enable it inside the TUI:

```text
/debug
```

Debug logs are written to:

```text
~/.config/chan/sessions/<session-id>/debug.log
```

Inspect manually:

```bash
chan debug-view --file ~/.config/chan/sessions/<session-id>/debug.log
tail -F ~/.config/chan/sessions/<session-id>/debug.log | jq .
```

## Repository Layout

```text
chan/    Go engine, CLI, TUI launcher, install script
web/     Project website and docs page assets
docs/    Architecture and integration guides
reference/  Reference material and external notes
```

## Internal Architecture

```text
┌──────────────────────────────┐
│  chan (Bun launcher)         │  ← Terminal UI
│    Renders TUI, handles I/O  │
│         │ stdin/stdout NDJSON│
│  ┌──────▼─────────────────┐  │
│  │ chan-engine (Go)       │  │  ← LLM client, tools, agent loop
│  │  Streams events out    │  │
│  │  Reads commands in     │  │
│  └────────────────────────┘  │
└──────────────────────────────┘
```

Both executables must be in the same directory, or `chan-engine` must be in `PATH`.

## Building from Source

Requires: Go 1.26+, Bun 1.0+

```bash
cd chan/tui
bun install --frozen-lockfile
bun run setup
bun run start

make release-local
make release
make install
```

`make release` writes GitHub-release-ready artifacts under `chan/tui/release/`.

## License

See [LICENSE](./LICENSE).
