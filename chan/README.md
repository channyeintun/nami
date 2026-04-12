# gocode

An agentic coding CLI powered by LLMs. Think, plan, and execute code changes from your terminal.

## Prerequisites

- macOS or Linux
- Bun 1.0+ to run the `gocode` launcher
- One configured model provider: set the matching API key for Anthropic, OpenAI, Google, DeepSeek, Groq, or Mistral; or install Ollama for local models
- Go 1.26+ only if you are building from source or rebuilding `gocode-engine` locally

## Install

### macOS / Linux (one command)

```bash
curl -fsSL https://raw.githubusercontent.com/channyeintun/gocode/main/gocode/install.sh | sh
```

This script downloads prebuilt `gocode` and `gocode-engine` release assets from GitHub Releases; it does not build from source. Current releases install a Bun launcher plus the Go engine, so Bun 1.0+ must already be installed on the machine where you run `gocode`. If prebuilt assets are unavailable for your platform, this command will fail, so use the manual install flow below instead.

The installer chooses a writable install directory automatically:

- `/usr/local/bin` if it is writable
- `~/.local/bin` otherwise

It installs two executables: `gocode` and `gocode-engine`.

After install, verify:

```bash
command -v gocode
```

If your shell still cannot find it, add the install directory to your PATH. For example:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

### Manual install

If you already have local `gocode` and `gocode-engine` executables, copy both files to a directory in your `PATH`:

```bash
sudo install -m 755 gocode /usr/local/bin/gocode
sudo install -m 755 gocode-engine /usr/local/bin/gocode-engine
```

`install -m 755` is used instead of `cp` so the binary is copied and marked executable in one step.

If you do not want to use `sudo`, install to a user-owned directory instead:

```bash
mkdir -p "$HOME/.local/bin"
install -m 755 gocode "$HOME/.local/bin/gocode"
install -m 755 gocode-engine "$HOME/.local/bin/gocode-engine"
```

Then make sure `~/.local/bin` is on your PATH:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

If you are working from a local clone and want to install the current build directly, use the built release directory:

```bash
cd gocode/tui
make release-local
mkdir -p "$HOME/.local/bin"
install -m 755 release/gocode "$HOME/.local/bin/gocode"
install -m 755 release/gocode-engine "$HOME/.local/bin/gocode-engine"
export PATH="$HOME/.local/bin:$PATH"
```

That local-clone build installs a Bun launcher plus the Go engine, so Bun must remain installed on the machine where you run `gocode`.

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

Start `gocode`, then run:

```text
/connect
```

`gocode` will:

- print the GitHub verification URL and device code in the transcript
- try to open the verification URL in your browser automatically
- wait for you to finish the GitHub authorization flow
- save the resulting credentials in `~/.config/gocode/config.json`
- switch the main model to `github-copilot/gpt-5.4`
- set the subagent model to `github-copilot/claude-haiku-4.5`

For GitHub Enterprise, pass the domain explicitly:

```text
/connect github-copilot your-company.example
```

After the first successful `/connect`, future `gocode` launches can use GitHub Copilot directly without reconnecting unless your saved GitHub authorization is revoked.

## Usage

```bash
gocode
```

That's it. You'll see a terminal UI with a prompt. Type your request and press Enter.

### First-Class Outputs

Artifacts are first-class outputs in `gocode`, not just long-text spill buckets. When the agent produces durable structured work, it should persist that work as an artifact so it remains reviewable in the artifact panel.

- Implementation plans are saved as explicit review artifacts before execution begins.
- Task lists and walkthroughs capture ongoing progress and completed-work summaries across turns.
- Large `web_fetch` and `git diff` results are routed into dedicated artifacts instead of overwhelming the transcript.
- The recent artifact panel favors durable review outputs such as plans, task lists, and walkthroughs; transient diff previews do not stay pinned there.
- Artifact bodies are intended to be resumed, revised, and inspected as part of the normal workflow.

### Options

```
gocode --model openai/gpt-4o        # Use a different model
gocode --model ollama/gemma3         # Use a local model via Ollama
gocode --mode fast                   # Skip planning, execute directly
gocode --help                        # Show help
```

