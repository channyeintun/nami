package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/channyeintun/nami/internal/config"
	skillspkg "github.com/channyeintun/nami/internal/skills"
)

type SkillTool struct{}

type skillDescriptor struct {
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Keywords     []string `json:"keywords,omitempty"`
	AllowedTools []string `json:"allowed_tools,omitempty"`
	ArgumentHint string   `json:"argument_hint,omitempty"`
	Source       string   `json:"source,omitempty"`
	Content      string   `json:"content,omitempty"`
	Input        string   `json:"input,omitempty"`
}

type skillListResponse struct {
	Mode    string            `json:"mode"`
	Query   string            `json:"query,omitempty"`
	Count   int               `json:"count"`
	Warning string            `json:"warning,omitempty"`
	Skills  []skillDescriptor `json:"skills"`
}

type skillInvokeResponse struct {
	Mode    string          `json:"mode"`
	Warning string          `json:"warning,omitempty"`
	Skill   skillDescriptor `json:"skill"`
}

func NewSkillTool() *SkillTool {
	return &SkillTool{}
}

func (t *SkillTool) Name() string {
	return "skill"
}

func (t *SkillTool) Description() string {
	return "List available skills or load a named skill's content and metadata for explicit reuse in the current turn."
}

func (t *SkillTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"name": map[string]any{
				"type":        "string",
				"description": "Exact skill name to load. If omitted, the tool lists available skills.",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Optional substring filter when listing skills.",
			},
			"input": map[string]any{
				"type":        "string",
				"description": "Optional invocation-specific instructions to associate with the loaded skill.",
			},
		},
	}
}

func (t *SkillTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *SkillTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *SkillTool) Validate(input ToolInput) error {
	if name, ok := stringParam(input.Params, "name"); ok && strings.TrimSpace(name) == "" {
		return fmt.Errorf("skill name must not be empty")
	}
	if query, ok := stringParam(input.Params, "query"); ok && strings.TrimSpace(query) == "" {
		return fmt.Errorf("skill query must not be empty")
	}
	return nil
}

func (t *SkillTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	select {
	case <-ctx.Done():
		return ToolOutput{}, ctx.Err()
	default:
	}

	cwd, err := os.Getwd()
	if err != nil {
		return ToolOutput{}, fmt.Errorf("resolve working directory: %w", err)
	}
	cfg := config.LoadForWorkingDir(cwd)
	loadedSkills, loadErr := skillspkg.LoadAll(cwd, cfg.SkillDir)
	warning := ""
	if loadErr != nil {
		if len(loadedSkills) == 0 {
			return ToolOutput{}, fmt.Errorf("load skills: %w", loadErr)
		}
		warning = loadErr.Error()
	}

	name, _ := stringParam(input.Params, "name")
	name = strings.TrimSpace(name)
	if name != "" {
		skill, ok := skillspkg.LookupByName(loadedSkills, name)
		if !ok {
			return ToolOutput{}, fmt.Errorf("unknown skill %q", name)
		}
		response := skillInvokeResponse{
			Mode:    "invoke",
			Warning: warning,
			Skill:   renderSkillDescriptor(skill, true),
		}
		if invocationInput, ok := stringParam(input.Params, "input"); ok {
			response.Skill.Input = strings.TrimSpace(invocationInput)
		}
		encoded, err := json.MarshalIndent(response, "", "  ")
		if err != nil {
			return ToolOutput{}, fmt.Errorf("marshal skill invocation: %w", err)
		}
		return ToolOutput{Output: string(encoded)}, nil
	}

	query, _ := stringParam(input.Params, "query")
	filtered := filterSkills(loadedSkills, query)
	response := skillListResponse{
		Mode:    "list",
		Query:   strings.TrimSpace(query),
		Count:   len(filtered),
		Warning: warning,
		Skills:  make([]skillDescriptor, 0, len(filtered)),
	}
	for _, skill := range filtered {
		response.Skills = append(response.Skills, renderSkillDescriptor(skill, false))
	}
	encoded, err := json.MarshalIndent(response, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal skill list: %w", err)
	}
	return ToolOutput{Output: string(encoded)}, nil
}

func filterSkills(skills []skillspkg.Skill, query string) []skillspkg.Skill {
	query = strings.ToLower(strings.TrimSpace(query))
	filtered := make([]skillspkg.Skill, 0, len(skills))
	for _, skill := range skills {
		if query == "" || skillMatchesQuery(skill, query) {
			filtered = append(filtered, skill)
		}
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		return strings.ToLower(strings.TrimSpace(filtered[i].Name)) < strings.ToLower(strings.TrimSpace(filtered[j].Name))
	})
	return filtered
}

func skillMatchesQuery(skill skillspkg.Skill, query string) bool {
	if strings.Contains(strings.ToLower(skill.Name), query) || strings.Contains(strings.ToLower(skill.Description), query) || strings.Contains(strings.ToLower(skill.ArgumentHint), query) {
		return true
	}
	for _, keyword := range skill.Keywords {
		if strings.Contains(strings.ToLower(keyword), query) {
			return true
		}
	}
	return false
}

func renderSkillDescriptor(skill skillspkg.Skill, includeContent bool) skillDescriptor {
	descriptor := skillDescriptor{
		Name:         strings.TrimSpace(skill.Name),
		Description:  strings.TrimSpace(skill.Description),
		Keywords:     append([]string(nil), skill.Keywords...),
		AllowedTools: append([]string(nil), skill.AllowedTools...),
		ArgumentHint: strings.TrimSpace(skill.ArgumentHint),
		Source:       strings.TrimSpace(skill.Source),
	}
	if includeContent {
		descriptor.Content = strings.TrimSpace(skill.Content)
	}
	return descriptor
}
