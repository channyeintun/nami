# Orchestration and Goal Loops

This document describes two features that change how a turn is shaped: workflows,
which run delegated work as a program the model writes once instead of steering it
turn by turn, and goal loops, which decide *when a turn is allowed to end*.

They are independent. A goal loop can drive plain single-agent work, and a workflow
can run without a goal set. They meet at the point where the agent would otherwise
stop. A workflow runs in the background, so a turn can end while the run goes on,
and the run's result arrives later as a turn of its own. A goal loop decides whether
a turn may end at all, judged on what the transcript shows. Launching a run is not
evidence that a goal holds; the run's result is, once it reaches the transcript
through `workflow_status`, which can wait for the run to finish, or through the
notification that arrives when it does.

## Workflows

### Why a script

Until this version, a workflow was a static dependency graph: a list of delegated
tasks, each declaring which others it waited for and quoting their results in its
prompt. The engine validated the graph, started each task as soon as the tasks it
waited for finished, and journaled the results. That handles *ordering* well. What it
cannot handle is work whose shape depends on what the run finds:

- **Data-dependent fan-out.** "Find likely bugs in each package, then verify each
  bug on its own" needs one verification per bug, and how many bugs there are is
  known only once the find step returns. A graph has to name every task before it
  starts.
- **Loops.** "Fix the failures, run the tests, repeat until they pass, at most three
  times" has no fixed number of steps.
- **Branching.** "Skip verification when nothing was found" or "ask a stronger model
  when the first answer is unsure" choose what runs next from a result.

With a graph, the only way to get any of these was for the parent model to run one
graph, read its results, and write the next one. That hands control flow back to
turn-by-turn decisions, which is the thing a workflow exists to take over, and it
routes every intermediate result through the parent's context window.

A script expresses all three as ordinary code: `.map` over what a step returned, a
`for` loop, an `if`. The parent writes the plan once, the plan runs as code, and the
parent reads only what the script returns.

### The script model

A workflow is a plain JavaScript file:

```js
export const meta = {
  name: 'fix-until-green',
  description: 'Fix failing tests, rerunning them until they pass',
  phases: [{ title: 'Check' }, { title: 'Fix' }],
}

const REPORT = {
  type: 'object',
  properties: { passed: { type: 'boolean' }, failures: { type: 'string' } },
  required: ['passed', 'failures'],
}
const check = () => agent('Run go test ./... and report every failure in full.',
  { label: 'run tests', phase: 'Check', schema: REPORT, agentType: 'verification' })

let report = await check()
for (let attempt = 1; attempt <= 3 && report && !report.passed; attempt++) {
  await agent(`Fix these test failures:\n${report.failures}`, { label: `fix #${attempt}`, phase: 'Fix' })
  report = await check()
}
return report
```

The fix step edits files, so this script needs a permission mode that lets child
agents write; see the notes on children below.

**Meta.** A script must begin with `export const meta = {...}`, and the object must be
a pure literal: no variables, calls, spreads, or interpolated template strings. Meta
is read without running the script, for the approval prompt, the list of saved
workflows, and the progress display before the first agent starts, and a literal is
the only thing that can be read that way. `name` (1 to 64 letters, digits, `.`, `_`,
or `-`) and `description` are required; `whenToUse` and `phases` are optional, and
`phases` seeds the progress display before the script reaches its first `phase()`.

**The body** runs inside an async function, so it can `await` at top level and
`return` a value. The value must be JSON-serializable; it is what the parent reads.
The hooks are that function's parameters rather than globals, which is what lets a
nested workflow have its own `phase`, `args`, and `agent` bindings. The engine runs
scripts, not modules, so the `export` keyword is blanked out before parsing, in a way
that keeps the line numbers in error messages matching the file; `import` is
unavailable, and TypeScript annotations fail to parse.

**Validation.** The `workflow` tool parses the script and reads its meta while
validating the call, so a syntax error or a bad meta comes back as a message to fix
before anything launches. That is all a script allows to be checked up front; "Why
an embedded script VM after all" below says what that costs.

**Hooks:**

- `agent(prompt, opts?)` runs a child agent and resolves to its final text, or, with
  `opts.schema`, to an object validated against that JSON Schema. It resolves to
  `null` when the agent fails. The options are `label` (display name), `phase`
  (progress group, for calls inside `pipeline()` and `parallel()` stages), `schema`,
  `model` (`provider/model`, defaulting to the session's subagent model), `effort`
  (`low` to `max`; only OpenAI reasoning models use it), and `agentType`
  (`general-purpose` by default, `Explore` for read-only search, `verification` for
  builds and tests without edits). A bad option makes `agent()` throw.
- `pipeline(items, ...stages)` and `parallel(tasks)` run work concurrently; see
  Scheduling.
- `phase(title)` starts a progress group, and `log(...)` (or `console.log`) prints a
  progress line.
- `workflow(name | {scriptPath}, args)` runs a saved workflow or a script file inline
  and returns its value.
- `args` is the tool's `args` input, verbatim, and `budget` is
  `{total, spent(), remaining()}` in output tokens.

**Schemas.** The root of a schema has to be `{type: 'object', properties: {...}}`,
because providers require a tool's parameters to be an object and a child returns
structured output by calling a tool whose parameters are the schema. The run
validates the output again before the script sees it, so a script can trust the
shape; output that does not match fails the agent. Schemas are encoded with sorted
keys, so the same schema written in a different key order is the same schema to
resume.

**Children.** Each `agent()` call is an ordinary child agent of the session. It sees
the project instructions but nothing of the conversation, so a prompt must carry
everything its step needs, earlier results included, and its final answer comes back
to the script as data. Workflow prompts reach children whole: a long prompt passed to
the `agent` tool is archived to a file and replaced by a brief, but a script builds
its prompts from data, and a brief would drop the data. Children never ask for
approval. They run under a copy of the permission context taken when the run
launched, so in the default permission mode they cannot edit files or run commands
that are not read-only.

### Why goja

Running a script needs a JavaScript engine inside the Go engine, and goja is one
written entirely in Go. Two properties decided it.

**It is pure Go.** `nami-engine` ships as one static binary, and the release build
compiles it for every platform with `CGO_ENABLED=0` from a single machine. A binding
to V8 or QuickJS would need cgo, a C toolchain for every target, and prebuilt native
libraries, and would end both. goja is an ordinary Go module.

**It is sandboxed by default.** A new goja runtime has the ECMAScript built-ins and
nothing else: no `require`, no `process`, no `fetch`, no timers, no filesystem.
Nothing has to be taken away, so there is nothing to forget to take away. The only
thing a script can do to the world is call `agent()`, so every side effect is made by
a child agent under Nami's own controls: the tool allowlist of its agent type, the
permission context it inherited, and the rules that already govern child agents.
Running scripts under the Node, Bun, or Deno runtime the launcher already needs would
start from the other end, with full access to remove piece by piece, and would put a
second process between the engine and its children.

The trade-offs are real but small here. goja is an interpreter and much slower than
V8, which does not matter for a program that spends nearly all its time awaiting
agents that take minutes. It runs scripts rather than ES modules, which is why the
`export` before `meta` is blanked out. And it implements the language, not a
platform, so anything a script needs from outside, such as a date, a seed, or a list
of files, arrives through `args` or through an agent.

### Scheduling

One goroutine owns the VM and all of a run's state. An `agent()` call creates a
promise, starts the child on a goroutine of its own, and returns; the child's start
and finish come back over a channel, and the script goroutine applies them and
settles the promise. goja runtimes are not safe for concurrent use, so this shape is
required, and it is also what keeps the failure and cancellation paths tractable:
there is exactly one place a call's status changes.

`pipeline(items, ...stages)` runs each item through every stage on its own. A stage
receives `(previous, item, index)`, and an item moves to its next stage the moment
its previous stage finishes. `parallel(tasks)` runs functions concurrently and
settles once all of them have: it is a barrier.

The difference is the old argument against phases. A barrier makes every item wait
for the slowest one. Finding bugs in two packages with one `parallel()` and then
verifying them with another makes the fast package's verification sit idle until the
slow package's search finishes, though it never needed that result. `pipeline()`
starts it right away, so a slow item delays only itself. That makes `pipeline()` the
default for multi-stage work, and `parallel()` the tool for a step that needs every
result at once, such as deduplicating all findings or stopping early when there are
none.

**Concurrency.** An `agent()` call beyond the limit waits as `queued` until a slot
frees. The limit is the machine's CPU count minus two, kept between 2 and 16, and it
is one pool for the whole engine, shared by every run. The pool bounds what children
do to the machine, since they run builds, tests, and searches on the same CPUs, and
how hard the engine presses on a provider's rate limits; with a pool per run, three
runs could start up to 48 children at once. A stopped run never starts a queued agent, even
when a slot frees at the moment it is stopped.

### Failure semantics

- **A failed agent resolves to `null`** rather than rejecting. One agent hitting a
  rate limit or its own turn limit should not take a run of hundreds down with it,
  and a rejection would force a `try` around every call. The error is kept on the
  agent's record, where the status snapshot shows it, and scripts drop failures with
  `.filter(Boolean)`. Output that misses its schema is a failure too.
- **A `parallel()` task or `pipeline()` stage that throws** turns its item into
  `null` (a pipeline item skips its remaining stages), and a log line names the item
  and the error. A failed agent does not throw and already shows as failed, so this line only
  appears for errors in the script's own code, which is where a typo would otherwise
  disappear silently.
- **An uncaught throw fails the run**, with the error and its stack, so the failure
  names the line that caused it.
- **A run that returns is `completed`**, even when some of its agents failed. The
  snapshot counts the failures, and the script decides what failure means. A run is
  `failed` only when the script itself fails, and `stopped` only when it is cancelled.
- **Nothing is left behind.** When the script returns while agents it did not await
  are still running, they are stopped and a warning says to await every call whose
  result matters. A script that awaits a promise nothing can settle fails instead of
  hanging.

### Background runs, ownership, and notifications

`workflow` returns at once with a `run_id`. The run goes on in the background, on a
context of its own rather than the turn's, so ending the turn or starting another one
does not stop it. `workflow_status` reports a snapshot and can wait, up to ten
minutes, for the run to finish; `workflow_stop` cancels a run and waits, five seconds by
default, for its agents to settle; stopping a run that already finished just returns
its snapshot. The
`/workflows` dialog shows the same snapshots and stops a running run with `x`.

Launching is gated like any execute-level tool, and the approval prompt names the
workflow by its meta name and description. Everything the run needs from the session
is captured at launch, on the goroutine running the tool call: the owning session and
its directory, the working directory, and a copy of the permission context. Capturing
there matters because the permission context is not safe to read from another
goroutine, and because the active session can change while the run goes on.

Ownership decides where progress goes. The run emits `workflow_updated` at most once
every 250 ms while it is running and its final snapshot immediately, and only while
its owning session is the active one, so switching sessions does not leak one
conversation's run into another.

When a run completes or fails, the TUI queues a `<task-notification>` turn for the
model, once per run, through the same queue a finished background command uses. The
queue delivers it when the conversation is idle, so it never interrupts a turn. It
carries a one-line summary, a preview of the result or the error, and the path of the
full result, and it tells the model to call `workflow_status` when the preview is not
enough. A stopped run does not notify: whoever stopped it already knows.

Each run gets a directory, `<session dir>/workflows/<run_id>/`, private to the user,
holding `script.js`, `journal.ndjson`, and `result.json`. The last is the final
snapshot plus the full return value, written through a temp-file rename so a crash
cannot leave half of it. The engine keeps a run in memory for five minutes after it
finishes; after that `workflow_status` reads `result.json`, so a finished run stays
inspectable for as long as its session's files do.

`isolation: 'worktree'` is refused. A worktree child changes the working directory of
the whole engine process while it runs, which is safe only when nothing else runs
beside it, and a background workflow always runs beside the conversation. The same
reasoning runs the other way: a workflow's agents resolve relative paths against the
process's working directory, so while a run is going the engine refuses anything
that would move it. That covers a worktree child started from the conversation,
`enter_worktree` and `exit_worktree`, and a `/resume` into a session that lives in
another directory. Each refusal says to stop the run or wait for it.

A run belongs to the session that launched it, captured on the tool call's goroutine
together with that session's working directory and a snapshot of its permissions.
Its updates reach the TUI only while that session is the one shown. The TUI forgets
runs when it loads a conversation, so after any session switch, `/resume` and
`/rewind` included, the engine re-announces the runs the active session owns. A run
that finished while its session was not shown never sent its final state, so that
re-announcement is also when its `<task-notification>` arrives. Each run notifies at
most once, and pressing Esc to interrupt an unrelated turn does not drop a
notification still waiting to be delivered.

### Resume

Every run journals each successful `agent()` result to its `journal.ndjson`, one line
per result, flushed line by line so a run that is killed keeps everything it
finished. Failures are never journaled, so a resume retries them. Passing
`resume_from_run_id`, a run of the same session, with the same script or an edited
one seeds the new run from that run's journal.

**Keys.** A call's key is a digest of what the call asks for, plus a count. The digest
covers the prompt, the schema, the model, the effort, the isolation, and the agent
type, taken after the engine has normalized the options, so two spellings of an
agent type replay the same result. Label and phase are left out, so relabelling a
call for a nicer display keeps its result. The count is how many earlier calls in
the run had the same digest. Keys carry a version prefix, so a journal written under
an older scheme never matches.

The count is per digest, not a position in the run, because call order is not
stable. A pipeline's second-stage call is made when its item's first stage finishes,
which depends on timing, and a resumed run settles replayed calls in the order they
are made rather than the order they once finished in. A global call index would give
the same work different keys from one run to the next and miss on every resume. A
digest depends only on what was asked, and the count tells apart only calls that
asked exactly the same thing. Those calls are interchangeable: if two of them trade
results between runs, nothing can tell. A loop that sends the same prompt three times
replays one result per iteration, which is what the example above relies on for its
repeated test runs.

The graph design keyed each task on the keys of the tasks it waited for, which tied a
result to the whole ancestry that produced it. A script declares no dependencies, and
any call may use anything the script has seen, so the script design gets the same
guarantee from a rule about time instead.

**The replay rule.** A resumed run replays every call whose key matches until the
first result from a live agent, one the journal could not answer, reaches the
script. From then on every call runs live.

Matching prompts is enough for data, but not for side effects. Take a script that
runs `await agent('fix X')` and then `await agent('run the tests')`, edited to fix Y
instead. The first call misses and runs live. The second call's prompt has not
changed, so its key matches, but its journaled answer describes the tests against the
old fix. Once a live result has reached the script, any later call might depend on
what that agent did, through data the script passes on or through files the agent
changed, and nothing can tell which; so every later call runs live.

The rule is sound in the other direction as well. Until a live result arrives, every
result the script has seen was replayed and is identical to the earlier run's, and
the script is deterministic, so it has made the same decisions the earlier run made.
A call made while a live agent is still running is one the script did not order
after that agent, so in no run could it rely on that agent's effects, and replaying
it is safe. The
price is conservatism: an edit near the start of a script re-runs everything after
it. That costs time; the alternative is a stale result reported as fresh.

Two details keep a replay honest. A journaled result that no longer validates against
the call's schema is a miss, not a value the script would then trip over. And
replayed results are appended to the new run's journal, so a resumed run's journal is
complete on its own and can itself be resumed.

Journal trouble never fails a launch, because a run without resume is still useful.
`resume_from_run_id` must be a bare run id; one with no journal starts cold with a
warning that nothing was replayed, an unreadable journal starts cold with a warning,
and a journal that cannot be written runs without one and says so. A result too large
for a journal line (4 MiB) is not recorded, with a warning that a resume would run
that agent again.

### Determinism

Resume matches calls by what they ask, so a script must ask the same things when it
sees the same results. The runtime removes the two ordinary sources of variation:
`Date.now()`, `Math.random()`, and `new Date()` without arguments throw, and so does
`Date()` called as a function, each with a message that says to pass timestamps or
seeds in through `args`. `new Date(value)` still works, so a script can parse and
format dates it was given, and there are no timers to read time through. What remains
non-deterministic is the agents' output, which is exactly what the journal records.
This is also what the soundness argument for the replay rule rests on.

### Limits

- **Agent calls.** A run makes at most 1000 `agent()` calls over its lifetime, nested
  workflows included. That is far above what a real workflow needs; it is a backstop
  for a loop that never ends.
- **Items.** `parallel()` and `pipeline()` take at most 4096 items. More is an error,
  never a silent truncation.
- **Tokens.** `token_budget` caps the output tokens a run's agents spend. The
  script sees it as `budget.total` (null without one), `budget.spent()`, and
  `budget.remaining()`, and can scale its fan-out to fit. The budget is checked
  twice: when `agent()` is called, which throws once `spent()` reaches the total,
  and again when a queued agent is about to start, since a fan-out admits every
  call before any agent has spent anything. A queued agent that finds the budget
  spent fails without starting and resolves to null. Agents already running
  finish, so the total can overshoot by what they spend. An agent's spend counts
  before it gives up its slot, so the next agent to start sees it. Replayed calls
  spend nothing.
- **Nesting.** `workflow()` nests one level: a nested workflow cannot call
  `workflow()`. The nested script shares its parent's slots, agent cap, journal,
  budget, and stop signal, and gets its own phase and args. `workflow`,
  `workflow_status`, and `workflow_stop` are not in any child agent's tool list, so a
  child cannot start a run of its own. Together these bound how far one run can fan
  out.
- **Runaway scripts.** The call stack is capped at 4096 frames, so runaway recursion
  becomes a catchable `RangeError` instead of a Go stack overflow that would take the
  engine down. A panic inside a hook fails the run, not the engine. A script stuck in
  a loop that never awaits is interrupted when the run is stopped.
- **Snapshots.** `workflow_status` lists at most 200 agents (running first, then
  failed, then the most recent), the last 20 log lines, and about 20000 characters of
  the result, with `result_path` pointing at the full value.

### Why an embedded script VM after all

The previous version of this document argued against embedding a script VM. The
semantics worth having, it said, were dataflow ordering, result interpolation,
failure containment, and resume, and a declarative graph expressed all four without
an interpreter while staying validatable up front and serializable into a journal.
Each of those claims held. What changed is which property mattered: in use, the
orchestration worth automating was shaped by data, and that is the one thing a graph
cannot express.

All four survive in the script design:

- **Dataflow ordering** is `pipeline()`: each item flows through its stages without
  waiting on the others.
- **Interpolation** is a template string. There are no declared dependencies to
  check it against: a result exists to quote only once the script has awaited it.
  Quoting a promise the script forgot to await puts `[object Promise]` in the prompt,
  a mistake the graph would have caught before running.
- **Failure containment** is `null`: a failed agent drops out of its item, and the
  rest of the run goes on.
- **Resume** is per-call keys plus the replay rule.

What was given up is validation. A graph could be checked whole before anything ran.
A script can only be parsed and have its meta read, and a logic error surfaces when
the run reaches it. Failing at the first bad line with a stack, and resuming with
everything before that line replayed, makes such an error cheap to recover from, but
it is a real loss.

The other half of the old argument was that an embedded interpreter cost more than
it bought. goja changed that: it adds a pure-Go dependency rather than a native one,
the engine stays one static binary, and the sandbox comes with the runtime instead of
having to be built.

## Goal loops

### The seam

`agent.QueryDeps.BeforeStop` is consulted whenever the loop is about to end a turn.
Returning `StopDecision{Continue: true}` appends a follow-up user message and keeps
the loop running. `/goal` is built entirely on that seam — no new control flow.

A turn can also end at a limit: `QueryState.MaxTurns`, or a run of replies without
tool calls that has used up the output budget of one reply or keeps coming back
short. BeforeStop is consulted there too, with the limit as the stop reason, so the
goal is still judged and stop hooks still run, but its decision cannot carry the
turn past the limit. The turn ends with that stop reason and a notice saying why,
and a goal that is not met stays set for the user's next message.

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

The verdict parser scans for the first balanced JSON object that carries a `met` or
`impossible` field rather than requiring the whole reply to be one, because models
routinely wrap JSON in prose or a code fence, and quote JSON evidence before they
answer. An object without either field is not a verdict, so it is skipped rather than
read as "not met". The parser tracks string state while scanning so a brace inside a
`reason` cannot end the object early.

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
internal/workflow/    the script host
                      script.go            parsing and the pure-literal meta
                      run.go               the run loop, hooks, limits, the replay rule
                      prelude.go           parallel(), pipeline(), console, no clock or randomness
                      journal.go           the resume journal and call keys
                      schema.go            agent() schema checks and output validation
                      saved.go             saved workflows
internal/tools/       workflow.go          workflow, workflow_status, workflow_stop
internal/engine/      workflow_runtime.go  background runs, the run registry, agent() bound to
                                           child agents, workflow_updated
                      subagent_scope.go    what children inherit from the launching turn
                      slash_command_workflows.go  the /workflows command: saved workflows,
                                           workflows_requested
                      goal_runtime.go      stop evaluation, block cap, store registry
                      slash_command_goal.go
internal/goal/        goal store and the evidence-based evaluator
internal/ipc/         protocol.go          workflow_updated, workflows_requested, workflow_stop
tui/src/              App.tsx              the <task-notification> for a finished run
                      components/WorkflowProgress.tsx  running workflows above the input
                      components/WorkflowsDialog.tsx   the /workflows dialog
```

On disk:

```text
<git root>/.nami/workflows/<name>.js            saved project workflows
<config dir>/workflows/<name>.js                saved user workflows; a project one
                                                with the same name wins
<config dir>/sessions/<session id>/workflows/<run_id>/
                      script.js                 the script as launched
                      journal.ndjson            successful agent() results, for resume
                      result.json               final snapshot and full return value
```
