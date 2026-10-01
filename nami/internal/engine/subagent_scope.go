package engine

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/channyeintun/nami/internal/api"
	artifactspkg "github.com/channyeintun/nami/internal/artifacts"
	"github.com/channyeintun/nami/internal/config"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/hooks"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/permissions"
	"github.com/channyeintun/nami/internal/session"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

// subagentScope is what a child agent would otherwise read from the live
// session on every call: the session it reports to, its working directory,
// and a snapshot of the parent's permissions. A workflow captures it once at
// launch, so agents it starts after /clear or /resume still belong to the
// session that launched it.
//
// cwd is the directory the child's session records and its role policy and
// environment context are read from. Its tools still resolve relative paths
// against the process's working directory, which is why nothing may move
// that directory while a workflow runs (see hasRunningWorkflows).
type subagentScope struct {
	ownerSessionID string
	cwd            string
	// permissionCtx is already a clone; nothing else mutates it, and a child
	// only reads it, so the children of one scope may share it.
	permissionCtx *permissions.Context
}

// captureSubagentScope snapshots the scope. It must run on the goroutine that
// owns permissionCtx, which is the tool call's.
func captureSubagentScope(state *engineLoopState, fallbackCWD string, permissionCtx *permissions.Context) subagentScope {
	return subagentScope{
		ownerSessionID: activeSessionID(),
		cwd:            currentSubagentCWD(state, fallbackCWD),
		permissionCtx:  permissions.CloneContext(permissionCtx),
	}
}

// makeScopedSubagentRunner is makeSubagentRunner with the scope fixed at
// capture time instead of read on every call. It runs every child
// synchronously; a request with Background set is an error.
func makeScopedSubagentRunner(
	bridge *ipc.Bridge,
	registry *toolpkg.Registry,
	parentTracker *costpkg.Tracker,
	sessionStore *session.Store,
	artifactManager *artifactspkg.Manager,
	hookRunner *hooks.Runner,
	modelState *ActiveModelState,
	subagentModelState *ActiveSubagentModelState,
	scope subagentScope,
) toolpkg.AgentRunner {
	deps := newSubagentRunnerDeps(bridge, registry, parentTracker, sessionStore, artifactManager, hookRunner, modelState, subagentModelState)
	// Scoped children run off the conversation's goroutine, so they read the
	// session's subagent model but never write it back.
	deps.chooseClient = func(modelOverride string) (api.LLMClient, string, error) {
		return chooseSubagentClient(modelState, subagentModelState, modelOverride, false)
	}
	return deps.scopedRunner(scope)
}

// scopedRunner runs every child in scope. A background launch is refused: its
// caller schedules and waits for its own children, and a background child
// would run on a detached context that cancelling the caller cannot reach.
func (d subagentRunnerDeps) scopedRunner(scope subagentScope) toolpkg.AgentRunner {
	return func(ctx context.Context, req toolpkg.AgentRunRequest) (toolpkg.AgentRunResult, error) {
		if req.Background {
			return toolpkg.AgentRunResult{}, errors.New("child agents started from a captured scope run synchronously; a background launch is not supported")
		}
		return d.run(ctx, req, scope)
	}
}

// checkSubagentModelOverride validates a per-child model override against the
// providers that are set up and returns it normalized. It never changes the
// session's subagent model.
func checkSubagentModelOverride(model string, modelState *ActiveModelState) (string, error) {
	if strings.TrimSpace(model) == "" {
		return "", errors.New("model override is empty")
	}
	_, activeModelID := modelState.Get()
	return normalizeSubagentModelOverride(config.Load(), activeModelID, model)
}

// normalizeSubagentModelOverride resolves a per-child model the way
// coerceSessionSubagentModel resolves the session's choice, without its
// fallback: an override no provider can serve is an error, not a quiet switch
// to some other model.
func normalizeSubagentModelOverride(cfg config.Config, activeModelID string, model string) (string, error) {
	model = strings.TrimSpace(model)
	activeProvider, _ := config.ParseModel(strings.TrimSpace(activeModelID))
	defaultProvider := ""
	if retainSelectionProvider(activeProvider) {
		defaultProvider = normalizeProvider(activeProvider)
	}
	if normalized, ok := normalizeUsableSubagentSelection(cfg, activeModelID, model, defaultProvider); ok {
		return normalized, nil
	}
	provider, modelID := resolveSubagentSelection(model, normalizeProvider(activeProvider), defaultProvider)
	if modelID == "" {
		return "", fmt.Errorf("model %q is not a model selection; use provider/model", model)
	}
	return "", fmt.Errorf("model %q is not available: provider %q is unknown or not set up", model, provider)
}

// reasoningEffortMax asks for the most reasoning the model offers.
const reasoningEffortMax = "max"

// reasoningEffortOverrides are the efforts a child may ask for, in order.
var reasoningEffortOverrides = []string{
	api.ReasoningEffortLow,
	api.ReasoningEffortMedium,
	api.ReasoningEffortHigh,
	api.ReasoningEffortXHigh,
	reasoningEffortMax,
}

// normalizeReasoningEffortOverride validates a per-child reasoning effort and
// returns its canonical spelling: low, medium, high, xhigh, or max.
func normalizeReasoningEffortOverride(effort string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(effort))
	if slices.Contains(reasoningEffortOverrides, normalized) {
		return normalized, nil
	}
	return "", fmt.Errorf("reasoning effort %q is not one of %s", effort, strings.Join(reasoningEffortOverrides, ", "))
}
