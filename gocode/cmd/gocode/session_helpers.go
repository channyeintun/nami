package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"github.com/channyeintun/gocode/internal/agent"
	"github.com/channyeintun/gocode/internal/api"
	artifactspkg "github.com/channyeintun/gocode/internal/artifacts"
	"github.com/channyeintun/gocode/internal/compact"
	costpkg "github.com/channyeintun/gocode/internal/cost"
	"github.com/channyeintun/gocode/internal/ipc"
	"github.com/channyeintun/gocode/internal/localmodel"
	"github.com/channyeintun/gocode/internal/session"
	"github.com/channyeintun/gocode/internal/timing"
	toolpkg "github.com/channyeintun/gocode/internal/tools"
)

func emitArtifactCreated(bridge *ipc.Bridge, artifact artifactspkg.Artifact) error {
	return bridge.Emit(ipc.EventArtifactCreated, ipc.ArtifactCreatedPayload{
		ID:      artifact.ID,
		Kind:    string(artifact.Kind),
		Scope:   string(artifact.Scope),
		Title:   artifact.Title,
		Version: artifact.Version,
		Source:  artifact.Source,
		Status:  artifactMetadataString(artifact, "status"),
	})
}

func emitArtifactUpdated(bridge *ipc.Bridge, artifact artifactspkg.Artifact, content string) error {
	return bridge.Emit(ipc.EventArtifactUpdated, ipc.ArtifactUpdatedPayload{
		ID:      artifact.ID,
		Content: content,
		Version: artifact.Version,
		Status:  artifactMetadataString(artifact, "status"),
	})
}

