package engine

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"github.com/channyeintun/chan/internal/agent"
	"github.com/channyeintun/chan/internal/api"
	artifactspkg "github.com/channyeintun/chan/internal/artifacts"
	"github.com/channyeintun/chan/internal/compact"
	"github.com/channyeintun/chan/internal/config"
	"github.com/channyeintun/chan/internal/ipc"
)

const (
	sessionMemoryArtifactSlot            = "active"
	sessionMemoryArtifactTitle           = "Session Memory"
	sessionMemoryArtifactSource          = "session-memory"
	sessionMemoryMinMessages             = 6
	sessionMemoryMinInitTokens           = 10000
	sessionMemoryMinUpdateTokens         = 5000
	sessionMemoryToolCallsBetweenUpdates = 3
	sessionMemoryMaxSectionItems         = 5
	sessionMemoryMaxSnippetLen           = 240
	sessionMemoryMaxFileCount            = 8
	sessionMemoryMaxSectionTokens        = 2000
	sessionMemoryMaxTotalTokens          = 12000
	sessionMemoryTranscriptDedupeWindow  = 8
)

type sessionMemoryDocument struct {
	SessionTitle          string
	CurrentState          string
	TaskSpecification     []string
	FilesAndFunctions     []string
	Workflow              []string
	ErrorsAndCorrections  []string
	CodebaseAndSystemDocs []string
	Learnings             []string
	KeyResults            []string
	Worklog               []string
}

func loadSessionMemorySnapshot(ctx context.Context, artifactManager *artifactspkg.Manager, sessionID string) (agent.SessionMemorySnapshot, error) {
	if !config.Load().EnableSessionMemory {
		return agent.SessionMemorySnapshot{}, nil
	}
	if artifactManager == nil || strings.TrimSpace(sessionID) == "" {
		return agent.SessionMemorySnapshot{}, nil
	}

	artifact, found, err := artifactManager.FindSessionArtifact(ctx, artifactspkg.KindSessionMemory, artifactspkg.ScopeSession, sessionID, sessionMemoryArtifactSlot)
	if err != nil || !found {
		return agent.SessionMemorySnapshot{}, err
	}

	loaded, content, err := artifactManager.LoadMarkdown(ctx, artifact.ID, 0)
	if err != nil {
		return agent.SessionMemorySnapshot{}, err
	}
	parsed := parseSessionMemoryMarkdown(content)

	return agent.SessionMemorySnapshot{
		ArtifactID:               loaded.ID,
		Title:                    firstNonEmptySnippet(metadataString(loaded.Metadata, "session_title"), parsed.SessionTitle, loaded.Title),
		Content:                  content,
		Version:                  loaded.Version,
		UpdatedAt:                loaded.UpdatedAt,
		SourceConversationTokens: metadataInt(loaded.Metadata, "source_conversation_tokens"),
		SourceToolCallCount:      metadataInt(loaded.Metadata, "source_tool_call_count"),
	}, nil
}

func maybeRefreshSessionMemory(ctx context.Context, bridge *ipc.Bridge, artifactManager *artifactspkg.Manager, sessionID string, turnID int, messages []api.Message, fromIndex int) error {
	if !config.Load().EnableSessionMemory {
		return nil
	}
	if artifactManager == nil || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	if !shouldRefreshSessionMemory(ctx, artifactManager, sessionID, turnID, messages, fromIndex) {
		return nil
	}

	previous, err := loadSessionMemorySnapshot(ctx, artifactManager, sessionID)
	if err != nil {
		previous = agent.SessionMemorySnapshot{}
	}

	content := buildSessionMemoryMarkdown(previous, messages, fromIndex)
	if strings.TrimSpace(content) == "" {
		return nil
	}
	parsed := parseSessionMemoryMarkdown(content)

	artifact, _, created, err := artifactManager.UpsertSessionMarkdown(ctx, artifactspkg.MarkdownRequest{
		Kind:    artifactspkg.KindSessionMemory,
		Scope:   artifactspkg.ScopeSession,
		Title:   sessionMemoryArtifactTitle,
		Source:  sessionMemoryArtifactSource,
		Content: content,
		Metadata: map[string]any{
			"status":                     "active",
			"session_title":              parsed.SessionTitle,
			"updated_turn":               turnID,
			"updated_message_count":      len(messages),
			"source_conversation_tokens": compact.EstimateConversationTokens(messages),
			"source_tool_call_count":     totalToolCallCount(messages),
		},
	}, sessionID, sessionMemoryArtifactSlot)
	if err != nil {
		if bridge == nil {
			return nil
		}
		return bridge.Emit(ipc.EventNotice, ipc.NoticePayload{Message: "session memory update skipped: " + err.Error()})
	}

	if bridge == nil {
		return nil
	}
	if created {
		if err := emitArtifactCreated(bridge, artifact); err != nil {
			return err
		}
	}
	return emitArtifactUpdated(bridge, artifact, content)
}

