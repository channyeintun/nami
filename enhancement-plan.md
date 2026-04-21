# Nami Enhancement Plan From SwarmForge Research

## Purpose

This document captures the strongest ideas from `reference/swarm-forge` and adapts them into a Nami-specific roadmap. The goal is not to copy SwarmForge's tmux launcher. The goal is to take the durable orchestration ideas behind it and implement them in Nami's Go engine, session store, artifact system, and TUI.

Adoption rule:

- do not take a SwarmForge idea just because it exists there
- take it only if it closes a real orchestration gap in Nami or improves clarity over Nami's current design
- reject it if Nami already has a stronger engine-native equivalent

## Research Basis

The most useful source material was:

- `reference/swarm-forge/README.md`
- `reference/swarm-forge/SwarmForgeInitSpec.md`
- `reference/swarm-forge/swarmforge.sh`
- `reference/swarm-forge/notify-agent.sh`
- `reference/swarm-forge/swarm-cleanup.sh`
- `reference/swarm-forge/features/*.feature`
- `reference/swarm-forge/examples/clojureHTW/swarmforge/*`

The examples matter more than the product spec alone because they show the actual discipline model: role-specific prompts, queueing rules, branch ownership, explicit handoff payloads, and review batching.

## Executive Summary

SwarmForge is not broadly a better orchestration runtime than Nami. Nami already has stronger runtime primitives. The useful value in SwarmForge is narrower and more specific: it makes workflow structure explicit in places where Nami is still implicit.

SwarmForge gets four things right that are highly relevant to Nami:

1. It makes multi-agent topology explicit instead of implicit.
2. It separates shared rules from role-specific behavior.
3. It treats handoffs as first-class workflow objects.
4. It keeps orchestration state visible and local.

Nami already has stronger foundations than SwarmForge in several places:

- durable artifacts
- session persistence
- bounded child agents
- lifecycle hooks
- background agent IPC
- memory and prompt layering

The main conclusion from the comparison is:

- keep Nami's engine-native child-agent runtime
- do not copy tmux, shell launchers, or keystroke messaging
- do add explicit topology, role contracts, handoff structure, queue semantics, and optional workspace isolation where Nami is still loose

Because of that, Nami should not reproduce the shell-script and tmux parts. It should implement a better orchestration layer inside the existing engine.

## Current Nami Orchestration Baseline

Before borrowing anything, this is what Nami already has today.

### What Nami already does better than SwarmForge

- First-class child-agent tools. `nami/internal/tools/agent.go` already exposes `agent`, `agent_status`, and `agent_stop` as typed runtime capabilities instead of shell conventions.
- Parallel agent launch. `nami/internal/tools/agent_team.go` and `nami/internal/engine/subagent_background.go` already support launching and polling a team of background child agents.
- Engine-native child runtime. `nami/internal/engine/subagent_runtime.go` already gives child agents bounded prompts, filtered tool sets, session ids, transcripts, result files, and model selection.
- Runtime policy hooks. `nami/internal/hooks/types.go` and child start/stop hook handling in `nami/internal/engine/subagent_runtime.go` already provide lifecycle interception that SwarmForge mostly encodes as prompt prose.
- Permission-aware delegation. `nami/internal/permissions/gating.go` already distinguishes safer read-only Explore launches from broader delegated work.
- Better observability primitives. `nami/internal/ipc/protocol.go`, `nami/internal/engine/background_tasks_ipc.go`, `nami/internal/session/store.go`, and the artifact system already provide richer observability than tmux panes and log tailing.

### What Nami still lacks compared with the best parts of SwarmForge

- No project-local orchestration graph. `agent` and `agent_team` launch work ad hoc, but there is no durable project-defined role topology.
- No directed handoff object. Child agents return summaries and transcript paths, but they do not produce a typed handoff for another role.
- No durable inter-agent inbox. Background agents can be launched and polled, but they do not exchange queued workflow messages.
- No role-owned workspace isolation. Child agents inherit a shared cwd today, which is weaker for multi-editor collaboration.
- No role-specific constitution layer. Nami has prompt layering, but not a project-local role overlay model equivalent to SwarmForge's constitution plus role prompts.

