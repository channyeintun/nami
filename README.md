# Nami

An agentic coding CLI powered by LLMs. Think, plan, and execute code changes from your terminal.

Nami combines a terminal UI, a Go-based execution engine, first-class artifacts, and bounded child agents so you can inspect code, plan work, edit safely, and verify results without leaving the terminal.

![Nami](./docs/nami.webp)

## Why Nami

- **Agentic terminal workflow** — chat with your codebase, run tools, and edit files in one place.
- **Two operating modes** — use **plan** mode for review-first workflows or **fast** mode for direct execution.
- **First-class artifacts** — implementation plans, task lists, walkthroughs, and search reports persist as reviewable outputs.
- **Bounded child agents** — delegate exploration, code search, or terminal-heavy work to specialized subagents.
- **Permission gating** — risky or sensitive actions require explicit approval.
- **Multi-provider model support** — works with Anthropic, OpenAI, Codex, Google, DeepSeek, Qwen, GLM, Groq, Mistral, Ollama, and GitHub Copilot.

## Architecture & Vision

Nami is built on three core pillars:

1. **TUI (Silvery)** — interactive terminal UX with streaming output, tool transcripts, progress, background task visibility, and artifact panels.
2. **Go Engine** — high-performance backend for the agent loop, tool execution, provider integration, session persistence, and permission gating.
3. **Artifacts** — durable structured outputs that can be reviewed, revised, and resumed across turns.

## Architecture Docs

- [Lean Retrieval Architecture](./docs/lean-retrieval-architecture.md)
- [Orchestration and Goal Loops](./docs/orchestration-and-goal-loops.md)
- [Silvery Guide for Nami](./docs/silvery-guide.md)

## Quick Start

### Prerequisites

- macOS, Linux, or Windows 11
- One supported JavaScript runtime to run the `nami` launcher: Node.js 24 or newer, Bun, or Deno. The Windows installer can bootstrap a local Node.js runtime automatically if none is already available.
- One configured model provider: Anthropic, OpenAI, Codex, Google, DeepSeek, Qwen, GLM, Groq, Mistral, Ollama, or GitHub Copilot
- Go 1.27.0 only if building from source or rebuilding `nami-engine`

### Install

#### macOS / Linux (one command)

```bash
curl -fsSL https://raw.githubusercontent.com/channyeintun/nami/main/nami/install.sh | sh
```

This downloads prebuilt `nami` and `nami-engine` release assets from GitHub Releases. It does **not** build from source.

Current releases install a launcher shim, a portable `nami.js` bundle, and the Go engine.

You need one of these runtimes on your `PATH` to run the installed launcher:

- `node` 24 or newer (older releases cannot run the TUI's renderer)
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

If `node` 24 or newer, `bun`, or `deno` is already on your `PATH`, the installer reuses it. If not, it downloads a local Node.js runtime (the current LTS) automatically and wires `nami` to use it.

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

You also need one supported runtime on your `PATH`: `node` 24 or newer, `bun`, or `deno`.

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
make install PREFIX="$HOME/.local/bin"
export PATH="$HOME/.local/bin:$PATH"
```

`make install` runs `make release-local`, then copies the launcher shim, `nami.js`, and `nami-engine` into `PREFIX`.

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
| Codex          | `CODEX_ACCESS_TOKEN` or `/connect codex` |
| OpenAI         | `OPENAI_API_KEY`         |
| Google         | `GEMINI_API_KEY`         |
| DeepSeek       | `DEEPSEEK_API_KEY` for `deepseek/deepseek-v4-flash` or `deepseek/deepseek-v4-pro` |
| Qwen           | `DASHSCOPE_API_KEY`      |
| GLM            | `GLM_API_KEY`            |
| Groq           | `GROQ_API_KEY`           |
| Mistral        | `MISTRAL_API_KEY`        |
| Ollama         | none — runs locally      |
| GitHub Copilot | use `/connect github-copilot` in Nami |

### GitHub Copilot setup

GitHub Copilot uses a device-login flow instead of a static API key.

Start Nami, then run `/connect github-copilot` (plain `/connect` opens a provider picker where you can choose it):

```text
/connect github-copilot
```

Nami will:

- print the GitHub verification URL and device code
- try to open the verification page automatically
- wait for authorization to complete
- save credentials in Nami's config file (see [Configuration](#configuration) for where it lives)
- switch the main model to `github-copilot/gpt-5.4`
- set the subagent model to `github-copilot/claude-haiku-4.5`

For GitHub Enterprise:

```text
/connect github-copilot your-company.example
```

### Codex setup

Codex uses ChatGPT OAuth or a bearer token.

```text
/connect codex
/connect codex headless
```

For manual token setup:

```bash
export CODEX_ACCESS_TOKEN="..."
```

Then run:

```text
/connect codex env
```

Use `codex/gpt-5.5` to select the Codex provider explicitly. Bare `gpt-5.4` stays the default OpenAI model selection, and bare `gpt-5.5` remains available as another OpenAI model selection.

DeepSeek defaults to `deepseek/deepseek-v4-flash`. Use `deepseek/deepseek-v4-pro` when you want the Pro v4 model explicitly.

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
nami --model deepseek/deepseek-v4-flash
nami --model ollama/gemma3
nami --model ollama/gemma4:e4b
nami --mode fast
nami --auto-mode
nami --help
```

Without `--model` (or `NAMI_MODEL`), Nami starts on the model you used last, else the `model` in your [config file](#configuration), else `anthropic/claude-sonnet-5`. If that model's provider is not set up, it switches to the first provider that is and says so.

Without `--mode`, it starts in the `default_mode` from your config file, else in plan mode.

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

Supported scopes, chosen with `--scope` (`-s`):

- `project`, the default for `add` and `add-json`, writes repo-local MCP config to `.nami/mcp.json` at the root of the current git repository; outside a repository, use `--scope user`
- `user` writes user MCP config to Nami's config file (see [Configuration](#configuration))

Notes:

- `add` supports `stdio`, `http`, `sse`, and `ws` transports
- `--env KEY=value` applies to `stdio` servers
- `--header 'Key: Value'` applies to `http`, `sse`, and `ws` servers
- `list` and `get` attempt real MCP connections, so listing a repo-scoped `stdio` server will spawn it briefly to inspect health and capabilities

### Slash commands

| Command               | Description                                    |
| --------------------- | ---------------------------------------------- |
| `/connect [provider]` | Connect provider auth and switch providers; with no provider, pick one from a list |
| `/providers`          | Show which providers are set up and usable     |
| `/logout [provider]`  | Clear stored `github-copilot` or `codex` credentials, or both (`all`, the default) |
| `/plan`               | Switch to plan mode                            |
| `/fast`               | Switch to fast mode                            |
| `/model [name]`       | Show or switch the active model                |
| `/subagent [model]`   | Show or switch the model child agents use      |
| `/reasoning [level]`  | Show or set reasoning effort: `low`, `medium`, `high`, `xhigh`, or `default` |
| `/compact`            | Compact conversation to save context           |
| `/rewind`             | Jump back to an earlier turn and drop what followed it |
| `/resume [id]`        | Resume a previous session                      |
| `/clear`              | Clear the conversation and start fresh         |
| `/status`             | Show current session and MCP server status     |
| `/sessions`           | List recent sessions                           |
| `/tasks`              | Open the background tasks dialog               |
| `/workflows`          | Show workflow runs and saved workflows         |
| `/debug [subcommand]` | Enable debug logging or inspect its path       |
| `/goal [condition]`   | Keep working until a condition holds           |
| `/help`               | Show slash-command help                        |

Skills, the Markdown playbooks in a project's `.agents/` directory or the `agents/` directory beside the config file, also run as slash commands: `/<skill-name> [instructions]`.

#### `/goal` — keep working until a condition holds

`/goal <condition>` turns one request into a loop. When the agent tries to end its
turn, the condition is judged against what the transcript shows actually happened —
commands run, files changed, output observed. If it does not hold yet, the turn
stays open and the agent is told the specific gap. The goal clears itself the
moment it is satisfied.

```text
/goal every test in ./... passes and go vet is clean
/goal            # show the active goal and the last check
/goal clear      # drop it early
```

The judge decides from evidence rather than from the agent's own account: an agent
that has stalled will report success, so its claim of completion — and equally its
claim that the goal is impossible — counts as evidence, not proof.

Two backstops keep a goal from trapping the session:

- If the judge cannot answer at all — an API error, a timeout, a reply that will not
  parse — the turn ends normally. A judge that cannot answer must never be able to
  hold the loop open.
- A goal that blocks eight times in a row without the agent using a tool in between
  yields the turn and stays set, so your next message resumes it. Any real work in
  between refills that budget, because the cap exists to catch a loop that is
  spinning, not one that is merely long. Set `NAMI_GOAL_BLOCK_CAP` to tune it, or
  `0` to disable it.

The active goal shows in the status bar, since while it is set the session behaves
differently: the agent will not stop on its own.

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
│ bash: go test ./...                        │
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
| `agent_team`                     | Launch a team of independent child agents             |
| `workflow`                       | Run a JavaScript workflow of child agents in the background |
| `workflow_status`                | Inspect a workflow run, or wait for it to finish      |
| `workflow_stop`                  | Stop a running workflow                               |
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

### Dynamic workflows

`agent` delegates one task and `agent_team` several independent ones. When the
orchestration itself should be deterministic code — fan out over items a step
discovered, verify each finding on its own, loop until a check passes — the
`workflow` tool runs a JavaScript script that drives child agents:

```js
export const meta = {
  name: 'verify-bugs',
  description: 'Find likely bugs per package and verify each one',
  phases: [{ title: 'Find' }, { title: 'Verify' }],
}

const BUGS = {
  type: 'object',
  properties: {
    bugs: {
      type: 'array',
      items: {
        type: 'object',
        properties: { file: { type: 'string' }, claim: { type: 'string' } },
        required: ['file', 'claim'],
      },
    },
  },
  required: ['bugs'],
}
const VERDICT = {
  type: 'object',
  properties: { real: { type: 'boolean' }, reason: { type: 'string' } },
  required: ['real', 'reason'],
}

const perPackage = await pipeline(args.packages,
  pkg => agent(`List likely bugs in ${pkg}.`,
    { label: `find ${pkg}`, phase: 'Find', schema: BUGS, agentType: 'Explore' }),
  found => parallel((found?.bugs ?? []).map(bug => () =>
    agent(`Try to refute: ${bug.claim} (${bug.file}). Answer real=false if unsure.`,
      { label: `verify ${bug.file}`, phase: 'Verify', schema: VERDICT })
      .then(verdict => (verdict?.real ? bug : null)))))

return perPackage.flat().filter(Boolean)
```

Run with `args` set to `{"packages": ["internal/auth", "internal/api"]}`, this finds
bugs in both packages at once and starts verifying each package's findings as soon as
that package's search is done. You rarely write the call yourself: describe the
orchestration and the agent writes the script, or save a script (below) and ask for
it by name. The script comes inline (`script`), from a `.js` file (`script_path`), or
from a saved workflow (`name`).

A script begins with a pure-literal `meta` export (no variables, calls, or
interpolation), and its body runs inside an async function, so it can `await` at top
level and `return` a JSON value. Plain JavaScript only; TypeScript annotations fail
to parse. The hooks:

- `agent(prompt, opts?)` runs a child agent and resolves to its final text, or, with
  `opts.schema`, to an object validated against that JSON Schema, whose root must be
  `{type: 'object', properties: {...}}`. A failed agent resolves to `null`, so filter
  results with `.filter(Boolean)`. Options: `label`, `phase`, `schema`, `model`
  (`provider/model`; defaults to the `/subagent` model), `effort`, and `agentType`
  (`general-purpose` by default, `Explore`, or `verification`).
- `pipeline(items, ...stages)` runs each item through every stage on its own, with no
  barrier between stages; a stage gets `(previous, item, index)`. Prefer it for
  multi-stage work.
- `parallel(tasks)` runs functions concurrently and waits for all of them. Use it when
  the next step needs every result at once.
- `phase(title)` and `log(message)` (or `console.log`) report progress.
- `workflow(name | {scriptPath}, args)` runs a saved workflow or a script file inline
  and returns its value, one level deep.
- `args` is the tool's `args` input, verbatim.
- `budget` is `{total, spent(), remaining()}` in output tokens, from the tool's
  `token_budget`.

A `parallel()` task or `pipeline()` stage that throws turns its item into `null`, and
a throw anywhere else fails the run. A run whose script returns counts as completed
even if some of its agents failed: the script decides what failure means.

**Background runs.** The `workflow` call returns a `run_id` at once, and the run goes
on in the background, across turns. A line above the input shows each running
workflow: its current phase, finished and total agents, what is running, how many
failed, and its latest log line. When a run completes or fails, the agent receives a
`<task-notification>` with a preview of the result or the error as soon as the
conversation is idle, and can call `workflow_status` for the full snapshot;
`workflow_stop` stops a run. `/workflows` opens a dialog with the session's runs
(phases, agents grouped by phase, logs, the result, and the files on disk) and the
saved workflows; press `x` on a running run to stop it. Each run keeps its script, journal, and `result.json` under
`sessions/<session-id>/workflows/<run_id>/` beside the config file.

**Resume.** Every successful `agent()` result is journaled. Pass a previous `run_id`
from the same session as `resume_from_run_id`, with the same script or an edited
one: unchanged calls replay instantly and failed ones run again. A changed call runs
live, and so does every call made after a re-run agent has returned, because that
agent may have changed files the later steps check.

**Sandbox and limits.**

- Scripts have no filesystem, network, process, or timer access. Every side effect
  goes through a child agent and Nami's permission rules.
- Child agents cannot ask for approval, so in the default permission mode they cannot
  edit files or run commands that are not read-only. Launching a workflow goes through
  the same approval as other execute-level tools.
- Children see the project instructions but nothing of the conversation: put
  everything a step needs, earlier results included, in its prompt.
- `Date.now()`, `Math.random()`, and `new Date()` without arguments throw, because
  resume matches calls by their prompts. Pass timestamps or seeds in through `args`.
- `isolation: 'worktree'` is not supported: worktree agents change the working
  directory of the whole engine, which is unsafe while a run goes on beside the
  conversation. For the same reason, while a workflow runs Nami refuses
  `enter_worktree`, `exit_worktree`, worktree child agents, and a `/resume` into
  a session in another directory.
- A run makes at most 1000 `agent()` calls, and `parallel()` and `pipeline()` take at
  most 4096 items. Agents beyond the concurrency limit (the CPU count minus two,
  between 2 and 16) queue, and all runs share that one limit. With `token_budget`
  set, once the run's agents have spent it `agent()` throws and agents still
  queued resolve to `null` without starting; agents already running finish.

#### Saved workflows

Save a script as `.nami/workflows/<name>.js` at the root of the current git
repository to share it with the project, or as `workflows/<name>.js` in Nami's
config directory (see [Configuration](#configuration)) to keep it for yourself. The
name is the file name without `.js`, matched without regard to case, and a project
workflow overrides a user one with the same name. Saved workflows are listed in the
`workflow` tool's description with their `whenToUse` (or `description`), so the
agent knows when to reach for them. It runs one with the `name` input, and a script
can call one with `workflow('<name>', args)`.

The design and the reasoning behind it are in
[Orchestration and Goal Loops](./docs/orchestration-and-goal-loops.md).

## Configuration

Config file, in your platform's user config directory:

| Platform | Path                                                                  |
| -------- | --------------------------------------------------------------------- |
| Linux    | `~/.config/nami/config.json` (or `$XDG_CONFIG_HOME/nami/config.json`) |
| macOS    | `~/Library/Application Support/nami/config.json`                      |
| Windows  | `%APPDATA%\nami\config.json`                                          |

Sessions, debug logs, user-global skills, and user [saved workflows](#saved-workflows) (in `workflows/`) live in the same `nami` directory.

Example:

```json
{
  "model": "anthropic/claude-sonnet-4-6",
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

Nami can load external MCP servers at startup from either the user config file or `.nami/mcp.json` at the root of the current git repository. The workspace file is merged on top of the user config for the current session, so team-local MCP settings can live in the repo without replacing your personal global setup.

Example user config:

```json
{
  "model": "anthropic/claude-sonnet-4-6",
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

Debug logs are written to `sessions/<session-id>/debug.log` beside the config file; `/debug path` prints the exact path. On Linux that is:

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
```

Some guides also mention `reference/`, a gitignored directory for local checkouts of external projects; it is not part of the repository.

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

Requires: Go 1.27.0, Node.js 24 or newer, and Vite+ `vp` for local builds

```bash
cd nami/tui
vp install
vp run setup
vp run start

make release-local
make release
make install
```

`make release` writes GitHub-release-ready artifacts under `nami/tui/release/`.

## License

See [LICENSE](./LICENSE).
