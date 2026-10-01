package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	artifactspkg "github.com/channyeintun/nami/internal/artifacts"
	"github.com/channyeintun/nami/internal/compact"
	"github.com/channyeintun/nami/internal/config"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/hooks"
	"github.com/channyeintun/nami/internal/ipc"
	memorypkg "github.com/channyeintun/nami/internal/memory"
	"github.com/channyeintun/nami/internal/permissions"
	"github.com/channyeintun/nami/internal/session"
	"github.com/channyeintun/nami/internal/swarm"
	"github.com/channyeintun/nami/internal/timing"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

const exploreSubagentType = "Explore"
const generalPurposeSubagentType = "general-purpose"
const verificationSubagentType = "verification"

const (
	delegationPromptArchiveName  = "delegation-prompt.txt"
	delegationPromptArchiveLimit = 1800
	delegationPromptBriefLimit   = 1400
	delegationPromptLineLimit    = 6
	delegationPromptAnchorLimit  = 8
)

var delegatedPromptAnchorPattern = regexp.MustCompile(`(?:/[A-Za-z0-9._-]+)+(?:\.[A-Za-z0-9._-]+)?|(?:[A-Za-z0-9._-]+/)+[A-Za-z0-9._-]+(?:\.[A-Za-z0-9._-]+)?|` + "`[^`]+`")

var exploreSubagentTools = []string{
	"think",
	"list_dir",
	"read_file",
	"file_diff_preview",
	"file_search",
	"grep_search",
	"go_definition",
	"go_references",
	"read_project_structure",
	"project_overview",
	"dependency_overview",
	"symbol_search",
	"web_search",
	"web_fetch",
	"git",
	"swarm_submit_handoff",
	"swarm_list_inbox",
	"swarm_update_handoff",
}

var verificationSubagentTools = []string{
	"bash",
	"list_commands",
	"command_status",
	"send_command_input",
	"stop_command",
	"forget_command",
	"list_dir",
	"read_file",
	"file_diff_preview",
	"file_search",
	"grep_search",
	"go_definition",
	"go_references",
	"read_project_structure",
	"project_overview",
	"dependency_overview",
	"symbol_search",
	"web_fetch",
	"git",
	"think",
	"swarm_submit_handoff",
	"swarm_list_inbox",
	"swarm_update_handoff",
}

var generalPurposeSubagentTools = []string{
	"bash",
	"think",
	"list_dir",
	"read_file",
	"file_diff_preview",
	"create_file",
	"file_write",
	"replace_string_in_file",
	"multi_replace_string_in_file",
	"apply_patch",
	"file_search",
	"grep_search",
	"go_definition",
	"go_references",
	"read_project_structure",
	"project_overview",
	"dependency_overview",
	"symbol_search",
	"web_search",
	"web_fetch",
	"git",
	"file_history",
	"swarm_submit_handoff",
	"swarm_list_inbox",
	"swarm_update_handoff",
}

// subagentRunnerDeps is what every child agent shares, whichever runner
// starts it.
type subagentRunnerDeps struct {
	bridge          *ipc.Bridge
	registry        *toolpkg.Registry
	parentTracker   *costpkg.Tracker
	sessionStore    *session.Store
	artifactManager *artifactspkg.Manager
	hookRunner      *hooks.Runner
	// chooseClient picks the model client a child runs on from its request's
	// model override. It is a field so a test can run a child without the
	// provider discovery the real choice does.
	chooseClient func(modelOverride string) (api.LLMClient, string, error)
}

func newSubagentRunnerDeps(
	bridge *ipc.Bridge,
	registry *toolpkg.Registry,
	parentTracker *costpkg.Tracker,
	sessionStore *session.Store,
	artifactManager *artifactspkg.Manager,
	hookRunner *hooks.Runner,
	modelState *ActiveModelState,
	subagentModelState *ActiveSubagentModelState,
) subagentRunnerDeps {
	return subagentRunnerDeps{
		bridge:          bridge,
		registry:        registry,
		parentTracker:   parentTracker,
		sessionStore:    sessionStore,
		artifactManager: artifactManager,
		hookRunner:      hookRunner,
		chooseClient: func(modelOverride string) (api.LLMClient, string, error) {
			return chooseSubagentClient(modelState, subagentModelState, modelOverride, true)
		},
	}
}

// childRun is one child agent, prepared on the calling goroutine and executed
// on whichever goroutine runs it.
type childRun struct {
	req          toolpkg.AgentRunRequest
	subagentType string
	invocationID string
	client       api.LLMClient
	modelID      string
	// reasoningEffort is the request's normalized override, or "" to use the
	// configured effort.
	reasoningEffort string
	scope           subagentScope
	rolePolicy      *subagentRolePolicy
	toolNames       []string
	swarmSessionID  string
}

func makeSubagentRunner(
	bridge *ipc.Bridge,
	registry *toolpkg.Registry,
	permissionCtx *permissions.Context,
	parentTracker *costpkg.Tracker,
	sessionStore *session.Store,
	artifactManager *artifactspkg.Manager,
	hookRunner *hooks.Runner,
	modelState *ActiveModelState,
	subagentModelState *ActiveSubagentModelState,
	state *engineLoopState,
	fallbackCWD string,
) toolpkg.AgentRunner {
	deps := newSubagentRunnerDeps(bridge, registry, parentTracker, sessionStore, artifactManager, hookRunner, modelState, subagentModelState)
	return func(ctx context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		// The scope is read afresh on every call, here on the calling
		// goroutine. The permission snapshot in particular: a background
		// child otherwise copied it from its own goroutine while the parent's
		// later turns can be adding approval rules to it.
		return deps.run(ctx, req, captureSubagentScope(state, fallbackCWD, permissionCtx))
	}
}

// run prepares a child from its request and runs it in the given scope: to
// completion, or in the background when the request asks for that.
func (d subagentRunnerDeps) run(ctx context.Context, req toolpkg.AgentRunRequest, scope subagentScope) (toolpkg.AgentRunResult, error) {
	reasoningEffort := ""
	if strings.TrimSpace(req.ReasoningEffort) != "" {
		var err error
		reasoningEffort, err = normalizeReasoningEffortOverride(req.ReasoningEffort)
		if err != nil {
			return toolpkg.AgentRunResult{}, err
		}
	}

	childClient, childModelID, err := d.chooseClient(req.Model)
	if err != nil {
		return toolpkg.AgentRunResult{}, err
	}

	subagentType := toolpkg.NormalizeSubagentType(req.SubagentType)
	if subagentType == "" {
		subagentType = exploreSubagentType
	}
	if !toolpkg.IsSupportedSubagentType(subagentType) {
		return toolpkg.AgentRunResult{}, fmt.Errorf("agent subagent_type %q is not supported yet", subagentType)
	}

	invocationID := newSessionID()

	rolePolicy, childToolNames, err := loadSubagentRolePolicy(scope.cwd, req.Role, d.registry, subagentType)
	if err != nil {
		return toolpkg.AgentRunResult{}, err
	}
	child := childRun{
		req:             req,
		subagentType:    subagentType,
		invocationID:    invocationID,
		client:          childClient,
		modelID:         childModelID,
		reasoningEffort: reasoningEffort,
		scope:           scope,
		rolePolicy:      rolePolicy,
		toolNames:       childToolNames,
		swarmSessionID:  toolpkg.CurrentSwarmRuntimeSessionID(),
	}
	description := strings.TrimSpace(req.Description)
	role := strings.TrimSpace(req.Role)

	if req.Background {
		// The child reports to scope.ownerSessionID, which it can outlive.
		launch := launchBackgroundAgent(d.bridge, description, role, subagentType, invocationID, scope.ownerSessionID, d.sessionStore, func(runCtx context.Context, stopControl *agent.StopController, reportStatus func(toolpkg.AgentRunResult)) (toolpkg.AgentRunResult, error) {
			return d.execute(runCtx, child, stopControl, reportStatus)
		})
		launch.SubagentType = subagentType
		launch.Tools = append([]string(nil), childToolNames...)
		return withChildMetadata(launch, description, role), nil
	}
	result, err := d.execute(ctx, child, nil, nil)
	if err != nil {
		// The partial result carries what the child spent before it failed.
		return result, err
	}
	return withChildMetadata(result, description, role), nil
}

