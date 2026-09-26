# Delve Command Recipes

## Install Delve

```bash
go install github.com/go-delve/delve/cmd/dlv@latest
```

With a local checkout at `reference/delve` (gitignored, not part of the repository), `cd reference/delve && make build` builds `reference/delve/dlv`; substitute that path for `dlv` below.

## Common Launch Commands

Debug a Go package that Delve should build:

```bash
dlv debug ./cmd/server -- --config dev.yaml
```

Debug a Go package with optimizations disabled at build time:

```bash
dlv debug --build-flags='-gcflags=all=-N -l' ./cmd/server -- --config dev.yaml
```

Debug a single Go test:

```bash
dlv test ./pkg/cache -- -test.run TestEvictExpired
```

Debug an existing binary:

```bash
dlv exec ./bin/server -- --config dev.yaml
```

Attach to a running process:

```bash
dlv attach 12345
```

Inspect a core dump or minidump:

```bash
dlv core ./bin/server ./crash.core
```

Replay an `rr` recording:

```bash
dlv replay /tmp/rr-trace
```

Trace matching functions without an interactive stop-and-step session:

```bash
dlv trace ./cmd/server 'cache.*' --timestamp -s 5
```

Start a headless server and connect to it from another terminal:

```bash
dlv --headless --listen=127.0.0.1:43000 --api-version=2 --accept-multiclient --continue debug ./cmd/server -- --config dev.yaml
dlv connect 127.0.0.1:43000
```

Start a DAP-only server for a DAP client such as VS Code Go:

```bash
dlv dap --listen=127.0.0.1:43001
```

Use `dlv dap` only with a DAP client. Use `dlv --headless <command>` when you need `dlv connect`, JSON-RPC, or remote attach to a target that is already running under Delve.

## Useful Interactive Commands

Discover where to stop:

```text
funcs cache.*
packages
sources
list main.main:30
```

Set a breakpoint by function:

```text
break main.main
break github.com/example/project/pkg/cache.(*Store).Get
```

Use precise stop conditions:

```text
break main.go:55 if i == 5
condition -hitcount 1 > 100
condition -per-g-hitcount 1 == 5
watch -w state
on 1 trace
breakpoints
```

Run or continue execution:

```text
continue
restart
checkpoint before-race
rewind
restart -r
```

Step through code:

```text
next
step
stepout
```

Inspect where you are:

```text
stack
frame 0
threads
goroutines -t
goroutines -with running
goroutines -chan jobs
goroutines -group userloc
```

Inspect values:

```text
args
locals
print req.ID
print err
whatis state
```

Switch execution context:

```text
goroutine 24
thread 7
goroutines -exec stack
```

Inspect machine-level state when needed:

```text
regs
disassemble
```

Quit the debugger:

```text
exit
exit -c
```

## Fast Evidence Checklist

For a panic:

```text
stack
locals
args
print <suspect-expr>
```

For a hang:

```text
Ctrl+C
goroutines -t
goroutines -with running
goroutines -chan jobs
stack
threads
```

For a wrong value:

```text
break <suspect-function>
continue
args
locals
print <expr>
```

## Unoptimized Build Recipe

When variables are optimized away or stepping is misleading, rebuild without inlining or optimizations and debug the resulting binary:

```bash
go build -gcflags='all=-N -l' -o ./tmp/debug-bin ./cmd/server
dlv exec ./tmp/debug-bin -- --config dev.yaml
```

## CLI and Remote Recipes

Debug a CLI program by assigning it a TTY:

```bash
dlv debug --tty /dev/pts/1 ./cmd/server
```

Run Delve headless inside a container and connect from outside:

```bash
dlv exec --headless --listen=:4040 --continue --accept-multiclient /path/to/executable
dlv connect :4040
```

If source paths do not resolve or breakpoints miss in a containerized or remote build, start with `sources` and Delve path-substitution config before assuming the binary is wrong.

When transport, client, or path mapping is the problem, enable Delve logging:

```bash
dlv --headless --listen=127.0.0.1:43000 --api-version=2 --log --log-output=rpc,dap debug ./cmd/server
```