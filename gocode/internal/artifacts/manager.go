package artifacts

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// MarkdownMimeType is the canonical MIME type for user-facing artifacts.
const MarkdownMimeType = "text/markdown; charset=utf-8"

// Manager coordinates markdown-backed artifact lifecycle operations.
type Manager struct {
	store Service
}

// MarkdownRequest describes a markdown artifact save or update.
type MarkdownRequest struct {
	ID       string
	Kind     Kind
	Scope    Scope
	Title    string
	Source   string
	Content  string
	Metadata map[string]any
}

// SessionArtifact includes the current artifact metadata plus its markdown body.
type SessionArtifact struct {
	Artifact Artifact
	Content  string
}

// ArtifactLoadWarning reports unreadable artifacts that were skipped while
// still returning the readable subset.
type ArtifactLoadWarning struct {
	Operation string
	Failures  []string
}

func (w *ArtifactLoadWarning) Error() string {
	if w == nil || len(w.Failures) == 0 {
		return ""
	}
	return fmt.Sprintf("%s skipped unreadable session artifacts: %s", strings.TrimSpace(w.Operation), strings.Join(w.Failures, "; "))
}

// NewManager constructs a markdown artifact manager.
func NewManager(store Service) *Manager {
	return &Manager{store: store}
}

// SaveMarkdown creates or updates a markdown artifact.
func (m *Manager) SaveMarkdown(ctx context.Context, req MarkdownRequest) (Artifact, ArtifactVersion, bool, error) {
	if m == nil || m.store == nil {
		return Artifact{}, ArtifactVersion{}, false, fmt.Errorf("artifact manager is not configured")
	}

	created := strings.TrimSpace(req.ID) == ""
	version, err := m.store.Save(ctx, SaveRequest{
		ID:       strings.TrimSpace(req.ID),
		Kind:     req.Kind,
		Scope:    req.Scope,
		Title:    req.Title,
		MimeType: MarkdownMimeType,
		Source:   req.Source,
		Content:  []byte(req.Content),
		Metadata: normalizeOwnershipMetadata(req.Scope, req.Metadata, "", ""),
	})
	if err != nil {
		return Artifact{}, ArtifactVersion{}, false, err
	}

	art, err := m.store.Load(ctx, LoadRequest{ID: version.ArtifactID})
	if err != nil {
		return Artifact{}, ArtifactVersion{}, false, err
	}

	return art, version, created && version.Version == 1, nil
}

// LoadMarkdown retrieves the markdown content for the requested artifact version.
func (m *Manager) LoadMarkdown(ctx context.Context, id string, version int) (Artifact, string, error) {
	if m == nil || m.store == nil {
		return Artifact{}, "", fmt.Errorf("artifact manager is not configured")
	}

	art, err := m.store.Load(ctx, LoadRequest{ID: id, Version: version})
	if err != nil {
		return Artifact{}, "", err
	}

	data, err := os.ReadFile(art.ContentPath)
	if err != nil {
		return Artifact{}, "", fmt.Errorf("read artifact content: %w", err)
	}

	return art, string(data), nil
}

// LoadSessionArtifacts returns the latest markdown-backed artifacts for a session.
func (m *Manager) LoadSessionArtifacts(ctx context.Context, sessionID string) ([]SessionArtifact, error) {
	if m == nil || m.store == nil {
		return nil, fmt.Errorf("artifact manager is not configured")
	}

	refs, err := m.store.List(ctx, ListRequest{Scope: ScopeSession})
	if err != nil {
		return nil, err
	}

	artifacts := make([]SessionArtifact, 0, len(refs))
	var failures []string
	for _, ref := range refs {
		art, content, err := m.LoadMarkdown(ctx, ref.ID, 0)
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", ref.ID, err))
			continue
		}
		if sessionOwnerID(art.Metadata) != sessionID {
			continue
		}
		artifacts = append(artifacts, SessionArtifact{Artifact: art, Content: content})
	}
	if len(failures) > 0 {
		return artifacts, &ArtifactLoadWarning{Operation: "loading session artifacts", Failures: failures}
	}

	return artifacts, nil
}

// FindSessionArtifact finds the latest session-scoped artifact matching the provided slot.
func (m *Manager) FindSessionArtifact(ctx context.Context, kind Kind, scope Scope, sessionID string, slot string) (Artifact, bool, error) {
	if m == nil || m.store == nil {
		return Artifact{}, false, fmt.Errorf("artifact manager is not configured")
	}

	refs, err := m.store.List(ctx, ListRequest{Kind: kind, Scope: scope})
	if err != nil {
		return Artifact{}, false, err
	}

	var failures []string
	for _, ref := range refs {
		art, err := m.store.Load(ctx, LoadRequest{ID: ref.ID})
		if err != nil {
			failures = append(failures, fmt.Sprintf("%s: %v", ref.ID, err))
			continue
		}
		if sessionOwnerID(art.Metadata) != sessionID {
			continue
		}
		if strings.TrimSpace(slot) != "" && metadataString(art.Metadata, MetadataSlot) != slot {
			continue
		}
		return art, true, nil
	}
	if len(failures) > 0 {
		return Artifact{}, false, &ArtifactLoadWarning{Operation: "finding session artifact", Failures: failures}
	}

	return Artifact{}, false, nil
}

