---
name: go-delve-debugging
description: 'Debug Go programs with Delve (`dlv`). Use for panics, hangs, deadlocks, wrong control flow, goroutine analysis, conditional breakpoints, watchpoints, trace/core/replay workflows, headless or DAP sessions, and any case where runtime evidence matters more than static reasoning.'
argument-hint: 'mode and target, for example: "debug ./cmd/server -- --config dev.yaml" or "attach 12345"'
user-invocable: true
---

# Go Delve Debugging

Use this skill when a Go bug depends on actual runtime state. Prefer a real Delve session over speculation when the problem involves control flow, concurrency, stack state, or values that only exist while the program is running.

## When to Use

- Panics, crashes, or unexpected exits.
- Hangs, deadlocks, livelocks, or requests that never complete.
- Wrong branch selection or suspicious state transitions.
- Goroutine coordination issues.
- Failing Go tests that need live inspection.
- Post-mortem debugging from a core dump.
- Lightweight function tracing when you need call flow without a full stop-and-step session.
- Non-deterministic failures that benefit from replay or checkpoints.
- Container, remote, or multi-terminal debugging where headless Delve or DAP is the right transport.
- Cases where variable values, frames, or goroutine state are more useful than reading more code.

## Default Rule

Collect debugger evidence before proposing a fix.

At minimum, capture the stop reason plus the relevant stack, goroutine, variable, tracepoint, or replay state. Do not keep reasoning abstractly once a live debugger session is cheap to start.

## Setup

If `dlv` is not already on `PATH`, install it:

```bash
go install github.com/go-delve/delve/cmd/dlv@latest
```

If this workspace has a local Delve checkout at `reference/delve` (that directory is gitignored, so a fresh clone has none), `cd reference/delve && make build` builds `reference/delve/dlv` instead; use that path wherever the commands below say `dlv`.

## Choose the Right Mode

- `debug` builds and debugs a Go package.
- `test` builds and debugs a Go test binary.
- `exec` debugs an existing binary.
- `attach` debugs a running process.
- `core` inspects a core dump or minidump.
- `replay` inspects an `rr` recording.
- `trace` traces matching functions without entering a full interactive session.
- `--headless <command>` starts a server for `dlv connect`, JSON-RPC clients, or remote DAP attach to an already-started target. Use `--api-version=2` and `--accept-multiclient` when reconnects matter.
- `dap` starts a DAP-only server. Use it when the client speaks DAP directly and will launch or attach itself. Do not pair `dlv dap` with `dlv connect`.

## Procedure

1. Reproduce the problem with the smallest command that still shows the bug.
2. Choose the correct Delve entry mode.
3. If the stop location is not obvious, find it first with `funcs`, `packages`, `sources`, and `list`.
4. Start an interactive or headless session in a persistent terminal.
5. Prefer precise stop conditions over repeated manual stepping:
   - `break <locspec> if <expr>`
   - `condition -hitcount <bp> <op> <n>`
   - `condition -per-g-hitcount <bp> <op> <n>`
   - `watch -w <expr>`
   - `on <bp> trace`
6. Run to the breakpoint, tracepoint, watchpoint, or interrupt the program when it hangs.
7. Inspect evidence in this order when relevant:
   - `stack`
   - `goroutines -t`
   - `goroutines -with running`
   - `goroutines -chan <expr>`
   - `threads`
   - `args`
   - `locals`
   - `print <expr>`
   - `whatis <expr>`
8. Only after gathering evidence, summarize the root cause and decide whether a code change is needed.

## Launch Patterns

Typical launches:

```bash
dlv debug ./cmd/server -- --config dev.yaml
dlv test ./pkg/cache -- -test.run TestEvictExpired
dlv exec ./bin/server -- --config dev.yaml
dlv attach 12345
dlv core ./bin/server ./crash.core
dlv replay /tmp/rr-trace
dlv trace ./cmd/server 'cache.*' --timestamp -s 5
dlv --headless --listen=127.0.0.1:43000 --api-version=2 --accept-multiclient --continue debug ./cmd/server -- --config dev.yaml
dlv connect 127.0.0.1:43000
dlv dap --listen=127.0.0.1:43001
```

Use `dlv dap` only when the client will speak DAP directly. Use `dlv --headless <command>` when you want `dlv connect`, JSON-RPC, or a remote DAP attach session against an already-started target.

If Delve is building the package for you and optimized code is hiding locals or confusing stepping, pass `--build-flags="-gcflags=all=-N -l"`.

## Investigation Playbooks

### Panic or Crash

- Stop at the panic site or the caller just above it.
- Capture `stack`, `frame 0`, `locals`, `args`, and the values involved in the failure.
- If the panic comes from a nil pointer or bad interface conversion, inspect the exact receiver or interface payload instead of inferring it from source.

### Hang or Deadlock

- Interrupt execution with Ctrl+C.
- Run `goroutines -t` first to find blocked goroutines.
- Narrow the set with `goroutines -with running`, `goroutines -chan <expr>`, or `goroutines -group userloc` when the process has many goroutines.
- Inspect the blocked goroutine stack, then the owning thread if needed.
- Look for channel sends or receives, lock waits, and goroutines stuck in syscalls.

### Wrong Branch or Wrong Value

- Break at the start of the suspicious function.
- Use conditional breakpoints or hit-count conditions instead of repeatedly stepping through hot paths.
- Use `watch -w <expr>` when the real problem is “who changed this value?” rather than “where am I?”
- Use `next`, `step`, and `stepout` sparingly.
- Print the condition inputs before the branch, not after the damage is already done.

### Trace Without Stopping

- Use `dlv trace [package] <regexp>` when you want fast function-call evidence without a full interactive session.
- Convert a breakpoint into a tracepoint with `on <bp> trace` when you want logging without halting execution.

### Post-Mortem or Non-Deterministic Failure

- Use `dlv core <binary> <core>` for crash dumps and minidumps.
- Use `dlv replay <trace-dir>` for `rr` recordings.
- In replay or recording workflows, use `checkpoint`, `rewind`, and `restart -r` to revisit the same execution path instead of trying to reproduce it again.

### Test Failure

- Prefer `dlv test` with a single failing test name.
- Keep the reproduction narrow so the agent does not spend time stepping through unrelated setup.

### Optimized or Inlined Code

- If locals are missing or line stepping is confusing, rebuild an unoptimized binary and debug that binary with `exec`.
- Use the concrete recipe in [Delve command recipes](./references/command-recipes.md).

### CLI, Remote, or Path Problems

- For interactive CLI programs, either attach from another terminal, use headless mode and connect from another terminal, or assign the target its own `--tty`.
- If source paths do not line up because of containers, symlinks, `-trimpath`, or a separate build environment, inspect `sources` and use Delve path-substitution configuration instead of assuming the breakpoint is wrong.
- When the client or transport looks suspicious, enable `--log --log-output=rpc,dap`.

## Terminal Discipline

- Keep one debugger session alive instead of relaunching repeatedly.
- Send one debugger command at a time and read the response before sending the next command.
- Capture only evidence that changes the diagnosis.
- In headless multi-client mode, use `exit -c` if you want the target to keep running after the terminal client disconnects.
- Quit the debugger cleanly when finished so the terminal is reusable.

## References

- [Delve command recipes](./references/command-recipes.md)