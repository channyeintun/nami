package compact

import (
	"context"

	"github.com/channyeintun/go-cli/internal/api"
)

// Strategy identifies a compaction strategy.
type Strategy string

const (
	StrategyToolTruncate Strategy = "tool_truncate" // Strategy A: zero API calls
	StrategySummarize    Strategy = "summarize"      // Strategy B: LLM call
	StrategyPartial      Strategy = "partial"        // Strategy C: scope to recent
)

// CompactResult holds the outcome of a compaction run.
type CompactResult struct {
	Messages     []api.Message
	Strategy     Strategy
	TokensBefore int
	TokensAfter  int
}

// Pipeline orchestrates multi-strategy compaction.
type Pipeline struct {
	contextWindow int
	summarizer    Summarizer
}

// Summarizer abstracts the LLM call for compaction summarization.
type Summarizer interface {
	Summarize(ctx context.Context, messages []api.Message) (string, error)
}

// NewPipeline creates a compaction pipeline.
func NewPipeline(contextWindow int, summarizer Summarizer) *Pipeline {
	return &Pipeline{
		contextWindow: contextWindow,
		summarizer:    summarizer,
	}
}

// Compact runs the tiered compaction cascade:
// 1. Tool result truncation (microcompact)
// 2. Full summarization
// 3. Partial compaction (if still over budget)
func (p *Pipeline) Compact(ctx context.Context, messages []api.Message, reason string) (CompactResult, error) {
	result := CompactResult{
		Messages: messages,
	}

	// Strategy A: Tool result truncation
	result.Messages = TruncateToolResults(result.Messages)
	result.Strategy = StrategyToolTruncate

	// Check if truncation was sufficient
	// (token estimation will be added when messages carry content)

	// Strategy B: Summarization (if still over threshold)
	// TODO: call p.summarizer.Summarize when implemented

	// Strategy C: Partial compaction (if summarization insufficient)
	// TODO: implement partial compaction scoped to recent messages

	return result, nil
}
