package agent

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/channyeintun/nami/internal/api"
)

func TestStableSystemPromptDoesNotDriftWithTheClock(t *testing.T) {
	// The stable prompt is the cached prefix shared by every query and by
	// compaction. Any text that changes with the wall clock makes each new
	// query miss the provider's prompt cache for the whole conversation.
	synctest.Test(t, func(t *testing.T) {
		capabilities := api.ModelCapabilities{SupportsToolUse: true, SupportsCaching: true}
		first := ComposeStableSystemPrompt("base prompt", SystemContext{}, capabilities)
		time.Sleep(90 * time.Second)
		second := ComposeStableSystemPrompt("base prompt", SystemContext{}, capabilities)
		if first != second {
			t.Fatalf("stable system prompt changed as time passed:\nfirst:\n%s\n\nsecond:\n%s", first, second)
		}
	})
}
