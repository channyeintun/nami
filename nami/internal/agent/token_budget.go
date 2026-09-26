package agent

const (
	ContinuationStopNone               = ""
	ContinuationStopBudgetExhausted    = "budget_exhausted"
	ContinuationStopDiminishingReturns = "diminishing_returns"
)

// ContinuationTracker judges whether it is still worth continuing a reply the
// model gave without calling a tool: one cut off at max_tokens, or one a stop
// hook or goal sent back for more work. Such replies form a run that the next
// tool turn ends, since a tool call is the work itself.
type ContinuationTracker struct {
	// ContinuationCount is the number of replies in the current run.
	ContinuationCount int
	// RecentTokenDeltas holds the output of the run's last few replies.
	RecentTokenDeltas []int
	// MaxBudgetTokens is the model's cap on the output of one reply.
	MaxBudgetTokens int
	// BudgetUsedTokens is the output of the current run.
	BudgetUsedTokens int
}

// ContinuationDecision describes whether another continuation is worthwhile.
type ContinuationDecision struct {
	ShouldStop bool
	Reason     string
}

// NewContinuationTracker creates a tracker with the given budget.
func NewContinuationTracker(maxBudget int) ContinuationTracker {
	return ContinuationTracker{
		MaxBudgetTokens: maxBudget,
	}
}

// Record records one model turn and its token output. A reply without tool
// calls continues the current run. A tool turn is productive work, not a sign
// of the model stalling or running on, so it ends the run and counts toward
// nothing: the budget is the cap on one reply, and charging every tool turn
// to it ended multi-step tasks after a few thousand tokens of output.
func (t *ContinuationTracker) Record(tokensProduced int, isToolTurn bool) {
	if isToolTurn {
		*t = NewContinuationTracker(t.MaxBudgetTokens)
		return
	}
	t.BudgetUsedTokens += tokensProduced
	t.ContinuationCount++
	t.RecentTokenDeltas = append(t.RecentTokenDeltas, tokensProduced)
	if len(t.RecentTokenDeltas) > 5 {
		t.RecentTokenDeltas = t.RecentTokenDeltas[1:]
	}
}

// Decision returns whether continuation is still worthwhile and why it stopped.
func (t *ContinuationTracker) Decision() ContinuationDecision {
	// Stop at 90% budget
	if t.MaxBudgetTokens > 0 && t.BudgetUsedTokens >= t.MaxBudgetTokens*9/10 {
		return ContinuationDecision{ShouldStop: true, Reason: ContinuationStopBudgetExhausted}
	}

	// Stop on diminishing returns: 3+ continuations, last 2 under 500 tokens
	if t.ContinuationCount >= 3 && len(t.RecentTokenDeltas) >= 2 {
		last := t.RecentTokenDeltas[len(t.RecentTokenDeltas)-1]
		prev := t.RecentTokenDeltas[len(t.RecentTokenDeltas)-2]
		if last < 500 && prev < 500 {
			return ContinuationDecision{ShouldStop: true, Reason: ContinuationStopDiminishingReturns}
		}
	}

	return ContinuationDecision{}
}