func currentSubagentCWD(state *engineLoopState, fallback string) string {
	if state != nil {
		if cwd := strings.TrimSpace(state.currentCWD()); cwd != "" {
			return cwd
		}
	}
	if cwd, err := os.Getwd(); err == nil {
		if trimmed := strings.TrimSpace(cwd); trimmed != "" {
			return trimmed
		}
	}
	return strings.TrimSpace(fallback)
}

// chooseSubagentClient picks the model client a child runs on: its request's
// model override when it has one, and otherwise the session's subagent model.
// saveSessionChoice says whether the session's choice, once coerced to a
// usable model, is written back to subagentModelState. Only a child started
// from the conversation's own turn may do that: a workflow's children run on
// a background goroutine, where the write would race with a slash command
// changing the subagent model between turns.
func chooseSubagentClient(modelState *ActiveModelState, subagentModelState *ActiveSubagentModelState, modelOverride string, saveSessionChoice bool) (api.LLMClient, string, error) {
	client, activeModelID := modelState.Get()
	if client == nil {
		return nil, "", fmt.Errorf("agent tool is unavailable: model client is not initialized")
	}
	if strings.TrimSpace(modelOverride) == "" {
		return resolveSubagentClient(client, activeModelID, subagentModelState, saveSessionChoice)
	}
	cfg := config.Load()
	selection, err := normalizeSubagentModelOverride(cfg, activeModelID, modelOverride)
	if err != nil {
		return nil, "", err
	}
	// Unlike the session's choice, an override is for this child only, so it
	// is not saved as the session's subagent model.
	return clientForSubagentSelection(client, activeModelID, selection, cfg)
}

func resolveSubagentClient(parent api.LLMClient, activeModelID string, subagentModelState *ActiveSubagentModelState, saveSessionChoice bool) (api.LLMClient, string, error) {
	cfg := config.Load()
	// Without a session choice, coerceSessionSubagentModel falls back to the
	// configured subagent model together with its provider. Reading
	// cfg.SubagentModel here instead dropped the provider the config keeps
	// in its own field.
	selection := coerceSessionSubagentModel(cfg, activeModelID, subagentModelState.Get())
	if subagentModelState != nil && saveSessionChoice {
		subagentModelState.Set(selection)
	}
	return clientForSubagentSelection(parent, activeModelID, selection, cfg)
}

// clientForSubagentSelection returns the client for a normalized subagent
// model selection, reusing the parent's client when the selection is the
// parent's own model.
func clientForSubagentSelection(parent api.LLMClient, activeModelID string, selection string, cfg config.Config) (api.LLMClient, string, error) {
	provider, activeModel := config.ParseModel(strings.TrimSpace(activeModelID))
	provider = normalizeProvider(provider)
	if strings.TrimSpace(activeModel) == "" {
		activeModel = strings.TrimSpace(provider)
		provider = ""
	}

	childProvider, childModel := resolveModelSelection(selection, provider)
	if childProvider == provider && strings.EqualFold(strings.TrimSpace(childModel), strings.TrimSpace(activeModel)) {
		return parent, modelRef(provider, activeModel), nil
	}
	childClient, err := newLLMClient(childProvider, childModel, cfg)
	if err != nil {
		return nil, "", fmt.Errorf("initialize subagent model %q: %w", selection, err)
	}

	return childClient, modelRef(childProvider, childClient.ModelID()), nil
}