### Comparison Rule For This Plan

Every recommendation below falls into one of three buckets:

- adopt: SwarmForge covers a real Nami gap
- adapt: SwarmForge has the right idea, but Nami should implement it differently because Nami already has better primitives
- reject: Nami already has a better answer or SwarmForge's mechanism is too weak

## Findings Worth Carrying Over

### 1. Declarative swarm topology

SwarmForge uses a tiny project-local config format:

```conf
window architect claude master
window coder codex coder
window reviewer codex reviewer
window logger none none
```

Why it is useful:

- the role graph is explicit
- the backend choice is explicit
- workspace ownership is explicit
- startup can validate the graph before anything runs

Nami comparison:

Nami already has `agent` and `agent_team`, but those launches are task-centric, not topology-centric. There is no project-local definition of roles, edges, ownership, or workflow shape.

Decision:

Adopt.

What Nami should take:

- a project-local swarm spec such as `.nami/swarm.json`
- explicit role definitions instead of ad hoc subagent prompts
- validation before launch for duplicate roles, invalid workspace modes, unsupported model assignments, and missing role policies

Why this is better in Nami:

Nami already has session and artifact infrastructure, so the spec can be reviewed, persisted, resumed, and surfaced in the UI instead of living only in a shell file.

### 2. Shared constitution plus role overlays

SwarmForge separates common discipline from role behavior:

- `constitution.prompt`
- subordinate constitution files like project, engineering, and workflow rules
- role prompts such as `architect.prompt`, `coder.prompt`, and `reviewer.prompt`

Why it is useful:

- keeps shared rules centralized
- keeps role prompts short and focused
- makes precedence explicit
- makes the swarm easier to evolve without giant prompts

Nami comparison:

Nami is already strong on prompt composition through `nami/internal/agent/memory_files.go`, `nami/internal/agent/iteration_pipeline.go`, and `nami/internal/agent/query_stream.go`. What it lacks is not prompt layering in general. What it lacks is a project-local role layer on top of the existing system.

Decision:

Adapt.

What Nami should take:

- role-aware prompt layering on top of the existing memory pipeline
- a precedence model like `global -> project -> swarm constitution -> role overlay -> task overlay`
- an inspectable rendered prompt artifact for each child agent

Why this is better in Nami:

Nami already loads instruction and memory files through `nami/internal/agent/memory_files.go` and composes prompt context in `nami/internal/agent/iteration_pipeline.go` and `nami/internal/agent/query_stream.go`. That means role overlays can be implemented as structured prompt inputs instead of shell-generated temporary files.

### 3. Explicit handoff contracts

SwarmForge's workflow rules require handoffs to include:

- branch name
- commit hash
- what changed
- what was verified

The example prompts also define who can hand work to whom and when.

Why it is useful:

- prevents vague subagent completion messages
- makes review work tractable
- creates a predictable chain of custody for changes

Nami comparison:

Nami child agents already return structured status, transcript paths, output files, tool lists, and lifecycle metadata through `nami/internal/tools/agent.go` and `nami/internal/engine/subagent_runtime.go`. That is better than SwarmForge's plain text notifications. But Nami still lacks a typed, directed handoff object between roles.

Decision:

Adopt, but as an artifact-backed handoff rather than a text message convention.

What Nami should take:

- a structured handoff artifact with required fields
- sender role, target role, task summary, changed files, commands run, risks, and next action
- the ability to batch or filter handoffs before the parent agent consumes them

Why this is better in Nami:

Nami already has durable artifacts and artifact lifecycle events. Handoffs should be artifacts, not just freeform assistant text.

### 4. Queueing and review batching semantics

One of the best ideas in the example prompts is not the launcher. It is the workflow rule. Agents process one message at a time, queue overflow, and the reviewer can batch coder handoffs into one review pass.

Why it is useful:

