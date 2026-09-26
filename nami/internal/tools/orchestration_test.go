package tools

import "testing"

// Subagents run their tool calls through ExecuteBatchWithOptions, so a panic
// there has to fail the one call just as it does in the streaming executor.
func TestExecuteBatchTurnsAToolPanicIntoAFailedCall(t *testing.T) {
	for _, concurrent := range []bool{false, true} {
		batch := Batch{
			Concurrent: concurrent,
			Calls: []PendingCall{
				{Index: 0, Tool: panickingTool("boom", concurrent)},
				{Index: 1, Tool: succeedingTool("other", concurrent)},
			},
		}

		results := ExecuteBatchWithOptions(t.Context(), batch, ExecuteOptions{})

		if len(results) != 2 {
			t.Fatalf("concurrent=%v: got %d results, want 2", concurrent, len(results))
		}
		assertPanicResult(t, results[0], "boom")
		if got := results[1]; got.Err != nil || got.Output.Output != "other ran" {
			t.Fatalf("concurrent=%v: other call = %+v, want it to run normally", concurrent, got)
		}
	}
}