// execute runs a prepared child to the end. stopControl and reportStatus come
// from the background launcher; a synchronous child has neither, and gets a
// stop controller of its own when structured_output needs one.
func (d subagentRunnerDeps) execute(
	ctx context.Context,
	child childRun,
	stopControl *agent.StopController,
	reportStatus func(toolpkg.AgentRunResult),
) (toolpkg.AgentRunResult, error) {
	req := child.req
	subagentType := child.subagentType
	invocationID := child.invocationID
	client := child.client
	cwd := child.scope.cwd
	childSessionID := invocationID

	var structuredOutput *toolpkg.StructuredOutputTool
	if len(req.OutputSchema) > 0 {
		if stopControl == nil {
			stopControl = agent.NewStopController()
		}
		var err error
		structuredOutput, err = newChildStructuredOutput(req.OutputSchema, stopControl)
		if err != nil {
			return toolpkg.AgentRunResult{}, err
		}
	}

	workspace, err := prepareDelegatedWorkspace(ctx, req, invocationID, cwd)
	if err != nil {
		return toolpkg.AgentRunResult{}, err
	}
	// Remove a freshly created worktree if setup fails before the child runs;
	// once the query starts the worktree may hold real work and is kept.
	setupComplete := false
	defer func() {
		if !setupComplete {
			cleanupDelegatedWorktree(workspace)
		}
	}()
	childCWD := cwd
	if strings.TrimSpace(workspace.Path) != "" {
		childCWD = workspace.Path
	}
	// Looked up once: the child saves its session after every step, and each
	// save would otherwise run git again for a branch that goes with the
	// working directory recorded at launch.
	childBranch := firstNonEmpty(strings.TrimSpace(workspace.Branch), currentGitBranch())
	childStartedAt := time.Now()
	childTracker := costpkg.NewTracker()
	childRegistry := d.registry.CloneFiltered(child.toolNames)
	if structuredOutput != nil {
		childRegistry.Register(structuredOutput)
	}
	childBridge := ipc.NewBridge(strings.NewReader(""), io.Discard)
	childTimingLogger := timing.NewSessionLogger(d.sessionStore.SessionDir(childSessionID))
	childSkills, err := loadAvailableSkills(d.bridge, childCWD)
	if err != nil {
		return toolpkg.AgentRunResult{}, err
	}
	childMode := agent.ModeFast
	startHookMessages := runChildStartHooks(ctx, d.hookRunner, childSessionID, invocationID, req, subagentType)
	childHandoff := childTaskMessage(d.bridge, d.sessionStore.SessionDir(childSessionID), req, subagentType)
	childMessages := []api.Message{{Role: api.RoleUser, Content: injectChildHookContext(childHandoff, startHookMessages)}}
	allowedDefs := childRegistry.Definitions()
	childPrompt, err := withRolePromptSections(subagentSystemPrompt(subagentType, allowedDefs), cwd, req.Role)
	if err != nil {
		return toolpkg.AgentRunResult{}, err
	}
	if section := childOutputPromptSection(req.ForWorkflow, structuredOutput != nil); section != "" {
		childPrompt = swarm.JoinPromptSections(childPrompt, section)
	}
	queryTools := allowedDefs
	executionRegistry := childRegistry
	transcriptPath := filepath.Join(d.sessionStore.SessionDir(childSessionID), "transcript.ndjson")
	resultFile := filepath.Join(d.sessionStore.SessionDir(childSessionID), "agent-result.json")
	lifecycle := &childLifecycleTracker{}
	nudger := &structuredOutputNudger{tool: structuredOutput}

	if err := persistSessionState(d.sessionStore, sessionStateParams{
		SessionID:     childSessionID,
		CreatedAt:     childStartedAt,
		Mode:          childMode,
		Model:         child.modelID,
		SubagentModel: "",
		CWD:           childCWD,
		Branch:        childBranch,
		Tracker:       childTracker,
		Messages:      childMessages,
	}); err != nil {
		return toolpkg.AgentRunResult{}, err
	}
	_ = d.sessionStore.SaveMetadata(session.Metadata{
		SessionID:     childSessionID,
		CreatedAt:     childStartedAt,
		UpdatedAt:     childStartedAt,
		Mode:          string(childMode),
		Model:         child.modelID,
		SubagentModel: "",
		CWD:           childCWD,
		Branch:        childBranch,
		TotalCostUSD:  0,
		Title:         req.Description,
	})

	// The child's own record of what it has read; see toolpkg.WithFileReadState.
	childReadState := toolpkg.NewFileReadState()
	childDeps := agent.QueryDeps{
		CallModel: func(callCtx context.Context, modelReq api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
			return trackModelStream(callCtx, childBridge, childTracker, client, modelReq)
		},
		ExecuteToolBatch: func(callCtx context.Context, calls []api.ToolCall) ([]api.ToolResult, error) {
			callCtx = toolpkg.WithFileReadState(callCtx, childReadState)
			return executeToolCallsForSubagent(callCtx, subagentType, child.rolePolicy, executionRegistry, child.scope.permissionCtx, d.hookRunner, d.artifactManager, childSessionID, d.sessionStore.SessionDir(childSessionID), childTracker, client.Capabilities().MaxOutputTokens, calls)
		},
		CompactMessages: func(callCtx context.Context, current []api.Message, reason agent.CompactReason) (compact.CompactResult, error) {
			sessionMemory, _ := loadSessionMemorySnapshot(callCtx, d.artifactManager, childSessionID)
			result, err := compactWithMetrics(callCtx, childBridge, childTracker, client, childTimingLogger, childSessionID, 0, string(reason), sessionMemory, childPrompt, queryTools, current)
			// The compacted conversation may no longer hold the reads the
			// child's "unchanged" answers would point back to.
			childReadState.Reset()
			return result, err
		},
		RecallMemory: func(callCtx context.Context, files []agent.MemoryFile, userPrompt string) ([]agent.MemoryRecallResult, error) {
			selector := memorypkg.RecallSelector{}
			return selector.Select(callCtx, files, userPrompt)
		},
		LoadSessionMemory: func(callCtx context.Context) (agent.SessionMemorySnapshot, error) {
			return loadSessionMemorySnapshot(callCtx, d.artifactManager, childSessionID)
		},
		BeforeStop: func(callCtx context.Context, stopReq agent.StopRequest) (agent.StopDecision, error) {
			if decision, nudge := nudger.beforeStop(stopReq.StopReason); nudge {
				return decision, nil
			}
			return evaluateChildStopHooks(callCtx, d.hookRunner, childSessionID, invocationID, req, subagentType, stopReq, lifecycle, transcriptPath, resultFile, reportStatus, child.rolePolicy, d.sessionStore, child.swarmSessionID, childStartedAt)
		},
		StopController: stopControl,
		ApplyResultBudget: func(current []api.Message) []api.Message {
			return current
		},
		EmitTelemetry: childBridge.EmitEvent,
		PersistMessages: func(updated []api.Message) {
			childMessages = updated
			_ = persistSessionState(d.sessionStore, sessionStateParams{
				SessionID:     childSessionID,
				CreatedAt:     childStartedAt,
				Mode:          childMode,
				Model:         child.modelID,
				SubagentModel: "",
				CWD:           childCWD,
				Branch:        childBranch,
				Tracker:       childTracker,
				Messages:      childMessages,
			})
		},
		Clock: time.Now,
	}
	turnStopReason := ""
	runQuery := func() error {
		stream := agent.QueryStream(ctx, agent.QueryRequest{
			Messages:        childMessages,
			SystemPrompt:    childPrompt,
			ModelID:         client.ModelID(),
			ReasoningEffort: queryReasoningEffort(child.reasoningEffort),
			Mode:            childMode,
			SessionID:       childSessionID,
			Skills:          childSkills,
			Tools:           queryTools,
			Capabilities:    client.Capabilities(),
			ContextWindow:   client.Capabilities().PromptTokenBudget(),
			MaxTokens:       client.Capabilities().MaxOutputTokens,
			SessionMemory:   agent.SessionMemorySnapshot{},
		}, childDeps)
		for event, streamErr := range stream {
			if streamErr != nil {
				runChildStopFailureHooks(ctx, d.hookRunner, childSessionID, invocationID, req, subagentType, childMessages, streamErr)
				return streamErr
			}
			if event.Type == ipc.EventTurnComplete {
				var payload ipc.TurnCompletePayload
				if err := json.Unmarshal(event.Payload, &payload); err == nil {
					turnStopReason = payload.StopReason
				}
			}
		}
		return nil
	}
	setupComplete = true
	var queryErr error
	if workspace.Strategy == swarm.WorkspaceWorktree {
		_, queryErr = withDelegatedWorkspace(workspace, func(string) (toolpkg.AgentRunResult, error) {
			return toolpkg.AgentRunResult{}, runQuery()
		})
	} else {
		queryErr = runQuery()
	}
	if queryErr != nil {
		// The tokens a child spent before it failed or was cancelled were
		// still spent. Charging them here keeps the session's cost honest,
		// and the usage that comes back with the error lets a workflow count
		// it against its budget.
		snapshot := childTracker.Snapshot()
		chargeChildCost(d.bridge, d.parentTracker, d.sessionStore, child.scope.ownerSessionID, snapshot)
		return toolpkg.AgentRunResult{
			Status:         "failed",
			InvocationID:   invocationID,
			SubagentType:   subagentType,
			SessionID:      childSessionID,
			TranscriptPath: transcriptPath,
			Error:          queryErr.Error(),
			TotalCostUSD:   snapshot.TotalCostUSD,
			InputTokens:    snapshot.TotalInputTokens,
			OutputTokens:   snapshot.TotalOutputTokens,
		}, queryErr
	}

	if err := persistSessionState(d.sessionStore, sessionStateParams{
		SessionID:     childSessionID,
		CreatedAt:     childStartedAt,
		Mode:          childMode,
		Model:         child.modelID,
		SubagentModel: "",
		CWD:           childCWD,
		Branch:        childBranch,
		Tracker:       childTracker,
		Messages:      childMessages,
	}); err != nil {
		return toolpkg.AgentRunResult{}, err
	}

	// A structured_output call ends the child with its own stop reason, which
	// is a completion like any other; only a cancel is not.
	status := "completed"
	errorMessage := ""
	if turnStopReason == "cancelled" {
		status = "cancelled"
		errorMessage = "background child agent cancelled"
	}

	childSnapshot := childTracker.Snapshot()
	chargeChildCost(d.bridge, d.parentTracker, d.sessionStore, child.scope.ownerSessionID, childSnapshot)

	summary := childSummary(childMessages, turnStopReason)
	var structured json.RawMessage
	if structuredOutput != nil && status == "completed" {
		if value, recorded := structuredOutput.Value(); recorded {
			structured = value
			// The recorded object is the child's answer; text it wrote
			// along the way is not.
			summary = string(value)
		} else {
			// The caller expects an object. The child's text, reported as a
			// success, would hand it prose instead. The run still comes back
			// as a result rather than a bare error, so its caller keeps the
			// transcript and the spend.
			status = "failed"
			errorMessage = "agent finished without calling structured_output"
			if isChildLimitStopReason(turnStopReason) {
				errorMessage = fmt.Sprintf("agent stopped at a limit (%s) before calling structured_output", turnStopReason)
			}
		}
	}

	result := toolpkg.AgentRunResult{
		Status:         status,
		InvocationID:   invocationID,
		SubagentType:   subagentType,
		SessionID:      childSessionID,
		TranscriptPath: transcriptPath,
		OutputFile:     resultFile,
		Summary:        summary,
		Error:          errorMessage,
		TotalCostUSD:   childSnapshot.TotalCostUSD,
		InputTokens:    childSnapshot.TotalInputTokens,
		OutputTokens:   childSnapshot.TotalOutputTokens,
		Tools:          toolDefinitionNames(childRegistry.Definitions()),
		Metadata:       decorateChildWorkspaceMetadata(lifecycle.metadata(), workspace),
		Structured:     structured,
	}
	if result.Metadata != nil {
		result.Metadata.Role = strings.TrimSpace(req.Role)
	}
	saveAgentResultFile(d.bridge, result)
	return result, nil
}