func shouldRefreshSessionMemory(ctx context.Context, artifactManager *artifactspkg.Manager, sessionID string, turnID int, messages []api.Message, fromIndex int) bool {
	if len(messages) < sessionMemoryMinMessages {
		return false
	}
	hasCompactionSummary := turnHasCompactionSummary(messages, fromIndex)
	hasToolActivity := turnHasToolActivity(messages, fromIndex)
	currentTokens := compact.EstimateConversationTokens(messages)
	currentToolCalls := totalToolCallCount(messages)
	current, err := loadSessionMemorySnapshot(ctx, artifactManager, sessionID)
	if err == nil && current.HasContent() {
		if hasCompactionSummary {
			return true
		}
		tokenDelta := currentTokens - current.SourceConversationTokens
		toolCallDelta := currentToolCalls - current.SourceToolCallCount
		if tokenDelta < 0 || toolCallDelta < 0 {
			return hasToolActivity
		}
		if tokenDelta >= sessionMemoryMinUpdateTokens {
			return true
		}
		if hasToolActivity && toolCallDelta >= sessionMemoryToolCallsBetweenUpdates {
			return true
		}
		return false
	}
	if hasCompactionSummary {
		return true
	}
	return hasToolActivity && currentTokens >= sessionMemoryMinInitTokens
}

func turnHasToolActivity(messages []api.Message, fromIndex int) bool {
	if fromIndex < 0 {
		fromIndex = 0
	}
	for index := fromIndex; index < len(messages); index++ {
		message := messages[index]
		if len(message.ToolCalls) > 0 || message.ToolResult != nil {
			return true
		}
	}
	return false
}

func turnHasCompactionSummary(messages []api.Message, fromIndex int) bool {
	if fromIndex < 0 {
		fromIndex = 0
	}
	for index := fromIndex; index < len(messages); index++ {
		if compact.IsSummaryMessage(messages[index]) {
			return true
		}
	}
	return false
}

func buildSessionMemoryMarkdown(previous agent.SessionMemorySnapshot, messages []api.Message, fromIndex int) string {
	durableMemoryCorpus := buildDurableMemoryCorpus()
	transcriptCorpus := buildRecentTranscriptCorpus(messages, fromIndex)
	current := sessionMemoryDocument{
		SessionTitle:          deriveSessionTitle(messages, previous),
		CurrentState:          firstNonEmptySnippet(recentAssistantSnippets(messages, fromIndex, 3)...),
		TaskSpecification:     recentUserBulletSnippets(messages, 4),
		FilesAndFunctions:     recentImportantFiles(messages, fromIndex),
		Workflow:              recentWorkflowSnippets(messages, fromIndex),
		ErrorsAndCorrections:  recentErrorSnippets(messages, fromIndex),
		CodebaseAndSystemDocs: recentDecisionSnippets(messages, fromIndex),
		Learnings:             recentLearningSnippets(messages, fromIndex),
		KeyResults:            recentAssistantBulletSnippets(messages, fromIndex, 2),
		Worklog:               recentWorklogEntries(messages, fromIndex),
	}
	merged := mergeSessionMemoryDocuments(parseSessionMemoryMarkdown(previous.Content), current)
	merged = filterSessionMemoryDocument(merged, durableMemoryCorpus, transcriptCorpus)

	var b strings.Builder
	b.WriteString("# Session Memory\n\n")
	b.WriteString("## Session Title\n\n")
	b.WriteString(fallbackText(merged.SessionTitle, "Active coding session"))
	b.WriteString("\n\n## Current State\n\n")
	b.WriteString(fallbackText(merged.CurrentState, "Implementation work is active."))
	b.WriteString("\n\n## Task Specification\n\n")
	b.WriteString(listOrFallback(limitBulletList(merged.TaskSpecification, sessionMemoryMaxSectionTokens), "- No task specification captured yet."))
	b.WriteString("\n\n## Files And Functions\n\n")
	b.WriteString(listOrFallback(limitBulletList(merged.FilesAndFunctions, sessionMemoryMaxSectionTokens), "- No file focus captured yet."))
	b.WriteString("\n\n## Workflow\n\n")
	b.WriteString(listOrFallback(limitBulletList(merged.Workflow, sessionMemoryMaxSectionTokens), "- No workflow captured yet."))
	b.WriteString("\n\n## Errors & Corrections\n\n")
	b.WriteString(listOrFallback(limitBulletList(merged.ErrorsAndCorrections, sessionMemoryMaxSectionTokens), "- No recent errors captured."))
	b.WriteString("\n\n## Codebase And System Documentation\n\n")
	b.WriteString(listOrFallback(limitBulletList(merged.CodebaseAndSystemDocs, sessionMemoryMaxSectionTokens), "- No codebase notes captured yet."))
	b.WriteString("\n\n## Learnings\n\n")
	b.WriteString(listOrFallback(limitBulletList(merged.Learnings, sessionMemoryMaxSectionTokens), "- No learnings captured yet."))
	b.WriteString("\n\n## Key Results\n\n")
	b.WriteString(listOrFallback(limitBulletList(merged.KeyResults, sessionMemoryMaxSectionTokens), "- No key results captured yet."))
	b.WriteString("\n\n## Worklog\n\n")
	b.WriteString(listOrFallback(limitBulletList(merged.Worklog, sessionMemoryMaxSectionTokens), "- No worklog entries captured yet."))
	return limitRenderedSessionMemory(strings.TrimSpace(b.String()) + "\n")
}

