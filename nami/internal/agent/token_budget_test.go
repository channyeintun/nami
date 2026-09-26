package agent

import "testing"

func TestContinuationBudgetIgnoresToolTurns(t *testing.T) {
	// The budget is the model's cap on one reply. Tool turns are the work of
	// a multi-step task, and charging them to it ended a 4096-token model's
	// tasks after about 3.7k tokens of output in all.
	tracker := NewContinuationTracker(4096)
	for range 20 {
		tracker.Record(1000, true)
	}
	if decision := tracker.Decision(); decision.ShouldStop {
		t.Fatalf("20k tokens of tool turns stopped the query: %q", decision.Reason)
	}
}

func TestContinuationBudgetCoversOneRunOfReplies(t *testing.T) {
	tracker := NewContinuationTracker(4096)
	tracker.Record(2000, false)
	if decision := tracker.Decision(); decision.ShouldStop {
		t.Fatalf("a reply within the budget stopped the query: %q", decision.Reason)
	}
	tracker.Record(2000, false)
	if decision := tracker.Decision(); decision.Reason != ContinuationStopBudgetExhausted {
		t.Fatalf("a run of replies past the budget gave %q, want %q", decision.Reason, ContinuationStopBudgetExhausted)
	}

	tracker.Record(100, true)
	tracker.Record(2000, false)
	if decision := tracker.Decision(); decision.ShouldStop {
		t.Fatalf("a tool turn did not start a fresh run: %q", decision.Reason)
	}
}

func TestDiminishingReturnsNeedConsecutiveShortReplies(t *testing.T) {
	// Short replies separated by tool work are the ends of productive
	// stretches, as in a goal loop, not a model stalling.
	tracker := NewContinuationTracker(128_000)
	for range 5 {
		tracker.Record(200, false)
		tracker.Record(300, true)
	}
	if decision := tracker.Decision(); decision.ShouldStop {
		t.Fatalf("short replies between tool turns stopped the query: %q", decision.Reason)
	}

	for range 3 {
		tracker.Record(200, false)
	}
	if decision := tracker.Decision(); decision.Reason != ContinuationStopDiminishingReturns {
		t.Fatalf("three short replies in a row gave %q, want %q", decision.Reason, ContinuationStopDiminishingReturns)
	}
}