// structuredOutputStopReason is the stop reason of a child that ended by
// calling structured_output.
const structuredOutputStopReason = "structured_output"

// newChildStructuredOutput makes a child's structured_output tool. A valid
// call ends the child's run at once: the result is in hand, and another model
// turn would only spend tokens on prose nobody reads.
func newChildStructuredOutput(schema json.RawMessage, stopControl *agent.StopController) (*toolpkg.StructuredOutputTool, error) {
	tool, err := toolpkg.NewStructuredOutputTool(schema, func() {
		stopControl.Request(structuredOutputStopReason)
	})
	if err != nil {
		return nil, fmt.Errorf("agent output schema: %w", err)
	}
	return tool, nil
}

// maxStructuredOutputNudges is how many times a child that tries to finish
// without calling structured_output is sent back to call it.
const maxStructuredOutputNudges = 2

const structuredOutputNudgeMessage = "You have not called structured_output. Your result is only delivered through that tool: call structured_output now with an object that matches its schema. Do not answer in text."

// structuredOutputNudger sends a child back to call structured_output when it
// tries to finish without having called it, a bounded number of times. Only
// the query loop calls it, so it needs no lock.
type structuredOutputNudger struct {
	tool *toolpkg.StructuredOutputTool
	sent int
}

// beforeStop returns the decision that keeps the child going and true when
// the child should be nudged, and false when the stop is for the stop hooks
// to judge.
func (n *structuredOutputNudger) beforeStop(stopReason string) (agent.StopDecision, bool) {
	if n == nil || n.tool == nil {
		return agent.StopDecision{}, false
	}
	// A cancel is the user overriding the child; nothing may hold it open.
	// At a limit the query cannot go on whatever the decision, so a nudge
	// would only keep the stop hooks from judging the child's real stop.
	if isCancelledStopReason(stopReason) || isChildLimitStopReason(stopReason) || n.sent >= maxStructuredOutputNudges {
		return agent.StopDecision{}, false
	}
	if _, recorded := n.tool.Value(); recorded {
		return agent.StopDecision{}, false
	}
	n.sent++
	return agent.StopDecision{
		Continue:        true,
		Reason:          "structured_output was not called",
		FollowUpMessage: structuredOutputNudgeMessage,
	}, true
}

// isChildLimitStopReason reports a stop the query loop forces at one of its
// limits, which no stop decision can carry the child past.
func isChildLimitStopReason(stopReason string) bool {
	switch stopReason {
	case agent.StopReasonMaxTurns, agent.ContinuationStopBudgetExhausted, agent.ContinuationStopDiminishingReturns:
		return true
	}
	return false
}

// childTaskMessage is the child's first message. A long prompt written by the
// parent model is archived and replaced by a brief. A workflow step's prompt
// is built by a program for that one step and arrives whole: a brief would
// drop exactly the data the step exists to process.
func childTaskMessage(bridge *ipc.Bridge, sessionDir string, req toolpkg.AgentRunRequest, subagentType string) string {
	if req.ForWorkflow {
		return strings.TrimSpace(req.Prompt)
	}
	promptArchivePath, archiveErr := archiveDelegatedPrompt(sessionDir, req.Description, req.Prompt)
	if archiveErr != nil && bridge != nil {
		_ = bridge.EmitNotice(fmt.Sprintf("archive child prompt: %v", archiveErr))
	}
	return buildDelegatedPromptBrief(req.Description, req.Prompt, req.Role, subagentType, promptArchivePath)
}

// childOutputPromptSection tells a child how its answer is read when that is
// not the default, a <final_answer> report to the parent agent.
func childOutputPromptSection(forWorkflow bool, hasOutputSchema bool) string {
	var sections []string
	if forWorkflow {
		workflow := `Workflow step:
You are one step of an automated workflow. A program, not a person, reads your result and uses it as data.
No preamble, no recap of how you worked, no offers of follow-up work.`
		if !hasOutputSchema {
			workflow += "\nPut exactly the result the task asks for inside <final_answer>; a format the task gives replaces the format above."
		}
		sections = append(sections, workflow)
	}
	if hasOutputSchema {
		sections = append(sections, `Structured output:
Deliver your result by calling the structured_output tool exactly once, with an object that matches its schema, when your work is done. That call is your answer and ends your run: do not write a final text answer or <final_answer>, whatever the format above says. If the call reports a schema mismatch, fix the object and call it again.`)
	}
	return swarm.JoinPromptSections(sections...)
}

// queryReasoningEffort is the effort a child's model calls ask for. Without an
// override it is the configured effort, read when the child starts as it
// always was. No provider has a "max" level: it asks for xhigh, the highest
// any offers, which the client lowers to high for a model without xhigh.
func queryReasoningEffort(override string) string {
	switch override {
	case "":
		return config.Load().ReasoningEffort
	case reasoningEffortMax:
		return api.ReasoningEffortXHigh
	default:
		return override
	}
}

