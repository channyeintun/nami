package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/channyeintun/gocode/internal/ipc"
	toolpkg "github.com/channyeintun/gocode/internal/tools"
)

const backgroundAgentRetention = 5 * time.Minute

type backgroundAgent struct {
	mu           sync.Mutex
	id           string
	description  string
	subagentType string
	result       toolpkg.AgentRunResult
	running      bool
	done         chan struct{}
	cancel       context.CancelFunc
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
	})
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
	execute func(context.Context) (toolpkg.AgentRunResult, error),
) toolpkg.AgentRunResult {
	agentID := newBackgroundAgentID()
	ctx, cancel := context.WithCancel(context.Background())
	bg := &backgroundAgent{
		id:           agentID,
		description:  description,
		subagentType: subagentType,
		done:         make(chan struct{}),
		cancel:       cancel,
		running:      true,
		result: toolpkg.AgentRunResult{
			Status:       "running",
			AgentID:      agentID,
			SubagentType: subagentType,
		},
	}
	registerBackgroundAgent(bg)
	emitBackgroundAgentUpdated(bridge, bg, bg.result)

	go func() {
		defer close(bg.done)
		result, err := execute(ctx)
		bg.mu.Lock()
		defer bg.mu.Unlock()
		defer scheduleBackgroundAgentCleanup(bg)
		bg.running = false
		if err != nil {
			if err == context.Canceled {
				bg.result.Status = "cancelled"
				bg.result.Error = "background child agent cancelled"
				writeBackgroundAgentResultFile(bg.result)
				emitBackgroundAgentUpdated(bridge, bg, bg.result)
				return
			}
			bg.result.Status = "failed"
			bg.result.Error = err.Error()
			writeBackgroundAgentResultFile(bg.result)
			emitBackgroundAgentUpdated(bridge, bg, bg.result)
			return
		}
		result.Status = "completed"
		result.AgentID = agentID
		bg.result = result
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
		Status:       "async_launched",
		AgentID:      agentID,
		SubagentType: subagentType,
	}
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
	if bg.running {
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
	bg.mu.Lock()
	if bg.running && bg.cancel != nil {
		bg.cancel()
	}
	if bg.running {
		bg.result.Status = "cancelling"
		shouldEmit = true
	}
	current := bg.result
	bg.mu.Unlock()
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
	if bg.running {
		result.Status = "cancelling"
	}
	return result, nil
}