func parseSessionMemoryMarkdown(content string) sessionMemoryDocument {
	content = strings.TrimSpace(content)
	if content == "" {
		return sessionMemoryDocument{}
	}
	sections := splitMarkdownSections(content)
	return sessionMemoryDocument{
		SessionTitle:          firstNonEmptySnippet(strings.TrimSpace(sections["Session Title"]), strings.TrimSpace(sections["Current Objective"])),
		CurrentState:          firstNonEmptySnippet(strings.TrimSpace(sections["Current State"]), strings.TrimSpace(sections["Current state"])),
		TaskSpecification:     parseBulletList(firstNonEmptySnippet(sections["Task Specification"], sections["Current Objective"])),
		FilesAndFunctions:     parseBulletList(firstNonEmptySnippet(sections["Files And Functions"], sections["Important Files"])),
		Workflow:              parseBulletList(sections["Workflow"]),
		ErrorsAndCorrections:  parseBulletList(firstNonEmptySnippet(sections["Errors & Corrections"], sections["Recent Errors And Corrections"])),
		CodebaseAndSystemDocs: parseBulletList(firstNonEmptySnippet(sections["Codebase And System Documentation"], sections["Recent Decisions And Findings"])),
		Learnings:             parseBulletList(sections["Learnings"]),
		KeyResults:            parseBulletList(firstNonEmptySnippet(sections["Key Results"], sections["Next Steps"])),
		Worklog:               parseBulletList(sections["Worklog"]),
	}
}

func mergeSessionMemoryDocuments(previous, current sessionMemoryDocument) sessionMemoryDocument {
	merged := sessionMemoryDocument{
		SessionTitle:          firstNonEmptySnippet(current.SessionTitle, previous.SessionTitle),
		CurrentState:          firstNonEmptySnippet(current.CurrentState, previous.CurrentState),
		TaskSpecification:     mergeBulletLists(current.TaskSpecification, previous.TaskSpecification, sessionMemoryMaxSectionItems),
		FilesAndFunctions:     mergeBulletLists(current.FilesAndFunctions, previous.FilesAndFunctions, sessionMemoryMaxFileCount),
		Workflow:              mergeBulletLists(current.Workflow, previous.Workflow, sessionMemoryMaxSectionItems),
		ErrorsAndCorrections:  mergeBulletLists(current.ErrorsAndCorrections, previous.ErrorsAndCorrections, sessionMemoryMaxSectionItems),
		CodebaseAndSystemDocs: mergeBulletLists(current.CodebaseAndSystemDocs, previous.CodebaseAndSystemDocs, sessionMemoryMaxSectionItems),
		Learnings:             mergeBulletLists(current.Learnings, previous.Learnings, sessionMemoryMaxSectionItems),
		KeyResults:            mergeBulletLists(current.KeyResults, previous.KeyResults, sessionMemoryMaxSectionItems),
		Worklog:               mergeBulletLists(current.Worklog, previous.Worklog, sessionMemoryMaxSectionItems*2),
	}
	return merged
}