// childSummary is the child's last reply, which the parent agent takes as its
// result. When the child stopped at a limit rather than finishing, a note
// says so: the reply may be work in progress, and the limit notice the user
// would see goes nowhere for a child.
func childSummary(messages []api.Message, stopReason string) string {
	summary := latestAssistantContent(messages)
	switch stopReason {
	case agent.StopReasonMaxTurns, agent.ContinuationStopBudgetExhausted, agent.ContinuationStopDiminishingReturns:
		note := fmt.Sprintf("[The agent stopped at a limit (%s) before it finished, so this may be incomplete.]", stopReason)
		return strings.TrimSpace(summary + "\n\n" + note)
	default:
		return summary
	}
}

// chargeChildCost adds a finished child's spend to the session that launched
// it. While that session is active, the spend goes into the live tracker. A
// background child can outlive its session, and then the tracker holds the
// session that replaced it, so the spend is added to the saved total of the
// child's own session instead.
func chargeChildCost(bridge *ipc.Bridge, parentTracker *costpkg.Tracker, store *session.Store, ownerSessionID string, snapshot costpkg.TrackerSnapshot) {
	if ownerSessionID == activeSessionID() {
		parentTracker.RecordChildAgentSnapshot(snapshot)
		_ = emitCostUpdate(bridge, parentTracker)
		return
	}
	err := store.UpdateMetadata(ownerSessionID, func(meta session.Metadata) session.Metadata {
		meta.TotalCostUSD += snapshot.TotalCostUSD
		return meta
	})
	if err != nil && bridge != nil {
		_ = bridge.EmitNotice(fmt.Sprintf("Could not add a background agent's cost of $%.4f to session %s: %v", snapshot.TotalCostUSD, ownerSessionID, err))
	}
}

type childLifecycleTracker struct {
	stopBlockReason string
	stopBlockCount  int
}

func (s *childLifecycleTracker) noteStopBlock(reason string) {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		reason = "blocked by child stop hook"
	}
	s.stopBlockReason = reason
	s.stopBlockCount++
}

func (s *childLifecycleTracker) metadata() *toolpkg.ChildAgentMetadata {
	if s == nil || s.stopBlockCount == 0 {
		return nil
	}
	return &toolpkg.ChildAgentMetadata{
		StopBlockReason: s.stopBlockReason,
		StopBlockCount:  s.stopBlockCount,
	}
}

func runChildStartHooks(
	ctx context.Context,
	hookRunner *hooks.Runner,
	childSessionID string,
	invocationID string,
	req toolpkg.AgentRunRequest,
	subagentType string,
) []string {
	if hookRunner == nil {
		return nil
	}
	responses, _ := hookRunner.Run(ctx, hooks.Payload{
		Type:      hooks.HookSubagentStart,
		SessionID: childSessionID,
		Extra: map[string]any{
			"child_agent":        true,
			"agent_id":           invocationID,
			"agent_type":         subagentType,
			"invocation_id":      invocationID,
			"description":        req.Description,
			"prompt":             req.Prompt,
			"role":               req.Role,
			"workspace_strategy": req.WorkspaceStrategy,
			"subagent_type":      subagentType,
			"run_in_background":  req.Background,
		},
	})
	messages := make([]string, 0, len(responses))
	for _, resp := range responses {
		if message := strings.TrimSpace(resp.Message); message != "" {
			messages = append(messages, message)
		}
	}
	return messages
}

