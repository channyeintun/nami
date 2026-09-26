package engine

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/session"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

func waitForBackgroundAgent(t *testing.T, agentID string) toolpkg.AgentRunResult {
	t.Helper()
	bg, err := getBackgroundAgent(agentID)
	if err != nil {
		t.Fatalf("getBackgroundAgent: %v", err)
	}
	select {
	case <-bg.done:
	case <-time.After(5 * time.Second):
		t.Fatal("background agent did not finish")
	}
	bg.mu.Lock()
	defer bg.mu.Unlock()
	return bg.result
}

// A cancelled child rarely unwinds with a bare context.Canceled: the model
// client, the retry loop and compaction all wrap or replace it. It is still a
// cancellation and must not be reported as a failure.
func TestLaunchBackgroundAgentReportsCancellationThroughWrappedErrors(t *testing.T) {
	bridge := ipc.NewBridge(strings.NewReader(""), io.Discard)
	store := session.NewStore(t.TempDir())

	t.Run("wrapped context.Canceled", func(t *testing.T) {
		launch := launchBackgroundAgent(bridge, "wrapped", "", exploreSubagentType, "invocation-wrapped", store,
			func(context.Context, *agent.StopController, func(toolpkg.AgentRunResult)) (toolpkg.AgentRunResult, error) {
				return toolpkg.AgentRunResult{}, fmt.Errorf("compact prompt: %w", context.Canceled)
			})
		result := waitForBackgroundAgent(t, launch.AgentID)
		if result.Status != "cancelled" {
			t.Fatalf("status = %q (error %q), want cancelled", result.Status, result.Error)
		}
	})

	t.Run("error after a hard cancel", func(t *testing.T) {
		started := make(chan struct{})
		launch := launchBackgroundAgent(bridge, "hard cancel", "", exploreSubagentType, "invocation-hard", store,
			func(ctx context.Context, _ *agent.StopController, _ func(toolpkg.AgentRunResult)) (toolpkg.AgentRunResult, error) {
				close(started)
				<-ctx.Done()
				return toolpkg.AgentRunResult{}, errors.New("anthropic request failed: connection reset")
			})
		<-started
		// The first stop is a soft request; a second one while it is still
		// pending cancels the context.
		cancelBackgroundAgent(launch.AgentID)
		cancelBackgroundAgent(launch.AgentID)
		result := waitForBackgroundAgent(t, launch.AgentID)
		if result.Status != "cancelled" {
			t.Fatalf("status = %q (error %q), want cancelled", result.Status, result.Error)
		}
	})

	t.Run("genuine failure", func(t *testing.T) {
		launch := launchBackgroundAgent(bridge, "failure", "", exploreSubagentType, "invocation-failure", store,
			func(context.Context, *agent.StopController, func(toolpkg.AgentRunResult)) (toolpkg.AgentRunResult, error) {
				return toolpkg.AgentRunResult{}, errors.New("model invocation failed")
			})
		result := waitForBackgroundAgent(t, launch.AgentID)
		if result.Status != "failed" || result.Error != "model invocation failed" {
			t.Fatalf("result = status %q error %q, want the failure reported", result.Status, result.Error)
		}
	})
}