### Slash Commands

| Command              | Description                                    |
| -------------------- | ---------------------------------------------- |
| `/connect`           | Connect GitHub Copilot with device login       |
| `/plan`              | Switch to plan mode (read-only until approved) |
| `/fast`              | Switch to fast mode (direct execution)         |
| `/model [name]`      | Show or switch the active model                |
| `/reasoning [level]` | Show or set GPT-5 reasoning effort             |
| `/cost`              | Show token usage and cost breakdown            |
| `/usage`             | Alias for `/cost`                              |
| `/compact`           | Compact the conversation to save context       |
| `/resume [id]`       | Resume a previous session                      |
| `/clear`             | Clear the conversation and start a new session |
| `/status`            | Show the current session status                |
| `/sessions`          | List recent sessions                           |
| `/diff [args]`       | Show git diff (for example `/diff --staged`)   |
| `/help`              | Show the slash-command help text               |

### Permission System

When the agent wants to run a command or write a file, you'll see a permission prompt:

```
╭─ Permission Required ──────────────────────╮
│ bash: git status                            │
│ Risk: execute                               │
│                                             │
│ [y] Allow  [n] Deny  [a] Always Allow       │
│ [s] Allow Safe (This Session)               │
╰─────────────────────────────────────────────╯
```

| Key | Action                                                                              |
| --- | ----------------------------------------------------------------------------------- |
| `y` | Allow this one command                                                              |
| `n` | Deny this command                                                                   |
| `a` | Always allow this exact command                                                     |
| `s` | Allow future read-only requests and non-destructive shell commands for this session |

Destructive commands (`rm -rf`, `git push --force`, `DROP TABLE`, etc.) always require explicit approval, even with `[s]`.

## Tools

The agent has access to:

| Tool                           | Description                                                                                                     |
| ------------------------------ | --------------------------------------------------------------------------------------------------------------- |
| **agent**                      | Spawn bounded child agents with stable invocation lineage and hook-aware child lifecycle handling               |
| **agent_status**               | Check a background child agent and retrieve structured child status, including stop-block metadata when present |
| **agent_stop**                 | Request a background child agent to stop and return its latest structured lifecycle status                      |
| **bash**                       | Execute shell commands                                                                                          |
| **think**                      | Record scratchpad reasoning with no side effects                                                                |
| **list_dir**                   | List directory contents as structured JSON                                                                      |
| **create_file**                | Create a new file and fail if it already exists                                                                 |
| **file_read**                  | Read text files with range support and safer partial-read guidance                                              |
| **file_write**                 | Overwrite the full contents of an existing file                                                                 |
| **file_edit**                  | Exact find-and-replace edits in existing files                                                                  |
| **apply_patch**                | Apply structured multi-hunk or multi-file text patches                                                          |
| **multi_replace_file_content** | Apply multiple validated block replacements in one existing file write                                          |
| **file_diff_preview**          | Preview a compact diff against another file or inline content                                                   |
| **glob**                       | Find files by pattern                                                                                           |
| **grep**                       | Search file contents (ripgrep)                                                                                  |
| **go_definition**              | Resolve Go symbol definitions with parser-backed locations                                                      |
| **go_references**              | Find Go identifier references with parser-backed context                                                        |
| **project_overview**           | Summarize repository structure, manifests, and languages                                                        |
| **dependency_overview**        | Summarize dependencies from common project manifests                                                            |
| **symbol_search**              | Find likely symbol definitions across source files                                                              |
| **web_search**                 | Search the web                                                                                                  |
| **web_fetch**                  | Fetch and read a URL                                                                                            |
| **list_commands**              | List background commands with recent activity and unread output previews                                        |
| **command_status**             | Check command metadata, timing, unread output, and state                                                        |
| **send_command_input**         | Send stdin and get the updated background command status                                                        |
| **stop_command**               | Stop a running background command and return final status                                                       |
| **forget_command**             | Remove a retained background command and return final metadata                                                  |
| **file_history**               | Inspect tracked file history, create snapshots, and diff them                                                   |
| **file_history_rewind**        | Restore tracked files to a previous file-history snapshot                                                       |
| **git**                        | Read-only git operations (status, diff, log, blame)                                                             |

