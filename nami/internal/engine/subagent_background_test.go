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

// registerRunningTestAgents registers background agents that stay running
// until the test ends, and a team made of them.
func registerRunningTestAgents(t *testing.T, count int) *backgroundTeam {
	t.Helper()
	team := &backgroundTeam{id: newBackgroundTeamID(), createdAt: time.Now()}
	agents := make([]*backgroundAgent, 0, count)
	for range count {
		bg := &backgroundAgent{
			id:      newBackgroundAgentID(),
			running: true,
			done:    make(chan struct{}),
			result:  toolpkg.AgentRunResult{Status: "running"},
		}
		registerBackgroundAgent(bg)
		agents = append(agents, bg)
		team.members = append(team.members, backgroundTeamMember{agentID: bg.id})
	}
	registerBackgroundTeam(team)
	t.Cleanup(func() {
		backgroundTeamsMu.Lock()
		delete(backgroundTeams, team.id)
		backgroundTeamsMu.Unlock()
		backgroundAgentsMu.Lock()
		defer backgroundAgentsMu.Unlock()
		for _, bg := range agents {
			bg.mu.Lock()
			bg.running = false
			bg.mu.Unlock()
			close(bg.done)
			delete(backgroundAgents, bg.id)
		}
	})
	return team
}

// wait_ms is how long agent_team_status may wait in total, not per member.
func TestLookupBackgroundTeamStatusWaitIsBoundedForTheWholeTeam(t *testing.T) {
	const members = 4
	const waitMs = 250
	team := registerRunningTestAgents(t, members)

	started := time.Now()
	status, err := lookupBackgroundTeamStatus(t.Context(), toolpkg.AgentTeamStatusRequest{TeamID: team.id, WaitMs: waitMs})
	elapsed := time.Since(started)
	if err != nil {
		t.Fatalf("lookupBackgroundTeamStatus: %v", err)
	}
	if status.Status != "running" || len(status.Agents) != members {
		t.Fatalf("status = %q with %d agents, want running with %d", status.Status, len(status.Agents), members)
	}
	// Waiting per member would take members*waitMs = 1s.
	if limit := 3 * waitMs * time.Millisecond; elapsed > limit {
		t.Fatalf("waited %v for a %dms wait_ms, want at most %v", elapsed, waitMs, limit)
	}
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
