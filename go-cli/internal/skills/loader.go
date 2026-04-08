package skills

import (
	"os"
	"path/filepath"
	"strings"
)

// Skill represents a loaded skill with its frontmatter and content.
type Skill struct {
	Name         string
	Description  string
	AllowedTools []string
	ArgumentHint string
	Content      string // markdown content after frontmatter
	Source       string // file path
}

// LoadAll discovers and loads skills from both user-global and project-local directories.
func LoadAll(projectRoot string) ([]Skill, error) {
	var skills []Skill

	// User-global: ~/.config/go-cli/agents/*.md
	home, _ := os.UserHomeDir()
	globalDir := filepath.Join(home, ".config", "go-cli", "agents")
	globalSkills, _ := loadFromDir(globalDir)
	skills = append(skills, globalSkills...)

	// Project-local: .agents/*.md
	if projectRoot != "" {
		localDir := filepath.Join(projectRoot, ".agents")
		localSkills, _ := loadFromDir(localDir)
		skills = append(skills, localSkills...)
	}

	return skills, nil
}

func loadFromDir(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var skills []Skill
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		skill, err := loadSkillFile(path)
		if err != nil {
			continue
		}
		skills = append(skills, skill)
	}
	return skills, nil
}

func loadSkillFile(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}

	content := string(data)
	skill := Skill{
		Name:   strings.TrimSuffix(filepath.Base(path), ".md"),
		Source: path,
	}

	// Parse YAML frontmatter
	frontmatter, body := ParseFrontmatter(content)
	skill.Content = body

	if v, ok := frontmatter["name"]; ok {
		skill.Name = v
	}
	if v, ok := frontmatter["description"]; ok {
		skill.Description = v
	}
	if v, ok := frontmatter["allowed-tools"]; ok {
		skill.AllowedTools = splitCSV(v)
	}
	if v, ok := frontmatter["argument-hint"]; ok {
		skill.ArgumentHint = v
	}

	return skill, nil
}

func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	var result []string
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			result = append(result, p)
		}
	}
	return result
}
