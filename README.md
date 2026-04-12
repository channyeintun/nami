# Chan

An agentic coding CLI powered by LLMs. Think, plan, and execute code changes from your terminal.

## Architecture & Vision

`Chan` is built on three core pillars:

1.  **TUI (React/Ink):** A highly interactive terminal UI that provides real-time feedback, grouped tool execution transcripts, and dynamic progress indicators.
2.  **Go Engine:** A high-performance backend that handles the agent loop, tool execution (bash, file system, git, search), and project-level orchestration with built-in permission gating.
3.  **Artifacts:** First-class outputs in the runtime, not just overflow containers. Plans, task lists, walkthroughs, search reports, diff previews, and other structured work products are persisted, reviewable, and surfaced in a dedicated panel.

![Agentic Coding CLI Architecture](./docs/architecture.png)

## Prerequisites

- macOS or Linux
- Bun 1.0+ to run the `chan` launcher
- One configured model provider: set the matching API key for Anthropic, OpenAI, Google, DeepSeek, Groq, or Mistral; or install Ollama for local models
- Go 1.26+ only if you are building from source or rebuilding `chan-engine` locally

## Install

### macOS / Linux (one command)

```bash
curl -fsSL https://raw.githubusercontent.com/channyeintun/chan/main/chan/install.sh | sh
```

This script downloads prebuilt `chan` and `chan-engine` release assets from GitHub Releases; it does not build from source. Current releases install a Bun launcher plus the Go engine, so Bun 1.0+ must already be installed on the machine where you run `chan`. If prebuilt assets are unavailable for your platform, this command will fail, so use the manual install flow below instead.

The installer chooses a writable install directory automatically:

- `/usr/local/bin` if it is writable
- `~/.local/bin` otherwise

It installs two executables: `chan` and `chan-engine`.

After install, verify:

```bash
command -v chan
```

If your shell still cannot find it, add the install directory to your PATH. For example:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

### Manual install

If you already have local `chan` and `chan-engine` executables, copy both files to a directory in your `PATH`:

```bash
sudo install -m 755 chan /usr/local/bin/chan
sudo install -m 755 chan-engine /usr/local/bin/chan-engine
```

`install -m 755` is used instead of `cp` so the binary is copied and marked executable in one step.

If you do not want to use `sudo`, install to a user-owned directory instead:

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 chan "$HOME/.local/bin/chan"
install -m 755 chan-engine "$HOME/.local/bin/chan-engine"
```

Then make sure `~/.local/bin` is on your PATH:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

If you are working from a local clone and want to install the current build directly, use the built release directory:

```bash
cd chan/tui
make release-local
mkdir -p "$HOME/.local/bin"
install -m 755 release/chan "$HOME/.local/bin/chan"
install -m 755 release/chan-engine "$HOME/.local/bin/chan-engine"
export PATH="$HOME/.local/bin:$PATH"
```

That local-clone build installs a Bun launcher plus the Go engine, so Bun must remain installed on the machine where you run `chan`.

## Setup

Set your API key:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```

Supported providers and their environment variables:

| Provider       | Env Variable          |
| -------------- | --------------------- |
| Anthropic      | `ANTHROPIC_API_KEY`   |
| GitHub Copilot | (use `/connect`)      |
| OpenAI         | `OPENAI_API_KEY`      |
| Google         | `GEMINI_API_KEY`      |
| DeepSeek       | `DEEPSEEK_API_KEY`    |
| Groq           | `GROQ_API_KEY`        |
| Mistral        | `MISTRAL_API_KEY`     |
| Ollama         | (none — runs locally) |

### GitHub Copilot Setup

GitHub Copilot uses an interactive device-login flow instead of a static API key.

Start `chan`, then run:

```text
/connect
```

`chan` will:

- print the GitHub verification URL and device code in the transcript
- try to open the verification URL in your browser automatically
- wait for you to finish the GitHub authorization flow
- save the resulting credentials in `~/.config/chan/config.json`
- switch the main model to `github-copilot/gpt-5.4`
- set the subagent model to `github-copilot/claude-haiku-4.5`

For GitHub Enterprise, pass the domain explicitly:

```text
/connect github-copilot your-company.example
```

After the first successful `/connect`, future `chan` launches can use GitHub Copilot directly without reconnecting unless your saved GitHub authorization is revoked.

## Usage

```bash
chan
```

That's it. You'll see a terminal UI with a prompt. Type your request and press Enter.

### First-Class Outputs

Artifacts are first-class outputs in `Chan`. When the agent produces durable structured work, it should save that work as an artifact instead of leaving it only in the chat transcript.

- Implementation plans are saved as reviewable plan artifacts before execution.
- Task lists and walkthroughs persist multi-step progress and completed-work summaries.
- Large `web_fetch` and `git diff` results are routed into dedicated artifacts so the transcript can stay concise.
- Artifact content is meant to be reopened, revised, resumed, and inspected across turns.

