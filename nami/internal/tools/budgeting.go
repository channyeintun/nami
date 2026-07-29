package tools

import (
	"path/filepath"
)

const (
	defaultBudgetScaleTokens = 8192
	minBudgetChars           = 25_000
	maxBudgetChars           = 250_000
	minAggregateBudgetChars  = 40_000
	maxAggregateBudgetChars  = 180_000
	minPreviewBudgetChars    = 1000
	maxPreviewBudgetChars    = 8000
)

// ResultBudget defines limits for tool output size.
type ResultBudget struct {
	MaxChars          int
	AggregateMaxChars int
	PreviewLen        int
	SpillDir          string
}

// AggregateResultBudget tracks total inline tool-output usage for a single turn.
type AggregateResultBudget struct {
	maxChars  int
	usedChars int
}

// DefaultResultBudgetForModel scales tool output budgets to the active model's
// output capacity so smaller models keep tighter inline results.
func DefaultResultBudgetForModel(sessionDir string, maxOutputTokens int) ResultBudget {
	return ResultBudget{
		MaxChars:          scaleBudget(MaxResultSizeChars, maxOutputTokens, minBudgetChars, maxBudgetChars),
		AggregateMaxChars: scaleBudget(MaxResultSizeChars*3/2, maxOutputTokens, minAggregateBudgetChars, maxAggregateBudgetChars),
		PreviewLen:        scaleBudget(PreviewChars, maxOutputTokens, minPreviewBudgetChars, maxPreviewBudgetChars),
		SpillDir:          filepath.Join(sessionDir, "artifacts", "tool-log"),
	}
}

// NewAggregateResultBudget creates a turn-scoped inline output tracker.
func NewAggregateResultBudget(budget ResultBudget) *AggregateResultBudget {
	maxChars := budget.AggregateMaxChars
	if maxChars <= 0 {
		maxChars = budget.MaxChars
	}
	return &AggregateResultBudget{maxChars: maxChars}
}

// RemainingChars returns the inline budget left for the current turn.
func (b *AggregateResultBudget) RemainingChars() int {
	if b == nil {
		return 0
	}
	remaining := b.maxChars - b.usedChars
	if remaining < 0 {
		return 0
	}
	return remaining
}

// Consume records inline output that was kept in the transcript.
func (b *AggregateResultBudget) Consume(chars int) {
	if b == nil || chars <= 0 {
		return
	}
	b.usedChars += chars
}

// MaxChars returns the configured total inline budget for the turn.
func (b *AggregateResultBudget) MaxChars() int {
	if b == nil {
		return 0
	}
	return b.maxChars
}

// InlineLimit determines how much output may remain inline for this result.
// It reports whether the output must spill and whether the aggregate budget forced it.
func (b *AggregateResultBudget) InlineLimit(outputLen int, budget ResultBudget) (int, bool, bool) {
	if outputLen <= 0 {
		return 0, false, false
	}

	remaining := outputLen
	if b != nil {
		remaining = b.RemainingChars()
	}

	perResultSpill := outputLen > budget.MaxChars
	aggregateSpill := b != nil && outputLen > remaining
	if !perResultSpill && !aggregateSpill {
		return outputLen, false, false
	}

	inlineLimit := budget.PreviewLen
	if aggregateSpill && remaining < inlineLimit {
		inlineLimit = remaining
	}
	if inlineLimit > outputLen {
		inlineLimit = outputLen
	}
	if inlineLimit < 0 {
		inlineLimit = 0
	}

	return inlineLimit, true, aggregateSpill
}

func scaleBudget(base, maxOutputTokens, minValue, maxValue int) int {
	if maxOutputTokens <= 0 {
		maxOutputTokens = defaultBudgetScaleTokens
	}
	scaled := base * maxOutputTokens / defaultBudgetScaleTokens
	if scaled < minValue {
		return minValue
	}
	if scaled > maxValue {
		return maxValue
	}
	return scaled
}