// withRolePromptSections layers a swarm role's prompt overlay and handoff
// instructions onto a child's system prompt. A child launched without a role
// keeps the prompt as is: the project's swarm spec only describes roles, so
// nothing in it applies, and asking it about an empty role name is an error.
func withRolePromptSections(prompt string, cwd string, role string) (string, error) {
	if strings.TrimSpace(role) == "" {
		return prompt, nil
	}
	if overlay, err := swarm.LoadRolePromptOverlay(cwd, role); err == nil {
		prompt = swarm.JoinPromptSections(prompt, overlay.Content)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if instructions, err := swarm.LoadRoleHandoffInstructions(cwd, role); err == nil {
		prompt = swarm.JoinPromptSections(prompt, instructions)
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return prompt, nil
}

func injectChildHookContext(prompt string, hookMessages []string) string {
	prompt = strings.TrimSpace(prompt)
	if len(hookMessages) == 0 {
		return prompt
	}
	lines := make([]string, 0, len(hookMessages)+3)
	lines = append(lines, "Additional local child-agent context:")
	for _, message := range hookMessages {
		message = strings.TrimSpace(message)
		if message == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s", message))
	}
	if prompt != "" {
		lines = append(lines, "", "Delegated task:", prompt)
	}
	return strings.Join(lines, "\n")
}

func evaluateChildStopHooks(
	ctx context.Context,
	hookRunner *hooks.Runner,
	childSessionID string,
	invocationID string,
	req toolpkg.AgentRunRequest,
	subagentType string,
	stopReq agent.StopRequest,
	lifecycle *childLifecycleTracker,
	transcriptPath string,
	resultFile string,
	reportStatus func(toolpkg.AgentRunResult),
	rolePolicy *subagentRolePolicy,
	store *session.Store,
	swarmSessionID string,
	childStartedAt time.Time,
) (agent.StopDecision, error) {
	// A cancel is the user overriding the child, so nothing may hold it open.
	// Getting here consumed the stop request, and the next one only escalates
	// to a hard cancel while a request is still pending - so a hook that kept
	// blocking would turn every later stop into another soft, blockable one
	// and the child could not be stopped at all.
	if isCancelledStopReason(stopReq.StopReason) {
		return agent.StopDecision{}, nil
	}
	if blocked, reason, followUp, err := rolePolicy.completionBlocked(store, swarmSessionID, childStartedAt, stopReq.StopReason); err != nil {
		return agent.StopDecision{}, err
	} else if blocked {
		return blockChildStop(lifecycle, reason, followUp, childSessionID, invocationID, req.Role, subagentType, transcriptPath, resultFile, reportStatus), nil
	}

	if hookRunner == nil {
		return agent.StopDecision{}, nil
	}
	responses, err := hookRunner.Run(ctx, hooks.Payload{
		Type:      hooks.HookSubagentStop,
		SessionID: childSessionID,
		Output:    stopReq.AssistantMessage.Content,
		Extra: map[string]any{
			"child_agent":        true,
			"agent_id":           invocationID,
			"agent_type":         subagentType,
			"invocation_id":      invocationID,
			"description":        req.Description,
			"role":               req.Role,
			"workspace_strategy": req.WorkspaceStrategy,
			"subagent_type":      subagentType,
			"run_in_background":  req.Background,
			"stop_reason":        stopReq.StopReason,
			"turn_count":         stopReq.TurnCount,
		},
	})
	if err != nil {
		return agent.StopDecision{}, err
	}
	blocked, reason := blockedChildStop(responses)
	if !blocked {
		return agent.StopDecision{}, nil
	}
	return blockChildStop(lifecycle, reason, childStopBlockedFollowUp(reason), childSessionID, invocationID, req.Role, subagentType, transcriptPath, resultFile, reportStatus), nil
}

func blockChildStop(
	lifecycle *childLifecycleTracker,
	reason string,
	followUp string,
	childSessionID string,
	invocationID string,
	role string,
	subagentType string,
	transcriptPath string,
	resultFile string,
	reportStatus func(toolpkg.AgentRunResult),
) agent.StopDecision {
	lifecycle.noteStopBlock(reason)
	if reportStatus != nil {
		reportStatus(toolpkg.AgentRunResult{
			Status:         "running",
			InvocationID:   invocationID,
			SubagentType:   subagentType,
			SessionID:      childSessionID,
			TranscriptPath: transcriptPath,
			OutputFile:     resultFile,
			Metadata: &toolpkg.ChildAgentMetadata{
				Role:            strings.TrimSpace(role),
				LifecycleState:  "stop_blocked",
				StatusMessage:   reason,
				StopBlockReason: lifecycle.stopBlockReason,
				StopBlockCount:  lifecycle.stopBlockCount,
			},
		})
	}
	return agent.StopDecision{
		Continue:        true,
		Reason:          reason,
		FollowUpMessage: followUp,
	}
}

func runChildStopFailureHooks(
	ctx context.Context,
	hookRunner *hooks.Runner,
	childSessionID string,
	invocationID string,
	req toolpkg.AgentRunRequest,
	subagentType string,
	messages []api.Message,
	err error,
) {
	if hookRunner == nil || err == nil {
		return
	}
	_, _ = hookRunner.Run(ctx, hooks.Payload{
		Type:      hooks.HookSubagentStopFailure,
		SessionID: childSessionID,
		Output:    latestAssistantContent(messages),
		Error:     err.Error(),
		Extra: map[string]any{
			"child_agent":        true,
			"agent_id":           invocationID,
			"agent_type":         subagentType,
			"invocation_id":      invocationID,
			"description":        req.Description,
			"role":               req.Role,
			"workspace_strategy": req.WorkspaceStrategy,
			"subagent_type":      subagentType,
			"run_in_background":  req.Background,
		},
	})
}

func blockedChildStop(responses []hooks.Response) (bool, string) {
	for _, resp := range responses {
		action := strings.ToLower(strings.TrimSpace(resp.Action))
		if action != "deny" && action != "stop" {
			continue
		}
		reason := strings.TrimSpace(resp.Message)
		if reason == "" {
			reason = "blocked by child stop hook"
		}
		return true, reason
	}
	return false, ""
}

func childStopBlockedFollowUp(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return "A local stop hook blocked completion. Continue working until the stop condition is satisfied."
	}
	return fmt.Sprintf("A local stop hook blocked completion: %s\n\nContinue working until the stop condition is satisfied.", reason)
}

func withChildMetadata(result toolpkg.AgentRunResult, description string, role string) toolpkg.AgentRunResult {
	result.Metadata = buildChildMetadata(result, description, role)
	return result
}

func buildChildMetadata(result toolpkg.AgentRunResult, description string, role string) *toolpkg.ChildAgentMetadata {
	invocationID := firstNonEmpty(result.InvocationID, result.SessionID)
	if invocationID == "" && result.AgentID == "" {
		return nil
	}
	metadata := &toolpkg.ChildAgentMetadata{}
	if result.Metadata != nil {
		*metadata = *result.Metadata
		metadata.Tools = append([]string(nil), result.Metadata.Tools...)
	}
	metadata.InvocationID = firstNonEmpty(invocationID, metadata.InvocationID)
	metadata.AgentID = firstNonEmpty(result.AgentID, metadata.AgentID)
	metadata.Description = firstNonEmpty(strings.TrimSpace(description), metadata.Description)
	metadata.Role = firstNonEmpty(strings.TrimSpace(role), metadata.Role)
	metadata.SubagentType = firstNonEmpty(result.SubagentType, metadata.SubagentType)
	metadata.LifecycleState = childLifecycleState(result.Status, metadata.LifecycleState)
	if strings.TrimSpace(result.Summary) != "" || strings.TrimSpace(result.Error) != "" || strings.TrimSpace(metadata.StatusMessage) == "" {
		metadata.StatusMessage = childStatusMessage(result)
	}
	metadata.SessionID = firstNonEmpty(result.SessionID, metadata.SessionID)
	metadata.TranscriptPath = firstNonEmpty(result.TranscriptPath, metadata.TranscriptPath)
	metadata.ResultPath = firstNonEmpty(result.OutputFile, metadata.ResultPath)
	if len(result.Tools) > 0 {
		metadata.Tools = append([]string(nil), result.Tools...)
	}
	return metadata
}

func childLifecycleState(status string, existing string) string {
	switch strings.TrimSpace(status) {
	case "", "async_launched":
		return "launching"
	case "running":
		if strings.TrimSpace(existing) == "stop_blocked" {
			return existing
		}
		return "running"
	default:
		return strings.TrimSpace(status)
	}
}

func childStatusMessage(result toolpkg.AgentRunResult) string {
	if summary := strings.TrimSpace(result.Summary); summary != "" {
		return summary
	}
	if errText := strings.TrimSpace(result.Error); errText != "" {
		return errText
	}
	switch strings.TrimSpace(result.Status) {
	case "async_launched":
		return "Background child agent launched."
	case "running":
		return "Background child agent is still running."
	case "cancelling":
		return "Cancellation requested for background child agent."
	case "completed":
		return "Background child agent completed."
	case "cancelled":
		return "Background child agent cancelled."
	case "failed":
		return "Background child agent failed."
	default:
		return "Child agent updated."
	}
}

func subagentSystemPrompt(subagentType string, defs []api.ToolDefinition) string {
	subagentType = toolpkg.NormalizeSubagentType(subagentType)
	names := toolDefinitionNames(defs)
	toolList := strings.Join(names, ", ")
	common := fmt.Sprintf(`You are Nami CLI %s, a bounded subagent in a fresh context.
Use tools early. Keep the transcript terse. Final answer concise, concrete, evidence-first.
Always use absolute paths. The working directory is in the environment context below.
No follow-up questions to the parent/user. If context is thin, inspect the workspace, make the best reasonable assumptions, and continue. State assumptions briefly in the final answer.
You are not alone in this codebase: the user, the parent agent, or sibling agents may be editing it at the same time. Never revert changes you did not make; adjust your work to accommodate them.
Available tools: %s.
If additional tool schemas appear for cache compatibility, treat any tool not listed above as unavailable; those calls will be rejected.
If swarm handoff tools are available, use them for structured role handoffs and inbox state instead of burying that state in prose.`, subagentDisplayName(subagentType), toolList)

	switch subagentType {
	case exploreSubagentType:
		return strings.TrimSpace(fmt.Sprintf(`%s

Read-only codebase explorer.
Search broad first. Narrow after evidence.
If first pass is weak, run another pass with different anchors.
Prefer file_search, grep_search, read_file, project_overview, read_project_structure, go_definition, go_references.
No file writes. No background control.
You may use swarm handoff and inbox tools when they are available.

Return ONLY <final_answer>.

Format:
Scope: <one sentence>
Findings:
- <fact>
Evidence:
- /absolute/path/to/file:10-40
Open questions:
- <only if needed>

Example:
<final_answer>
Scope: trace auth token refresh path
Findings:
- Token refresh starts in /absolute/path/to/auth.go:44-91 and retries once in /absolute/path/to/client.go:120-168.
Evidence:
- /absolute/path/to/auth.go:44-91
- /absolute/path/to/client.go:120-168
</final_answer>`, common))
	case verificationSubagentType:
		return strings.TrimSpace(fmt.Sprintf(`%s

Verification specialist. Try to break the work.
Reading code is not verification. Run commands. Check output.
Do not modify project files. Do not install deps. Do not run git write commands.
No background control beyond provided command tools.
You may use swarm handoff and inbox tools when they are available.
If environment blocks verification, say exactly why.

Return ONLY <final_answer>.

Format:
Checks:
- <command> -> <result>
Findings:
- <fact>
VERDICT: PASS|FAIL|PARTIAL

Example:
<final_answer>
Checks:
- go test ./... -> FAIL: ./internal/api timeout in TestStream
Findings:
- Reproduced failure. No evidence of fix.
VERDICT: FAIL
</final_answer>`, common))
	case generalPurposeSubagentType:
		return strings.TrimSpace(fmt.Sprintf(`%s

General-purpose subagent.
Complete the delegated task fully. Research first when needed. Edit only when the task requires it.
Stay inside the files and modules the parent assigned you; if the fix truly requires touching code outside that scope, note it in the final answer instead of expanding on your own.
No interactive approval. If denied, explain the constraint, then continue with safe inspection.
Use swarm handoff and inbox tools when they are available and the delegated workflow needs structured state.

Return ONLY <final_answer>.

Format:
Scope: <one sentence>
Done:
- <completed work>
Evidence:
- /absolute/path/to/file:10-40
Next:
- <only if needed>`, common))
	default:
		return strings.TrimSpace(fmt.Sprintf(`%s

Read-only, artifact-safe: no file modifications, no artifact writes, no background process control.
Delegated task only. Parallel read-only exploration when helpful. Concise findings with paths and next steps.`, common))
	}
}

func subagentToolNames(subagentType string) []string {
	switch toolpkg.NormalizeSubagentType(subagentType) {
	case generalPurposeSubagentType:
		return generalPurposeSubagentTools
	case verificationSubagentType:
		return verificationSubagentTools
	default:
		return exploreSubagentTools
	}
}

func toolDefinitionNames(defs []api.ToolDefinition) []string {
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}
	return names
}

