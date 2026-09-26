package engine

import (
	"context"
	"io"
	"iter"
	"strings"
	"testing"
	"time"

	"github.com/channyeintun/nami/internal/api"
	costpkg "github.com/channyeintun/nami/internal/cost"
	"github.com/channyeintun/nami/internal/ipc"
)

// runningTotalUsageClient reports usage the way the providers do: a running
// total for the call, sent once when the response starts and again when it
// ends.
type runningTotalUsageClient struct{}

func (runningTotalUsageClient) ModelID() string                     { return "claude-sonnet-5" }
func (runningTotalUsageClient) Capabilities() api.ModelCapabilities { return api.ModelCapabilities{} }

func (runningTotalUsageClient) Stream(context.Context, api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
	events := []api.ModelEvent{
		{Type: api.ModelEventUsage, Usage: &api.Usage{InputTokens: 1_000, OutputTokens: 1, CacheReadTokens: 5_000}},
		{Type: api.ModelEventToken, Text: "# Session Memory"},
		{Type: api.ModelEventUsage, Usage: &api.Usage{InputTokens: 1_000, OutputTokens: 200, CacheReadTokens: 5_000}},
		{Type: api.ModelEventStop, StopReason: "end_turn"},
	}
	return func(yield func(api.ModelEvent, error) bool) {
		for _, event := range events {
			if !yield(event, nil) {
				return
			}
		}
	}, nil
}

// Each usage report is the call's total so far. Summing them charged every
// Anthropic call's input and cache tokens twice, and a provider that reports
// on every streamed chunk once per chunk.
func TestModelCallsRecordTheLatestUsageReport(t *testing.T) {
	client := runningTotalUsageClient{}
	calls := []struct {
		name string
		run  func(*ipc.Bridge, *costpkg.Tracker) error
	}{
		{
			name: "main loop",
			run: func(bridge *ipc.Bridge, tracker *costpkg.Tracker) error {
				stream, err := trackModelStream(t.Context(), bridge, tracker, client, api.ModelRequest{})
				if err != nil {
					return err
				}
				for _, err := range stream {
					if err != nil {
						return err
					}
				}
				return nil
			},
		},
		{
			name: "session memory refinement",
			run: func(bridge *ipc.Bridge, tracker *costpkg.Tracker) error {
				_, err := newSessionMemoryRefiner(bridge, tracker, client)(t.Context(), "draft", nil)
				return err
			},
		},
		{
			name: "compaction summary",
			run: func(bridge *ipc.Bridge, tracker *costpkg.Tracker) error {
				summarizer := &compactionSummarizer{bridge: bridge, tracker: tracker, client: client}
				stream, err := client.Stream(t.Context(), api.ModelRequest{})
				if err != nil {
					return err
				}
				_, err = summarizer.collectSummaryStream(stream, time.Now())
				return err
			},
		},
	}

	want := api.Usage{InputTokens: 1_000, OutputTokens: 200, CacheReadTokens: 5_000}
	for _, call := range calls {
		t.Run(call.name, func(t *testing.T) {
			bridge := ipc.NewBridge(strings.NewReader(""), io.Discard)
			tracker := costpkg.NewTracker()
			if err := call.run(bridge, tracker); err != nil {
				t.Fatalf("call: %v", err)
			}

			got := tracker.Snapshot()
			if got.TotalInputTokens != want.InputTokens || got.TotalOutputTokens != want.OutputTokens || got.TotalCacheReadTokens != want.CacheReadTokens {
				t.Fatalf("recorded input %d, output %d, cache read %d; want %d, %d, %d",
					got.TotalInputTokens, got.TotalOutputTokens, got.TotalCacheReadTokens,
					want.InputTokens, want.OutputTokens, want.CacheReadTokens)
			}
			if wantCost := costpkg.CalculateUSDCost(client.ModelID(), want); got.TotalCostUSD != wantCost {
				t.Fatalf("recorded cost %v, want %v", got.TotalCostUSD, wantCost)
			}
		})
	}
}

// cutOffClient reports the prompt's usage and then fails partway, as a stream
// does when the user presses stop or the connection drops.
type cutOffClient struct{}