### Child Agent Modes

The `agent` tool supports four bounded child-agent modes:

| Mode              | Intended use                                                                      |
| ----------------- | --------------------------------------------------------------------------------- |
| `explore`         | Broad read-only codebase research and architectural investigation                 |
| `search`          | Iterative code discovery that returns compact file-and-line references            |
| `execution`       | Terminal-heavy delegated work such as builds, tests, installs, and log inspection |
| `general-purpose` | Broader delegated work when the task does not fit a specialized mode              |

`explore` remains the default for backward compatibility.

The `search` mode is workspace-focused: it can inspect the repository and report references, but it does not include web search tools.

The `execution` mode is intentionally narrow and non-writing by default. It can run commands and inspect local context, but there is no nested interactive approval flow inside a child session. If the cloned permission policy would require approval for a command, the child agent will report that the action was not approved instead of prompting interactively.

Background child agents continue to surface through `agent_status` and `agent_stop`, and the TUI background agent panel now distinguishes explore, search, execution, and general-purpose runs.

### Edit Tool Selection

- `file_edit`: one exact snippet replacement in one existing file.
- `multi_replace_file_content`: several exact, non-overlapping replacements in one existing file when current line ranges and target text are known.
- `apply_patch`: multi-file, multi-hunk, create/delete, or broader structural edits.
- `file_write`: overwrite the full contents of one existing file.
- `create_file`: create one brand-new file.

## Configuration

Config file: `~/.config/gocode/config.json`

```json
{
  "model": "anthropic/claude-sonnet-4-20250514",
  "default_mode": "plan"
}
```

Environment variables override the config file:

| Variable                 | Description                                      |
| ------------------------ | ------------------------------------------------ |
| `GOCODE_MODEL`           | Model to use                                     |
| `GOCODE_API_KEY`         | API key (overrides provider-specific keys)       |
| `GOCODE_BASE_URL`        | Custom API base URL                              |
| `GOCODE_DEBUG`           | Enable runtime debug logging to the session log  |
| `GOCODE_PERMISSION_MODE` | `default`, `autoApprove`, or `bypassPermissions` |

If you use GitHub Copilot, the config file will also persist Copilot credentials and may include a `subagent_model` field after `/connect` completes.

### Debug Logging

To capture low-level runtime diagnostics for provider streams, tool traffic, IPC, and model-event sequencing, launch `gocode` with:

```bash
GOCODE_DEBUG=1 gocode
```

This writes a structured `debug.log` into the current session directory under `~/.config/gocode/sessions/<session-id>/debug.log`.

## Architecture

```
┌──────────────────────────────┐
│  gocode (Bun launcher)       │  ← Terminal UI (React Ink)
│    Renders TUI, handles I/O  │
│         │ stdin/stdout NDJSON│
│  ┌──────▼────────────────┐   │
│  │ gocode-engine (Go)     │  │  ← LLM client, tools,agent loop
│  │  Streams events out    │  │
│  │  Reads commands in     │  │
│  └────────────────────────┘  │
└──────────────────────────────┘
```

Both executables must be in the same directory (or `gocode-engine` must be in `PATH`). When using released or locally built launcher assets, Bun must also be installed on the target machine.

## Building from Source

Requires: Go 1.26+, Bun 1.0+

```bash
cd gocode/tui
bun install --frozen-lockfile   # Install JS deps from bun.lock
bun run setup                   # Build TS and compile Go engine
bun run start                   # Run the TUI in development

make release-local     # Build a Bun launcher + Go engine for your platform
make release           # Package Bun-launcher release assets + per-platform engines
make install           # Install to /usr/local/bin
```

`make release` writes GitHub-release-ready artifacts under `gocode/tui/release/`, including per-platform tarballs plus direct-upload launcher and engine binaries in `gocode/tui/release/assets/`.

## License

See [LICENSE](../LICENSE).