- prevents message loss when an agent is already busy
- allows review to optimize for batches instead of noisy single-item interrupts
- gives the orchestrator a real scheduling model instead of pure improvisation

Nami comparison:

Nami already supports background child agents, status lookup, stop, and team-level aggregation. What it does not have is agent-to-agent workflow state. There is no inbox, no ack state, no review batch semantics, and no durable message queue between roles.

Decision:

Adopt.

What Nami should take:

- durable per-agent inboxes backed by session storage
- message states such as `pending`, `acked`, `in_progress`, `completed`, and `blocked`
- role-specific queue policy such as FIFO, batch-review, or latest-wins for certain message classes

Why this is better in Nami:

SwarmForge implements messaging by sending keystrokes into tmux panes. Nami can do this as structured state in the engine.

### 5. Workspace isolation by role

SwarmForge creates a git worktree per role for non-main agents.

Why it is useful:

- avoids file contention
- makes agent ownership visible
- creates a natural merge boundary for review
- allows parallel experiments safely

Nami comparison:

Today `nami/internal/engine/subagent_runtime.go` passes child agents a shared cwd. That is fine for read-only exploration and many delegated tasks, but it is weaker for multiple edit-capable child agents working concurrently.

Decision:

Adopt later and make it optional, not mandatory.

What Nami should take:

- optional worktree-backed child agents for edit-heavy tasks
- workspace strategies such as `shared`, `worktree`, and later possibly `snapshot`
- per-role or per-invocation worktree choice based on task risk and parallelism

Why this is better in Nami:

Nami does not need worktrees for every subagent. It should turn them on when multiple editing agents would otherwise collide.

### 6. Fail-fast startup validation

SwarmForge rejects:

- missing config
- missing constitution
- duplicate roles
- duplicate worktrees
- unsupported backends
- unsafe worktree names

Why it is useful:

- moves failure to startup instead of mid-run
- gives a stable contract for project-local orchestration

Nami comparison:

Nami already validates tool inputs, permissions, and review gates during execution. The missing piece is a project-local orchestration spec that can be validated before launch.

Decision:

Adopt once swarm specs exist.

What Nami should take:

- swarm spec validation before any child agent starts
- clear diagnostics artifact explaining exactly what is invalid and how to fix it
- review gate before running a broken swarm definition

### 7. Visible local state

SwarmForge writes local orchestration state such as:

- `.swarmforge/sessions.tsv`
- `.swarmforge/prompts/*`
- `logs/agent_messages.log`

Why it is useful:

- easy to inspect without special tooling
- easier to debug than hidden runtime state
- easier to resume or repair manually

Nami comparison:

Nami already has transcripts, result files, artifacts, and IPC-backed background updates. In that sense, Nami already beats SwarmForge. The actual gap is narrower: there is no single orchestration ledger showing roles, queues, workspaces, and handoff state together.

Decision:

Adapt, not copy.

What Nami should take:

- a session-backed swarm ledger containing roles, queue state, workspace mapping, and latest handoff ids
- artifact-backed prompt snapshots and event logs
- UI surfaces that reflect that state live

## What Nami Should Improve Beyond SwarmForge

These are the parts to learn from but not clone.

### Do not copy the shell-first runtime

SwarmForge's implementation is centered on `swarmforge.sh`, tmux, Terminal, and shell scripts. That is fine for an MVP, but it is not the right long-term architecture for Nami.

Nami should keep orchestration in Go, not in a large shell launcher.

### Do not use terminal keystrokes as the message bus

`notify-agent.sh` logs a message and injects it into a tmux pane with sleeps. That is clever but fragile.

Nami should use structured queue state in the session store.

### Do not depend on prompt obedience alone

SwarmForge encodes workflow discipline mostly in prompt text. Nami should push key workflow constraints into runtime policy:

- role permissions
- allowed tools
- required handoff fields
- allowed workspace scope
- completion gate semantics

### Do not hardcode role names

SwarmForge defaults naturally toward architect, coder, reviewer. Nami should support those roles but not bake them in as the only valid graph.