func filterSessionMemoryDocument(doc sessionMemoryDocument, durableMemoryCorpus string, transcriptCorpus string) sessionMemoryDocument {
	doc.TaskSpecification = filterRedundantBullets(doc.TaskSpecification, durableMemoryCorpus, transcriptCorpus)
	doc.Workflow = filterRedundantBullets(doc.Workflow, transcriptCorpus)
	doc.CodebaseAndSystemDocs = filterRedundantBullets(doc.CodebaseAndSystemDocs, durableMemoryCorpus, transcriptCorpus)
	doc.Learnings = filterRedundantBullets(doc.Learnings, durableMemoryCorpus, transcriptCorpus)
	doc.KeyResults = filterRedundantBullets(doc.KeyResults, transcriptCorpus)
	return doc
}

func splitMarkdownSections(content string) map[string]string {
	sections := map[string]string{}
	var current string
	var buffer strings.Builder
	flush := func() {
		if current == "" {
			return
		}
		sections[current] = strings.TrimSpace(buffer.String())
		buffer.Reset()
	}
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "## ") {
			flush()
			current = strings.TrimSpace(strings.TrimPrefix(trimmed, "## "))
			continue
		}
		if current == "" {
			continue
		}
		buffer.WriteString(line)
		buffer.WriteString("\n")
	}
	flush()
	return sections
}

func parseBulletList(section string) []string {
	lines := strings.Split(strings.TrimSpace(section), "\n")
	items := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "- ") {
			items = append(items, trimmed)
			continue
		}
		items = append(items, "- "+trimmed)
	}
	return items
}

func firstListEntry(section string) string {
	items := parseBulletList(section)
	if len(items) == 0 {
		return ""
	}
	return items[0]
}

func mergeBulletLists(primary, fallback []string, limit int) []string {
	merged := make([]string, 0, limit)
	seen := make(map[string]struct{}, limit)
	appendItem := func(item string) {
		item = strings.TrimSpace(item)
		if item == "" {
			return
		}
		if !strings.HasPrefix(item, "- ") {
			item = "- " + item
		}
		if _, ok := seen[item]; ok {
			return
		}
		seen[item] = struct{}{}
		merged = append(merged, item)
	}
	for _, item := range primary {
		if limit > 0 && len(merged) >= limit {
			return merged
		}
		appendItem(item)
	}
	for _, item := range fallback {
		if limit > 0 && len(merged) >= limit {
			break
		}
		appendItem(item)
	}
	return merged
}

func recentUserSnippets(messages []api.Message, limit int) []string {
	results := make([]string, 0, limit)
	for index := len(messages) - 1; index >= 0 && len(results) < limit; index-- {
		message := messages[index]
		if message.Role != api.RoleUser {
			continue
		}
		snippet := normalizeSnippet(message.Content)
		if snippet != "" {
			results = append(results, snippet)
		}
	}
	return results
}

func recentAssistantSnippets(messages []api.Message, fromIndex int, limit int) []string {
	results := make([]string, 0, limit)
	if fromIndex < 0 {
		fromIndex = 0
	}
	for index := len(messages) - 1; index >= fromIndex && len(results) < limit; index-- {
		message := messages[index]
		if message.Role != api.RoleAssistant {
			continue
		}
		snippet := normalizeSnippet(message.Content)
		if snippet != "" {
			results = append(results, snippet)
		}
	}
	return results
}

func recentDecisionSnippets(messages []api.Message, fromIndex int) []string {
	items := recentAssistantSnippets(messages, fromIndex, sessionMemoryMaxSectionItems)
	for index := range items {
		items[index] = "- " + items[index]
	}
	return items
}

func recentUserBulletSnippets(messages []api.Message, limit int) []string {
	items := recentUserSnippets(messages, limit)
	for index := range items {
		items[index] = "- " + items[index]
	}
	return items
}

func recentAssistantBulletSnippets(messages []api.Message, fromIndex int, limit int) []string {
	items := recentAssistantSnippets(messages, fromIndex, limit)
	for index := range items {
		items[index] = "- " + items[index]
	}
	return items
}

func recentErrorSnippets(messages []api.Message, fromIndex int) []string {
	if fromIndex < 0 {
		fromIndex = 0
	}
	items := make([]string, 0, sessionMemoryMaxSectionItems)
	for index := len(messages) - 1; index >= fromIndex && len(items) < sessionMemoryMaxSectionItems; index-- {
		message := messages[index]
		if message.ToolResult == nil || !message.ToolResult.IsError {
			continue
		}
		snippet := normalizeSnippet(message.ToolResult.Output)
		if snippet != "" {
			items = append(items, "- "+snippet)
		}
	}
	return items
}

