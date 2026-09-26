package tools

import (
	"slices"
	"testing"

	"github.com/channyeintun/nami/internal/swarm"
)

// An unrecognized status filter used to be dropped, which left no filter at
// all: asking for "complete" listed every handoff as though it matched.
func TestSwarmListInboxRejectsUnknownStatusFilters(t *testing.T) {
	tool := NewSwarmListInboxTool()
	for _, params := range []map[string]any{
		{"status": "complete"},
		{"statuses": []any{"pending", "done"}},
	} {
		if err := tool.Validate(ToolInput{Params: params}); err == nil {
			t.Errorf("Validate(%v) accepted an unknown status", params)
		}
	}

	statuses, err := collectHandoffStatuses(map[string]any{"statuses": []any{"in-progress", "blocked"}, "status": "blocked"})
	if err != nil {
		t.Fatalf("collectHandoffStatuses: %v", err)
	}
	want := []swarm.HandoffStatus{swarm.HandoffStatusInProgress, swarm.HandoffStatusBlocked}
	if !slices.Equal(statuses, want) {
		t.Fatalf("statuses = %v, want %v", statuses, want)
	}
}

// Listing the inbox changes nothing and may run alongside other calls, but a
// dequeue applies the role's queue policy, which supersedes older handoffs
// under latest-wins. It has to keep its place in the call order, or a listing
// issued beside it sees the inbox before or after the dequeue at random.
func TestSwarmListInboxRunsInParallelOnlyWhenItOnlyReads(t *testing.T) {
	tool := NewSwarmListInboxTool()
	cases := []struct {
		params map[string]any
		want   ConcurrencyDecision
	}{
		{params: map[string]any{"role": "reviewer"}, want: ConcurrencyParallel},
		{params: map[string]any{"role": "reviewer", "dequeue": false}, want: ConcurrencyParallel},
		{params: map[string]any{"role": "reviewer", "dequeue": true}, want: ConcurrencySerial},
	}
	for _, tc := range cases {
		if got := tool.Concurrency(ToolInput{Params: tc.params}); got != tc.want {
			t.Errorf("Concurrency(%v) = %v, want %v", tc.params, got, tc.want)
		}
	}
}
