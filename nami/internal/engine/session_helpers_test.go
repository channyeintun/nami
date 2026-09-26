package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"strings"
	"testing"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/compact"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/ipc"
)

// cancelledRequestClient fails every request the way API clients report a
// cancelled call: the context's error wrapped with what was being done.
type cancelledRequestClient struct {
	requests int
}

func (c *cancelledRequestClient) ModelID() string { return "claude-sonnet-5" }

func (c *cancelledRequestClient) Capabilities() api.ModelCapabilities {
	return api.ModelCapabilities{SupportsCaching: true}
}

func (c *cancelledRequestClient) Stream(context.Context, api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
	c.requests++
	return nil, fmt.Errorf("send summary request: %w", context.Canceled)
}

// A cancelled compaction has to stop rather than send a second, fresh summary
// request. The check compared errors with ==, so a wrapped cancellation got
// past it.
func TestCompactionSummaryStopsWhenItsRequestIsCancelled(t *testing.T) {
	client := &cancelledRequestClient{}
	summarizer := &compactionSummarizer{
		bridge:       ipc.NewBridge(strings.NewReader(""), io.Discard),
		tracker:      costpkg.NewTracker(),
		client:       client,
		systemPrompt: "system",
	}

	messages := []api.Message{{Role: api.RoleUser, Content: "hello"}}
	_, err := summarizer.SummarizeWithPrompt(t.Context(), messages, compact.CompactionPromptTemplate)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want the cancellation", err)
	}
	if client.requests != 1 {
		t.Fatalf("sent %d summary requests; a cancelled compaction must not retry", client.requests)
	}
}
