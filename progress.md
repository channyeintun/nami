# Progress

## Active Task

- Completed: lean retrieval architecture implementation for the agent harness.
- Completed: review-driven corrections for retrieval, preference recall, and telemetry wiring.
- Completed: structural edge expansion, test-covers edges, and attempt-log surfaced telemetry.
- Completed: architecture docs sync for structural retrieval edges and surfaced telemetry.
- Completed: session-scoped retrieval graph with multi-language support, 2-hop expansion, and retry-match telemetry.
- Completed: fix immediate spinner display after artifact approval.
- Completed: inject corrective nudge on repeated edit failures to prevent stale-content retry loops.
- Completed: migrated the TUI from Ink to Silvery, including native Spinner adoption and runtime-compatible paste handling.
- Completed: adjusted the Silvery TUI layout for a fixed status/composer shell, scrollable transcript region, sticky transcript context header, and live-tail scroll targeting.
- Completed: fixed Silvery text entry to use literal typed characters so shifted punctuation like `?` inserts correctly in the composer and prompt dialogs.
- Completed: fixed transcript paging so older conversation windows reset to the top instead of preserving a stale scroll offset that could show blank space above the transcript.
- Completed: replaced manual transcript slice paging with full-history scrolling so wheel/trackpad navigation traverses the real conversation instead of a capped window.
- Completed: switched the TUI to a local wrapper package built from the latest Silvery reference dist so new framework components and fixes are available without relying on the stale published package.
- Completed: restructured the TUI shell around a Silvery Screen root with bounded transcript, sidebar, and bottom prompt regions so artifacts and permission prompts can scroll instead of being clipped below the viewport.
- Completed: replaced the transcript pane's Box overflow scrolling with Silvery ListView so fullscreen history navigation uses the framework's intended list primitive instead of a passive scroll box.
- Completed: removed the separate artifact pane and rendered artifacts inline in the transcript ListView so artifact content shares the conversation's vertical scroll surface and no longer obscures the composer.
- Completed: removed the fixed 45% height cap from permission and artifact-review prompts so approval choices are not clipped at the bottom on small terminals.
- Completed: moved permission and artifact-review scrolling into the prompt components themselves so tall prompts can reveal their bottom border and actions instead of clipping a single oversized child.
- Completed: made the footer controls hint responsive so narrow terminals stack earlier, shorten the hint, and truncate cleanly instead of showing clipped partial labels like "Thin".
- Completed: constrained markdown text blocks to the available card width and forced explicit wrapping so quoted artifact content does not render past the artifact border.
- Completed: removed Home/End and PgUp/PgDn footer shortcut hints so the footer only advertises the remaining active controls.

## Notes

- Implemented session-scoped RetrievalGraph on QueryState with lazy file parsing, mod-time invalidation, and multi-hop scoring.
- Added language parsers for Go, TS/JS, Python, Rust, Ruby, Java, and C/C++ — extracting symbols, imports, and test functions via line-by-line regex.
- Multi-language test pairing: Go (_test.go), TS/JS (.test.ts/.spec.ts), Python (test\_\_.py/_\_test.py), Ruby (\_spec.rb), Java (\*Test.java).
- Graph-based scoring seeds from exact anchors, walks 1-hop edges at full weight, conditionally expands 2nd hop at 50% penalty when first-hop is sparse.
- ExtractAnchors now also matches symbol names against known graph nodes for symbol-level cross-referencing.
- Graph invalidation wired into tool execution: touched files are invalidated so they re-parse next turn.
- Added attempt_repeated telemetry event emitted when a new tool failure matches a previously logged attempt-log signature.
- Added structural edge expansion (1-hop) to the retrieval pipeline: Go import edges resolve local-package imports to candidate files; test ↔ source edges associate \_test.go and .test.ts files with their counterparts.
- Added attempt_log_surfaced telemetry event emitted each turn when attempt-log entries are loaded into the prompt.
- Added edges_expanded field to retrieval telemetry payload and TUI footer display.
- Added a session-scoped attempt log and wired failed tool-attempt recording into the query loop.
- Updated web docs architecture copy to describe exact-anchor retrieval, 1-hop structural expansion, preference-framed durable memory, and surfaced retrieval telemetry.
- Added live retrieval with anchor extraction, candidate scoring, live snippet reads, prompt injection, and retrieval telemetry.
- Narrowed durable memory framing toward preferences and conventions instead of repo facts.
- Shared retrieval token budgeting with context-pressure handling and wired attempt-log creation from the engine session directory.
- Replaced durable-memory model side-query with deterministic preference matching and stopped injecting unrecalled memory index entries.
- Normalized retrieval candidate paths, boosted error-context matches, and expanded touched-file tracking to use tool results and compatibility field names.
- Wired retrieval telemetry into the TUI footer so per-turn retrieval usage is visible instead of being dropped on the frontend.
- Verified the Go module builds successfully with `go build ./...`.
- Replaced `ink` and `ink-spinner` in the TUI with `silvery`, updated entrypoints to `render(...).run()`, and switched the prompt paste handler to `usePaste` from `silvery/runtime` to match the published API.
- Reworked the transcript panel to use a native Silvery scroll container with pinned outer chrome, sticky in-scroll context/search labels, and declarative `scrollTo` targeting for live output and transcript search.
- Switched editable TUI fields from Silvery's normalized `input` to `key.text ?? input` for insertion so shifted punctuation and IME text are preserved instead of being flattened to hotkey base keys.
- Kept transcript `scrollTo` active while browsing older transcript slices so paging/history navigation anchors the viewport to the top of the visible window instead of freezing the previous offset.
- Removed the manual 200-block transcript windowing layer from the TUI so Silvery scrolls the full mounted transcript; idle renders no longer pin history to the tail, while streaming and transcript search still use declarative scroll targeting.
- Replaced the raw local Silvery source dependency with a local wrapper package built from the reference repo's latest `dist` output, because the upstream workspace package metadata points at source exports that are not directly consumable from this TUI.
- Moved plan and artifact content into a bounded right-side pane and wrapped pending permission/review prompts in bounded scroll containers under a Screen root, so oversized panels no longer push the transcript or composer out of view.
- Reworked the transcript surface to use `ListView` with measured variable-height rows and wheel-driven cursor targeting, matching the latest Silvery docs for fullscreen list/history behavior.
- Removed the dedicated artifact sidebar/bottom pane and appended visible artifacts to the transcript `ListView`, so the fullscreen shell keeps the input/footer anchored while artifact content scrolls with the conversation.
- Let permission and artifact-review prompts expand into the available lower pane instead of forcing them into a 45% box, which prevented multi-option approval prompts from clipping their lower actions on short terminals.
- Shifted scroll ownership from the lower-pane wrapper into the permission/review prompt boxes themselves, because parent overflow on a single oversized child could not reveal the prompt's bottom border or last actions.
- Adjusted the footer layout and hint text by terminal width so control labels degrade predictably on narrow screens rather than clipping mid-word.
- Updated markdown text rendering to use the container width explicitly, preventing blockquotes and other long markdown paragraphs from shrink-wrapping past artifact card boundaries.
- Simplified the footer hint copy by dropping Home/End and Page Up/Page Down references, reducing clutter and avoiding stale control guidance.
