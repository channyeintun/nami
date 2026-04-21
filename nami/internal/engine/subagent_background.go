package engine

import (
	"context"
	"encoding/json"
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
	result       toolpkg.AgentRunResult
	running      bool
	done         chan struct{}
	cancel       context.CancelFunc
	stopControl  *agent.StopController
}

var (
	backgroundAgents   = make(map[string]*backgroundAgent)
	backgroundAgentsMu sync.RWMutex
	backgroundAgentCtr uint64
	backgroundTeams    = make(map[string]*backgroundTeam)
	backgroundTeamsMu  sync.RWMutex
	backgroundTeamCtr  uint64
)

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
	return fmt.Sprintf("agent_%d", atomic.AddUint64(&backgroundAgentCtr, 1))
}

func newBackgroundTeamID() string {
	return fmt.Sprintf("team_%d", atomic.AddUint64(&backgroundTeamCtr, 1))
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

func writeBackgroundAgentResultFile(result toolpkg.AgentRunResult) {
	if result.OutputFile == "" {
		return
	}
	data, err := json.MarshalIndent(result, "", "  ")
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(result.OutputFile), 0o755)
	_ = os.WriteFile(result.OutputFile, data, 0o644)
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

func launchBackgroundAgent(
	parentCtx context.Context,
	bridge *ipc.Bridge,
	description string,
	role string,
	subagentType string,
	invocationID string,
	sessionStore *session.Store,
	execute func(context.Context, *agent.StopController, func(toolpkg.AgentRunResult)) (toolpkg.AgentRunResult, error),
) toolpkg.AgentRunResult {
	agentID := newBackgroundAgentID()
	ctx, cancel := context.WithCancel(context.Background())
	stopControl := agent.NewStopController()
	transcriptPath := filepath.Join(sessionStore.SessionDir(invocationID), "transcript.ndjson")
	resultFile := filepath.Join(sessionStore.SessionDir(invocationID), "agent-result.json")
	bg := &backgroundAgent{
		id:           agentID,
		invocationID: invocationID,
		description:  description,
		role:         role,
		subagentType: subagentType,
		done:         make(chan struct{}),
		cancel:       cancel,
		stopControl:  stopControl,
		running:      true,
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
			if err == context.Canceled {
				bg.result.Status = "cancelled"
				bg.result.Error = "background child agent cancelled"
				bg.result = withChildMetadata(bg.result, bg.description, bg.role)
				writeBackgroundAgentResultFile(bg.result)
				emitBackgroundAgentUpdated(bridge, bg, bg.result)
				return
			}
			bg.result.Status = "failed"
			bg.result.Error = err.Error()
			bg.result = withChildMetadata(bg.result, bg.description, bg.role)
			writeBackgroundAgentResultFile(bg.result)
			emitBackgroundAgentUpdated(bridge, bg, bg.result)
			return
		}
		if strings.TrimSpace(result.Status) == "" {
			result.Status = "completed"
		}
		result.AgentID = agentID
		bg.result = withChildMetadata(result, bg.description, bg.role)
		writeBackgroundAgentResultFile(bg.result)
		emitBackgroundAgentUpdated(bridge, bg, bg.result)
	}()

	go func() {
		select {
		case <-parentCtx.Done():
		case <-bg.done:
		}
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
	bg.result = withChildMetadata(result, bg.description, "")
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
		bg.result = withChildMetadata(bg.result, bg.description, "")
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
	return toolpkg.AgentTeamLaunchResult{Status: "async_launched", TeamID: teamID, Description: req.Description, Agents: results}, nil
}

func lookupBackgroundTeamStatus(ctx context.Context, req toolpkg.AgentTeamStatusRequest) (toolpkg.AgentTeamStatusResult, error) {
	team, err := getBackgroundTeam(req.TeamID)
	if err != nil {
		return toolpkg.AgentTeamStatusResult{}, err
	}
	results := make([]toolpkg.AgentRunResult, 0, len(team.members))
	overall := "completed"
	for _, member := range team.members {
		result, err := lookupBackgroundTeamMemberStatus(ctx, member, req.WaitMs)
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
