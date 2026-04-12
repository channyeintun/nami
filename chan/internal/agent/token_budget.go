package agent

const (
	ContinuationStopNone               = ""
	ContinuationStopBudgetExhausted    = "budget_exhausted"
	ContinuationStopDiminishingReturns = "diminishing_returns"
)

// ContinuationTracker monitors whether continued iteration is worthwhile.
type ContinuationTracker struct {
	ContinuationCount int
	RecentTokenDeltas []int
	MaxBudgetTokens   int
	BudgetUsedTokens  int
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

// Record records a continuation with its token output.
// When isToolTurn is true the output tokens are counted toward the overall
// budget but excluded from the diminishing-returns window, because tool-use
// turns are productive work — not signs of the model stalling.
func (t *ContinuationTracker) Record(tokensProduced int, isToolTurn bool) {
	t.BudgetUsedTokens += tokensProduced
	if !isToolTurn {
		t.ContinuationCount++
		t.RecentTokenDeltas = append(t.RecentTokenDeltas, tokensProduced)
		if len(t.RecentTokenDeltas) > 5 {
			t.RecentTokenDeltas = t.RecentTokenDeltas[1:]
		}
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

// ShouldStop returns true when continuation is no longer useful.
func (t *ContinuationTracker) ShouldStop() bool {
	return t.Decision().ShouldStop
}
