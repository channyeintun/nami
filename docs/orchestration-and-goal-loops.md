# Orchestration and Goal Loops

This document describes two features that change how a turn is shaped: workflow
graphs, which decide the *order* delegated work runs in, and goal loops, which
decide *when a turn is allowed to end*.

They are independent. A goal loop can drive plain single-agent work, and a workflow
can run without a goal set. They compose because both act on the same seam — the
point where the agent would otherwise stop.

## Workflow graphs

### The problem with phases

The obvious way to run multi-stage delegated work is in phases: run every task in
stage one, wait, run every task in stage two, wait. The waiting is the problem. A
phase boundary is a barrier, and a barrier makes every branch wait for the slowest
member of the current phase — including branches that never needed that member's
result.

Concretely: given a graph where `a → b` and `c → d`, phases run `{a, c}`, then
`{b, d}`. If `a` takes ten minutes and `c` takes ten seconds, `d` sits idle for
almost ten minutes waiting on work it does not depend on.

### Dataflow scheduling

`internal/workflow` schedules on dependencies instead. Each node starts the moment
its own dependencies finish. In the example above, `d` starts as soon as `c` is
done, regardless of what `a` is doing. A slow node delays only its own dependents.

The scheduler is a single loop that launches everything ready up to the concurrency
cap, waits for one completion, applies it, and repeats. Keeping the state mutation
in one goroutine is what makes the failure and cancellation paths tractable — there
is exactly one place a node's status changes.

### Validation before execution

`Spec.Resolve` validates the whole graph in one pass and reports every problem at
once, rather than one problem per attempt:

- ids are unique and well-formed
- every `depends_on` entry names a node that exists
- there is no cycle — and the error names the cycle's nodes, not merely that one
  exists
- every `${outputs.x}` reference names a *declared dependency*, not just an existing
  node

That last rule is what guarantees a prompt can never reach a node with an
unsubstituted placeholder in it: if a node interpolates a result, it must have
waited for it.

Resolution is deterministic. A graph with several valid topological orders always
resolves to the same one, so run transcripts and journals stay comparable between
runs.

The `workflow` tool resolves the graph during `Validate`, before anything launches.
A malformed graph comes back as a message the model can act on instead of a run that
starts and then falls over.

### Failure containment

By default a failed node skips only its transitive dependents, and independent
branches finish. A workflow is usually several separate lines of work; one broken
line should not discard the others. `"on_node_failure": "abort"` opts into stopping
the whole run when that is what you want.

A child agent that reports `failed` without returning an error also fails its node.
Otherwise its error message would flow downstream through `${outputs.*}` as though
it were a result.

### Resume

Every run writes an append-only journal of node results. A node's key is a hash of
its *dependencies' keys* plus its own executable identity — id, expanded prompt, and
the agent settings that change how it runs.

Keying on dependency keys rather than on a linear "everything before this node"
chain is what makes resume work at all for a graph that runs in parallel. Launch
order is not stable once more than one node is in flight: given two sibling chains,
whichever root finishes first launches its child first, and that depends on timing.
A linear chain would therefore produce different keys for identical work and miss on
every resume. Dependency keys depend only on the graph and the data flowing through
it, so two runs of the same graph agree regardless of scheduling.

It is also more precise. Because a key transitively commits to the whole ancestry
that produced it, a match can only happen when that entire ancestry matched — so
replay is sound without any global "chain broken" latch, and editing one branch
re-runs that branch alone instead of everything downstream of it in some arbitrary
order.

The *expanded* prompt is what closes the loop on upstream results. If a dependency
re-ran and produced different output, any node interpolating that output has a
different prompt and so a different key; a node that does not interpolate it was
genuinely unaffected, and replaying it is correct.

Node descriptions are deliberately excluded from the key, so relabeling a node for a
nicer progress display does not throw away its cached result.

### Why not an embedded script VM

Claude Code's equivalent executes a JavaScript program with `agent()`, `parallel()`,
and `pipeline()` injected. That shape needs a sandboxed script host. Embedding one
in a Go binary buys nothing here: the semantics worth having are dataflow ordering,
result interpolation, failure containment, and prefix-committed resume — and a
declarative graph expresses all four without an interpreter, while staying
validatable up front and serializable into a journal.

## Goal loops

### The seam

`agent.QueryDeps.BeforeStop` is consulted whenever the loop is about to end a turn.
Returning `StopDecision{Continue: true}` appends a follow-up user message and keeps
the loop running. `/goal` is built entirely on that seam — no new control flow.

Two evaluators share it, in a fixed order:

1. File stop hooks (`internal/hooks`) — user-authored, free.
2. Goal evaluation — costs a model call.

The order is not incidental. Goal evaluation must never run when a cheap
user-authored hook has already decided to keep the turn open.

### Judging from evidence

The judge reads the transcript, not the agent's summary of it. Tool calls and tool
results are labelled distinctly so the model can tell what was *claimed* from what
was *observed*.

The system prompt states that the agent asserting completion is evidence rather than
proof, and — symmetrically — that the agent asserting the goal is impossible is also
evidence rather than proof. Without the first clause the loop rubber-stamps a stalled
agent. Without the second, an agent can end its own loop by declaring defeat.

The transcript keeps its tail when truncated, since the evidence that settles a goal
is almost always the most recent work.

### Failing open

Every failure path returns "met":

- the model call errors
- the call times out
- the reply contains no parseable verdict

A judge that cannot answer must not be able to trap the user in a loop. Failing
closed here would mean an API blip locks the session into an unstoppable turn.

The verdict parser scans for the first balanced JSON object rather than requiring the
whole reply to be one, because models routinely wrap JSON in prose or a code fence.
It tracks string state while scanning so a brace inside a `reason` cannot end the
object early.

### The block cap

`QueryState.MaxTurns` bounds a turn's total length, but it counts all turns, not
consecutive fruitless ones. A goal the agent cannot satisfy would spin against that
bound.

So the goal tracks *consecutive blocks*, and any tool use between blocks resets the
counter. The cap is meant to catch a loop that is spinning, not one that is merely
long; an agent still doing real work should not be cut off for taking a while.

At the cap the turn is released but the goal stays set, so the user's next message
resumes it rather than the goal being silently lost. `NAMI_GOAL_BLOCK_CAP` tunes the
threshold; `0` disables it.

### Persistence

The goal is mirrored to the session directory through a temp-file rename. Atomicity
matters here: a crash mid-write must not leave a half-written file that reads back as
a *different* condition on reload. Persistence failures are silent — losing the
mirror costs the goal its survival across a reconnect, which is a better trade than
refusing to run the loop at all.

Stores are keyed by session directory, which is what lets the slash handler and the
stop evaluator — which reach the session by different routes — share one goal.

## Where things live

```text
internal/workflow/    graph engine: spec, validation, topology, scheduler, journal
internal/tools/       workflow + workflow_status tool definitions
internal/engine/      workflow_runtime.go  binds graph nodes to child agents
                      goal_runtime.go      stop evaluation, block cap, store registry
                      slash_command_goal.go
internal/goal/        goal store and the evidence-based evaluator
```
