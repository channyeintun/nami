package swarm

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/channyeintun/nami/internal/api"
	"github.com/channyeintun/nami/internal/config"
	toolpkg "github.com/channyeintun/nami/internal/tools"
)

const ProjectSpecRelativePath = ".nami/swarm.json"

type WorkspaceStrategy string

const (
	WorkspaceShared   WorkspaceStrategy = "shared"
	WorkspaceWorktree WorkspaceStrategy = "worktree"
	WorkspaceSnapshot WorkspaceStrategy = "snapshot"
)

type QueuePolicy string

const (
	QueueFIFO        QueuePolicy = "fifo"
	QueueBatchReview QueuePolicy = "batch-review"
	QueueLatestWins  QueuePolicy = "latest-wins"
)

type HandoffField string

const (
	HandoffFieldSummary      HandoffField = "summary"
	HandoffFieldChangedFiles HandoffField = "changed_files"
	HandoffFieldCommandsRun  HandoffField = "commands_run"
	HandoffFieldVerification HandoffField = "verification"
	HandoffFieldRisks        HandoffField = "risks"
	HandoffFieldNextAction   HandoffField = "next_action"
)

type Spec struct {
	Version int        `json:"version,omitempty"`
	Roles   []RoleSpec `json:"roles,omitempty"`
}

type RoleSpec struct {
	Name         string       `json:"name"`
	Purpose      string       `json:"purpose,omitempty"`
	SubagentType string       `json:"subagent_type,omitempty"`
	Model        string       `json:"model,omitempty"`
	Workspace    string       `json:"workspace,omitempty"`
	QueuePolicy  string       `json:"queue_policy,omitempty"`
	AllowTools   []string     `json:"allow_tools,omitempty"`
	DenyTools    []string     `json:"deny_tools,omitempty"`
	Handoff      HandoffSpec  `json:"handoff,omitempty"`
	Metadata     RoleMetadata `json:"metadata,omitempty"`
}

type RoleMetadata struct {
	Owner string `json:"owner,omitempty"`
}

type HandoffSpec struct {
	Required       bool     `json:"required,omitempty"`
	Targets        []string `json:"targets,omitempty"`
	RequiredFields []string `json:"required_fields,omitempty"`
}

type ResolvedSpec struct {
	ProjectRoot string
	Path        string
	Version     int
	Roles       []ResolvedRole
}

type ResolvedRole struct {
	Name              string
	Purpose           string
	SubagentType      string
	Model             string
	WorkspaceStrategy WorkspaceStrategy
	QueuePolicy       QueuePolicy
	AllowTools        []string
	DenyTools         []string
	Handoff           ResolvedHandoff
	Metadata          RoleMetadata
}

type ResolvedHandoff struct {
	Required       bool
	Targets        []string
	RequiredFields []HandoffField
}

type ValidationError struct {
	Path     string
	Problems []string
}

func (e *ValidationError) Error() string {
	if e == nil || len(e.Problems) == 0 {
		return "invalid swarm spec"
	}
	if strings.TrimSpace(e.Path) == "" {
		return fmt.Sprintf("invalid swarm spec: %s", strings.Join(e.Problems, "; "))
	}
	return fmt.Sprintf("invalid swarm spec %s: %s", e.Path, strings.Join(e.Problems, "; "))
}

