package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/channyeintun/nami/internal/agent"
	"github.com/channyeintun/nami/internal/ipc"
	"github.com/channyeintun/nami/internal/session"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

const backgroundAgentRetention = 5 * time.Minute

type backgroundAgent struct {
	mu           sync.Mutex
	id           string
	invocationID string
	description  string
	role         string
	subagentType string
	// ownerSessionID is the session that launched the agent.
	ownerSessionID string
	result         toolpkg.AgentRunResult
	running        bool
	done           chan struct{}
	cancel         context.CancelFunc
	stopControl    *agent.StopController
}

var (
	backgroundAgents   = make(map[string]*backgroundAgent)
	backgroundAgentsMu sync.RWMutex
	backgroundAgentCtr atomic.Uint64
	backgroundTeams    = make(map[string]*backgroundTeam)
	backgroundTeamsMu  sync.RWMutex
	backgroundTeamCtr  atomic.Uint64
)

// activeSession is the session the engine is serving. A background agent can
// outlive the turn and the session that launched it, so it checks this before
// reporting: one left running across /clear or /resume must not report into
// the session that replaced its own.
var activeSession struct {
	mu sync.RWMutex
	id string
}

// setActiveSession records the session the engine now serves. A command that
// switches sessions calls it before telling the TUI, so no report from the
// session left behind can arrive after the TUI has moved on.
func setActiveSession(sessionID string) {
	activeSession.mu.Lock()
	defer activeSession.mu.Unlock()
	activeSession.id = sessionID
}

// belongsToActiveSession reports whether a background command's update is for
// the session the engine serves. A command keeps running when the user moves
// to another session with /clear or /resume, but the conversation it was
// started for is gone: the TUI would hand its completion to the new
// conversation's model as input about a command it never ran.
func belongsToActiveSession(update toolpkg.BackgroundCommandUpdate) bool {
	return update.SessionID == "" || update.SessionID == activeSessionID()
}

func activeSessionID() string {
	activeSession.mu.RLock()
	defer activeSession.mu.RUnlock()
	return activeSession.id
}

type backgroundTeam struct {
	id          string
	description string
	members     []backgroundTeamMember
	createdAt   time.Time
}

type backgroundTeamMember struct {
	agentID    string
	outputFile string
}

func newBackgroundAgentID() string {
	return fmt.Sprintf("agent_%d", backgroundAgentCtr.Add(1))
}

func newBackgroundTeamID() string {
	return fmt.Sprintf("team_%d", backgroundTeamCtr.Add(1))
}

func registerBackgroundAgent(bg *backgroundAgent) {
	backgroundAgentsMu.Lock()
	defer backgroundAgentsMu.Unlock()
	backgroundAgents[bg.id] = bg
}

func getBackgroundAgent(agentID string) (*backgroundAgent, error) {
	bg, ok := findBackgroundAgent(agentID)
	if !ok {
		return nil, fmt.Errorf("agent %q not found", agentID)
	}
	return bg, nil
}

func findBackgroundAgent(agentID string) (*backgroundAgent, bool) {
	backgroundAgentsMu.RLock()
	defer backgroundAgentsMu.RUnlock()
	bg, ok := backgroundAgents[agentID]
	return bg, ok
}

func registerBackgroundTeam(team *backgroundTeam) {
	backgroundTeamsMu.Lock()
	defer backgroundTeamsMu.Unlock()
	backgroundTeams[team.id] = team
}

func getBackgroundTeam(teamID string) (*backgroundTeam, error) {
	backgroundTeamsMu.RLock()
	defer backgroundTeamsMu.RUnlock()
	team, ok := backgroundTeams[teamID]
	if !ok {
		return nil, fmt.Errorf("team %q not found", teamID)
	}
	return team, nil
}

func scheduleBackgroundTeamCleanup(team *backgroundTeam) {
	time.AfterFunc(backgroundAgentRetention, func() {
		if teamHasRunningMembers(team) {
			scheduleBackgroundTeamCleanup(team)
			return
		}
		backgroundTeamsMu.Lock()
		defer backgroundTeamsMu.Unlock()
		if current, ok := backgroundTeams[team.id]; ok && current == team {
			delete(backgroundTeams, team.id)
		}
	})
}

