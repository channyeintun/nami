package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/channyeintun/gocode/internal/api"
	artifactspkg "github.com/channyeintun/gocode/internal/artifacts"
	toolpkg "github.com/channyeintun/gocode/internal/tools"
)

const (
	plannerSource                  = "planner"
	planArtifactSlot               = "active"
	planStatusDraft                = "draft"
	planStatusFinal                = "final"
	planArtifactTitle              = "Implementation Plan"
	taskListArtifactSlot           = "active"
	taskListArtifactTitle          = "Task List"
	saveImplementationPlanToolName = "save_implementation_plan"
	planModePromptHint             = "When plan mode is active, you MUST still use read tools (file_read, glob, grep, bash with read-only commands like ls/cat/find) to gather information. Only avoid write tools (create_file, file_write, file_edit, apply_patch, multi_replace_file_content). When you have a real implementation plan, save or update it with save_implementation_plan so it remains the primary review artifact for the task. Ask the user to review the plan, revise it in place when needed, and only move to /fast when they want implementation to begin."
)

// ArtifactUpdate describes an artifact mutation that should be emitted to the UI.
type ArtifactUpdate struct {
	Artifact artifactspkg.Artifact
	Content  string
	Created  bool
}

// Planner coordinates plan artifacts and write-before-plan enforcement.
type Planner struct {
	mode            ExecutionMode
	sessionID       string
	artifactManager *artifactspkg.Manager
}

// NewPlanner constructs a planner for the current session and mode.
func NewPlanner(mode ExecutionMode, sessionID string, artifactManager *artifactspkg.Manager) *Planner {
	return &Planner{
		mode:            mode,
		sessionID:       strings.TrimSpace(sessionID),
		artifactManager: artifactManager,
	}
}

// BeginTurn creates or refreshes session-scoped planning artifacts for the current turn.
func (p *Planner) BeginTurn(ctx context.Context, userRequest string) ([]ArtifactUpdate, error) {
	return nil, nil
}

// FinalizeTurn persists the plan text produced during the current turn.
func (p *Planner) FinalizeTurn(ctx context.Context, artifactID string, userRequest string, messages []api.Message, fromIndex int) ([]ArtifactUpdate, error) {
	return nil, nil
}

// ValidateTool blocks write tools while plan mode is active.
// ReadOnly and Execute tools (bash, git) are always allowed.
func (p *Planner) ValidateTool(ctx context.Context, toolName string, permission toolpkg.PermissionLevel) error {
	if p == nil || permission != toolpkg.PermissionWrite {
		return nil
	}
	if p.mode != ModePlan || !ProfileForMode(p.mode).RequirePlanBeforeWrite {
		return nil
	}

	status, title, err := p.planStatus(ctx)
	if err != nil {
		return fmt.Errorf("write tool %q blocked in plan mode: planner state unavailable: %w", toolName, err)
	}

	if status == planStatusFinal {
		return fmt.Errorf("write tool %q blocked in plan mode: implementation plan %q is ready and awaiting user review — do not call write tools until the user approves the plan and the mode switches to fast", toolName, title)
	}

	return fmt.Errorf("write tool %q blocked in plan mode: you must call save_implementation_plan with a complete implementation plan before modifying any files — write the plan, save it, and wait for the user to review and approve it", toolName)
}

// PlanModePromptHint returns the instruction that keeps plan mode read-only.
func PlanModePromptHint() string {
	return planModePromptHint
}

func (p *Planner) enabled() bool {
	return p != nil && p.mode == ModePlan && strings.TrimSpace(p.sessionID) != "" && p.artifactManager != nil
}

func (p *Planner) planStatus(ctx context.Context) (string, string, error) {
	if !p.enabled() {
		return "", "", nil
	}

	artifact, found, err := p.artifactManager.FindSessionArtifact(ctx, artifactspkg.KindImplementationPlan, artifactspkg.ScopeSession, p.sessionID, planArtifactSlot)
	if err != nil {
		return "", "", err
	}
	if !found {
		return "", "", nil
	}

	if status, ok := artifact.Metadata["status"].(string); ok && strings.TrimSpace(status) != "" {
		return status, artifact.Title, nil
	}

	_, content, err := p.artifactManager.LoadMarkdown(ctx, artifact.ID, 0)
	if err != nil {
		return "", artifact.Title, err
	}
	if strings.Contains(content, "_Planning in progress._") {
		return planStatusDraft, artifact.Title, nil
	}
	return planStatusFinal, artifact.Title, nil
}

