package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/channyeintun/go-cli/internal/api"
	artifactspkg "github.com/channyeintun/go-cli/internal/artifacts"
	toolpkg "github.com/channyeintun/go-cli/internal/tools"
)

const (
	plannerSource      = "planner"
	planArtifactSlot   = "active"
	planStatusDraft    = "draft"
	planStatusFinal    = "final"
	planArtifactTitle  = "Implementation Plan"
	planModePromptHint = "When plan mode is active, stay read-only: produce or revise the implementation plan, do not call write tools, and tell the user to switch to /fast when they want implementation to begin."
)

// PlanArtifactUpdate describes a plan artifact mutation that should be emitted to the UI.
type PlanArtifactUpdate struct {
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

// BeginTurn creates or refreshes the active plan artifact in plan mode.
func (p *Planner) BeginTurn(ctx context.Context, userRequest string) (*PlanArtifactUpdate, error) {
	if !p.enabled() {
		return nil, nil
	}

	content := artifactspkg.DraftImplementationPlanMarkdown(userRequest)
	artifact, _, created, err := p.artifactManager.UpsertSessionMarkdown(ctx, artifactspkg.MarkdownRequest{
		Kind:    artifactspkg.KindImplementationPlan,
		Scope:   artifactspkg.ScopeSession,
		Title:   planArtifactTitle,
		Source:  plannerSource,
		Content: content,
		Metadata: map[string]any{
			"mode":   string(ModePlan),
			"status": planStatusDraft,
		},
	}, p.sessionID, planArtifactSlot)
	if err != nil {
		return nil, err
	}

	return &PlanArtifactUpdate{
		Artifact: artifact,
		Content:  content,
		Created:  created,
	}, nil
}

// FinalizeTurn persists the plan text produced during the current turn.
func (p *Planner) FinalizeTurn(ctx context.Context, artifactID string, userRequest string, messages []api.Message, fromIndex int) (*PlanArtifactUpdate, error) {
	if !p.enabled() {
		return nil, nil
	}

	plan := latestAssistantPlanSince(messages, fromIndex)
	if strings.TrimSpace(plan) == "" {
		return nil, nil
	}

	content := artifactspkg.RenderImplementationPlanMarkdown(userRequest, plan)
	request := artifactspkg.MarkdownRequest{
		ID:      strings.TrimSpace(artifactID),
		Kind:    artifactspkg.KindImplementationPlan,
		Scope:   artifactspkg.ScopeSession,
		Title:   planArtifactTitle,
		Source:  plannerSource,
		Content: content,
		Metadata: map[string]any{
			"mode":   string(ModePlan),
			"status": planStatusFinal,
		},
	}

	var (
		artifact artifactspkg.Artifact
		created  bool
		err      error
	)
	if strings.TrimSpace(artifactID) == "" {
		artifact, _, created, err = p.artifactManager.UpsertSessionMarkdown(ctx, request, p.sessionID, planArtifactSlot)
	} else {
		artifact, _, created, err = p.artifactManager.SaveMarkdown(ctx, request)
	}
	if err != nil {
		return nil, err
	}

	return &PlanArtifactUpdate{
		Artifact: artifact,
		Content:  content,
		Created:  created,
	}, nil
}

// ValidateTool blocks write tools while plan mode is active.
func (p *Planner) ValidateTool(ctx context.Context, toolName string, permission toolpkg.PermissionLevel) error {
	if p == nil || permission == toolpkg.PermissionReadOnly {
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
		return fmt.Errorf("write tool %q blocked in plan mode: implementation plan %q is ready; switch to /fast before modifying files", toolName, title)
	}

	return fmt.Errorf("write tool %q blocked in plan mode: finish the implementation plan before modifying files", toolName)
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