func teamHasRunningMembers(team *backgroundTeam) bool {
	for _, member := range team.members {
		bg, ok := findBackgroundAgent(member.agentID)
		if !ok {
			continue
		}
		bg.mu.Lock()
		running := bg.running
		bg.mu.Unlock()
		if running {
			return true
		}
	}
	return false
}

func scheduleBackgroundAgentCleanup(bg *backgroundAgent) {
	time.AfterFunc(backgroundAgentRetention, func() {
		backgroundAgentsMu.Lock()
		defer backgroundAgentsMu.Unlock()

		current, ok := backgroundAgents[bg.id]
		if !ok || current != bg {
			return
		}
		current.mu.Lock()
		defer current.mu.Unlock()
		if current.running {
			return
		}
		delete(backgroundAgents, bg.id)
	})
}

// saveAgentResultFile writes a child's result file and tells the user when
// that fails: once a background agent is evicted from memory, its team's
// status can only be read back from this file.
func saveAgentResultFile(bridge *ipc.Bridge, result toolpkg.AgentRunResult) {
	if err := writeBackgroundAgentResultFile(result); err != nil && bridge != nil {
		_ = bridge.EmitNotice(fmt.Sprintf("save child agent result: %v", err))
	}
}

func writeBackgroundAgentResultFile(result toolpkg.AgentRunResult) error {
	if result.OutputFile == "" {
		return nil
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return fmt.Errorf("encode %s: %w", result.OutputFile, err)
	}
	if err := os.MkdirAll(filepath.Dir(result.OutputFile), sessionDataDirMode); err != nil {
		return err
	}
	return os.WriteFile(result.OutputFile, data, sessionDataFileMode)
}

func readBackgroundAgentResultFile(path string) (toolpkg.AgentRunResult, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return toolpkg.AgentRunResult{}, err
	}
	var result toolpkg.AgentRunResult
	if err := json.Unmarshal(data, &result); err != nil {
		return toolpkg.AgentRunResult{}, err
	}
	return result, nil
}

func cancelBackgroundAgent(agentID string) {
	bg, ok := findBackgroundAgent(strings.TrimSpace(agentID))
	if !ok {
		return
	}
	bg.mu.Lock()
	forceCancel := false
	if bg.running {
		if bg.stopControl != nil {
			forceCancel = bg.stopControl.Request("cancelled")
		} else if bg.cancel != nil {
			forceCancel = true
		}
		if strings.TrimSpace(bg.result.Status) == "" || bg.result.Status == "running" || bg.result.Status == "async_launched" {
			bg.result.Status = "cancelling"
			bg.result = withChildMetadata(bg.result, bg.description, bg.role)
		}
	}
	cancel := bg.cancel
	bg.mu.Unlock()
	if forceCancel && cancel != nil {
		cancel()
	}
}

func cancelBackgroundTeamMembers(members []backgroundTeamMember) {
	for _, member := range members {
		cancelBackgroundAgent(member.agentID)
	}
}

func lookupBackgroundTeamMemberStatus(ctx context.Context, member backgroundTeamMember, waitMs int) (toolpkg.AgentRunResult, error) {
	agentID := strings.TrimSpace(member.agentID)
	if agentID != "" {
		if _, ok := findBackgroundAgent(agentID); ok {
			return lookupBackgroundAgentStatus(ctx, toolpkg.AgentStatusRequest{AgentID: agentID, WaitMs: waitMs})
		}
	}
	if outputFile := strings.TrimSpace(member.outputFile); outputFile != "" {
		return readBackgroundAgentResultFile(outputFile)
	}
	if agentID != "" {
		return toolpkg.AgentRunResult{}, fmt.Errorf("agent %q not found", agentID)
	}
	return toolpkg.AgentRunResult{}, fmt.Errorf("background team member is missing result metadata")
}

