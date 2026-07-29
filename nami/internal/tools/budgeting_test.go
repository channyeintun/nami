package tools

import "testing"

func TestScaleBudgetClampsToBounds(t *testing.T) {
	// A tiny output ceiling must not shrink the budget below the floor.
	if got := scaleBudget(100_000, 1, minBudgetChars, maxBudgetChars); got != minBudgetChars {
		t.Errorf("scaleBudget(tiny) = %d, want the floor %d", got, minBudgetChars)
	}
	// A huge one must not blow past the ceiling.
	if got := scaleBudget(100_000, 1_000_000, minBudgetChars, maxBudgetChars); got != maxBudgetChars {
		t.Errorf("scaleBudget(huge) = %d, want the ceiling %d", got, maxBudgetChars)
	}
}

func TestScaleBudgetDefaultsWhenOutputCeilingUnknown(t *testing.T) {
	// A zero or negative ceiling falls back to the reference token count, so the
	// budget is the unscaled base.
	want := scaleBudget(50_000, defaultBudgetScaleTokens, minBudgetChars, maxBudgetChars)
	for _, tokens := range []int{0, -1} {
		if got := scaleBudget(50_000, tokens, minBudgetChars, maxBudgetChars); got != want {
			t.Errorf("scaleBudget(base, %d) = %d, want %d", tokens, got, want)
		}
	}
}

func TestNewAggregateResultBudgetFallsBackToPerResultMax(t *testing.T) {
	budget := NewAggregateResultBudget(ResultBudget{MaxChars: 1234})
	if got := budget.MaxChars(); got != 1234 {
		t.Fatalf("MaxChars = %d, want the per-result max when no aggregate is set", got)
	}
}

func TestAggregateResultBudgetTracksConsumption(t *testing.T) {
	budget := NewAggregateResultBudget(ResultBudget{AggregateMaxChars: 100})
	if got := budget.RemainingChars(); got != 100 {
		t.Fatalf("RemainingChars = %d, want 100", got)
	}

	budget.Consume(30)
	if got := budget.RemainingChars(); got != 70 {
		t.Fatalf("RemainingChars = %d, want 70", got)
	}

	// Non-positive consumption is ignored rather than crediting the budget back.
	budget.Consume(0)
	budget.Consume(-50)
	if got := budget.RemainingChars(); got != 70 {
		t.Fatalf("RemainingChars = %d, want 70 after non-positive consumes", got)
	}

	// Overspending clamps at zero instead of going negative.
	budget.Consume(1000)
	if got := budget.RemainingChars(); got != 0 {
		t.Fatalf("RemainingChars = %d, want 0", got)
	}
}

func TestAggregateResultBudgetNilIsSafe(t *testing.T) {
	var budget *AggregateResultBudget
	if got := budget.RemainingChars(); got != 0 {
		t.Errorf("nil RemainingChars = %d, want 0", got)
	}
	if got := budget.MaxChars(); got != 0 {
		t.Errorf("nil MaxChars = %d, want 0", got)
	}
	budget.Consume(10) // must not panic
}

func TestInlineLimitKeepsSmallOutputInline(t *testing.T) {
	budget := NewAggregateResultBudget(ResultBudget{AggregateMaxChars: 1000})
	limit, spill, aggregate := budget.InlineLimit(100, ResultBudget{MaxChars: 500, PreviewLen: 50})
	if limit != 100 || spill || aggregate {
		t.Fatalf("InlineLimit = (%d, %v, %v), want the whole output inline", limit, spill, aggregate)
	}
}

func TestInlineLimitSpillsOversizedOutputToPreview(t *testing.T) {
	budget := NewAggregateResultBudget(ResultBudget{AggregateMaxChars: 10_000})
	limit, spill, aggregate := budget.InlineLimit(900, ResultBudget{MaxChars: 500, PreviewLen: 50})
	if !spill {
		t.Fatal("oversized output should spill")
	}
	if aggregate {
		t.Fatal("the per-result limit caused the spill, not the aggregate")
	}
	if limit != 50 {
		t.Fatalf("limit = %d, want the preview length", limit)
	}
}

func TestInlineLimitReportsAggregatePressure(t *testing.T) {
	budget := NewAggregateResultBudget(ResultBudget{AggregateMaxChars: 100})
	budget.Consume(80) // 20 left

	limit, spill, aggregate := budget.InlineLimit(50, ResultBudget{MaxChars: 10_000, PreviewLen: 40})
	if !spill || !aggregate {
		t.Fatalf("InlineLimit = (%d, %v, %v), want an aggregate-forced spill", limit, spill, aggregate)
	}
	// The remaining budget is tighter than the preview, so it wins.
	if limit != 20 {
		t.Fatalf("limit = %d, want the remaining budget", limit)
	}
}

func TestInlineLimitNeverExceedsOutputLength(t *testing.T) {
	budget := NewAggregateResultBudget(ResultBudget{AggregateMaxChars: 10})
	budget.Consume(10) // nothing left

	limit, spill, _ := budget.InlineLimit(5, ResultBudget{MaxChars: 1, PreviewLen: 500})
	if !spill {
		t.Fatal("expected a spill")
	}
	if limit > 5 || limit < 0 {
		t.Fatalf("limit = %d, want within [0, outputLen]", limit)
	}
}

func TestInlineLimitIgnoresEmptyOutput(t *testing.T) {
	budget := NewAggregateResultBudget(ResultBudget{AggregateMaxChars: 100})
	for _, outputLen := range []int{0, -1} {
		limit, spill, aggregate := budget.InlineLimit(outputLen, ResultBudget{MaxChars: 1, PreviewLen: 1})
		if limit != 0 || spill || aggregate {
			t.Errorf("InlineLimit(%d) = (%d, %v, %v), want a no-op", outputLen, limit, spill, aggregate)
		}
	}
}

func TestDefaultResultBudgetForModelScalesWithOutputCeiling(t *testing.T) {
	small := DefaultResultBudgetForModel("/tmp/session", 4096)
	large := DefaultResultBudgetForModel("/tmp/session", 128_000)

	if large.MaxChars < small.MaxChars {
		t.Fatalf("a larger output ceiling shrank the budget: %d < %d", large.MaxChars, small.MaxChars)
	}
	if small.SpillDir == "" || large.SpillDir == "" {
		t.Fatal("spill directory should be derived from the session dir")
	}
}
