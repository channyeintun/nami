package main

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/channyeintun/chan/internal/agent"
	"github.com/channyeintun/chan/internal/api"
	costpkg "github.com/channyeintun/chan/internal/cost"
	"github.com/channyeintun/chan/internal/ipc"
)

const (
	memoryRecallMaxCandidates = 32
	memoryRecallMaxSelections = 8
	memoryRecallMaxTokens     = 256
)

type memoryRecallSelector struct {
	bridge  *ipc.Bridge
	tracker *costpkg.Tracker
	client  api.LLMClient
}

type memoryRecallCandidate struct {
	ID       string
	FilePath string
	Line     string
	Title    string
	FileType string
	NotePath string
	Updated  time.Time
	Index    int
}

type memoryRecallResponse struct {
	SelectedIDs []string `json:"selected_ids"`
}

func (s memoryRecallSelector) Select(ctx context.Context, files []agent.MemoryFile, userPrompt string) ([]agent.MemoryRecallResult, error) {
	if s.client == nil || strings.TrimSpace(userPrompt) == "" {
		return nil, nil
	}

	candidates := buildMemoryRecallCandidates(files)
	if len(candidates) == 0 {
		return nil, nil
	}
	if len(candidates) <= memoryRecallMaxSelections {
		return buildMemoryRecallResults(candidates, "bounded candidate passthrough"), nil
	}

	raw, _, err := s.queryMemoryRecall(ctx, userPrompt, candidates)
	if err != nil {
		return nil, nil
	}

	selectedIDs := parseMemoryRecallResponse(raw)
	if len(selectedIDs) == 0 {
		return nil, nil
	}

	selectedSet := make(map[string]struct{}, len(selectedIDs))
	for _, id := range selectedIDs {
		selectedSet[id] = struct{}{}
	}

	selected := make([]memoryRecallCandidate, 0, len(selectedSet))
	for _, candidate := range candidates {
		if _, ok := selectedSet[candidate.ID]; ok {
			selected = append(selected, candidate)
		}
	}
	if len(selected) == 0 {
		return nil, nil
	}

	return buildMemoryRecallResults(selected, "model side-query"), nil
}

func (s memoryRecallSelector) queryMemoryRecall(ctx context.Context, userPrompt string, candidates []memoryRecallCandidate) (string, api.Usage, error) {
	request := api.ModelRequest{
		Messages: []api.Message{{
			Role:    api.RoleUser,
			Content: renderMemoryRecallPrompt(userPrompt, candidates),
		}},
		SystemPrompt: memoryRecallSystemPrompt,
		MaxTokens:    memoryRecallMaxTokens,
	}

	stream, err := s.client.Stream(ctx, request)
	if err != nil {
		return "", api.Usage{}, err
	}

	startedAt := time.Now()
	var usage api.Usage
	var builder strings.Builder

	for event, streamErr := range stream {
		if streamErr != nil {
			return "", api.Usage{}, streamErr
		}
		switch event.Type {
		case api.ModelEventToken:
			builder.WriteString(event.Text)
		case api.ModelEventUsage:
			if event.Usage != nil {
				usage = mergeUsage(usage, *event.Usage)
			}
		case api.ModelEventRateLimits:
			if s.bridge != nil && event.RateLimits != nil {
				_ = emitRateLimitUpdate(s.bridge, event.RateLimits)
			}
		}
	}

	if s.tracker != nil && (usage.InputTokens > 0 || usage.OutputTokens > 0 || usage.CacheReadTokens > 0 || usage.CacheCreationTokens > 0) {
		s.tracker.RecordMemoryRecallCall(
			s.client.ModelID(),
			usage.InputTokens,
			usage.OutputTokens,
			usage.CacheReadTokens,
			usage.CacheCreationTokens,
			time.Since(startedAt),
			costpkg.CalculateUSDCost(s.client.ModelID(), usage),
		)
		if s.bridge != nil {
			_ = emitCostUpdate(s.bridge, s.tracker)
		}
	}

	return builder.String(), usage, nil
}

