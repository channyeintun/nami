package tools

import (
	"slices"
	"testing"

	artifactspkg "github.com/channyeintun/nami/internal/artifacts"
	"github.com/channyeintun/nami/internal/session"
	"github.com/channyeintun/nami/internal/swarm"
)

// NormalizeHandoffStatus turns an unknown status into "", and the store reads
// "" as pending. Passing an unrecognized status straight through therefore
// reopened the handoff: asking to mark it "done" set it back to pending.
func TestSwarmUpdateHandoffRejectsUnknownStatus(t *testing.T) {
	const sessionID = "session-under-test"
	store := session.NewStore(t.TempDir())
	manager := artifactspkg.NewManager(artifactspkg.NewLocalStore(t.TempDir()))
	SetGlobalSwarmRuntime(sessionID, manager, store, t.TempDir())
	t.Cleanup(func() { SetGlobalSwarmRuntime("", nil, nil, "") })

	handoff, err := swarm.PrepareHandoff(swarm.Handoff{ID: swarm.NewHandoffID(), SourceRole: "coder", TargetRole: "reviewer", Summary: "parser"})
	if err != nil {
		t.Fatalf("PrepareHandoff: %v", err)
	}
	if _, err := swarm.UpsertHandoff(store, sessionID, handoff); err != nil {
		t.Fatalf("UpsertHandoff: %v", err)
	}
	if _, err := swarm.UpdateHandoffStatus(store, sessionID, handoff.ID, swarm.HandoffStatusInProgress, ""); err != nil {
		t.Fatalf("UpdateHandoffStatus: %v", err)
	}

	tool := NewSwarmUpdateHandoffTool()
	for _, status := range []string{"done", "", "superseded"} {
		input := ToolInput{Params: map[string]any{"handoff_id": handoff.ID, "status": status}}
		if err := tool.Validate(input); err == nil {
			t.Errorf("Validate accepted status %q", status)
		}
		if _, err := tool.Execute(t.Context(), input); err == nil {
			t.Errorf("Execute accepted status %q", status)
		}
	}

	handoffs, err := swarm.ListHandoffs(store, sessionID, "", nil)
	if err != nil || len(handoffs) != 1 {
		t.Fatalf("ListHandoffs = %v, %v", handoffs, err)
	}
	if got := handoffs[0].Status; got != swarm.HandoffStatusInProgress {
		t.Fatalf("status = %q after rejected updates, want it left in_progress", got)
	}
}

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
