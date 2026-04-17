# Chat Timeline Plan

Scope: compare Chan's current chat transcript behavior against the reference implementation in `reference/vscode-copilot-chat/`, with emphasis on the fast, properly ordered mixed stream shown in the screenshot.

This plan intentionally excludes test work. The goal is to fix the chat event model and rendering order, not to add or run tests.

## Target behavior

The desired chat flow is an append-only timeline that shows work in the order it actually happens:

- short progress/commentary updates appear inline while work is underway
- tool invocations appear at the point they start, not in a separate top or bottom region
- tool rows update in place as progress/result arrives
- assistant text, thinking, progress, tool activity, and notices share one coherent narrative stream
- resume and replay preserve the same order the user originally saw

This is the behavior that makes vscode-copilot-chat feel fast and legible in the screenshot.

## What vscode-copilot-chat is doing

### 1. It uses one ordered response stream for all visible parts

In `reference/vscode-copilot-chat/src/util/common/chatResponseStreamImpl.ts`, the response stream is not only markdown text. It carries ordered UI parts such as:

- markdown/text
- thinking progress
- progress updates
- references
- warnings
- tool invocation begin/update events

That is the core reason the UI reads like a live narrative instead of a final answer plus a detached tool log.

### 2. It emits model text, thinking, and tool-call parts in stream order

In `reference/vscode-copilot-chat/src/extension/conversation/vscode-node/languageModelAccess.ts`, the language-model bridge reports:

- thinking deltas immediately
- text deltas immediately
- tool call parts immediately

The UI does not reconstruct this later from a coarse transcript. It receives ordered parts as they happen.

### 3. It has first-class progress parts for “working” narration

`ChatResponseStreamImpl.progress(...)` exists specifically for inline progress narration. That matches the screenshot pattern where the assistant says what it is doing between tool operations instead of only after everything finishes.

### 4. It treats tool lifecycle as a first-class stream with begin/update semantics

The reference stream supports:

- `beginToolInvocation(toolCallId, toolName, streamData)`
- `updateToolInvocation(toolCallId, streamData)`

That allows the UI to anchor one tool row in the timeline and update it in place, rather than faking chronology from separate message and tool collections.

### 5. It persists an ordered transcript/event log

`reference/vscode-copilot-chat/src/platform/chat/common/sessionTranscriptService.ts` defines an append-only transcript with typed entries such as:

- `user.message`
- `assistant.turn_start`
- `assistant.message`
- `tool.execution_start`
- `tool.execution_complete`
- `assistant.turn_end`

Those entries also carry timestamps and `parentId`, which makes replay and inspection naturally chronological.

## What Chan is missing today

### 1. Chan captures a transcript, then the renderer reorders it

Chan's TUI currently takes transcript blocks and then reorders them in `chan/tui/src/components/StreamOutput.tsx`:

- `reorderTurnBlocks(...)`
- `promoteLeadAssistantRun(...)`

This means the final UI is not the literal event order anymore. It is a curated layout.

That is the opposite of the Copilot-style timeline model.

### 2. Chan groups tool activity into synthetic blocks that hide chronology

`chan/tui/src/components/StreamOutput.tsx` also groups multiple read/search tools into a synthetic `tool_group`.

That may reduce noise, but it breaks “show me what happened when it happened.” The screenshot behavior is closer to ordered tool activity with optional compact presentation, not chronology-destroying grouping.

### 3. Chan's transcript model is too coarse

Chan's transcript entry model in `chan/tui/src/hooks/useEvents.ts` and `chan/tui/src/protocol/types.ts` only distinguishes:

- `message`
- `tool_call`
- `artifact`

That is not rich enough to represent the Copilot-style stream. It has no first-class entries for:

- progress/commentary updates
- tool invocation start vs progress vs completion
- assistant turn start/end boundaries
- thinking lifecycle boundaries
- in-place update anchors for visible timeline rows

### 4. Chan's fast-status updates mostly live outside the transcript

Chan already has `statusLine`, `notice`, and some tool-progress state in `chan/tui/src/hooks/useEvents.ts`, but much of that is ephemeral UI state rather than durable inline timeline content.

That is why the UI can feel like:

- transcript over here
- active status elsewhere
- tool block somewhere else

instead of one coherent stream.

### 5. Chan's hydration/replay path flattens history too aggressively

`chan/internal/engine/conversation_hydration.go` rebuilds hydrated chat from stored messages and tool calls, but only as:

- message entries
- tool-call entries

It does not preserve richer event sequencing such as tool start/complete boundaries, progress commentary, or turn-phase markers. Resume can therefore only reconstruct a simplified transcript, not the actual live narrative.

### 6. Chan lacks a proper progress-part concept like Copilot chat

The screenshot's fast feel comes from regular inline “I’m doing X” updates. Chan does not currently have a durable, transcript-visible equivalent of Copilot chat’s `progress(...)` part.

Without that, even perfect tool ordering still feels sparse and slower because the user does not see a live narrative between events.

