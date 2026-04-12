package main

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

	"github.com/channyeintun/gocode/internal/agent"
	"github.com/channyeintun/gocode/internal/ipc"
	"github.com/channyeintun/gocode/internal/session"
	toolpkg "github.com/channyeintun/gocode/internal/tools"
)

const backgroundAgentRetention = 5 * time.Minute

type backgroundAgent struct {
	mu           sync.Mutex
	id           string
	invocationID string
	description  string
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
)

func newBackgroundAgentID() string {
	return fmt.Sprintf("agent_%d", atomic.AddUint64(&backgroundAgentCtr, 1))
}

func registerBackgroundAgent(bg *backgroundAgent) {
	backgroundAgentsMu.Lock()
	defer backgroundAgentsMu.Unlock()
	backgroundAgents[bg.id] = bg
}

func getBackgroundAgent(agentID string) (*backgroundAgent, error) {
	backgroundAgentsMu.RLock()
	defer backgroundAgentsMu.RUnlock()
	bg, ok := backgroundAgents[agentID]
	if !ok {
		return nil, fmt.Errorf("agent %q not found", agentID)
	}
	return bg, nil
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

func emitBackgroundAgentUpdated(bridge *ipc.Bridge, bg *backgroundAgent, result toolpkg.AgentRunResult) {
	if bridge == nil || bg == nil {
		return
	}
	_ = bridge.Emit(ipc.EventBackgroundAgentUpdated, ipc.BackgroundAgentUpdatedPayload{
		AgentID:        bg.id,
		InvocationID:   firstNonEmpty(result.InvocationID, bg.invocationID, result.SessionID),
		Description:    bg.description,
		SubagentType:   firstNonEmpty(result.SubagentType, bg.subagentType),
		Status:         result.Status,
		Summary:        result.Summary,
		SessionID:      result.SessionID,
		TranscriptPath: result.TranscriptPath,
		OutputFile:     result.OutputFile,
		Error:          result.Error,
		TotalCostUSD:   result.TotalCostUSD,
		InputTokens:    result.InputTokens,
		OutputTokens:   result.OutputTokens,
		Metadata:       toIPCChildAgentMetadata(buildChildMetadata(result, bg.description)),
	})
}

func toIPCChildAgentMetadata(metadata *toolpkg.ChildAgentMetadata) *ipc.ChildAgentMetadataPayload {
	if metadata == nil {
		return nil
	}
	tools := append([]string(nil), metadata.Tools...)
	return &ipc.ChildAgentMetadataPayload{
		InvocationID:    metadata.InvocationID,
		AgentID:         metadata.AgentID,
		Description:     metadata.Description,
		SubagentType:    metadata.SubagentType,
		LifecycleState:  metadata.LifecycleState,
		StatusMessage:   metadata.StatusMessage,
		StopBlockReason: metadata.StopBlockReason,
		StopBlockCount:  metadata.StopBlockCount,
		SessionID:       metadata.SessionID,
		TranscriptPath:  metadata.TranscriptPath,
		ResultPath:      metadata.ResultPath,
		Tools:           tools,
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
	bg.result = withChildMetadata(bg.result, description)
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
				bg.result = withChildMetadata(bg.result, bg.description)
				writeBackgroundAgentResultFile(bg.result)
				emitBackgroundAgentUpdated(bridge, bg, bg.result)
				return
			}
			bg.result.Status = "failed"
			bg.result.Error = err.Error()
			bg.result = withChildMetadata(bg.result, bg.description)
			writeBackgroundAgentResultFile(bg.result)
			emitBackgroundAgentUpdated(bridge, bg, bg.result)
			return
		}
		if strings.TrimSpace(result.Status) == "" {
			result.Status = "completed"
		}
		result.AgentID = agentID
		bg.result = withChildMetadata(result, bg.description)
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
	bg.result = withChildMetadata(result, bg.description)
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
		bg.result = withChildMetadata(bg.result, bg.description)
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
