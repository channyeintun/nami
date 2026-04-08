package agent

// ContinuationTracker monitors whether continued iteration is worthwhile.
type ContinuationTracker struct {
	ContinuationCount int
	RecentTokenDeltas []int
	MaxBudgetTokens   int
	BudgetUsedTokens  int
}

// NewContinuationTracker creates a tracker with the given budget.
func NewContinuationTracker(maxBudget int) ContinuationTracker {
	return ContinuationTracker{
		MaxBudgetTokens: maxBudget,
	}
}

// Record records a continuation with its token output.
func (t *ContinuationTracker) Record(tokensProduced int) {
	t.ContinuationCount++
	t.BudgetUsedTokens += tokensProduced
	t.RecentTokenDeltas = append(t.RecentTokenDeltas, tokensProduced)
	if len(t.RecentTokenDeltas) > 5 {
		t.RecentTokenDeltas = t.RecentTokenDeltas[1:]
	}
}

// ShouldStop returns true when continuation is no longer useful.
func (t *ContinuationTracker) ShouldStop() bool {
	// Stop at 90% budget
	if t.MaxBudgetTokens > 0 && t.BudgetUsedTokens >= t.MaxBudgetTokens*9/10 {
		return true
	}

	// Stop on diminishing returns: 3+ continuations, last 2 under 500 tokens
	if t.ContinuationCount >= 3 && len(t.RecentTokenDeltas) >= 2 {
		last := t.RecentTokenDeltas[len(t.RecentTokenDeltas)-1]
		prev := t.RecentTokenDeltas[len(t.RecentTokenDeltas)-2]
		if last < 500 && prev < 500 {
			return true
		}
	}

	return false
}
