package tools

import (
	"context"
	"fmt"
	"strings"
	"sync"

	artifactspkg "github.com/channyeintun/gocode/internal/artifacts"
)

const (
	implementationPlanArtifactSlot   = "active"
	implementationPlanArtifactTitle  = "Implementation Plan"
	implementationPlanArtifactSource = "implementation-plan-tool"
	taskListArtifactSlot             = "active"
	taskListArtifactTitle            = "Task List"
	taskListArtifactSource           = "task-list-tool"
	walkthroughArtifactSlot          = "latest"
	walkthroughArtifactTitle         = "Walkthrough"
	walkthroughArtifactSource        = "walkthrough-tool"
)

type sessionArtifactRuntime struct {
	mu        sync.RWMutex
	sessionID string
	manager   *artifactspkg.Manager
}

var globalSessionArtifactRuntime sessionArtifactRuntime

// SetGlobalSessionArtifacts installs the active session artifact runtime.
func SetGlobalSessionArtifacts(sessionID string, manager *artifactspkg.Manager) {
	globalSessionArtifactRuntime.mu.Lock()
	defer globalSessionArtifactRuntime.mu.Unlock()
	globalSessionArtifactRuntime.sessionID = strings.TrimSpace(sessionID)
	globalSessionArtifactRuntime.manager = manager
}

func getSessionArtifactRuntime() (string, *artifactspkg.Manager, error) {
	globalSessionArtifactRuntime.mu.RLock()
	defer globalSessionArtifactRuntime.mu.RUnlock()
	if strings.TrimSpace(globalSessionArtifactRuntime.sessionID) == "" || globalSessionArtifactRuntime.manager == nil {
		return "", nil, fmt.Errorf("session artifacts are unavailable")
	}
	return globalSessionArtifactRuntime.sessionID, globalSessionArtifactRuntime.manager, nil
}

// SaveImplementationPlanTool creates or updates the active session implementation-plan artifact.
type SaveImplementationPlanTool struct{}

// NewSaveImplementationPlanTool constructs the implementation-plan artifact tool.
func NewSaveImplementationPlanTool() *SaveImplementationPlanTool {
	return &SaveImplementationPlanTool{}
}

func (t *SaveImplementationPlanTool) Name() string {
	return "save_implementation_plan"
}

func (t *SaveImplementationPlanTool) Description() string {
	return "Create or update the session implementation-plan artifact with the final markdown plan for the current task. After you call this, the runtime will present the plan to the user for review. If approved, the session automatically switches to fast mode for the next turn — do not instruct the user to switch modes manually. If the user requests revisions, you will receive feedback in the next turn; call this tool again with the updated plan rather than creating a new artifact."
}

func (t *SaveImplementationPlanTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "The final markdown implementation plan to persist for the current session.",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "Optional artifact title. Defaults to Implementation Plan.",
			},
		},
		"required": []string{"content"},
	}
}

func (t *SaveImplementationPlanTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *SaveImplementationPlanTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *SaveImplementationPlanTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	sessionID, manager, err := getSessionArtifactRuntime()
	if err != nil {
		return ToolOutput{}, err
	}

	content, ok := stringParam(input.Params, "content")
	if !ok || strings.TrimSpace(content) == "" {
		return ToolOutput{}, fmt.Errorf("save_implementation_plan requires content")
	}

	title, _ := stringParam(input.Params, "title")
	title = strings.TrimSpace(title)
	if title == "" {
		title = implementationPlanArtifactTitle
	}

	artifact, _, created, err := manager.UpsertSessionMarkdown(ctx, artifactspkg.MarkdownRequest{
		Kind:    artifactspkg.KindImplementationPlan,
		Scope:   artifactspkg.ScopeSession,
		Title:   title,
		Source:  implementationPlanArtifactSource,
		Content: content,
		Metadata: map[string]any{
			"mode":   "plan",
			"status": "final",
		},
	}, sessionID, implementationPlanArtifactSlot)
	if err != nil {
		return ToolOutput{}, err
	}

	verb := "updated"
	if created {
		verb = "created"
	}

	return ToolOutput{
		Output: fmt.Sprintf("Implementation-plan artifact %s: %s", verb, artifact.ID),
		Artifacts: []ArtifactMutation{{
			Artifact: artifact,
			Content:  content,
			Created:  created,
			Focused:  true,
		}},
	}, nil
}

// UpsertTaskListTool creates or updates the active session task-list artifact.
type UpsertTaskListTool struct{}

// NewUpsertTaskListTool constructs the task-list artifact tool.
func NewUpsertTaskListTool() *UpsertTaskListTool {
	return &UpsertTaskListTool{}
}

func (t *UpsertTaskListTool) Name() string {
	return "upsert_task_list"
}

func (t *UpsertTaskListTool) Description() string {
	return "Create or update the session task-list artifact as GitHub-flavored markdown. Use this for multi-step tasks to track live progress."
}

func (t *UpsertTaskListTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "The markdown body for the task list. Prefer checkboxes and short status sections.",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "Optional artifact title. Defaults to Task List.",
			},
		},
		"required": []string{"content"},
	}
}