func recentImportantFiles(messages []api.Message, fromIndex int) []string {
	if fromIndex < 0 {
		fromIndex = 0
	}
	seen := make(map[string]struct{}, sessionMemoryMaxFileCount)
	files := make([]string, 0, sessionMemoryMaxFileCount)
	for index := len(messages) - 1; index >= fromIndex && len(files) < sessionMemoryMaxFileCount; index-- {
		message := messages[index]
		if message.ToolResult != nil {
			path := strings.TrimSpace(message.ToolResult.FilePath)
			if path != "" {
				if _, ok := seen[path]; !ok {
					seen[path] = struct{}{}
					files = append(files, "- "+path)
				}
			}
		}
		for _, call := range message.ToolCalls {
			for _, path := range extractPathsFromToolInput(call.Input) {
				if _, ok := seen[path]; ok {
					continue
				}
				seen[path] = struct{}{}
				files = append(files, "- "+path)
				if len(files) >= sessionMemoryMaxFileCount {
					break
				}
			}
		}
	}
	sort.Strings(files)
	return files
}

func recentWorkflowSnippets(messages []api.Message, fromIndex int) []string {
	if fromIndex < 0 {
		fromIndex = 0
	}
	items := make([]string, 0, sessionMemoryMaxSectionItems)
	for index := len(messages) - 1; index >= fromIndex && len(items) < sessionMemoryMaxSectionItems; index-- {
		message := messages[index]
		for _, call := range message.ToolCalls {
			if !looksLikeWorkflowTool(call.Name) {
				continue
			}
			snippet := normalizeSnippet(call.Name + ": " + call.Input)
			if snippet != "" {
				items = append(items, "- "+snippet)
			}
		}
	}
	return items
}

func recentLearningSnippets(messages []api.Message, fromIndex int) []string {
	items := recentDecisionSnippets(messages, fromIndex)
	if len(items) > sessionMemoryMaxSectionItems {
		return items[:sessionMemoryMaxSectionItems]
	}
	return items
}

func recentWorklogEntries(messages []api.Message, fromIndex int) []string {
	if fromIndex < 0 {
		fromIndex = 0
	}
	items := make([]string, 0, sessionMemoryMaxSectionItems*2)
	for index := fromIndex; index < len(messages) && len(items) < sessionMemoryMaxSectionItems*2; index++ {
		message := messages[index]
		summary := summarizeMessageForWorklog(message)
		if summary == "" {
			continue
		}
		items = append(items, "- "+summary)
	}
	return items
}

func summarizeMessageForWorklog(message api.Message) string {
	switch message.Role {
	case api.RoleUser:
		return "User: " + normalizeSnippet(message.Content)
	case api.RoleAssistant:
		if len(message.ToolCalls) > 0 {
			return "Assistant issued tools: " + normalizeSnippet(joinToolNames(message.ToolCalls))
		}
		return "Assistant: " + normalizeSnippet(message.Content)
	case api.RoleTool:
		if message.ToolResult != nil {
			return "Tool result: " + normalizeSnippet(message.ToolResult.Output)
		}
	}
	return ""
}

