package engine

import (
	"strings"
	"testing"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/api"
	goalpkg "github.com/channyeintun/nami/internal/goal"
)

func TestGoalStoreForIsPerSessionDirectory(t *testing.T) {
	first := t.TempDir()
	second := t.TempDir()

	a := goalStoreFor(first)
	b := goalStoreFor(first)
	c := goalStoreFor(second)

	if a != b {
		t.Fatal("the same session directory produced two stores")
	}
	if a == c {
		t.Fatal("two session directories shared one store")
	}
	if goalStoreFor("  ") != nil {
		t.Fatal("a blank session directory produced a store")
	}
}

func TestGoalStoreForReloadsAPersistedGoal(t *testing.T) {
	dir := t.TempDir()
	seed := goalpkg.NewStore(dir)
	if _, err := seed.Set("green build"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// A fresh registry entry must pick the goal up from disk, which is what
	// makes a goal survive a reconnect.
	store := goalStoreFor(dir)
	state, ok := store.Snapshot()
	if !ok || state.Condition != "green build" {
		t.Fatalf("state = %+v, ok = %v", state, ok)
	}
}

// With no goal set, the evaluator must be inert — no model call, no block.
func TestEvaluateSessionGoalIsInertWithoutAGoal(t *testing.T) {
	decision, err := evaluateSessionGoal(t.Context(), nil, goalStoreFor(t.TempDir()), nil, agent.StopRequest{})
	if err != nil {
		t.Fatalf("evaluateSessionGoal: %v", err)
	}
	if decision.Continue {
		t.Fatal("blocked the turn without a goal set")
	}
}

// A nil client makes the judge unavailable. That must release the turn rather
// than trap it: an evaluator that cannot answer must never hold the loop open.
func TestEvaluateSessionGoalReleasesWhenTheJudgeIsUnavailable(t *testing.T) {
	dir := t.TempDir()
	store := goalStoreFor(dir)
	if _, err := store.Set("all tests pass"); err != nil {
		t.Fatalf("Set: %v", err)
	}

	decision, err := evaluateSessionGoal(t.Context(), nil, store, nil, agent.StopRequest{})
	if err != nil {
		t.Fatalf("evaluateSessionGoal: %v", err)
	}
	if decision.Continue {
		t.Fatal("an unavailable judge blocked the turn")
	}
	if store.Active() {
		t.Fatal("goal survived a Met verdict")
	}
}

// Once the block budget is spent, the loop yields but keeps the goal, so the
// user's next message resumes it.
func TestEvaluateSessionGoalYieldsAtTheBlockCap(t *testing.T) {
	dir := t.TempDir()
	store := goalStoreFor(dir)
	if _, err := store.Set("ship it"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	for range goalpkg.DefaultBlockCap {
		store.NoteBlocked("not yet")
	}

	decision, err := evaluateSessionGoal(t.Context(), nil, store, nil, agent.StopRequest{})
	if err != nil {
		t.Fatalf("evaluateSessionGoal: %v", err)
	}
	if decision.Continue {
		t.Fatal("blocked past the cap")
	}
	if !store.Active() {
		t.Fatal("the cap cleared the goal; it should stay set so the next prompt resumes it")
	}
	state, _ := store.Snapshot()
	if state.Iterations != 0 {
		t.Fatalf("the capped turn still ran an evaluation: %+v", state)
	}
}

// Tool use between blocks is what distinguishes a long loop from a stuck one,
// so it has to refill the budget before the cap is checked.
func TestEvaluateSessionGoalResetsTheBudgetOnToolUse(t *testing.T) {
	dir := t.TempDir()
	store := goalStoreFor(dir)
	if _, err := store.Set("ship it"); err != nil {
		t.Fatalf("Set: %v", err)
	}
	for range goalpkg.DefaultBlockCap {
		store.NoteBlocked("not yet")
	}

	stopReq := agent.StopRequest{Messages: []api.Message{
		{Role: api.RoleUser, Content: "go"},
		{Role: api.RoleAssistant, ToolCalls: []api.ToolCall{{Name: "bash"}}},
		{Role: api.RoleTool, Content: "ok"},
	}}
	if _, err := evaluateSessionGoal(t.Context(), nil, store, nil, stopReq); err != nil {
		t.Fatalf("evaluateSessionGoal: %v", err)
	}
	// The budget was refilled, so this turn evaluated instead of yielding at
	// the cap; the nil judge then cleared the goal as met.
	if store.Active() {
		t.Fatal("goal was not evaluated after the budget reset")
	}
}

func TestAssistantUsedTools(t *testing.T) {
	tests := []struct {
		name     string
		messages []api.Message
		want     bool
	}{
		{name: "empty", messages: nil, want: false},
		{
			name: "assistant only talked",
			messages: []api.Message{
				{Role: api.RoleUser, Content: "go"},
				{Role: api.RoleAssistant, Content: "done"},
			},
			want: false,
		},
		{
			name: "assistant called a tool",
			messages: []api.Message{
				{Role: api.RoleUser, Content: "go"},
				{Role: api.RoleAssistant, ToolCalls: []api.ToolCall{{Name: "bash"}}},
			},
			want: true,
		},
		{
			// Tool use before the latest user message belongs to an earlier
			// stretch of work and must not count as progress in this one.
			name: "tool use before the latest user turn",
			messages: []api.Message{
				{Role: api.RoleAssistant, ToolCalls: []api.ToolCall{{Name: "bash"}}},
				{Role: api.RoleUser, Content: "keep going"},
				{Role: api.RoleAssistant, Content: "done"},
			},
			want: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := assistantUsedTools(test.messages); got != test.want {
				t.Fatalf("assistantUsedTools = %v, want %v", got, test.want)
			}
		})
	}
}

func TestGoalBlockCapHonorsTheEnvironmentOverride(t *testing.T) {
	if got := goalBlockCap(); got != goalpkg.DefaultBlockCap {
		t.Fatalf("goalBlockCap = %d, want the default %d", got, goalpkg.DefaultBlockCap)
	}
	t.Setenv("NAMI_GOAL_BLOCK_CAP", "3")
	if got := goalBlockCap(); got != 3 {
		t.Fatalf("goalBlockCap = %d, want 3", got)
	}
	t.Setenv("NAMI_GOAL_BLOCK_CAP", "0")
	if got := goalBlockCap(); got != 0 {
		t.Fatalf("goalBlockCap = %d, want the cap disabled", got)
	}
	// A malformed override falls back rather than disabling the backstop.
	t.Setenv("NAMI_GOAL_BLOCK_CAP", "lots")
	if got := goalBlockCap(); got != goalpkg.DefaultBlockCap {
		t.Fatalf("goalBlockCap = %d, want the default for a bad value", got)
	}
	t.Setenv("NAMI_GOAL_BLOCK_CAP", "-4")
	if got := goalBlockCap(); got != goalpkg.DefaultBlockCap {
		t.Fatalf("goalBlockCap = %d, want the default for a negative value", got)
	}
}

// The follow-up has to name the goal and the gap: by this point the agent
// believes it is finished, so a generic nudge would not redirect it.
func TestGoalBlockedFollowUpNamesTheGoalAndTheGap(t *testing.T) {
	followUp := goalBlockedFollowUp("all tests pass", "two tests still fail")
	for _, want := range []string{"all tests pass", "two tests still fail", "Keep working"} {
		if !strings.Contains(followUp, want) {
			t.Fatalf("follow-up missing %q:\n%s", want, followUp)
		}
	}
	if got := goalBlockedFollowUp("x", "  "); !strings.Contains(got, "no reason given") {
		t.Fatalf("follow-up without a reason = %q", got)
	}
}

func TestGoalAcknowledgementPromptStartsWorkImmediately(t *testing.T) {
	prompt := goalAcknowledgementPrompt("ship the release")
	for _, want := range []string{"ship the release", "immediately start", "clears itself"} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("prompt missing %q:\n%s", want, prompt)
		}
	}
}
