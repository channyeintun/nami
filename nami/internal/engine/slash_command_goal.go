package engine

import (
	"fmt"
	"slices"
	"strings"
	"time"

	goalpkg "github.com/channyeintun/nami/internal/goal"
)

// goalClearWords are the arguments that drop the active goal instead of
// becoming one. A user typing "/goal stop" means to stop the loop, not to set
// a goal literally named "stop".
var goalClearWords = []string{"clear", "cancel", "none", "off", "reset", "stop"}

func handleGoalSlashCommand(cmd *slashCommandContext) error {
	store := goalStoreFor(cmd.store.SessionDir(cmd.state.SessionID))
	if store == nil {
		return emitTextResponse(cmd.bridge, "Goals are unavailable: this session has no directory to record one in.")
	}

	args := strings.TrimSpace(cmd.args)
	switch {
	case args == "":
		return emitTextResponse(cmd.bridge, describeGoal(store))
	case slices.Contains(goalClearWords, strings.ToLower(args)):
		return clearGoal(cmd, store)
	default:
		return setGoal(cmd, store, args)
	}
}

func describeGoal(store *goalpkg.Store) string {
	state, ok := store.Snapshot()
	if !ok {
		return "No goal set. Use `/goal <condition>` to keep working until a condition holds."
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Goal: %s\n", state.Condition)
	fmt.Fprintf(&b, "Set %s ago", formatGoalAge(time.Since(state.SetAt)))
	if state.Iterations > 0 {
		fmt.Fprintf(&b, " · checked %s", pluralizeGoalChecks(state.Iterations))
	}
	b.WriteString("\n")
	if state.LastReason != "" {
		fmt.Fprintf(&b, "Last check: %s\n", state.LastReason)
	}
	b.WriteString("\nUse `/goal clear` to drop it.")
	return b.String()
}

func clearGoal(cmd *slashCommandContext, store *goalpkg.Store) error {
	previous := store.Clear()
	if previous == "" {
		return emitTextResponse(cmd.bridge, "No goal set.")
	}
	// The dispatcher announces the session's goal state after every slash
	// command, so there is nothing to emit here.
	return emitTextResponse(cmd.bridge, fmt.Sprintf("Goal cleared: %s", previous))
}

func setGoal(cmd *slashCommandContext, store *goalpkg.Store, condition string) error {
	previous, err := store.Set(condition)
	if err != nil {
		return emitTextResponse(cmd.bridge, fmt.Sprintf("Could not set that goal: %v", err))
	}

	state, _ := store.Snapshot()
	header := fmt.Sprintf("Goal set: %s", state.Condition)
	if previous != "" {
		header = fmt.Sprintf("Goal replaced (was: %s)\nGoal set: %s", previous, state.Condition)
	}
	appendSlashResponse(cmd.bridge, header+"\n\n")

	// Setting a goal is a request to start working on it, so the turn continues
	// into an injected user prompt rather than ending here. Deliberately no
	// emitTextResponse: that would close the turn before the work begins.
	cmd.state.FollowUpPrompt = goalAcknowledgementPrompt(state.Condition)
	return nil
}

func formatGoalAge(age time.Duration) string {
	switch {
	case age < time.Minute:
		return "less than a minute"
	case age < time.Hour:
		return pluralizeGoalUnit(int(age.Minutes()), "minute")
	default:
		return pluralizeGoalUnit(int(age.Hours()), "hour")
	}
}

func pluralizeGoalChecks(count int) string {
	return pluralizeGoalUnit(count, "time")
}

func pluralizeGoalUnit(count int, unit string) string {
	if count == 1 {
		return fmt.Sprintf("1 %s", unit)
	}
	return fmt.Sprintf("%d %ss", count, unit)
}
