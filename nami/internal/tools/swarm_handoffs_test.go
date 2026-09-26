package tools

import "testing"

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