func buildMemoryRecallCandidates(files []agent.MemoryFile) []memoryRecallCandidate {
	candidates := make([]memoryRecallCandidate, 0, memoryRecallMaxCandidates)
	for _, file := range files {
		if file.Type != "project-index" && file.Type != "user-index" {
			continue
		}
		entries := agent.ParseMemoryIndexEntries(file)
		for _, entry := range entries {
			line := strings.TrimSpace(entry.RawLine)
			if line == "" || strings.TrimSpace(entry.Issue) != "" {
				continue
			}
			candidates = append(candidates, memoryRecallCandidate{
				ID:       fmt.Sprintf("m%d", len(candidates)+1),
				FilePath: file.Path,
				Line:     line,
				Title:    entry.Title,
				FileType: memoryRecallFirstNonEmpty(entry.NoteType, file.Type),
				NotePath: entry.NotePath,
				Updated:  file.UpdatedAt,
				Index:    entry.Order,
			})
			if len(candidates) >= memoryRecallMaxCandidates {
				return candidates
			}
		}
	}
	return candidates
}

func buildMemoryRecallResults(candidates []memoryRecallCandidate, source string) []agent.MemoryRecallResult {
	if len(candidates) == 0 {
		return nil
	}

	sort.SliceStable(candidates, func(i, j int) bool {
		if candidates[i].FilePath != candidates[j].FilePath {
			return candidates[i].FilePath < candidates[j].FilePath
		}
		return candidates[i].Index < candidates[j].Index
	})

	byPath := make(map[string][]string)
	orderedPaths := make([]string, 0, len(candidates))
	seenPaths := make(map[string]struct{})
	for _, candidate := range candidates {
		if _, ok := seenPaths[candidate.FilePath]; !ok {
			seenPaths[candidate.FilePath] = struct{}{}
			orderedPaths = append(orderedPaths, candidate.FilePath)
		}
		byPath[candidate.FilePath] = append(byPath[candidate.FilePath], candidate.Line)
	}

	results := make([]agent.MemoryRecallResult, 0, len(orderedPaths))
	for _, path := range orderedPaths {
		results = append(results, agent.MemoryRecallResult{
			Path:   path,
			Lines:  byPath[path],
			Source: source,
		})
	}
	return results
}

func parseMemoryRecallResponse(raw string) []string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	start := strings.Index(raw, "{")
	end := strings.LastIndex(raw, "}")
	if start < 0 || end <= start {
		return nil
	}

	var payload memoryRecallResponse
	if err := json.Unmarshal([]byte(raw[start:end+1]), &payload); err != nil {
		return nil
	}
	if len(payload.SelectedIDs) > memoryRecallMaxSelections {
		payload.SelectedIDs = payload.SelectedIDs[:memoryRecallMaxSelections]
	}

	seen := make(map[string]struct{}, len(payload.SelectedIDs))
	result := make([]string, 0, len(payload.SelectedIDs))
	for _, id := range payload.SelectedIDs {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		result = append(result, id)
	}
	return result
}

func renderMemoryRecallPrompt(userPrompt string, candidates []memoryRecallCandidate) string {
	var b strings.Builder
	b.WriteString("Current user request:\n")
	b.WriteString(strings.TrimSpace(userPrompt))
	b.WriteString("\n\nCandidate durable memory index entries:\n")
	for _, candidate := range candidates {
		b.WriteString("- id: ")
		b.WriteString(candidate.ID)
		b.WriteString(" | type: ")
		b.WriteString(candidate.FileType)
		if strings.TrimSpace(candidate.Title) != "" {
			b.WriteString(" | title: ")
			b.WriteString(candidate.Title)
		}
		if !candidate.Updated.IsZero() {
			b.WriteString(" | updated: ")
			b.WriteString(candidate.Updated.UTC().Format("2006-01-02"))
		}
		b.WriteString(" | path: ")
		b.WriteString(candidate.FilePath)
		if strings.TrimSpace(candidate.NotePath) != "" {
			b.WriteString(" | note_path: ")
			b.WriteString(candidate.NotePath)
		}
		b.WriteString("\n  ")
		b.WriteString(candidate.Line)
		b.WriteString("\n")
	}
	return strings.TrimSpace(b.String())
}

func memoryRecallFirstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

const memoryRecallSystemPrompt = `Select the most relevant durable memory index entries for the current coding request.

Return ONLY valid JSON with this exact schema:
{"selected_ids":["m1","m2"]}

Rules:
- Select at most 8 ids.
- Prefer entries that directly affect implementation choices, workflow constraints, or repository-specific guidance.
- Prefer project-specific entries over generic user-level entries when both are relevant.
- Prefer newer entries when relevance is otherwise similar.
- Do not invent ids.
- If nothing is relevant, return {"selected_ids":[]}.`