### 7. Chan's data model is split across separate collections instead of one source of truth timeline

Chan stores:

- `messages`
- `toolCalls`
- `artifacts`
- `transcript` as references into those collections

That can work, but today it encourages presentation-time reconstruction and reordering. Copilot chat is closer to a single ordered stream of response parts with stable IDs for updates.

## Fix Plan

## Phase 1: Stop presentation-time reordering

Goal: make the visible transcript respect actual event order.

- Remove `reorderTurnBlocks(...)` and `promoteLeadAssistantRun(...)` from `chan/tui/src/components/StreamOutput.tsx`.
- Remove or heavily narrow synthetic tool grouping that changes chronology, especially `tool_group` for read/search runs.
- Keep purely visual compaction only if it does not change the underlying order of entries.

## Phase 2: Introduce a first-class timeline event model

Goal: represent the chat as ordered timeline parts, not just messages plus tool references.

- Replace or extend the coarse transcript entry model with typed timeline entries such as:
  - user message
  - assistant progress update
  - assistant thinking update
  - assistant text chunk or finalized message
  - tool invocation start
  - tool invocation progress update
  - tool invocation completion
  - system notice
  - artifact event
  - assistant turn start/end
- Give timeline entries stable IDs and optional update targets so a tool/progress row can be updated in place.
- Make the timeline itself the rendering source of truth, not a derived ordering pass over separate collections.

## Phase 3: Add explicit progress/commentary parts to Chan IPC

Goal: stream “what I’m doing” narration inline like Copilot chat.

- Add a new IPC event type for transcript-visible progress/commentary updates.
- Distinguish durable progress rows from transient footer/status text.
- Route intermediary updates from the engine/agent into these timeline parts so the user sees the same kind of inline narration shown in the screenshot.
- Keep footer status for compact ambient state, but do not rely on it as the primary record of work.

## Phase 4: Rework tool lifecycle rendering around begin/update semantics

Goal: anchor tool rows once and update them in place.

- Model tool activity explicitly as:
  - begin
  - update/progress
  - complete/error
- Keep one visible timeline row per tool invocation, keyed by tool ID.
- Update that row in place as progress and result arrive instead of relying on separate status areas or synthetic regrouping.
- Preserve exact start position in the timeline so the user can see what text or progress preceded the tool call.

## Phase 5: Preserve timeline fidelity on hydration and resume

Goal: make restored sessions look like the original chat, not a simplified reconstruction.

- Replace the current message/tool-call-only hydration payload with a richer ordered timeline payload.
- Persist timeline entries with timestamps and parent/sequence linkage, similar in spirit to `sessionTranscriptService.ts` in the reference.
- Hydrate from the ordered event log rather than regenerating a transcript from message arrays.
- Ensure `/resume` and `/rewind` restore the same mixed ordering of messages, progress items, and tool activity.

## Phase 6: Keep the UI fast by streaming small parts early

Goal: match the responsive feel of Copilot chat.

- Emit small progress updates early instead of waiting for a full assistant message.
- Stream thinking/text/tool events independently when available.
- Avoid renderer work that rescans and reshapes the whole transcript every time a new event arrives.
- Prefer in-place row updates for running tools over append-and-reflow behavior.

## Phase 7: Separate semantic compaction from visual compaction

Goal: stay readable without destroying the event timeline.

- If multiple adjacent low-signal events should be visually collapsed, do it as an explicit expandable UI affordance.
- Do not replace chronological entries with synthetic grouped blocks that erase sequencing.
- Apply compaction after preserving event order and identity, not before.

## Recommended execution order

1. Phase 1: stop renderer reordering
2. Phase 2: introduce first-class timeline entries
3. Phase 3: add progress/commentary IPC parts
4. Phase 4: rework tool begin/update/complete rendering
5. Phase 5: preserve timeline fidelity on hydration and resume
6. Phase 6: reduce latency and layout churn
7. Phase 7: add optional visual compaction without changing order

## Reference mapping

- Ordered mixed response stream:
  - `reference/vscode-copilot-chat/src/util/common/chatResponseStreamImpl.ts`
- Model bridge emits thinking/text/tool parts in stream order:
  - `reference/vscode-copilot-chat/src/extension/conversation/vscode-node/languageModelAccess.ts`
- Shared request handler writes into one response stream:
  - `reference/vscode-copilot-chat/src/extension/conversation/vscode-node/chatParticipants.ts`
- Append-only ordered transcript model for replay:
  - `reference/vscode-copilot-chat/src/platform/chat/common/sessionTranscriptService.ts`

## Expected outcome

If this plan is implemented well, Chan should feel much closer to the Copilot chat flow in the screenshot:

- the transcript reads top-to-bottom in true execution order
- progress updates explain what is happening before the final answer lands
- tool calls no longer feel detached from the narrative
- running work updates inline instead of splitting across transcript and status chrome
- resume/replay preserves the same story the user saw live