func latestAssistantContent(messages []api.Message) string {
	for _, msg := range slices.Backward(messages) {
		if msg.Role != api.RoleAssistant {
			continue
		}
		if strings.TrimSpace(msg.Content) == "" {
			continue
		}
		return normalizeSubagentFinalAnswer(msg.Content)
	}
	return "Subagent completed without a final text response. See the child transcript for details."
}

func executeToolCallsForSubagent(
	ctx context.Context,
	subagentType string,
	rolePolicy *subagentRolePolicy,
	registry *toolpkg.Registry,
	permissionCtx *permissions.Context,
	hookRunner *hooks.Runner,
	artifactManager *artifactspkg.Manager,
	sessionID string,
	sessionDir string,
	tracker *costpkg.Tracker,
	maxOutputTokens int,
	calls []api.ToolCall,
) ([]api.ToolResult, error) {
	results := make([]api.ToolResult, len(calls))
	pending := make([]toolpkg.PendingCall, 0, len(calls))
	budget := toolpkg.DefaultResultBudgetForModel(sessionDir, maxOutputTokens)
	aggregateBudget := toolpkg.NewAggregateResultBudget(budget)
	permissionGate := permissions.ExecutorGate{Context: permissionCtx}

	for index, call := range calls {
		normalized, err := normalizeToolCall(call)
		if err != nil {
			results[index] = api.ToolResult{ToolCallID: call.ID, Output: err.Error(), IsError: true}
			continue
		}
		alwaysAllowed := childAlwaysMayCall(normalized.Name)
		if !alwaysAllowed && !subagentAllowsToolName(subagentType, normalized.Name) {
			results[index] = api.ToolResult{ToolCallID: normalized.ID, Output: fmt.Sprintf("tool %q is not allowed in the %s subagent", normalized.Name, subagentType), IsError: true}
			continue
		}
		if !alwaysAllowed && rolePolicy != nil && !rolePolicy.allowsToolName(normalized.Name) {
			results[index] = api.ToolResult{ToolCallID: normalized.ID, Output: rolePolicy.toolDeniedMessage(normalized.Name), IsError: true}
			continue
		}
		tool, err := registry.Get(normalized.Name)
		if err != nil {
			results[index] = api.ToolResult{ToolCallID: normalized.ID, Output: err.Error(), IsError: true}
			continue
		}
		input, err := decodeToolInput(normalized)
		if err != nil {
			results[index] = api.ToolResult{ToolCallID: normalized.ID, Output: err.Error(), IsError: true}
			continue
		}
		if err := toolpkg.ValidateToolCall(tool, input); err != nil {
			results[index] = api.ToolResult{ToolCallID: normalized.ID, Output: err.Error(), IsError: true}
			continue
		}
		if !alwaysAllowed && !subagentAllowsTool(subagentType, tool.Permission()) {
			results[index] = api.ToolResult{ToolCallID: normalized.ID, Output: fmt.Sprintf("tool %q is not allowed in the %s subagent", tool.Name(), subagentType), IsError: true}
			continue
		}
		// The user's tool hooks apply to every call the agent makes; if they
		// skipped children, delegating a call would be a way around them.
		if reason, denied := preToolUseHookDenial(ctx, hookRunner, sessionID, normalized); denied {
			results[index] = api.ToolResult{ToolCallID: normalized.ID, Output: reason, IsError: true}
			continue
		}
		pending = append(pending, toolpkg.PendingCall{Index: index, Tool: tool, Input: input})
	}

	for _, batch := range toolpkg.PartitionBatches(pending) {
		batchStart := time.Now()
		batchResults := toolpkg.ExecuteBatchWithOptions(ctx, batch, toolpkg.ExecuteOptions{
			PermissionGate: permissionGate.Check,
		})
		if tracker != nil {
			tracker.RecordToolDuration(time.Since(batchStart))
		}
		for _, result := range batchResults {
			call := calls[result.Index]
			toolResult := api.ToolResult{ToolCallID: call.ID, FilePath: result.Output.FilePath}
			if result.Err != nil {
				toolResult.Output = result.Err.Error()
				toolResult.IsError = true
				results[result.Index] = toolResult
				continue
			}

			// A failed call's output is budgeted too: a failing command can
			// return megabytes. When saving the full output fails,
			// budgetToolOutput still returns the truncated preview. Falling
			// back to the raw output instead would put an arbitrarily large
			// result into the child's context.
			output, _, budgetInfo, err := budgetToolOutput(ctx, artifactManager, sessionID, budget, aggregateBudget, call, result.Output.Output)
			if err != nil {
				output += fmt.Sprintf("\n[The full output could not be saved: %v]", err)
			}
			toolResult.Output = output
			toolResult.IsError = result.Output.IsError
			results[result.Index] = toolResult
			rememberInlineReadResult(toolpkg.FileReadStateFor(ctx), result.Output, budgetInfo.Spilled)
			if !result.Output.IsError {
				runPostToolUseHooks(ctx, hookRunner, sessionID, call, output)
			}
		}
	}

	return results, nil
}