var roleNamePattern = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]*$`)

var defaultRequiredHandoffFields = []HandoffField{
	HandoffFieldSummary,
	HandoffFieldVerification,
}

var allowedHandoffFields = map[HandoffField]struct{}{
	HandoffFieldSummary:      {},
	HandoffFieldChangedFiles: {},
	HandoffFieldCommandsRun:  {},
	HandoffFieldVerification: {},
	HandoffFieldRisks:        {},
	HandoffFieldNextAction:   {},
}

func ProjectSpecPath(cwd string) string {
	projectRoot := config.FindProjectRoot(cwd)
	if strings.TrimSpace(projectRoot) == "" {
		return ""
	}
	return filepath.Join(projectRoot, ProjectSpecRelativePath)
}

func LoadProjectSpec(cwd string) (ResolvedSpec, error) {
	path := ProjectSpecPath(cwd)
	if strings.TrimSpace(path) == "" {
		return ResolvedSpec{}, os.ErrNotExist
	}
	return LoadProjectSpecFromPath(path)
}

func LoadProjectSpecFromPath(path string) (ResolvedSpec, error) {
	trimmedPath := strings.TrimSpace(path)
	if trimmedPath == "" {
		return ResolvedSpec{}, os.ErrNotExist
	}

	data, err := os.ReadFile(trimmedPath)
	if err != nil {
		return ResolvedSpec{}, err
	}
	if strings.TrimSpace(string(data)) == "" {
		return ResolvedSpec{}, os.ErrNotExist
	}

	var spec Spec
	if err := json.Unmarshal(data, &spec); err != nil {
		return ResolvedSpec{}, fmt.Errorf("parse swarm spec %s: %w", trimmedPath, err)
	}

	projectRoot := filepath.Dir(filepath.Dir(trimmedPath))
	return spec.Resolve(projectRoot, trimmedPath)
}

func (s Spec) Resolve(projectRoot string, path string) (ResolvedSpec, error) {
	resolved := ResolvedSpec{
		ProjectRoot: strings.TrimSpace(projectRoot),
		Path:        strings.TrimSpace(path),
		Version:     s.Version,
	}
	if resolved.Version == 0 {
		resolved.Version = 1
	}

	var problems []string
	if resolved.Version != 1 {
		problems = append(problems, fmt.Sprintf("unsupported version %d", resolved.Version))
	}
	if len(s.Roles) == 0 {
		problems = append(problems, "roles must contain at least one role")
	}

	resolvedRoles := make([]ResolvedRole, 0, len(s.Roles))
	roleIndex := make(map[string]int, len(s.Roles))
	for idx, role := range s.Roles {
		displayIndex := idx + 1
		resolvedRole, roleProblems := resolveRole(role, displayIndex)
		if len(roleProblems) > 0 {
			problems = append(problems, roleProblems...)
			continue
		}
		if existing, ok := roleIndex[resolvedRole.Name]; ok {
			problems = append(problems, fmt.Sprintf("%s duplicates %s", roleRef(displayIndex, resolvedRole.Name), roleRef(existing, resolvedRole.Name)))
			continue
		}
		roleIndex[resolvedRole.Name] = displayIndex
		resolvedRoles = append(resolvedRoles, resolvedRole)
	}

	if len(problems) == 0 {
		for idx := range resolvedRoles {
			roleProblems := validateResolvedRoleEdges(resolvedRoles[idx], roleIndex, idx+1)
			if len(roleProblems) > 0 {
				problems = append(problems, roleProblems...)
			}
		}
	}

	if len(problems) > 0 {
		return ResolvedSpec{}, &ValidationError{Path: resolved.Path, Problems: problems}
	}

	resolved.Roles = resolvedRoles
	return resolved, nil
}

func (s ResolvedSpec) SummaryMarkdown() string {
	var b strings.Builder
	b.WriteString("# Swarm Spec\n\n")
	b.WriteString("## Summary\n\n")
	b.WriteString(fmt.Sprintf("- Version: %d\n", s.Version))
	if strings.TrimSpace(s.ProjectRoot) != "" {
		b.WriteString(fmt.Sprintf("- Project root: %s\n", s.ProjectRoot))
	}
	if strings.TrimSpace(s.Path) != "" {
		b.WriteString(fmt.Sprintf("- Spec path: %s\n", s.Path))
	}
	b.WriteString(fmt.Sprintf("- Roles: %d\n", len(s.Roles)))

	for _, role := range s.Roles {
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("## Role: %s\n\n", role.Name))
		b.WriteString(fmt.Sprintf("- Purpose: %s\n", role.Purpose))
		b.WriteString(fmt.Sprintf("- Subagent type: %s\n", role.SubagentType))
		if strings.TrimSpace(role.Model) == "" {
			b.WriteString("- Model: session default\n")
		} else {
			b.WriteString(fmt.Sprintf("- Model: %s\n", role.Model))
		}
		b.WriteString(fmt.Sprintf("- Workspace: %s\n", role.WorkspaceStrategy))
		b.WriteString(fmt.Sprintf("- Queue policy: %s\n", role.QueuePolicy))
		if strings.TrimSpace(role.Metadata.Owner) != "" {
			b.WriteString(fmt.Sprintf("- Owner: %s\n", role.Metadata.Owner))
		}
		b.WriteString(fmt.Sprintf("- Allowed tools: %s\n", joinListOrDefault(role.AllowTools, "all runtime tools allowed")))
		b.WriteString(fmt.Sprintf("- Denied tools: %s\n", joinListOrDefault(role.DenyTools, "none")))
		b.WriteString(fmt.Sprintf("- Handoff required: %t\n", role.Handoff.Required))
		b.WriteString(fmt.Sprintf("- Handoff targets: %s\n", joinListOrDefault(role.Handoff.Targets, "none")))

		fields := make([]string, 0, len(role.Handoff.RequiredFields))
		for _, field := range role.Handoff.RequiredFields {
			fields = append(fields, string(field))
		}
		b.WriteString(fmt.Sprintf("- Handoff fields: %s\n", joinListOrDefault(fields, "none")))
	}

	return b.String()
}

func resolveRole(role RoleSpec, idx int) (ResolvedRole, []string) {
	var problems []string

	name := normalizeRoleName(role.Name)
	if name == "" {
		problems = append(problems, fmt.Sprintf("role[%d] name is required", idx))
	} else if !roleNamePattern.MatchString(name) {
		problems = append(problems, fmt.Sprintf("role[%d] name %q must match %s", idx, role.Name, roleNamePattern.String()))
	}

	purpose := strings.TrimSpace(role.Purpose)
	if purpose == "" {
		problems = append(problems, fmt.Sprintf("role[%d] purpose is required", idx))
	}

	subagentType := toolpkg.NormalizeSubagentType(role.SubagentType)
	if subagentType == "" {
		subagentType = toolpkg.NormalizeSubagentType("general-purpose")
	}
	if !toolpkg.IsSupportedSubagentType(subagentType) {
		problems = append(problems, fmt.Sprintf("role[%d] subagent_type %q is not supported", idx, role.SubagentType))
	}

	workspace := normalizeWorkspaceStrategy(role.Workspace)
	if workspace == "" {
		problems = append(problems, fmt.Sprintf("role[%d] workspace %q is invalid", idx, role.Workspace))
	}

	queuePolicy := normalizeQueuePolicy(role.QueuePolicy)
	if queuePolicy == "" {
		problems = append(problems, fmt.Sprintf("role[%d] queue_policy %q is invalid", idx, role.QueuePolicy))
	}

	model := strings.TrimSpace(role.Model)
	if model != "" {
		if modelProblem := validateModelRef(model); modelProblem != "" {
			problems = append(problems, fmt.Sprintf("role[%d] %s", idx, modelProblem))
		}
	}

	allowTools, allowProblems := normalizeUniqueList(role.AllowTools, fmt.Sprintf("role[%d] allow_tools", idx))
	denyTools, denyProblems := normalizeUniqueList(role.DenyTools, fmt.Sprintf("role[%d] deny_tools", idx))
	problems = append(problems, allowProblems...)
	problems = append(problems, denyProblems...)
	if overlap := listOverlap(allowTools, denyTools); len(overlap) > 0 {
		problems = append(problems, fmt.Sprintf("role[%d] tools cannot appear in both allow_tools and deny_tools: %s", idx, strings.Join(overlap, ", ")))
	}

	handoff, handoffProblems := resolveHandoff(role.Handoff, idx)
	problems = append(problems, handoffProblems...)

	if len(problems) > 0 {
		return ResolvedRole{}, problems
	}

	return ResolvedRole{
		Name:              name,
		Purpose:           purpose,
		SubagentType:      subagentType,
		Model:             model,
		WorkspaceStrategy: workspace,
		QueuePolicy:       queuePolicy,
		AllowTools:        allowTools,
		DenyTools:         denyTools,
		Handoff:           handoff,
		Metadata: RoleMetadata{
			Owner: strings.TrimSpace(role.Metadata.Owner),
		},
	}, nil
}

func resolveHandoff(handoff HandoffSpec, idx int) (ResolvedHandoff, []string) {
	var problems []string
	targets, targetProblems := normalizeUniqueRoleTargets(handoff.Targets, fmt.Sprintf("role[%d] handoff.targets", idx))
	problems = append(problems, targetProblems...)

	fields, fieldProblems := normalizeHandoffFields(handoff.RequiredFields, fmt.Sprintf("role[%d] handoff.required_fields", idx), handoff.Required)
	problems = append(problems, fieldProblems...)

	return ResolvedHandoff{
		Required:       handoff.Required,
		Targets:        targets,
		RequiredFields: fields,
	}, problems
}

func validateResolvedRoleEdges(role ResolvedRole, roleIndex map[string]int, idx int) []string {
	var problems []string
	for _, target := range role.Handoff.Targets {
		if role.Name == target {
			problems = append(problems, fmt.Sprintf("role[%d] handoff target %q cannot point to itself", idx, target))
			continue
		}
		if _, ok := roleIndex[target]; !ok {
			problems = append(problems, fmt.Sprintf("role[%d] handoff target %q does not exist", idx, target))
		}
	}
	return problems
}

func normalizeRoleName(raw string) string {
	return strings.ToLower(strings.TrimSpace(raw))
}

func normalizeWorkspaceStrategy(raw string) WorkspaceStrategy {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(WorkspaceShared):
		return WorkspaceShared
	case string(WorkspaceWorktree):
		return WorkspaceWorktree
	case string(WorkspaceSnapshot):
		return WorkspaceSnapshot
	default:
		return ""
	}
}

func normalizeQueuePolicy(raw string) QueuePolicy {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(QueueFIFO):
		return QueueFIFO
	case string(QueueBatchReview), "batch_review":
		return QueueBatchReview
	case string(QueueLatestWins), "latest_wins":
		return QueueLatestWins
	default:
		return ""
	}
}

func validateModelRef(raw string) string {
	provider, model := config.ParseModel(strings.TrimSpace(raw))
	provider = strings.ToLower(strings.TrimSpace(provider))
	model = strings.TrimSpace(model)
	if provider == "" && model == "" {
		return "model must not be empty"
	}
	if provider == "" {
		return ""
	}
	if model == "" {
		return fmt.Sprintf("model %q is missing the model name", raw)
	}
	if _, ok := api.Presets[provider]; !ok {
		providers := make([]string, 0, len(api.Presets))
		for key := range api.Presets {
			providers = append(providers, key)
		}
		sort.Strings(providers)
		return fmt.Sprintf("provider %q is unsupported; expected one of: %s", provider, strings.Join(providers, ", "))
	}
	return ""
}

func normalizeUniqueList(values []string, field string) ([]string, []string) {
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	var problems []string
	for _, value := range values {
		trimmed := strings.TrimSpace(value)
		if trimmed == "" {
			problems = append(problems, fmt.Sprintf("%s cannot contain empty entries", field))
			continue
		}
		if _, ok := seen[trimmed]; ok {
			problems = append(problems, fmt.Sprintf("%s contains duplicate entry %q", field, trimmed))
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized, problems
}

func normalizeUniqueRoleTargets(values []string, field string) ([]string, []string) {
	seen := make(map[string]struct{}, len(values))
	normalized := make([]string, 0, len(values))
	var problems []string
	for _, value := range values {
		trimmed := normalizeRoleName(value)
		if trimmed == "" {
			problems = append(problems, fmt.Sprintf("%s cannot contain blank role names", field))
			continue
		}
		if !roleNamePattern.MatchString(trimmed) {
			problems = append(problems, fmt.Sprintf("%s contains invalid role name %q", field, trimmed))
			continue
		}
		if _, ok := seen[trimmed]; ok {
			problems = append(problems, fmt.Sprintf("%s contains duplicate entry %q", field, trimmed))
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized, problems
}

func normalizeHandoffFields(values []string, field string, required bool) ([]HandoffField, []string) {
	if len(values) == 0 && required {
		copied := append([]HandoffField(nil), defaultRequiredHandoffFields...)
		return copied, nil
	}

	seen := make(map[HandoffField]struct{}, len(values))
	normalized := make([]HandoffField, 0, len(values))
	var problems []string
	for _, value := range values {
		trimmed := HandoffField(strings.ToLower(strings.TrimSpace(value)))
		if trimmed == "" {
			problems = append(problems, fmt.Sprintf("%s cannot contain empty entries", field))
			continue
		}
		if _, ok := allowedHandoffFields[trimmed]; !ok {
			problems = append(problems, fmt.Sprintf("%s contains unsupported field %q", field, trimmed))
			continue
		}
		if _, ok := seen[trimmed]; ok {
			problems = append(problems, fmt.Sprintf("%s contains duplicate field %q", field, trimmed))
			continue
		}
		seen[trimmed] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	return normalized, problems
}

func listOverlap(left []string, right []string) []string {
	rightSet := make(map[string]struct{}, len(right))
	for _, value := range right {
		rightSet[value] = struct{}{}
	}
	overlap := make([]string, 0)
	for _, value := range left {
		if _, ok := rightSet[value]; ok {
			overlap = append(overlap, value)
		}
	}
	sort.Strings(overlap)
	return overlap
}

func joinListOrDefault(values []string, fallback string) string {
	if len(values) == 0 {
		return fallback
	}
	return strings.Join(values, ", ")
}

func roleRef(idx int, name string) string {
	return fmt.Sprintf("role[%d] %s", idx, name)
}