func emitBackgroundAgentUpdated(bridge *ipc.Bridge, bg *backgroundAgent, result toolpkg.AgentRunResult) {
	if bridge == nil || bg == nil {
		return
	}
	// The TUI files every update under the session it shows. An agent whose
	// session was left keeps its result for agent_status and its result file,
	// but stays out of the session that replaced its own.
	if bg.ownerSessionID != activeSessionID() {
		return
	}
	displayResult := toolpkg.DisplaySafeAgentResult(result)
	_ = bridge.Emit(ipc.EventBackgroundAgentUpdated, ipc.BackgroundAgentUpdatedPayload{
		AgentID:        bg.id,
		InvocationID:   firstNonEmpty(displayResult.InvocationID, bg.invocationID, displayResult.SessionID),
		Description:    bg.description,
		SubagentType:   firstNonEmpty(displayResult.SubagentType, bg.subagentType),
		Status:         displayResult.Status,
		Summary:        displayResult.Summary,
		SessionID:      displayResult.SessionID,
		TranscriptPath: displayResult.TranscriptPath,
		OutputFile:     displayResult.OutputFile,
		Error:          displayResult.Error,
		TotalCostUSD:   displayResult.TotalCostUSD,
		InputTokens:    displayResult.InputTokens,
		OutputTokens:   displayResult.OutputTokens,
		Metadata:       toIPCChildAgentMetadata(buildChildMetadata(displayResult, bg.description, bg.role)),
	})
}