### Do not require one terminal per role

Nami already has background child agent state and artifacts. The TUI can be the orchestration dashboard.

### Do not copy utility roles that only compensate for SwarmForge's weak runtime

SwarmForge's `logger` role exists partly because tmux windows and flat logs need manual observation helpers.

Nami already has better artifacts, transcripts, and IPC events. It does not need fake agent roles for observability.

### Do not replace Nami's typed subagent modes with freeform role sprawl

Nami's existing Explore, verification, and general-purpose subagent types are useful because they already shape tool access and safety boundaries.

Any new role system should layer on top of those boundaries, not discard them.

## Recommended Roadmap

### Phase 1: Add a swarm spec and validator

Goal:

Make orchestration explicit and reviewable before execution.

Why this is justified against current Nami:

This fills a real gap. Nami can launch teams, but it cannot yet describe a durable role graph for a project.

Deliverables:

- project-local swarm spec, likely `.nami/swarm.json`
- schema for roles, model, purpose, workspace strategy, queue policy, permissions, and handoff requirements
- validator for duplicates, missing role overlays, invalid workspace strategy, and unsupported model configuration
- artifact showing the resolved swarm graph before launch

Suggested implementation points:

- new engine package for swarm config parsing and validation
- artifact integration via the existing artifact manager
- planner and review gating reuse from `nami/internal/agent/planner.go`
- build on top of existing `agent` and `agent_team` launch paths rather than replacing them

Why this phase first:

It adds discipline without changing the runtime model yet.

### Phase 2: Add role-aware prompt composition

Goal:

Bring over SwarmForge's constitution and role model using Nami's existing prompt system.

Why this is justified against current Nami:

This is an extension of a strength, not a replacement. Nami already has prompt layering, but not role-local orchestration overlays.

Deliverables:

- shared swarm constitution files under `.nami/swarm/constitution/`
- role overlays under `.nami/swarm/roles/<role>.md`
- effective prompt rendering for each child agent as a saved artifact
- precedence rules so role overlays remain small and predictable

Suggested implementation points:

- extend `nami/internal/agent/memory_files.go`
- extend prompt assembly in `nami/internal/agent/iteration_pipeline.go`
- extend prompt injection in `nami/internal/agent/query_stream.go`
- keep current typed subagent prompts in `nami/internal/engine/subagent_runtime.go` as the safety baseline

Why this phase second:

It directly ports one of the best SwarmForge ideas and fits Nami's current architecture.

### Phase 3: Add structured handoff artifacts

Goal:

Replace vague child-agent summaries with durable, typed workflow handoffs.

Why this is justified against current Nami:

Nami already has child result metadata, but it still lacks directed, typed handoffs between roles.

Deliverables:

- new artifact kind such as `handoff`
- minimum schema: source role, target role, summary, file set, verification summary, risks, and requested next action
- inbox APIs for listing, acknowledging, and resolving handoffs
- artifact and IPC events so the UI can show pending handoffs

Suggested implementation points:

- add artifact kind in `nami/internal/artifacts/types.go`
- extend session persistence in `nami/internal/session/store.go`
- extend IPC payloads near the background child agent events in `nami/internal/ipc/protocol.go`
- preserve existing child result JSON and add handoff artifacts as a higher-level orchestration primitive

Why this phase third:

It gives Nami most of the workflow leverage of SwarmForge without needing worktree support yet.

### Phase 4: Add durable inboxes and queue policy

Goal:

Model agent-to-agent communication as queue state, not transient text.

Why this is justified against current Nami:

Nami has launch and status orchestration, but not workflow-message orchestration.

Deliverables:

- per-role inbox persisted in the session store
- queue states and timestamps
- policies such as FIFO and batch-review
- resume-safe orchestration after interruption

Suggested implementation points:

- session-backed queue files or a small structured store under the session directory
- lifecycle enforcement through existing hooks in `nami/internal/hooks/types.go`
- UI state updates through IPC
- reuse team and background-agent state instead of inventing a second async runtime