### Options

```
chan --model openai/gpt-4o        # Use a different model
chan --model ollama/gemma3         # Use a local model via Ollama
chan --mode fast                   # Skip planning, execute directly
chan --help                        # Show help
```

### Slash Commands

| Command      | Description                                |
| ------------ | ------------------------------------------ |
| `/connect`   | Connect GitHub Copilot with device login   |
| `/plan`      | Switch to plan mode (think before writing) |
| `/fast`      | Switch to fast mode (execute directly)     |
| `/model <m>` | Change model (e.g. `/model openai/gpt-4o`) |
| `/cost`      | Show token usage and cost                  |
| `/compact`   | Compress conversation to free up context   |
| `/resume`    | Resume a previous session                  |

### Permission System

When the agent wants to run a command or write a file, you'll see a permission prompt:

```
╭─ Permission Required ───────────────────────╮
│ bash: git status                            │
│ Risk: execute                               │
│                                             │
│ [y] Allow  [n] Deny  [a] Always Allow       │
│ [s] Allow All (This Session)                │
╰─────────────────────────────────────────────╯
```

| Key | Action                                              |
| --- | --------------------------------------------------- |
| `y` | Allow this one command                              |
| `n` | Deny this command                                   |
| `a` | Always allow this exact command                     |
| `s` | Allow all non-destructive commands for this session |

Destructive commands (`rm -rf`, `git push --force`, `DROP TABLE`, etc.) always require explicit approval, even with `[s]`.

Background shell sessions are also supported through the `bash` tool by setting `background=true`, then following up with `command_status` and `send_command_input` using the returned `CommandId`.

## Tools

The agent has access to:

| Tool            | Description                                         |
| --------------- | --------------------------------------------------- |
| **bash**        | Execute shell commands                              |
| **create_file** | Create a new file and fail if it already exists     |
| **file_read**   | Read text file contents                             |
| **file_write**  | Overwrite the full contents of an existing file     |
| **file_edit**   | Find-and-replace edits in existing files            |
| **apply_patch** | Apply structured multi-hunk or multi-file patches   |
| **glob**        | Find files by pattern                               |
| **grep**        | Search file contents (ripgrep)                      |
| **web_search**  | Search the web                                      |
| **web_fetch**   | Fetch and read a URL                                |
| **git**         | Read-only git operations (status, diff, log, blame) |

### Edit Tool Selection

- `file_edit`: one exact snippet replacement in one existing file.
- `apply_patch`: multi-hunk, multi-file, or structural edits.
- `file_write`: full overwrite of one existing file.
- `create_file`: create one brand-new file.

## Configuration

Config file: `~/.config/chan/config.json`

```json
{
  "model": "anthropic/claude-sonnet-4-20250514",
  "default_mode": "plan"
}
```

Environment variables override the config file:

| Variable                 | Description                                                      |
| ------------------------ | ---------------------------------------------------------------- |
| `CHAN_MODEL`           | Model to use                                                     |
| `CHAN_API_KEY`         | API key (overrides provider-specific keys)                       |
| `CHAN_BASE_URL`        | Custom API base URL                                              |
| `CHAN_PERMISSION_MODE` | `default`, `autoApprove`, or `bypassPermissions`                 |
| `USE_LOCAL_MODEL`        | Opt in to using Ollama for internal helper tasks like compaction |

`USE_LOCAL_MODEL` does not change the main chat model. It only enables local routing for internal helper tasks that are already wired for it. Right now that means compaction.

If you use GitHub Copilot, the config file will also persist Copilot credentials and may include a `subagent_model` field after `/connect` completes.

## Architecture

```
┌──────────────────────────────┐
│  chan (Bun launcher)         │  ← Terminal UI (React Ink)
│    Renders TUI, handles I/O  │
│         │ stdin/stdout NDJSON│
│  ┌──────▼─────────────────┐  │
│  │ chan-engine (Go)       │  │  ← LLM client, tools, agent loop
│  │  Streams events out    │  │
│  │  Reads commands in     │  │
│  └────────────────────────┘  │
└──────────────────────────────┘
```

Both executables must be in the same directory (or `chan-engine` must be in `PATH`). When using released or locally built launcher assets, Bun must also be installed on the target machine.

## Building from Source

Requires: Go 1.26+, Bun 1.0+

```bash
cd chan/tui
bun install --frozen-lockfile   # Install JS deps from bun.lock
bun run setup                   # Build TS and compile Go engine
bun run start                   # Run the TUI in development

make release-local     # Build a Bun launcher + Go engine for your platform
make release           # Package Bun-launcher release assets + per-platform engines
make install           # Install to /usr/local/bin
```

## License

See [LICENSE](LICENSE).