func subagentDisplayName(subagentType string) string {
	switch toolpkg.NormalizeSubagentType(subagentType) {
	case generalPurposeSubagentType:
		return "General Purpose"
	case verificationSubagentType:
		return "Verification"
	default:
		return "Explore"
	}
}

func subagentAllowsTool(subagentType string, permission toolpkg.PermissionLevel) bool {
	switch toolpkg.NormalizeSubagentType(subagentType) {
	case exploreSubagentType:
		return permission == toolpkg.PermissionReadOnly
	case verificationSubagentType:
		return permission != toolpkg.PermissionWrite
	default:
		return true
	}
}

// childAlwaysMayCall reports whether a tool passes every per-child allowlist:
// the subagent type's, a swarm role's, and the permission limit of the type.
// structured_output is in none of them, since a child has it exactly when its
// request set an output schema, and the child's own registry is what grants
// it. A role or type that restricts tools must not stop a child from
// delivering the result it was asked for.
func childAlwaysMayCall(toolName string) bool {
	return toolName == toolpkg.StructuredOutputToolName
}

func subagentAllowsToolName(subagentType string, toolName string) bool {
	allowed := subagentToolNames(subagentType)
	return slices.Contains(allowed, toolName)
}

func normalizeSubagentFinalAnswer(content string) string {
	const openTag, closeTag = "<final_answer>", "</final_answer>"
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return ""
	}
	// The tags are found in a lowercased copy and the offsets used to slice
	// the original, so the copy must keep every byte where it was. Unicode
	// lowercasing does not: "Ⱥ" grows to three bytes and "İ" shrinks to one,
	// which shifted the cut into the wrong text or past the end of it.
	lower := asciiLower(trimmed)
	start := strings.Index(lower, openTag)
	beforeClose, _, found := strings.CutLast(lower, closeTag)
	if start == -1 || !found || len(beforeClose) <= start {
		return trimmed
	}
	inner := strings.TrimSpace(trimmed[start+len(openTag) : len(beforeClose)])
	if inner == "" {
		return trimmed
	}
	return inner
}

// asciiLower lowercases ASCII letters only, leaving every other byte - and so
// every byte offset - unchanged.
func asciiLower(s string) string {
	lowered := []byte(s)
	for i, c := range lowered {
		if 'A' <= c && c <= 'Z' {
			lowered[i] = c + ('a' - 'A')
		}
	}
	return string(lowered)
}

// Session data - prompts, transcripts, results - routinely carries secrets
// from the conversation, so it is kept private to the user like the rest of
// the session store.
const (
	sessionDataDirMode  = 0o700
	sessionDataFileMode = 0o600
)

func archiveDelegatedPrompt(sessionDir string, description string, prompt string) (string, error) {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" || utf8.RuneCountInString(prompt) <= delegationPromptArchiveLimit {
		return "", nil
	}
	if err := os.MkdirAll(sessionDir, sessionDataDirMode); err != nil {
		return "", err
	}
	path := filepath.Join(sessionDir, delegationPromptArchiveName)
	var builder strings.Builder
	if strings.TrimSpace(description) != "" {
		builder.WriteString("Description: ")
		builder.WriteString(strings.TrimSpace(description))
		builder.WriteString("\n\n")
	}
	builder.WriteString(prompt)
	builder.WriteString("\n")
	if err := os.WriteFile(path, []byte(builder.String()), sessionDataFileMode); err != nil {
		return "", err
	}
	return path, nil
}

func buildDelegatedPromptBrief(description string, prompt string, role string, subagentType string, archivePath string) string {
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return ""
	}
	if utf8.RuneCountInString(prompt) <= delegationPromptArchiveLimit {
		return prompt
	}

	briefLines := collectDelegatedPromptLines(prompt, delegationPromptLineLimit, delegationPromptBriefLimit)
	anchors := extractDelegatedPromptAnchors(prompt, delegationPromptAnchorLimit)

	var builder strings.Builder
	builder.WriteString("Delegated task brief:\n")
	if strings.TrimSpace(description) != "" {
		builder.WriteString("- Summary: ")
		builder.WriteString(strings.TrimSpace(description))
		builder.WriteString("\n")
	}
	if strings.TrimSpace(role) != "" {
		builder.WriteString("- Role: ")
		builder.WriteString(strings.TrimSpace(role))
		builder.WriteString("\n")
	}
	builder.WriteString("- Subagent: ")
	builder.WriteString(subagentDisplayName(subagentType))
	builder.WriteString("\n")
	if len(briefLines) > 0 {
		builder.WriteString("- Key instructions:\n")
		for _, line := range briefLines {
			builder.WriteString("  - ")
			builder.WriteString(line)
			builder.WriteString("\n")
		}
	}
	if len(anchors) > 0 {
		builder.WriteString("- Key anchors:\n")
		for _, anchor := range anchors {
			builder.WriteString("  - ")
			builder.WriteString(anchor)
			builder.WriteString("\n")
		}
	}
	if archivePath != "" {
		builder.WriteString("- Full delegated prompt saved at ")
		builder.WriteString(archivePath)
		builder.WriteString(". Read it only if the brief is insufficient.\n")
	}
	return strings.TrimSpace(builder.String())
}

func collectDelegatedPromptLines(prompt string, maxLines int, maxChars int) []string {
	lines := strings.Split(prompt, "\n")
	selected := make([]string, 0, maxLines)
	seen := make(map[string]struct{}, maxLines)
	totalChars := 0
	for _, raw := range lines {
		line := normalizeDelegatedPromptLine(raw)
		if line == "" {
			continue
		}
		if _, ok := seen[line]; ok {
			continue
		}
		if totalChars+len(line) > maxChars && len(selected) > 0 {
			break
		}
		selected = append(selected, line)
		seen[line] = struct{}{}
		totalChars += len(line)
		if len(selected) >= maxLines {
			break
		}
	}
	return selected
}

func normalizeDelegatedPromptLine(value string) string {
	line := strings.TrimSpace(value)
	if line == "" {
		return ""
	}
	line = strings.TrimPrefix(line, "- ")
	line = strings.TrimPrefix(line, "* ")
	line = strings.TrimSpace(line)
	if line == "" || line == "```" {
		return ""
	}
	line = strings.Join(strings.Fields(line), " ")
	// Cut on characters, not bytes, so a multibyte character is never split.
	if runes := []rune(line); len(runes) > 220 {
		line = strings.TrimSpace(string(runes[:220]))
	}
	return line
}

func extractDelegatedPromptAnchors(prompt string, limit int) []string {
	matches := delegatedPromptAnchorPattern.FindAllString(prompt, -1)
	anchors := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	for _, match := range matches {
		anchor := strings.TrimSpace(strings.Trim(match, "`"))
		if anchor == "" {
			continue
		}
		if _, ok := seen[anchor]; ok {
			continue
		}
		seen[anchor] = struct{}{}
		anchors = append(anchors, anchor)
		if len(anchors) >= limit {
			break
		}
	}
	return anchors
}