func joinToolNames(calls []api.ToolCall) string {
	names := make([]string, 0, len(calls))
	for _, call := range calls {
		if name := strings.TrimSpace(call.Name); name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

func totalToolCallCount(messages []api.Message) int {
	total := 0
	for _, message := range messages {
		total += len(message.ToolCalls)
	}
	return total
}

func deriveSessionTitle(messages []api.Message, previous agent.SessionMemorySnapshot) string {
	previousDoc := parseSessionMemoryMarkdown(previous.Content)
	if title := strings.TrimSpace(previousDoc.SessionTitle); title != "" && !strings.EqualFold(title, sessionMemoryArtifactTitle) {
		return title
	}
	if title := strings.TrimSpace(previous.Title); title != "" && !strings.EqualFold(title, sessionMemoryArtifactTitle) {
		return title
	}
	objective := firstNonEmptySnippet(recentUserSnippets(messages, 1)...)
	if objective == "" {
		return sessionMemoryArtifactTitle
	}
	if len(objective) > 72 {
		return strings.TrimSpace(objective[:72])
	}
	return objective
}

func buildDurableMemoryCorpus() string {
	files := agent.LoadMemoryFiles()
	parts := make([]string, 0, len(files))
	for _, file := range files {
		content := normalizeMemoryText(file.Content)
		if content != "" {
			parts = append(parts, content)
		}
	}
	return strings.Join(parts, "\n")
}

func buildRecentTranscriptCorpus(messages []api.Message, fromIndex int) string {
	if fromIndex < 0 {
		fromIndex = 0
	}
	start := fromIndex
	windowStart := len(messages) - sessionMemoryTranscriptDedupeWindow
	if windowStart > start {
		start = windowStart
	}
	if start < 0 {
		start = 0
	}
	parts := make([]string, 0, (len(messages)-start)*2)
	for index := start; index < len(messages); index++ {
		message := messages[index]
		if content := normalizeMemoryText(message.Content); content != "" {
			parts = append(parts, content)
		}
		for _, call := range message.ToolCalls {
			if input := normalizeMemoryText(call.Input); input != "" {
				parts = append(parts, input)
			}
		}
		if message.ToolResult != nil {
			if output := normalizeMemoryText(message.ToolResult.Output); output != "" {
				parts = append(parts, output)
			}
		}
	}
	return strings.Join(parts, "\n")
}

func filterRedundantBullets(items []string, corpora ...string) []string {
	if len(corpora) == 0 {
		return items
	}
	filtered := make([]string, 0, len(items))
	for _, item := range items {
		normalized := normalizeMemoryText(item)
		if len(normalized) >= 24 && redundantInCorpora(normalized, corpora...) {
			continue
		}
		filtered = append(filtered, item)
	}
	return filtered
}

func redundantInCorpora(value string, corpora ...string) bool {
	for _, corpus := range corpora {
		if strings.TrimSpace(corpus) == "" {
			continue
		}
		if strings.Contains(corpus, value) {
			return true
		}
	}
	return false
}

func normalizeMemoryText(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	return strings.Join(strings.Fields(value), " ")
}

func metadataInt(metadata map[string]any, key string) int {
	if metadata == nil {
		return 0
	}
	switch value := metadata[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	default:
		return 0
	}
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	if value, ok := metadata[key].(string); ok {
		return strings.TrimSpace(value)
	}
	return ""
}

func looksLikeWorkflowTool(name string) bool {
	name = strings.TrimSpace(strings.ToLower(name))
	switch name {
	case "bash", "run_in_terminal", "command_status", "grep_search", "file_search":
		return true
	default:
		return false
	}
}

func limitBulletList(items []string, maxTokens int) []string {
	if maxTokens <= 0 {
		return items
	}
	limited := make([]string, 0, len(items))
	used := 0
	for _, item := range items {
		tokens := compact.EstimateTokens(item)
		if used > 0 && used+tokens > maxTokens {
			break
		}
		limited = append(limited, item)
		used += tokens
	}
	return limited
}

func limitRenderedSessionMemory(content string) string {
	if compact.EstimateTokens(content) <= sessionMemoryMaxTotalTokens {
		return content
	}
	maxChars := sessionMemoryMaxTotalTokens * 4
	if len(content) <= maxChars {
		return content
	}
	return strings.TrimSpace(content[:maxChars]) + "\n"
}

func extractPathsFromToolInput(raw string) []string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil
	}
	var decoded map[string]any
	if err := json.Unmarshal([]byte(trimmed), &decoded); err != nil {
		return nil
	}
	keys := []string{"filePath", "path", "dirPath", "includePattern"}
	paths := make([]string, 0, len(keys))
	for _, key := range keys {
		value, ok := decoded[key].(string)
		if !ok {
			continue
		}
		value = strings.TrimSpace(value)
		if value == "" || strings.ContainsAny(value, "*?[]") {
			continue
		}
		paths = append(paths, value)
	}
	return paths
}

func bulletOrFallback(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "- " + fallback
	}
	return "- " + value
}

func fallbackText(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func listOrFallback(items []string, fallback string) string {
	if len(items) == 0 {
		return fallback
	}
	return strings.Join(items, "\n")
}

func firstNonEmptySnippet(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func normalizeSnippet(value string) string {
	value = strings.Join(strings.Fields(strings.TrimSpace(value)), " ")
	if value == "" {
		return ""
	}
	if len(value) > sessionMemoryMaxSnippetLen {
		return strings.TrimSpace(value[:sessionMemoryMaxSnippetLen]) + "..."
	}
	return value
}