func artifactMetadataString(artifact artifactspkg.Artifact, key string) string {
	if v, ok := artifact.Metadata[key].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func emitArtifactFocused(bridge *ipc.Bridge, artifact artifactspkg.Artifact) error {
	return bridge.Emit(ipc.EventArtifactFocused, ipc.ArtifactFocusedPayload{
		ID:      artifact.ID,
		Kind:    string(artifact.Kind),
		Title:   artifact.Title,
		Version: artifact.Version,
		Status:  artifactMetadataString(artifact, "status"),
	})
}

func emitArtifactFocusedForTurn(bridge *ipc.Bridge, artifact artifactspkg.Artifact, turnMetrics *timing.CheckpointRecorder) error {
	if turnMetrics != nil && turnMetrics.Mark("first_artifact_focus") {
		if err := emitTurnTimingCheckpoint(bridge, turnMetrics, "first_artifact_focus"); err != nil {
			return err
		}
	}
	return emitArtifactFocused(bridge, artifact)
}

func emitTurnTimingCheckpoint(bridge *ipc.Bridge, recorder *timing.CheckpointRecorder, checkpoint string) error {
	if bridge == nil || recorder == nil || checkpoint == "" {
		return nil
	}
	snapshot := recorder.Snapshot()
	at, ok := snapshot.Checkpoints[checkpoint]
	if !ok {
		return nil
	}
	return bridge.Emit(ipc.EventTurnTiming, ipc.TurnTimingPayload{
		Checkpoint: checkpoint,
		ElapsedMS:  at.Sub(snapshot.StartedAt).Milliseconds(),
	})
}

func emitArtifactStatusChanged(bridge *ipc.Bridge, artifact artifactspkg.Artifact) error {
	return bridge.Emit(ipc.EventArtifactStatusChanged, ipc.ArtifactStatusChangedPayload{
		ID:     artifact.ID,
		Status: artifactMetadataString(artifact, "status"),
	})
}

func budgetToolOutput(
	ctx context.Context,
	artifactManager *artifactspkg.Manager,
	sessionID string,
	budget toolpkg.ResultBudget,
	aggregateBudget *toolpkg.AggregateResultBudget,
	call api.ToolCall,
	output string,
) (string, artifactspkg.Artifact, toolBudgetInfo, error) {
	inlineLimit, shouldSpill, aggregateLimited := aggregateBudget.InlineLimit(len(output), budget)
	if !shouldSpill {
		aggregateBudget.Consume(len(output))
		return output, artifactspkg.Artifact{}, toolBudgetInfo{InlineChars: len(output)}, nil
	}
	if artifactManager == nil {
		preview := truncateOutputPreview(output, inlineLimit, "", len(output))
		aggregateBudget.Consume(len(preview))
		return preview, artifactspkg.Artifact{}, toolBudgetInfo{InlineChars: len(preview), Spilled: true, AggregateLimited: aggregateLimited}, nil
	}

	artifact, _, _, err := artifactManager.UpsertSessionMarkdown(ctx, artifactspkg.MarkdownRequest{
		Kind:    artifactspkg.KindToolLog,
		Scope:   artifactspkg.ScopeSession,
		Title:   fmt.Sprintf("Tool Log: %s", call.Name),
		Source:  call.Name,
		Content: artifactspkg.RenderToolLogMarkdown(sessionID, call.Name, call.ID, call.Input, output),
		Metadata: map[string]any{
			"tool_call_id": call.ID,
			"tool_name":    call.Name,
		},
	}, sessionID, "tool-log-"+call.ID)
	if err != nil {
		preview := truncateOutputPreview(output, inlineLimit, "", len(output))
		aggregateBudget.Consume(len(preview))
		return preview, artifactspkg.Artifact{}, toolBudgetInfo{InlineChars: len(preview), Spilled: true, AggregateLimited: aggregateLimited}, err
	}

	preview := truncateOutputPreview(output, inlineLimit, artifact.ContentPath, len(output))
	aggregateBudget.Consume(len(preview))
	return preview, artifact, toolBudgetInfo{InlineChars: len(preview), Spilled: true, AggregateLimited: aggregateLimited}, nil
}

type turnExecutionStats struct {
	AggregateBudgetChars     int
	AggregateBudgetSpills    int
	ContinuationBudgetTokens int
	ContinuationCount        int
	ContinuationStopReason   string
	ContinuationUsedTokens   int
	ToolInlineChars          int
	ToolResultCount          int
	ToolSpillCount           int
}

type toolBudgetInfo struct {
	AggregateLimited bool
	InlineChars      int
	Spilled          bool
}

func truncateOutputPreview(output string, previewLen int, artifactPath string, totalChars int) string {
	if previewLen <= 0 || previewLen > len(output) {
		previewLen = len(output)
	}
	preview := output[:previewLen]
	if artifactPath == "" {
		return fmt.Sprintf("%s\n\n[Output truncated (%d chars).]", preview, totalChars)
	}
	return fmt.Sprintf("%s\n\n[Output truncated. Full markdown artifact saved to %s (%d chars)]", preview, artifactPath, totalChars)
}

type sessionStateParams struct {
	SessionID string
	CreatedAt time.Time
	Mode      agent.ExecutionMode
	Model     string
	CWD       string
	Branch    string
	Tracker   *costpkg.Tracker
	Messages  []api.Message
}

type compactionSummarizer struct {
	bridge  *ipc.Bridge
	tracker *costpkg.Tracker
	client  api.LLMClient
	router  *localmodel.Router
}

func newCompactionPipeline(bridge *ipc.Bridge, tracker *costpkg.Tracker, client api.LLMClient) *compact.Pipeline {
	return compact.NewPipeline(client.Capabilities().MaxContextWindow, compactionSummarizer{
		bridge:  bridge,
		tracker: tracker,
		client:  client,
		router:  localmodel.NewRouter(client),
	})
}

func compactWithMetrics(
	ctx context.Context,
	bridge *ipc.Bridge,
	tracker *costpkg.Tracker,
	client api.LLMClient,
	timingLogger *timing.Logger,
	sessionID string,
	turnID int,
	reason string,
	messages []api.Message,
) (compact.CompactResult, error) {
	metrics := timing.NewCheckpointRecorder(time.Now())
	pipeline := newCompactionPipeline(bridge, tracker, client)
	result, err := pipeline.Compact(ctx, messages, reason)
	if err != nil {
		metrics.Mark("compact_failed")
		_ = timingLogger.AppendSnapshot("compaction", "compaction_duration", sessionID, turnID, metrics, map[string]any{
			"reason":        reason,
			"status":        "failed",
			"tokens_before": compact.EstimateConversationTokens(messages),
		})
		return compact.CompactResult{}, err
	}

	metrics.Mark("compact_completed")
	_ = timingLogger.AppendSnapshot("compaction", "compaction_duration", sessionID, turnID, metrics, map[string]any{
		"reason":        reason,
		"status":        "completed",
		"strategy":      string(result.Strategy),
		"tokens_after":  result.TokensAfter,
		"tokens_before": result.TokensBefore,
	})
	return result, nil
}

func (s compactionSummarizer) Summarize(ctx context.Context, messages []api.Message) (string, error) {
	return s.SummarizeWithPrompt(ctx, messages, compact.CompactionPromptTemplate)
}

func (s compactionSummarizer) SummarizeWithPrompt(ctx context.Context, messages []api.Message, prompt string) (string, error) {
	if summary, usedLocal, err := s.summarizeWithLocal(prompt, messages); usedLocal {
		if err == nil && strings.TrimSpace(summary) != "" {
			return compact.NormalizeSummary(summary), nil
		}
	}

	stream, err := s.client.Stream(ctx, api.ModelRequest{
		Messages:     messages,
		SystemPrompt: prompt,
		MaxTokens:    2048,
	})
	if err != nil {
		return "", err
	}

	startedAt := time.Now()
	var usage api.Usage
	var builder strings.Builder

	for event, streamErr := range stream {
		if streamErr != nil {
			return "", streamErr
		}
		switch event.Type {
		case api.ModelEventToken:
			builder.WriteString(event.Text)
		case api.ModelEventUsage:
			if event.Usage != nil {
				usage = mergeUsage(usage, *event.Usage)
			}
		}
	}

	s.tracker.RecordAPICall(
		s.client.ModelID(),
		usage.InputTokens,
		usage.OutputTokens,
		usage.CacheReadTokens,
		usage.CacheCreationTokens,
		time.Since(startedAt),
		costpkg.CalculateUSDCost(s.client.ModelID(), usage),
	)
	if err := emitCostUpdate(s.bridge, s.tracker); err != nil {
		return "", err
	}

	return compact.NormalizeSummary(builder.String()), nil
}

func (s compactionSummarizer) summarizeWithLocal(prompt string, messages []api.Message) (string, bool, error) {
	if s.router == nil {
		return "", false, nil
	}

	prompt = renderCompactionPrompt(prompt, messages)
	if strings.TrimSpace(prompt) == "" {
		return "", false, nil
	}

	return s.router.TryLocal(localmodel.TaskCompaction, prompt, 2048)
}

func renderCompactionPrompt(promptTemplate string, messages []api.Message) string {
	var builder strings.Builder
	builder.WriteString(promptTemplate)
	builder.WriteString("\n\nConversation:\n")

	for _, message := range messages {
		content := strings.TrimSpace(message.Content)
		if content == "" && len(message.ToolCalls) == 0 && message.ToolResult == nil {
			continue
		}

		builder.WriteString("\n[")
		builder.WriteString(strings.ToUpper(string(message.Role)))
		builder.WriteString("]\n")

		if content != "" {
			builder.WriteString(content)
			builder.WriteString("\n")
		}
		for _, call := range message.ToolCalls {
			builder.WriteString("Tool call ")
			builder.WriteString(call.Name)
			builder.WriteString(": ")
			builder.WriteString(call.Input)
			builder.WriteString("\n")
		}
		if message.ToolResult != nil && strings.TrimSpace(message.ToolResult.Output) != "" {
			builder.WriteString("Tool result: ")
			builder.WriteString(strings.TrimSpace(message.ToolResult.Output))
			builder.WriteString("\n")
		}
	}

	builder.WriteString("\nSummary:\n")
	return builder.String()
}

func persistSessionState(store *session.Store, params sessionStateParams) error {
	if err := store.SaveTranscript(params.SessionID, params.Messages); err != nil {
		return err
	}

	totalCost := 0.0
	if params.Tracker != nil {
		totalCost = params.Tracker.Snapshot().TotalCostUSD
	}

	title := ""
	if existing, err := store.LoadMetadata(params.SessionID); err == nil {
		title = existing.Title
	}

	return store.SaveMetadata(session.Metadata{
		SessionID:    params.SessionID,
		CreatedAt:    params.CreatedAt,
		UpdatedAt:    time.Now(),
		Mode:         string(params.Mode),
		Model:        params.Model,
		CWD:          params.CWD,
		Branch:       params.Branch,
		TotalCostUSD: totalCost,
		Title:        title,
	})
}

func newSessionID() (string, error) {
	buf := make([]byte, 16)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session id: %w", err)
	}
	encoded := hex.EncodeToString(buf)
	return fmt.Sprintf("%s-%s-%s-%s-%s", encoded[0:8], encoded[8:12], encoded[12:16], encoded[16:20], encoded[20:32]), nil
}
