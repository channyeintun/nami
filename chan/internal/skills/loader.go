package skills

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"
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

const (
	maxAutoSelectedSkills = 3
	maxInjectedSkillChars = 12000
)

var ignoredPromptTokens = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "be": {}, "build": {}, "code": {}, "do": {}, "for": {},
	"from": {}, "help": {}, "how": {}, "i": {}, "implement": {}, "in": {}, "is": {},
	"it": {}, "me": {}, "of": {}, "on": {}, "or": {}, "please": {}, "the": {}, "this": {},
	"to": {}, "use": {}, "with": {}, "write": {},
}

// LoadAll discovers and loads skills from both user-global and project-local directories.
func LoadAll(projectRoot string) ([]Skill, error) {
	var skills []Skill

	// User-global: ~/.config/gocode/agents/*.md
	home, _ := os.UserHomeDir()
	globalDir := filepath.Join(home, ".config", "gocode", "agents")
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

// SelectRelevant chooses the most relevant skills for the current user prompt.
func SelectRelevant(available []Skill, userPrompt string) []Skill {
	promptTokens := tokenSet(userPrompt)
	if len(promptTokens) == 0 {
		return nil
	}

	type scoredSkill struct {
		skill Skill
		score int
	}

	lowerPrompt := strings.ToLower(userPrompt)
	scored := make([]scoredSkill, 0, len(available))
	for _, skill := range available {
		score := scoreSkill(skill, lowerPrompt, promptTokens)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredSkill{skill: skill, score: score})
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score == scored[j].score {
			return scored[i].skill.Name < scored[j].skill.Name
		}
		return scored[i].score > scored[j].score
	})

	limit := maxAutoSelectedSkills
	if len(scored) < limit {
		limit = len(scored)
	}
	selected := make([]Skill, 0, limit)
	for i := 0; i < limit; i++ {
		selected = append(selected, scored[i].skill)
	}
	return selected
}

// FormatPromptSection renders selected skills as additional system instructions.
func FormatPromptSection(selected []Skill) string {
	if len(selected) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.Grow(1024)
	builder.WriteString("<skills>\n")
	builder.WriteString("Apply the following auto-selected skills when they match the user's request. Treat each skill body as additional instructions for this turn.\n\n")

	for _, skill := range selected {
		entry := formatSkillEntry(skill)
		if builder.Len()+len(entry)+len("</skills>\n") > maxInjectedSkillChars {
			break
		}
		builder.WriteString(entry)
	}

	if builder.Len() == len("<skills>\n")+len("Apply the following auto-selected skills when they match the user's request. Treat each skill body as additional instructions for this turn.\n\n") {
		return ""
	}
	builder.WriteString("</skills>\n")
	return builder.String()
}

func formatSkillEntry(skill Skill) string {
	var builder strings.Builder
	builder.WriteString("<skill>\n")
	builder.WriteString("Name: ")
	builder.WriteString(strings.TrimSpace(skill.Name))
	builder.WriteString("\n")
	if description := strings.TrimSpace(skill.Description); description != "" {
		builder.WriteString("Description: ")
		builder.WriteString(description)
		builder.WriteString("\n")
	}
	if len(skill.AllowedTools) > 0 {
		builder.WriteString("Allowed tools: ")
		builder.WriteString(strings.Join(skill.AllowedTools, ", "))
		builder.WriteString("\n")
	}
	if argumentHint := strings.TrimSpace(skill.ArgumentHint); argumentHint != "" {
		builder.WriteString("Argument hint: ")
		builder.WriteString(argumentHint)
		builder.WriteString("\n")
	}
	builder.WriteString("\n")
	builder.WriteString(strings.TrimSpace(skill.Content))
	builder.WriteString("\n</skill>\n\n")
	return builder.String()
}

func scoreSkill(skill Skill, lowerPrompt string, promptTokens map[string]struct{}) int {
	score := 0
	lowerName := strings.ToLower(strings.TrimSpace(skill.Name))
	if lowerName != "" {
		if strings.Contains(lowerPrompt, "/"+lowerName) || strings.Contains(lowerPrompt, lowerName) {
			score += 8
		}
		score += overlapScore(tokenSet(lowerName), promptTokens, 3)
	}
	score += overlapScore(tokenSet(skill.Description), promptTokens, 2)
	score += overlapScore(tokenSet(skill.ArgumentHint), promptTokens, 1)
	return score
}

func overlapScore(tokens map[string]struct{}, promptTokens map[string]struct{}, weight int) int {
	score := 0
	for token := range tokens {
		if _, ok := promptTokens[token]; ok {
			score += weight
		}
	}
	return score
}

func tokenSet(text string) map[string]struct{} {
	fields := strings.Fields(strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			return unicode.ToLower(r)
		}
		return ' '
	}, text))

	tokens := make(map[string]struct{}, len(fields))
	for _, field := range fields {
		if len(field) < 3 {
			continue
		}
		if _, ignored := ignoredPromptTokens[field]; ignored {
			continue
		}
		tokens[field] = struct{}{}
	}
	return tokens
}
