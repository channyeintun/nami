package agent

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/compact"
	"github.com/channyeintun/nami/internal/ipc"
)

func runProactiveCompaction(
	ctx context.Context,
	state *QueryState,
	deps QueryDeps,
	yield func(ipc.StreamEvent, error) bool,
) error {
	if deps.CompactMessages == nil || state.ContextWindow <= 0 {
		return nil
	}
	if state.AutoCompactFailures >= compact.MaxConsecutiveFailures {
		return nil
	}

	pressure := EvaluateContextPressure(state.Messages, state.ContextWindow, state.MaxTokens, state.Continuation, ContextPressureSignals{
		SessionMemory:    state.SessionMemory,
		RetrievalTouched: state.RetrievalTouched,
		AttemptEntries:   state.AttemptEntries,
	})
	hasSessionMemory := state.SessionMemory.HasContent()
	hasFreshSessionMemory := state.SessionMemory.IsFresh(time.Now())
	if !pressure.ShouldCompact && !(hasFreshSessionMemory && pressure.WarningThreshold > 0 && pressure.ConversationTokens >= pressure.WarningThreshold) {
		return nil
	}
	tokensBefore := pressure.ConversationTokens
	if tokensBefore <= 0 {
		return nil
	}

	if err := yieldEvent(yield, ipc.EventCompactStart, ipc.CompactStartPayload{
		Strategy:         string(CompactAuto),
		TokensBefore:     tokensBefore,
		HasSessionMemory: hasSessionMemory,
	}); err != nil {
		return err
	}

	compacted, err := deps.CompactMessages(ctx, state.Messages, CompactAuto)
	if err != nil {
		state.AutoCompactFailures++
		message := fmt.Sprintf("auto compact failed: %v", err)
		if err := yieldEvent(yield, ipc.EventError, ipc.ErrorPayload{
			Message:     message,
			Recoverable: true,
		}); err != nil {
			return err
		}
		return nil
	}

	state.AutoCompactFailures = 0
	state.Messages = withConversationTurn(compacted.Messages)

	if err := yieldEvent(yield, ipc.EventCompactEnd, ipc.CompactEndPayload{
		Strategy:                string(compacted.Strategy),
		TokensBefore:            compacted.TokensBefore,
		TokensAfter:             compacted.TokensAfter,
		TokensSaved:             compacted.TokensBefore - compacted.TokensAfter,
		MicrocompactApplied:     compacted.MicrocompactApplied,
		MicrocompactTokensSaved: compacted.MicrocompactTokensSaved,
		HasSessionMemory:        hasSessionMemory,
	}); err != nil {
		return err
	}

	return nil
}

// withConversationTurn appends a continuation user turn when compaction kept
// nothing but summaries, as it does mid-query when the transcript ended on an
// assistant reply (a max_tokens stop). Providers fold summaries into the
// system prompt, so the next request would otherwise have no turn at all.
func withConversationTurn(messages []api.Message) []api.Message {
	for _, message := range messages {
		if message.Role != api.RoleSystem {
			return messages
		}
	}
	return append(messages, api.Message{Role: api.RoleUser, Content: compact.SummaryContinuationPrompt})
}

func normalizeStopReason(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "end_turn"
	}
	return reason
}