func (t *UpsertTaskListTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *UpsertTaskListTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *UpsertTaskListTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	sessionID, manager, err := getSessionArtifactRuntime()
	if err != nil {
		return ToolOutput{}, err
	}

	content, ok := stringParam(input.Params, "content")
	if !ok || strings.TrimSpace(content) == "" {
		return ToolOutput{}, fmt.Errorf("upsert_task_list requires content")
	}

	title, _ := stringParam(input.Params, "title")
	title = strings.TrimSpace(title)
	if title == "" {
		title = taskListArtifactTitle
	}

	artifact, _, created, err := manager.UpsertSessionMarkdown(ctx, artifactspkg.MarkdownRequest{
		Kind:    artifactspkg.KindTaskList,
		Scope:   artifactspkg.ScopeSession,
		Title:   title,
		Source:  taskListArtifactSource,
		Content: content,
	}, sessionID, taskListArtifactSlot)
	if err != nil {
		return ToolOutput{}, err
	}

	verb := "updated"
	if created {
		verb = "created"
	}

	return ToolOutput{
		Output: fmt.Sprintf("Task-list artifact %s: %s", verb, artifact.ID),
		Artifacts: []ArtifactMutation{{
			Artifact: artifact,
			Content:  content,
			Created:  created,
			Focused:  true,
		}},
	}, nil
}

// SaveWalkthroughTool creates or updates the session walkthrough artifact.
type SaveWalkthroughTool struct{}

// NewSaveWalkthroughTool constructs the walkthrough artifact tool.
func NewSaveWalkthroughTool() *SaveWalkthroughTool {
	return &SaveWalkthroughTool{}
}

func (t *SaveWalkthroughTool) Name() string {
	return "save_walkthrough"
}

func (t *SaveWalkthroughTool) Description() string {
	return "Create or update the session walkthrough artifact with a concise markdown summary of completed work."
}

func (t *SaveWalkthroughTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{
				"type":        "string",
				"description": "The markdown walkthrough summary to persist for the current session.",
			},
			"title": map[string]any{
				"type":        "string",
				"description": "Optional artifact title. Defaults to Walkthrough.",
			},
		},
		"required": []string{"content"},
	}
}

func (t *SaveWalkthroughTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *SaveWalkthroughTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencySerial
}

func (t *SaveWalkthroughTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	sessionID, manager, err := getSessionArtifactRuntime()
	if err != nil {
		return ToolOutput{}, err
	}

	content, ok := stringParam(input.Params, "content")
	if !ok || strings.TrimSpace(content) == "" {
		return ToolOutput{}, fmt.Errorf("save_walkthrough requires content")
	}

	title, _ := stringParam(input.Params, "title")
	title = strings.TrimSpace(title)
	if title == "" {
		title = walkthroughArtifactTitle
	}

	artifact, _, created, err := manager.UpsertSessionMarkdown(ctx, artifactspkg.MarkdownRequest{
		Kind:    artifactspkg.KindWalkthrough,
		Scope:   artifactspkg.ScopeSession,
		Title:   title,
		Source:  walkthroughArtifactSource,
		Content: content,
	}, sessionID, walkthroughArtifactSlot)
	if err != nil {
		return ToolOutput{}, err
	}

	verb := "updated"
	if created {
		verb = "created"
	}

	return ToolOutput{
		Output: fmt.Sprintf("Walkthrough artifact %s: %s", verb, artifact.ID),
		Artifacts: []ArtifactMutation{{
			Artifact: artifact,
			Content:  content,
			Created:  created,
			Focused:  true,
		}},
	}, nil
}

// saveSearchReportArtifact persists a web_fetch result as a search-report artifact.
// Returns the mutation and true if saved; returns false on any error (caller continues without artifact).
func saveSearchReportArtifact(ctx context.Context, url string, query string, content string) (ArtifactMutation, bool) {
	sessionID, manager, err := getSessionArtifactRuntime()
	if err != nil {
		return ArtifactMutation{}, false
	}

	title := "Fetch: " + url
	if len(title) > 80 {
		title = title[:80] + "…"
	}
	description := url
	if query != "" {
		description = url + "\n\nQuery: " + query
	}
	mdContent := artifactspkg.RenderSearchReportMarkdown(description, content)

	slot := "web-fetch-" + sanitizeArtifactSlot(url)
	artifact, _, created, saveErr := manager.UpsertSessionMarkdown(ctx, artifactspkg.MarkdownRequest{
		Kind:    artifactspkg.KindSearchReport,
		Scope:   artifactspkg.ScopeSession,
		Title:   title,
		Source:  "web_fetch",
		Content: mdContent,
		Metadata: map[string]any{
			"url":   url,
			"query": query,
		},
	}, sessionID, slot)
	if saveErr != nil {
		return ArtifactMutation{}, false
	}

	return ArtifactMutation{Artifact: artifact, Content: mdContent, Created: created}, true
}

// saveDiffPreviewArtifact persists a git diff result as a diff-preview artifact.
// Returns the mutation and true if saved; returns false on any error.
func saveDiffPreviewArtifact(ctx context.Context, description string, diff string) (ArtifactMutation, bool) {
	sessionID, manager, err := getSessionArtifactRuntime()
	if err != nil {
		return ArtifactMutation{}, false
	}

	title := "Diff: " + description
	if len(title) > 80 {
		title = title[:80] + "…"
	}
	mdContent := artifactspkg.RenderDiffPreviewMarkdown(description, diff)

	artifact, _, created, saveErr := manager.UpsertSessionMarkdown(ctx, artifactspkg.MarkdownRequest{
		Kind:    artifactspkg.KindDiffPreview,
		Scope:   artifactspkg.ScopeSession,
		Title:   title,
		Source:  "git",
		Content: mdContent,
	}, sessionID, "git-diff-latest")
	if saveErr != nil {
		return ArtifactMutation{}, false
	}

	return ArtifactMutation{Artifact: artifact, Content: mdContent, Created: created}, true
}

// sanitizeArtifactSlot returns a short normalized slot key from an arbitrary string.
func sanitizeArtifactSlot(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 40 {
		s = s[:40]
	}
	var b strings.Builder
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
		} else {
			b.WriteRune('-')
		}
	}
	result := strings.Trim(b.String(), "-.")
	if result == "" {
		return "item"
	}
	return result
}