Why this phase matters:

This is where Nami surpasses SwarmForge's `notify-agent.sh` approach.

### Phase 5: Add optional worktree-backed child agents

Goal:

Use workspace isolation only where it improves safety and parallelism.

Why this is justified against current Nami:

This addresses a real weakness for multi-editor flows, but it is not needed for most current delegation. That is why it belongs later.

Deliverables:

- worktree manager for child agent runs
- workspace strategy on a role or invocation
- diff preview and merge guidance artifacts
- cleanup and resume behavior for orphaned worktrees

Suggested implementation points:

- new worktree orchestration package in the engine
- background child agent metadata extended with workspace details
- merge and review flow integrated with artifacts
- keep shared-cwd execution as the default for simple or read-only delegation

Why this phase is later:

It is high leverage, but it is operationally heavier than the earlier phases.

### Phase 6: Add a swarm dashboard in the TUI

Goal:

Replace tmux-window observability with a Nami-native control surface.

Why this is justified against current Nami:

Nami already has the raw event stream for background agents. The missing piece is a composed orchestration view.

Deliverables:

- list of active roles and current state
- pending handoffs and queue depth
- current workspace per role
- last summary or result per child agent
- quick jump to relevant artifact or transcript

Suggested implementation points:

- reuse existing background child agent events
- extend UI to show swarm state as first-class session data

### Phase 7: Add policy enforcement per role

Goal:

Move role discipline from prompt prose into runtime constraints.

Why this is justified against current Nami:

This is partly already present through permissions, tool filtering, and hooks. The work here is to make those policies role-aware instead of just subagent-type-aware.

Deliverables:

- tool allowlists or denylists per role
- permission profile by role
- completion policy by role
- escalation rules when a role violates its contract

Suggested implementation points:

- reuse permission gating already present in the engine
- reuse hook types in `nami/internal/hooks/types.go`
- extend current subagent-type tool filtering in `nami/internal/engine/subagent_runtime.go` rather than creating a separate policy stack

This is the phase that prevents the swarm from becoming prompt theater.

## Best First Milestone

The best near-term milestone is not full swarm mode. It is a smaller vertical slice:

1. Add the swarm spec and validator.
2. Add role-aware prompt overlays.
3. Add the handoff artifact kind.
4. Add a minimal inbox backed by the session store.

That gives Nami a real orchestration model without taking on worktree lifecycle, merge automation, or a large new UI surface immediately.

It also keeps the existing Nami runtime intact: `agent`, `agent_team`, background-agent IPC, prompt layering, and session persistence remain the foundation.

## Concrete Mapping To Current Nami Architecture

These are the current Nami seams that make the work practical:

- `nami/internal/agent/memory_files.go` already loads layered instruction material.
- `nami/internal/agent/iteration_pipeline.go` already composes per-turn prompt state.
- `nami/internal/agent/query_stream.go` already carries session-scoped state through iterations.
- `nami/internal/agent/planner.go` already knows how to create reviewable artifacts.
- `nami/internal/artifacts/types.go` already supports durable work products.
- `nami/internal/session/store.go` already persists session-local state and can hold queue metadata.
- `nami/internal/ipc/protocol.go` already has background child agent event payloads.
- `nami/internal/hooks/types.go` already has subagent lifecycle hooks.

The architecture is already pointed in the right direction. The missing layer is explicit orchestration state.

## Recommendation

Nami should borrow SwarmForge's workflow ideas, not its shell runtime.

More specifically:

- SwarmForge is better at explicit workflow topology and role contracts.
- Nami is already better at runtime execution, persistence, permissioning, and observability.
- The plan should therefore extend Nami's current orchestration, not replace it.

The highest-value changes are:

1. explicit swarm specs
2. role-aware prompt overlays
3. structured handoff artifacts
4. durable inboxes
5. optional worktree-backed child agents

If Nami executes those five items well, it will preserve the useful parts of SwarmForge while ending up with a stronger, cross-platform, engine-native orchestration model.
