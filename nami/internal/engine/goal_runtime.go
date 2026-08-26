package engine

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	goalpkg "github.com/channyeintun/nami/internal/goal"
	"github.com/channyeintun/nami/internal/ipc"
)

var (
	goalStoresMu sync.Mutex
	goalStores   = map[string]*goalpkg.Store{}
)

// goalStoreFor returns the goal store for a session directory, creating it on
// first use. Keying on the directory rather than threading a store through the
// turn context is what lets the slash handler and the stop evaluator — which
// reach the session by different routes — share one goal.
func goalStoreFor(sessionDir string) *goalpkg.Store {
	sessionDir = strings.TrimSpace(sessionDir)
	if sessionDir == "" {
		return nil
	}
	goalStoresMu.Lock()
	defer goalStoresMu.Unlock()
	if store, ok := goalStores[sessionDir]; ok {
		return store
	}
	store := goalpkg.NewStore(sessionDir)
	store.Load()
	goalStores[sessionDir] = store
	return store
}

// goalBlockCap is how many times in a row the goal may block completion before
// the loop yields. NAMI_GOAL_BLOCK_CAP overrides it; 0 disables the cap.
func goalBlockCap() int {
	raw := strings.TrimSpace(os.Getenv("NAMI_GOAL_BLOCK_CAP"))
	if raw == "" {
		return goalpkg.DefaultBlockCap
	}
	cap, err := strconv.Atoi(raw)
	if err != nil || cap < 0 {
		return goalpkg.DefaultBlockCap
	}
	return cap
}

// evaluateSessionGoal decides whether an active goal should hold the turn open.
// It runs after the file stop hooks, which are cheap and user-authored; this
// one costs a model call, so it should never be the first thing tried.
func evaluateSessionGoal(
	ctx context.Context,
	bridge *ipc.Bridge,
	store *goalpkg.Store,
	client api.LLMClient,
	stopReq agent.StopRequest,
) (agent.StopDecision, error) {
	state, ok := store.Snapshot()
	if !ok {
		return agent.StopDecision{}, nil
	}

	// Work done since the last block earns the loop a fresh budget. The cap is
	// there to catch a loop that is spinning, not one that is merely long.
	if assistantUsedTools(stopReq.Messages) {
		store.NoteProgress()
		state, _ = store.Snapshot()
	}

	if limit := goalBlockCap(); limit > 0 && state.ConsecutiveBlocks >= limit {
		// The goal stays set so the user's next prompt resumes the loop; only
		// this turn is released.
		emitGoalNotice(bridge, fmt.Sprintf(
			"Goal paused after %d checks without progress: %s. Send another message to resume, or /goal clear to drop it.",
			state.ConsecutiveBlocks, state.Condition,
		))
		return agent.StopDecision{}, nil
	}

	store.NoteEvaluated()
	verdict := goalpkg.Evaluate(ctx, client, state.Condition, stopReq.Messages)
	state, _ = store.Snapshot()

	switch {
	case verdict.Met:
		store.Clear()
		emitGoalState(bridge, ipc.GoalStateChangedPayload{
			Active:     false,
			Met:        true,
			Condition:  state.Condition,
			Reason:     verdict.Reason,
			Iterations: state.Iterations,
		})
		return agent.StopDecision{}, nil

	case verdict.Impossible:
		// An impossible goal is cleared, not blocked: looping on it would only
		// burn turns. It is recorded as failed rather than achieved.
		store.Clear()
		emitGoalState(bridge, ipc.GoalStateChangedPayload{
			Active:     false,
			Met:        false,
			Failed:     true,
			Condition:  state.Condition,
			Reason:     verdict.Reason,
			Iterations: state.Iterations,
		})
		emitGoalNotice(bridge, fmt.Sprintf("Goal cleared as unreachable: %s", goalReasonOrDefault(verdict.Reason)))
		return agent.StopDecision{}, nil
	}

	store.NoteBlocked(verdict.Reason)
	state, _ = store.Snapshot()
	emitGoalState(bridge, ipc.GoalStateChangedPayload{
		Active:     true,
		Met:        false,
		Condition:  state.Condition,
		Reason:     verdict.Reason,
		Iterations: state.Iterations,
	})
	return agent.StopDecision{
		Continue:        true,
		Reason:          verdict.Reason,
		FollowUpMessage: goalBlockedFollowUp(state.Condition, verdict.Reason),
	}, nil
}

// assistantUsedTools reports whether the assistant did anything beyond talking
// in the tail of the transcript, which is the signal that the loop is still
// moving rather than restating itself.
func assistantUsedTools(messages []api.Message) bool {
	for i := len(messages) - 1; i >= 0; i-- {
		message := messages[i]
		if message.Role == api.RoleUser {
			// Reached the start of the current stretch of assistant work.
			return false
		}
		if message.Role == api.RoleAssistant && len(message.ToolCalls) > 0 {
			return true
		}
	}
	return false
}

// emitGoalState and emitGoalNotice tolerate a nil bridge. Bridge's own methods
// do not, and goal evaluation runs from a stop path that must never panic the
// turn just to report on itself.
func emitGoalState(bridge *ipc.Bridge, payload ipc.GoalStateChangedPayload) {
	if bridge == nil {
		return
	}
	_ = bridge.Emit(ipc.EventGoalStateChanged, payload)
}

// emitCurrentGoalState announces whatever goal a session currently holds,
// including "none", so the UI always reflects the session it is attached to.
func emitCurrentGoalState(bridge *ipc.Bridge, store *goalpkg.Store) {
	state, ok := store.Snapshot()
	if !ok {
		emitGoalState(bridge, ipc.GoalStateChangedPayload{Active: false})
		return
	}
	emitGoalState(bridge, ipc.GoalStateChangedPayload{
		Active:     true,
		Condition:  state.Condition,
		Reason:     state.LastReason,
		Iterations: state.Iterations,
	})
}

func emitGoalNotice(bridge *ipc.Bridge, message string) {
	if bridge == nil {
		return
	}
	_ = bridge.EmitNotice(message)
}

func goalReasonOrDefault(reason string) string {
	if trimmed := strings.TrimSpace(reason); trimmed != "" {
		return trimmed
	}
	return "no reason given"
}

// goalBlockedFollowUp is the message the agent reads when the goal holds the
// turn open. It restates the condition and what is missing, because by this
// point the agent believes it is finished and needs the specific gap.
func goalBlockedFollowUp(condition string, reason string) string {
	return fmt.Sprintf(
		"The session goal is not satisfied yet.\n\nGoal: %s\nStill missing: %s\n\nKeep working until the goal holds. Do not stop to ask what to do next, and do not report completion until the goal is actually met.",
		strings.TrimSpace(condition),
		goalReasonOrDefault(reason),
	)
}

// goalAcknowledgementPrompt is injected as a user turn when a goal is set, so
// work starts immediately instead of waiting for the user to say "go".
func goalAcknowledgementPrompt(condition string) string {
	return fmt.Sprintf(
		"A session-scoped goal is now active: %q. Briefly acknowledge it, then immediately start (or continue) working toward it — treat the goal itself as your directive and do not pause to ask what to do. The turn cannot end until the goal holds. It clears itself once satisfied, so do not tell the user to run /goal clear after success; that is only for dropping a goal early.",
		strings.TrimSpace(condition),
	)
}
