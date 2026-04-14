Tasks: Lean Retrieval Architecture
Component 1: Session Attempt Log
 Create internal/agent/attempt_log.go
 Add AttemptLog *AttemptLog to QueryDeps in query_stream.go
 Wire attempt recording into loop.go after tool execution
 Inject attempt log section into composeSystemPrompt
Component 2: Live Retrieval Graph
 Create internal/agent/retrieval.go with anchor extraction, scoring, and snippet reading
 Add RetrievalTouched []string to QueryState
 Update composeSystemPrompt to accept and inject live retrieval section
 Wire retrieval into loop.go before prompt assembly
 Collect touched files after tool batch execution
Component 3: Preference Narrowing
 Update memory index header text in memory_files.go
Component 4: Shared Token Budget
 Add RetrievalBudgetTokens and SkipLiveRetrieval to ContextPressureDecision
Component 5: Engine Wire-up
 Pass AttemptLog into QueryDeps in engine.go
Component 6: Telemetry
 Add EventRetrievalUsed and RetrievalUsedPayload to the IPC package
 Emit EventRetrievalUsed from loop.go
Post-Implementation
 go build ./... — verify build passes
 gofmt all modified files
 Commit with descriptive message
 Update progress.md