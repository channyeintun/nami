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
