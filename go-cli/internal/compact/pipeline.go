package compact

import (
	"context"
	"strings"

	"github.com/channyeintun/go-cli/internal/api"
)

// Strategy identifies a compaction strategy.
type Strategy string

const (
	StrategyToolTruncate Strategy = "tool_truncate" // Strategy A: zero API calls
	StrategySummarize    Strategy = "summarize"     // Strategy B: LLM call
	StrategyPartial      Strategy = "partial"       // Strategy C: scope to recent
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
	result.TokensBefore = EstimateConversationTokens(messages)

	// Strategy A: Tool result truncation
	result.Messages = TruncateToolResults(result.Messages)
	result.Strategy = StrategyToolTruncate
	result.TokensAfter = EstimateConversationTokens(result.Messages)

	if p.summarizer == nil || !shouldRunSummary(reason, result.TokensBefore, result.TokensAfter, p.contextWindow) {
		return result, nil
	}

	toSummarize, retained := SplitMessagesForSummary(result.Messages)
	if len(toSummarize) == 0 {
		return result, nil
	}

	summary, err := p.summarizer.Summarize(ctx, toSummarize)
	if err != nil {
		return CompactResult{}, err
	}
	if strings.TrimSpace(summary) == "" {
		return result, nil
	}

	result.Messages = BuildSummaryMessages(summary, retained)
	result.Strategy = StrategySummarize
	result.TokensAfter = EstimateConversationTokens(result.Messages)

	// Strategy C: Partial compaction (if summarization insufficient)
	// TODO: implement partial compaction scoped to recent messages

	return result, nil
}

func shouldRunSummary(reason string, tokensBefore, tokensAfter, contextWindow int) bool {
	switch strings.ToLower(strings.TrimSpace(reason)) {
	case "manual", "auto":
		return true
	}
	if tokensAfter >= AutocompactThreshold(contextWindow) {
		return true
	}
	return tokensAfter >= tokensBefore
}