func toIPCChildAgentMetadata(metadata *toolpkg.ChildAgentMetadata) *ipc.ChildAgentMetadataPayload {
	if metadata == nil {
		return nil
	}
	tools := append([]string(nil), metadata.Tools...)
	return &ipc.ChildAgentMetadataPayload{
		InvocationID:      metadata.InvocationID,
		AgentID:           metadata.AgentID,
		Description:       metadata.Description,
		Role:              metadata.Role,
		SubagentType:      metadata.SubagentType,
		WorkspaceStrategy: metadata.WorkspaceStrategy,
		WorkspacePath:     metadata.WorkspacePath,
		RepositoryRoot:    metadata.RepositoryRoot,
		WorktreeBranch:    metadata.WorktreeBranch,
		WorktreeCreated:   metadata.WorktreeCreated,
		LifecycleState:    metadata.LifecycleState,
		StatusMessage:     metadata.StatusMessage,
		StopBlockReason:   metadata.StopBlockReason,
		StopBlockCount:    metadata.StopBlockCount,
		SessionID:         metadata.SessionID,
		TranscriptPath:    metadata.TranscriptPath,
		ResultPath:        metadata.ResultPath,
		Tools:             tools,
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}

// launchBackgroundAgent runs the child on a detached context so it outlives
// the parent turn; cancellation happens only via stopControl or agent_stop.
func launchBackgroundAgent(
	bridge *ipc.Bridge,
	description string,
	role string,
	subagentType string,
	invocationID string,
	ownerSessionID string,
	sessionStore *session.Store,
	execute func(context.Context, *agent.StopController, func(toolpkg.AgentRunResult)) (toolpkg.AgentRunResult, error),
) toolpkg.AgentRunResult {
	agentID := newBackgroundAgentID()
	ctx, cancel := context.WithCancel(context.Background())
	stopControl := agent.NewStopController()
	transcriptPath := filepath.Join(sessionStore.SessionDir(invocationID), "transcript.ndjson")
	resultFile := filepath.Join(sessionStore.SessionDir(invocationID), "agent-result.json")
	bg := &backgroundAgent{
		id:             agentID,
		invocationID:   invocationID,
		description:    description,
		role:           role,
		subagentType:   subagentType,
		ownerSessionID: ownerSessionID,
		done:           make(chan struct{}),
		cancel:         cancel,
		stopControl:    stopControl,
		running:        true,
		result: toolpkg.AgentRunResult{
			Status:         "running",
			InvocationID:   invocationID,
			AgentID:        agentID,
			SubagentType:   subagentType,
			SessionID:      invocationID,
			TranscriptPath: transcriptPath,
			OutputFile:     resultFile,
		},
	}
	bg.result = withChildMetadata(bg.result, description, role)
	registerBackgroundAgent(bg)
	emitBackgroundAgentUpdated(bridge, bg, bg.result)

	go func() {
		defer close(bg.done)
		result, err := execute(ctx, stopControl, func(update toolpkg.AgentRunResult) {
			updateBackgroundAgentRunningState(bridge, bg, update)
		})
		bg.mu.Lock()
		defer bg.mu.Unlock()
		defer scheduleBackgroundAgentCleanup(bg)
		bg.running = false
		if err != nil {
			// Once the run's context is cancelled, whatever error the child
			// unwinds with - often wrapped by the model client or compaction -
			// is the cancellation, not a failure.
			if errors.Is(err, context.Canceled) || ctx.Err() != nil {
				bg.result.Status = "cancelled"
				bg.result.Error = "background child agent cancelled"
				bg.result = withChildMetadata(bg.result, bg.description, bg.role)
				saveAgentResultFile(bridge, bg.result)
				emitBackgroundAgentUpdated(bridge, bg, bg.result)
				return
			}
			bg.result.Status = "failed"
			bg.result.Error = err.Error()
			bg.result = withChildMetadata(bg.result, bg.description, bg.role)
			saveAgentResultFile(bridge, bg.result)
			emitBackgroundAgentUpdated(bridge, bg, bg.result)
			return
		}
		if strings.TrimSpace(result.Status) == "" {
			result.Status = "completed"
		}
		result.AgentID = agentID
		bg.result = withChildMetadata(result, bg.description, bg.role)
		saveAgentResultFile(bridge, bg.result)
		emitBackgroundAgentUpdated(bridge, bg, bg.result)
	}()

	return toolpkg.AgentRunResult{
		Status:         "async_launched",
		InvocationID:   invocationID,
		AgentID:        agentID,
		SubagentType:   subagentType,
		SessionID:      invocationID,
		TranscriptPath: transcriptPath,
		OutputFile:     resultFile,
	}
}

func updateBackgroundAgentRunningState(bridge *ipc.Bridge, bg *backgroundAgent, result toolpkg.AgentRunResult) {
	if bg == nil {
		return
	}
	bg.mu.Lock()
	if !bg.running {
		bg.mu.Unlock()
		return
	}
	if result.AgentID == "" {
		result.AgentID = bg.id
	}
	if result.InvocationID == "" {
		result.InvocationID = bg.invocationID
	}
	if result.SubagentType == "" {
		result.SubagentType = bg.subagentType
	}
	if result.Status == "" {
		result.Status = "running"
	}
	if result.SessionID == "" {
		result.SessionID = bg.result.SessionID
	}
	if result.TranscriptPath == "" {
		result.TranscriptPath = bg.result.TranscriptPath
	}
	if result.OutputFile == "" {
		result.OutputFile = bg.result.OutputFile
	}
	bg.result = withChildMetadata(result, bg.description, bg.role)
	current := bg.result
	bg.mu.Unlock()
	emitBackgroundAgentUpdated(bridge, bg, current)
}

func lookupBackgroundAgentStatus(ctx context.Context, req toolpkg.AgentStatusRequest) (toolpkg.AgentRunResult, error) {
	bg, err := getBackgroundAgent(req.AgentID)
	if err != nil {
		return toolpkg.AgentRunResult{}, err
	}
	if req.WaitMs > 0 {
		timer := time.NewTimer(time.Duration(req.WaitMs) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-bg.done:
		case <-timer.C:
		case <-ctx.Done():
			return toolpkg.AgentRunResult{}, ctx.Err()
		}
	}

	bg.mu.Lock()
	defer bg.mu.Unlock()
	result := bg.result
	if bg.running && strings.TrimSpace(result.Status) == "" {
		result.Status = "running"
	}
	return result, nil
}

func stopBackgroundAgent(ctx context.Context, bridge *ipc.Bridge, req toolpkg.AgentStopRequest) (toolpkg.AgentRunResult, error) {
	bg, err := getBackgroundAgent(req.AgentID)
	if err != nil {
		return toolpkg.AgentRunResult{}, err
	}

	shouldEmit := false
	forceCancel := false
	bg.mu.Lock()
	if bg.running {
		if bg.stopControl != nil {
			forceCancel = bg.stopControl.Request("cancelled")
		} else if bg.cancel != nil {
			forceCancel = true
		}
	}
	if bg.running {
		bg.result.Status = "cancelling"
		bg.result = withChildMetadata(bg.result, bg.description, bg.role)
		shouldEmit = true
	}
	current := bg.result
	bg.mu.Unlock()
	if forceCancel && bg.cancel != nil {
		bg.cancel()
	}
	if shouldEmit {
		emitBackgroundAgentUpdated(bridge, bg, current)
	}

	if req.WaitMs > 0 {
		timer := time.NewTimer(time.Duration(req.WaitMs) * time.Millisecond)
		defer timer.Stop()
		select {
		case <-bg.done:
		case <-timer.C:
		case <-ctx.Done():
			return toolpkg.AgentRunResult{}, ctx.Err()
		}
	}

	bg.mu.Lock()
	defer bg.mu.Unlock()
	result := bg.result
	if bg.running && strings.TrimSpace(result.Status) == "" {
		result.Status = "cancelling"
	}
	return result, nil
}

func launchBackgroundTeam(ctx context.Context, runner toolpkg.AgentRunner, req toolpkg.AgentTeamLaunchRequest) (toolpkg.AgentTeamLaunchResult, error) {
	if runner == nil {
		return toolpkg.AgentTeamLaunchResult{}, fmt.Errorf("agent team launcher is not configured")
	}
	teamID := newBackgroundTeamID()
	team := &backgroundTeam{id: teamID, description: req.Description, createdAt: time.Now(), members: make([]backgroundTeamMember, 0, len(req.Tasks))}
	results := make([]toolpkg.AgentRunResult, 0, len(req.Tasks))
	for _, task := range req.Tasks {
		result, err := runner(ctx, toolpkg.AgentRunRequest{
			Description:       task.Description,
			Prompt:            task.Prompt,
			Role:              task.Role,
			WorkspaceStrategy: task.WorkspaceStrategy,
			SubagentType:      toolpkg.NormalizeSubagentType(task.SubagentType),
			Background:        true,
		})
		if err != nil {
			cancelBackgroundTeamMembers(team.members)
			return toolpkg.AgentTeamLaunchResult{}, err
		}
		team.members = append(team.members, backgroundTeamMember{agentID: strings.TrimSpace(result.AgentID), outputFile: strings.TrimSpace(result.OutputFile)})
		results = append(results, result)
	}
	registerBackgroundTeam(team)
	scheduleBackgroundTeamCleanup(team)
	return toolpkg.AgentTeamLaunchResult{Status: "async_launched", TeamID: teamID, Description: req.Description, Agents: results}, nil
}

func lookupBackgroundTeamStatus(ctx context.Context, req toolpkg.AgentTeamStatusRequest) (toolpkg.AgentTeamStatusResult, error) {
	team, err := getBackgroundTeam(req.TeamID)
	if err != nil {
		return toolpkg.AgentTeamStatusResult{}, err
	}
	results := make([]toolpkg.AgentRunResult, 0, len(team.members))
	overall := "completed"
	// wait_ms bounds the whole call. Giving each member the full wait in turn
	// would block for up to one wait per member still running.
	deadline := time.Now().Add(time.Duration(req.WaitMs) * time.Millisecond)
	for _, member := range team.members {
		waitMs := 0
		if req.WaitMs > 0 {
			waitMs = max(0, int(time.Until(deadline).Milliseconds()))
		}
		result, err := lookupBackgroundTeamMemberStatus(ctx, member, waitMs)
		if err != nil {
			return toolpkg.AgentTeamStatusResult{}, err
		}
		results = append(results, result)
		switch strings.ToLower(strings.TrimSpace(result.Status)) {
		case "failed":
			overall = "failed"
		case "running", "async_launched", "cancelling":
			if overall != "failed" {
				overall = "running"
			}
		case "cancelled":
			if overall == "completed" {
				overall = "cancelled"
			}
		}
	}
	if len(results) == 0 {
		overall = "empty"
	}
	return toolpkg.AgentTeamStatusResult{Status: overall, TeamID: team.id, Description: team.description, Agents: results}, nil
}