func (cutOffClient) ModelID() string                     { return "claude-sonnet-5" }
func (cutOffClient) Capabilities() api.ModelCapabilities { return api.ModelCapabilities{} }

func (cutOffClient) Stream(context.Context, api.ModelRequest) (iter.Seq2[api.ModelEvent, error], error) {
	return func(yield func(api.ModelEvent, error) bool) {
		if !yield(api.ModelEvent{Type: api.ModelEventUsage, Usage: &api.Usage{InputTokens: 50_000, OutputTokens: 1}}, nil) {
			return
		}
		if !yield(api.ModelEvent{Type: api.ModelEventToken, Text: "partial"}, nil) {
			return
		}
		yield(api.ModelEvent{}, context.Canceled)
	}, nil
}

// The provider bills the prompt as soon as it reports it. A call that ended
// early used to vanish from the cost entirely.
func TestModelCallsThatEndEarlyAreStillCharged(t *testing.T) {
	tests := []struct {
		name    string
		consume func(iter.Seq2[api.ModelEvent, error])
	}{
		{
			name: "stream cut off",
			consume: func(stream iter.Seq2[api.ModelEvent, error]) {
				for _, err := range stream {
					if err != nil {
						return
					}
				}
			},
		},
		{
			name: "reader stops early",
			consume: func(stream iter.Seq2[api.ModelEvent, error]) {
				for range stream {
					return
				}
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			bridge := ipc.NewBridge(strings.NewReader(""), io.Discard)
			tracker := costpkg.NewTracker()
			stream, err := trackModelStream(t.Context(), bridge, tracker, cutOffClient{}, api.ModelRequest{})
			if err != nil {
				t.Fatalf("trackModelStream: %v", err)
			}
			tc.consume(stream)

			if got := tracker.Snapshot().TotalInputTokens; got != 50_000 {
				t.Fatalf("recorded %d input tokens, want the 50000 the provider reported", got)
			}
		})
	}
}

// The goal check and the session title are model calls like any other; their
// usage has to reach the session's cost.
func TestSideCallsAreCharged(t *testing.T) {
	sideUsage := api.Usage{InputTokens: 700, OutputTokens: 30}
	newClient := func() *scriptedClient {
		return &scriptedClient{
			caps:      api.ModelCapabilities{SupportsToolUse: true, MaxContextWindow: 200_000, MaxOutputTokens: 8_000},
			turns:     []scriptedTurn{{text: "All done."}},
			sideUsage: sideUsage,
		}
	}
	charged := func(tracker *costpkg.Tracker) bool {
		got := tracker.Snapshot()
		return got.TotalInputTokens == sideUsage.InputTokens && got.TotalOutputTokens == sideUsage.OutputTokens
	}

	t.Run("goal check", func(t *testing.T) {
		h := newTurnHarness(t, newClient(), echoTool{})
		if _, err := goalStoreFor(h.state.sessionDir).Set("the tests pass"); err != nil {
			t.Fatalf("Set: %v", err)
		}
		if err := handleUserInputMessage(t.Context(), ipc.UserInputPayload{Text: "finish up"}, h.deps, h.state); err != nil {
			t.Fatalf("handleUserInputMessage: %v", err)
		}
		if !charged(h.deps.tracker) {
			t.Fatalf("tracker = %+v, want the goal check's %+v", h.deps.tracker.Snapshot(), sideUsage)
		}
	})

	t.Run("session title", func(t *testing.T) {
		h := newTurnHarness(t, newClient(), echoTool{})
		h.state.titleGenerated = false
		if err := handleUserInputMessage(t.Context(), ipc.UserInputPayload{Text: "fix the login bug"}, h.deps, h.state); err != nil {
			t.Fatalf("handleUserInputMessage: %v", err)
		}
		// The title is generated in the background; its metadata write is the
		// last thing it does before reporting.
		deadline := time.Now().Add(5 * time.Second)
		for {
			meta, err := h.deps.sessionStore.LoadMetadata(h.state.sessionID)
			if err == nil && meta.Title != "" {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("the session title was never generated")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if !charged(h.deps.tracker) {
			t.Fatalf("tracker = %+v, want the title call's %+v", h.deps.tracker.Snapshot(), sideUsage)
		}
	})
}
