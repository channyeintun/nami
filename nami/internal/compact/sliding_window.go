package compact

import (
	"slices"

	"github.com/channyeintun/nami/internal/api"
)

// SlidingWindow identifies the portion of the conversation that can be
// compacted without re-summarizing already compacted history.
type SlidingWindow struct {
	Prefix       []api.Message
	ToSummarize  []api.Message
	RetainedTail []api.Message
}

// SelectPartialWindow preserves all messages through the latest summary marker
// and returns only the newer slice for partial compaction.
func SelectPartialWindow(messages []api.Message) (SlidingWindow, bool) {
	lastSummary := findLastSummaryIndex(messages)
	if lastSummary < 0 || lastSummary >= len(messages)-1 {
		return SlidingWindow{}, false
	}

	recent := append([]api.Message(nil), messages[lastSummary+1:]...)
	toSummarize, retained := SplitMessagesForSummary(recent)
	if len(toSummarize) == 0 {
		return SlidingWindow{}, false
	}

	return SlidingWindow{
		Prefix:       append([]api.Message(nil), messages[:lastSummary+1]...),
		ToSummarize:  toSummarize,
		RetainedTail: retained,
	}, true
}

func findLastSummaryIndex(messages []api.Message) int {
	for i, message := range slices.Backward(messages) {
		if IsSummaryMessage(message) {
			return i
		}
	}
	return -1
}
