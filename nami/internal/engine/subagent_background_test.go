package engine

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	costpkg "github.com/channyeintun/nami/internal/cost"
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

// The result file holds the child's final answer, which can quote anything it
// read.
func TestWriteBackgroundAgentResultFileIsPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX permission bits")
	}
	outputFile := filepath.Join(t.TempDir(), "child-session", "agent-result.json")
	if err := writeBackgroundAgentResultFile(toolpkg.AgentRunResult{Status: "completed", Summary: "found the key", OutputFile: outputFile}); err != nil {
		t.Fatalf("writeBackgroundAgentResultFile: %v", err)
	}
	requirePrivate(t, filepath.Dir(outputFile))
	requirePrivate(t, outputFile)
}

// Team status falls back to the result file once an agent is evicted, so a
// file that could not be written has to be reported, not skipped silently.
func TestSaveAgentResultFileReportsFailures(t *testing.T) {
	blocker := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(blocker, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocker: %v", err)
	}
	var emitted bytes.Buffer
	bridge := ipc.NewBridge(strings.NewReader(""), &emitted)

	saveAgentResultFile(bridge, toolpkg.AgentRunResult{Status: "completed", OutputFile: filepath.Join(blocker, "agent-result.json")})

	if !strings.Contains(emitted.String(), `"type":"notice"`) || !strings.Contains(emitted.String(), "save child agent result") {
		t.Fatalf("no notice for the failed write; emitted %q", emitted.String())
	}
}

// A cancelled child rarely unwinds with a bare context.Canceled: the model
// client, the retry loop and compaction all wrap or replace it. It is still a
// cancellation and must not be reported as a failure.
func TestLaunchBackgroundAgentReportsCancellationThroughWrappedErrors(t *testing.T) {
	bridge := ipc.NewBridge(strings.NewReader(""), io.Discard)
	store := session.NewStore(t.TempDir())

	t.Run("wrapped context.Canceled", func(t *testing.T) {
		launch := launchBackgroundAgent(bridge, "wrapped", "", exploreSubagentType, "invocation-wrapped", "", store,
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
		launch := launchBackgroundAgent(bridge, "hard cancel", "", exploreSubagentType, "invocation-hard", "", store,
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
		launch := launchBackgroundAgent(bridge, "failure", "", exploreSubagentType, "invocation-failure", "", store,
			func(context.Context, *agent.StopController, func(toolpkg.AgentRunResult)) (toolpkg.AgentRunResult, error) {
				return toolpkg.AgentRunResult{}, errors.New("model invocation failed")
			})
		result := waitForBackgroundAgent(t, launch.AgentID)
		if result.Status != "failed" || result.Error != "model invocation failed" {
			t.Fatalf("result = status %q error %q, want the failure reported", result.Status, result.Error)
		}
	})
}

// useActiveSession makes sessionID the active session for the test.
func useActiveSession(t *testing.T, sessionID string) {
	t.Helper()
	previous := activeSessionID()
	t.Cleanup(func() { setActiveSession(previous) })
	setActiveSession(sessionID)
}

// A background agent outlives the session that launched it. Once /clear or
// /resume moves the engine on, its updates must not land in the session that
// replaced its own, where the TUI files them as that session's agents.
func TestBackgroundAgentStopsReportingWhenItsSessionIsLeft(t *testing.T) {
	useActiveSession(t, "session-a")
	output := &lockedBuffer{}
	bridge := ipc.NewBridge(strings.NewReader(""), output)
	release := make(chan struct{})
	launch := launchBackgroundAgent(bridge, "long task", "", exploreSubagentType, "invocation-left-behind", "session-a", session.NewStore(t.TempDir()),
		func(context.Context, *agent.StopController, func(toolpkg.AgentRunResult)) (toolpkg.AgentRunResult, error) {
			<-release
			return toolpkg.AgentRunResult{Status: "completed", Summary: "done"}, nil
		})
	if !strings.Contains(output.String(), launch.AgentID) {
		t.Fatalf("the launch was not reported while its session was active; emitted %q", output.String())
	}

	setActiveSession("session-b")
	reportedBefore := output.String()
	close(release)
	result := waitForBackgroundAgent(t, launch.AgentID)

	if after := output.String(); after != reportedBefore {
		t.Fatalf("an agent from the session left behind reported into the new one: %s", strings.TrimPrefix(after, reportedBefore))
	}
	// agent_status still has the result for whoever asks by id.
	if result.Status != "completed" || result.Summary != "done" {
		t.Fatalf("result = %+v, want the completed run", result)
	}
}

// A child's spend belongs to the session that launched it: in the live
// tracker while that session is active, and in its saved total once the
// engine has moved on, never in the total of the session that replaced it.
func TestChildCostGoesToTheSessionThatLaunchedIt(t *testing.T) {
	store := session.NewStore(t.TempDir())
	savedSpend := costpkg.NewTracker()
	savedSpend.RecordAPICall("claude-sonnet-5", 0, 0, 0, 0, 0, 1.00)
	if err := persistSessionState(store, sessionStateParams{SessionID: "session-a", CreatedAt: time.Now(), Mode: agent.ModeFast, Tracker: savedSpend}); err != nil {
		t.Fatalf("persist session-a: %v", err)
	}
	bridge := ipc.NewBridge(strings.NewReader(""), io.Discard)
	child := costpkg.TrackerSnapshot{TotalCostUSD: 0.40, TotalInputTokens: 10}

	useActiveSession(t, "session-a")
	liveTracker := costpkg.NewTracker()
	chargeChildCost(bridge, liveTracker, store, "session-a", child)
	if got := liveTracker.Snapshot().TotalCostUSD; got != 0.40 {
		t.Fatalf("active session's tracker = %v, want the child's 0.40", got)
	}

	setActiveSession("session-b")
	newSessionTracker := costpkg.NewTracker()
	chargeChildCost(bridge, newSessionTracker, store, "session-a", child)
	if got := newSessionTracker.Snapshot().TotalCostUSD; got != 0 {
		t.Fatalf("the new session was charged %v for a child of the session left behind", got)
	}
	meta, err := store.LoadMetadata("session-a")
	if err != nil {
		t.Fatalf("LoadMetadata: %v", err)
	}
	if meta.TotalCostUSD != 1.40 {
		t.Fatalf("session-a's saved cost = %v, want 1.00 plus the child's 0.40", meta.TotalCostUSD)
	}
}
