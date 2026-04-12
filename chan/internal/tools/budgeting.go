package tools

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
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

// DefaultResultBudget returns the standard budget.
func DefaultResultBudget(sessionDir string) ResultBudget {
	return DefaultResultBudgetForModel(sessionDir, defaultBudgetScaleTokens)
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

// UsedChars returns the amount of inline budget already consumed.
func (b *AggregateResultBudget) UsedChars() int {
	if b == nil {
		return 0
	}
	return b.usedChars
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

// ApplyBudget truncates output if it exceeds the budget, spilling to disk.
// Returns the (possibly truncated) output and the spill path if any.
func ApplyBudget(budget ResultBudget, toolID string, output string) (string, string, error) {
	if len(output) <= budget.MaxChars {
		return output, "", nil
	}

	if err := os.MkdirAll(budget.SpillDir, 0o755); err != nil {
		return output[:budget.MaxChars], "", fmt.Errorf("create spill dir: %w", err)
	}

	spillPath := filepath.Join(budget.SpillDir, sanitizeToolLogID(toolID)+".log")
	if err := os.WriteFile(spillPath, []byte(output), 0o644); err != nil {
		return output[:budget.MaxChars], "", fmt.Errorf("write spill file: %w", err)
	}

	preview := output[:budget.PreviewLen]
	truncated := fmt.Sprintf(
		"%s\n\n[Output truncated. Full result saved to %s (%d chars)]",
		preview, spillPath, len(output),
	)
	return truncated, spillPath, nil
}

func sanitizeToolLogID(toolID string) string {
	trimmed := strings.TrimSpace(toolID)
	if trimmed == "" {
		return "tool"
	}

	var builder strings.Builder
	builder.Grow(len(trimmed))
	for _, r := range trimmed {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r), r == '-', r == '_', r == '.':
			builder.WriteRune(r)
		default:
			builder.WriteByte('_')
		}
	}

	sanitized := strings.Trim(builder.String(), "._")
	if sanitized == "" {
		sanitized = "tool"
	}
	if sanitized == trimmed {
		return sanitized
	}

	sum := sha256.Sum256([]byte(trimmed))
	return fmt.Sprintf("%s_%s", sanitized, hex.EncodeToString(sum[:4]))
}
