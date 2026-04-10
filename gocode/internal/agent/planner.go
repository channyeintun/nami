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
	planArtifactSlot   = "active"
	planStatusFinal    = "final"
	planModePromptHint = "When plan mode is active, you MUST still use read tools (file_read, glob, grep, bash with read-only commands like ls/cat/find) to gather information. Only avoid write tools (file_write, file_edit). When you have a real implementation plan, save it with save_implementation_plan and tell the user to switch to /fast when they want implementation to begin."
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
	return planStatusFinal, artifact.Title, nil
}