func latestAssistantPlanSince(messages []api.Message, fromIndex int) string {
	if fromIndex < 0 {
		fromIndex = 0
	}
	if fromIndex > len(messages) {
		fromIndex = len(messages)
	}
	for index := len(messages) - 1; index >= fromIndex; index-- {
		message := messages[index]
		if message.Role != api.RoleAssistant {
			continue
		}
		if strings.TrimSpace(message.Content) == "" {
			continue
		}
		return message.Content
	}
	return ""
}

func turnUsedToolSince(messages []api.Message, fromIndex int, toolName string) bool {
	if fromIndex < 0 {
		fromIndex = 0
	}
	if fromIndex > len(messages) {
		fromIndex = len(messages)
	}
	for index := fromIndex; index < len(messages); index++ {
		for _, toolCall := range messages[index].ToolCalls {
			if toolCall.Name == toolName {
				return true
			}
		}
	}
	return false
}

func shouldMaintainPlanDraft(userRequest string) bool {
	request := normalizeIntentText(userRequest)
	if request == "" {
		return false
	}
	return containsAny(request, planIntentTerms) || containsAny(request, implementationIntentTerms)
}

func shouldMaintainTaskList(userRequest string) bool {
	request := normalizeIntentText(userRequest)
	if request == "" {
		return false
	}
	return containsAny(request, planIntentTerms) || containsAny(request, implementationIntentTerms)
}

func shouldUpdateDraftPlan(userRequest string, assistantResponse string) bool {
	request := normalizeIntentText(userRequest)
	response := strings.ToLower(strings.TrimSpace(assistantResponse))

	if request == "" || response == "" {
		return false
	}
	if !shouldMaintainPlanDraft(userRequest) {
		return false
	}
	if looksLikeQuestion(assistantResponse) {
		return false
	}
	return containsAny(response, explicitPlanResponseTerms) || containsStructuredSteps(assistantResponse)
}

func (p *Planner) shouldManageSessionArtifact(ctx context.Context, kind artifactspkg.Kind, slot string) (bool, error) {
	if p == nil || strings.TrimSpace(p.sessionID) == "" || p.artifactManager == nil {
		return false, nil
	}

	artifact, found, err := p.artifactManager.FindSessionArtifact(ctx, kind, artifactspkg.ScopeSession, p.sessionID, slot)
	if err != nil {
		return false, err
	}
	if !found {
		return true, nil
	}
	return strings.TrimSpace(artifact.Source) == "" || artifact.Source == plannerSource, nil
}

var planIntentTerms = []string{
	"implementation plan",
	"plan this",
	"make a plan",
	"give me a plan",
	"step by step plan",
	"plan for",
	"approach for",
	"how should we implement",
	"how should i implement",
}

var implementationIntentTerms = []string{
	"implement",
	"implementation",
	"fix",
	"add",
	"change",
	"update",
	"refactor",
	"build",
	"create",
	"rename",
	"support",
	"wire",
	"patch",
	"edit",
	"modify",
	"migrate",
	"remove",
	"replace",
}

var explicitPlanResponseTerms = []string{
	"implementation plan",
	"proposed plan",
	"here's the plan",
	"here is the plan",
	"steps:",
	"next steps",
	"plan:",
	"approach:",
	"i would",
}

var questionPrefixes = []string{
	"what",
	"why",
	"how",
	"when",
	"where",
	"which",
	"who",
	"explain",
	"review",
	"analyze",
	"tell me",
	"can you explain",
	"could you explain",
}

func containsAny(text string, terms []string) bool {
	for _, term := range terms {
		if strings.Contains(text, term) {
			return true
		}
	}
	return false
}

func looksLikeQuestion(text string) bool {
	text = normalizeIntentText(text)
	if strings.Contains(text, "?") {
		return true
	}
	for _, prefix := range questionPrefixes {
		if strings.HasPrefix(text, prefix+" ") || text == prefix {
			return true
		}
	}
	return false
}

func normalizeIntentText(text string) string {
	text = strings.ToLower(strings.TrimSpace(text))
	for _, prefix := range []string{"please ", "kindly ", "pls "} {
		text = strings.TrimPrefix(text, prefix)
	}
	return text
}

func containsStructuredSteps(text string) bool {
	return strings.Contains(text, "\n1.") ||
		strings.HasPrefix(text, "1.") ||
		strings.Contains(text, "\n- ") ||
		strings.Contains(text, "\n## ")
}
