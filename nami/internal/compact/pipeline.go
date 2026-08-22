package compact

import (
	"context"
	"strings"

	"github.com/channyeintun/nami/internal/api"
)

// Strategy identifies a compaction strategy.
type Strategy string

// SummaryMode identifies how the summary request itself was issued.
type SummaryMode string

const (
	StrategyNone         Strategy = "none"
	StrategyToolTruncate Strategy = "tool_truncate" // Strategy A: zero API calls
	StrategySummarize    Strategy = "summarize"     // Strategy B: LLM call
	StrategyPartial      Strategy = "partial"       // Strategy C: scope to recent

	SummaryModeNone      SummaryMode = "none"
	SummaryModeFresh     SummaryMode = "fresh"
	SummaryModeCacheSafe SummaryMode = "cache_safe"
)

// CompactResult holds the outcome of a compaction run.
type CompactResult struct {
	Messages                []api.Message
	Strategy                Strategy
	SummaryMode             SummaryMode
	TokensBefore            int
	TokensAfter             int
	MicrocompactApplied     bool
	MicrocompactTokensSaved int
}

// Pipeline orchestrates multi-strategy compaction.
type Pipeline struct {
	contextWindow       int
	summarizer          Summarizer
	microcompactEnabled bool
	summaryMode         SummaryMode
	// SessionMemoryHint is the current session memory content. When set, the
	// compaction prompt tells the summarizer to skip facts already preserved
	// in session memory, producing a more complementary summary.
	SessionMemoryHint string
}

// Summarizer abstracts the LLM call for compaction summarization.
type Summarizer interface {
	Summarize(ctx context.Context, messages []api.Message) (string, error)
}

// PromptSummarizer supports alternate compaction prompts such as partial
// compaction over only the recent portion of the conversation.
type PromptSummarizer interface {
	SummarizeWithPrompt(ctx context.Context, messages []api.Message, prompt string) (string, error)
}

// SummaryModeReporter exposes how the summarizer issued its last request.
type SummaryModeReporter interface {
	LastSummaryMode() SummaryMode
}

// NewPipeline creates a compaction pipeline.
func NewPipeline(contextWindow int, summarizer Summarizer, microcompactEnabled bool) *Pipeline {
	return &Pipeline{
		contextWindow:       contextWindow,
		summarizer:          summarizer,
		microcompactEnabled: microcompactEnabled,
	}
}

// Compact runs the tiered compaction cascade:
// 1. Tool result truncation (microcompact)
// 2. Full summarization
// 3. Partial compaction (if still over budget)
func (p *Pipeline) Compact(ctx context.Context, messages []api.Message, reason string) (CompactResult, error) {
	result := CompactResult{
		Messages:     messages,
		SummaryMode:  SummaryModeNone,
		Strategy:     StrategyNone,
		TokensBefore: EstimateConversationTokens(messages),
	}
	result.TokensAfter = result.TokensBefore

	// Strategy A: Tool result truncation
	if p.microcompactEnabled {
		truncated := TruncateToolResults(result.Messages)
		truncatedTokens := EstimateConversationTokens(truncated)
		result.Messages = truncated
		result.Strategy = StrategyToolTruncate
		result.TokensAfter = truncatedTokens
		if truncatedTokens < result.TokensBefore {
			result.MicrocompactApplied = true
			result.MicrocompactTokensSaved = result.TokensBefore - truncatedTokens
		}
	}

	if p.summarizer == nil || !shouldRunSummary(reason, result.TokensBefore, result.TokensAfter, p.contextWindow) {
		return result, nil
	}

	if partialResult, ok, err := p.compactRecentWindow(ctx, result.Messages); err != nil {
		return CompactResult{}, err
	} else if ok {
		return partialResult, nil
	}

	toSummarize, retained := SplitMessagesForSummary(result.Messages)
	if len(toSummarize) == 0 {
		return result, nil
	}

	summary, err := p.summarize(ctx, toSummarize, BuildCompactionPrompt(CompactionPromptTemplate, p.SessionMemoryHint))
	if err != nil {
		return CompactResult{}, err
	}
	if strings.TrimSpace(summary) == "" {
		return result, nil
	}

	result.Messages = BuildSummaryMessages(summary, retained)
	result.Strategy = StrategySummarize
	result.SummaryMode = p.summaryMode
	result.TokensAfter = EstimateConversationTokens(result.Messages)

	return result, nil
}

func (p *Pipeline) compactRecentWindow(ctx context.Context, messages []api.Message) (CompactResult, bool, error) {
	window, ok := SelectPartialWindow(messages)
	if !ok {
		return CompactResult{}, false, nil
	}
	tokensBefore := EstimateConversationTokens(messages)
	truncatedTokens := tokensBefore
	if p.microcompactEnabled {
		truncatedTokens = EstimateConversationTokens(TruncateToolResults(messages))
	}

	summary, err := p.summarize(ctx, window.ToSummarize, BuildCompactionPrompt(PartialCompactionPromptTemplate, p.SessionMemoryHint))
	if err != nil {
		return CompactResult{}, false, err
	}
	if strings.TrimSpace(summary) == "" {
		return CompactResult{}, false, nil
	}

	compacted := BuildSummaryMessagesWithPrefix(window.Prefix, summary, window.RetainedTail)
	return CompactResult{
		Messages:                compacted,
		Strategy:                StrategyPartial,
		SummaryMode:             p.summaryMode,
		TokensBefore:            tokensBefore,
		TokensAfter:             EstimateConversationTokens(compacted),
		MicrocompactApplied:     truncatedTokens < tokensBefore,
		MicrocompactTokensSaved: max(tokensBefore-truncatedTokens, 0),
	}, true, nil
}

func (p *Pipeline) summarize(ctx context.Context, messages []api.Message, prompt string) (string, error) {
	p.summaryMode = SummaryModeNone
	if promptSummarizer, ok := p.summarizer.(PromptSummarizer); ok {
		summary, err := promptSummarizer.SummarizeWithPrompt(ctx, messages, prompt)
		if reporter, ok := p.summarizer.(SummaryModeReporter); ok {
			p.summaryMode = reporter.LastSummaryMode()
		}
		return summary, err
	}
	summary, err := p.summarizer.Summarize(ctx, messages)
	if reporter, ok := p.summarizer.(SummaryModeReporter); ok {
		p.summaryMode = reporter.LastSummaryMode()
	}
	return summary, err
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