// UpsertSessionMarkdown updates an existing session artifact when present, or creates one.
func (m *Manager) UpsertSessionMarkdown(ctx context.Context, req MarkdownRequest, sessionID string, slot string) (Artifact, ArtifactVersion, bool, error) {
	existing, found, err := m.FindSessionArtifact(ctx, req.Kind, req.Scope, sessionID, slot)
	if err != nil {
		return Artifact{}, ArtifactVersion{}, false, err
	}
	if found {
		req.ID = existing.ID
	}

	metadata := cloneMetadata(req.Metadata)
	req.Metadata = normalizeOwnershipMetadata(req.Scope, metadata, sessionID, slot)

	return m.SaveMarkdown(ctx, req)
}

// DraftImplementationPlanMarkdown renders the initial in-progress plan artifact.
func DraftImplementationPlanMarkdown(userRequest string) string {
	return RenderImplementationPlanMarkdown(userRequest, "_Planning in progress._")
}

// RenderImplementationPlanMarkdown wraps a plan response with the captured request.
func RenderImplementationPlanMarkdown(userRequest string, plan string) string {
	request := strings.TrimSpace(userRequest)
	if request == "" {
		request = "No request captured."
	}

	plan = strings.TrimSpace(plan)
	if plan == "" {
		plan = "_Planning in progress._"
	}

	return strings.TrimSpace(fmt.Sprintf(`# Implementation Plan

## Request

%s

---

%s
`, blockquote(request), plan)) + "\n"
}

// DraftTaskListMarkdown renders the initial in-progress task-list artifact.
func DraftTaskListMarkdown(userRequest string) string {
	return RenderTaskListMarkdown(userRequest, strings.TrimSpace(`## Progress

- [ ] Inspect the relevant code and constraints
- [ ] Implement or revise the requested changes
- [ ] Verify the result and summarize the outcome`))
}

// RenderTaskListMarkdown wraps a task list with the captured request.
func RenderTaskListMarkdown(userRequest string, taskList string) string {
	request := strings.TrimSpace(userRequest)
	if request == "" {
		request = "No request captured."
	}

	taskList = strings.TrimSpace(taskList)
	if taskList == "" {
		taskList = "- [ ] Add task details."
	}

	return strings.TrimSpace(fmt.Sprintf(`# Task List

## Request

%s

---

%s
`, blockquote(request), taskList)) + "\n"
}

// RenderWalkthroughMarkdown wraps a walkthrough summary with the captured request.
func RenderWalkthroughMarkdown(userRequest string, summary string) string {
	request := strings.TrimSpace(userRequest)
	if request == "" {
		request = "No request captured."
	}

	summary = strings.TrimSpace(summary)
	if summary == "" {
		summary = "No walkthrough summary was provided."
	}

	return strings.TrimSpace(fmt.Sprintf(`# Walkthrough

## Request

%s

---

%s
`, blockquote(request), summary)) + "\n"
}

// RenderSearchReportMarkdown formats web search results as a markdown artifact.
func RenderSearchReportMarkdown(query string, results string) string {
	q := strings.TrimSpace(query)
	if q == "" {
		q = "web search"
	}
	return strings.TrimSpace(fmt.Sprintf("# Search Report\n\n**Query:** %s\n\n---\n\n%s\n", q, strings.TrimSpace(results))) + "\n"
}

// RenderDiffPreviewMarkdown formats a git diff output as a markdown artifact.
func RenderDiffPreviewMarkdown(description string, diff string) string {
	desc := strings.TrimSpace(description)
	if desc == "" {
		desc = "git diff"
	}
	return strings.TrimSpace(fmt.Sprintf("# Diff Preview\n\n**%s**\n\n~~~diff\n%s\n~~~\n", desc, strings.TrimSpace(diff))) + "\n"
}

// RenderToolLogMarkdown formats an oversized tool result as a markdown artifact.
func RenderToolLogMarkdown(sessionID string, toolName string, toolCallID string, rawInput string, output string) string {
	var builder strings.Builder
	builder.WriteString("# Tool Log\n\n")
	builder.WriteString("## Details\n\n")
	builder.WriteString("- Session ID: " + sessionID + "\n")
	builder.WriteString("- Tool: " + toolName + "\n")
	builder.WriteString("- Tool Call ID: " + toolCallID + "\n\n")

	if strings.TrimSpace(rawInput) != "" {
		builder.WriteString("## Input\n\n")
		builder.WriteString("~~~json\n")
		builder.WriteString(strings.TrimSpace(rawInput))
		if !strings.HasSuffix(rawInput, "\n") {
			builder.WriteString("\n")
		}
		builder.WriteString("~~~\n\n")
	}

	builder.WriteString("## Output\n\n")
	builder.WriteString("~~~text\n")
	builder.WriteString(output)
	if !strings.HasSuffix(output, "\n") {
		builder.WriteString("\n")
	}
	builder.WriteString("~~~\n")

	return builder.String()
}

func metadataString(metadata map[string]any, key string) string {
	if metadata == nil {
		return ""
	}
	value, ok := metadata[key]
	if !ok {
		return ""
	}
	stringValue, ok := value.(string)
	if !ok {
		return ""
	}
	return stringValue
}

func blockquote(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		if strings.TrimSpace(line) == "" {
			lines[i] = ">"
			continue
		}
		lines[i] = "> " + line
	}
	return strings.Join(lines, "\n")
}
