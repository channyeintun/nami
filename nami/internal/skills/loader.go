package skills

import (
	"errors"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/textutil"
)

// Skill represents a loaded skill with its frontmatter and content.
type Skill struct {
	Name         string
	Description  string
	Keywords     []string
	AllowedTools []string
	ArgumentHint string
	Content      string // markdown content after frontmatter
	Source       string // file path
}

const (
	maxAutoSelectedSkills = 3
	maxInjectedSkillChars = 12000

	skillsSectionHeader = "<skills>\nAuto-selected skills. Apply when matching the user's request. Each skill body = additional instructions for this turn.\n\n"
	skillsSectionFooter = "</skills>\n"
)

var ignoredPromptTokens = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "be": {}, "build": {}, "code": {}, "do": {}, "for": {},
	"from": {}, "help": {}, "how": {}, "i": {}, "implement": {}, "in": {}, "is": {},
	"it": {}, "me": {}, "of": {}, "on": {}, "or": {}, "please": {}, "the": {}, "this": {},
	"to": {}, "use": {}, "with": {}, "write": {},
}

// LoadAll discovers built-in, user-global, and project-local skills.
func LoadAll(projectRoot string, userGlobalOverride ...string) ([]Skill, error) {
	var skills []Skill
	var loadErrs []error

	builtinSkills, err := loadBuiltinSkills()
	if err != nil {
		loadErrs = append(loadErrs, fmt.Errorf("load built-in skills: %w", err))
	}
	skills = mergeSkills(skills, builtinSkills)

	globalDir := config.GlobalSkillDir()
	if len(userGlobalOverride) > 0 {
		override := strings.TrimSpace(userGlobalOverride[0])
		if override != "" {
			globalDir = override
		}
	}
	globalSkills, err := loadFromDir(globalDir)
	if err != nil {
		loadErrs = append(loadErrs, fmt.Errorf("load global skills: %w", err))
	}
	skills = mergeSkills(skills, globalSkills)

	// Project-local: .agents/*.md
	if projectRoot != "" {
		localDir := filepath.Join(projectRoot, ".agents")
		localSkills, err := loadFromDir(localDir)
		if err != nil {
			loadErrs = append(loadErrs, fmt.Errorf("load project skills: %w", err))
		}
		skills = mergeSkills(skills, localSkills)
	}

	return skills, errors.Join(loadErrs...)
}

func loadFromDir(dir string) ([]Skill, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var skills []Skill
	var loadErrs []error
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		skill, err := loadSkillFile(path)
		if err != nil {
			loadErrs = append(loadErrs, fmt.Errorf("load %s: %w", path, err))
			continue
		}
		skills = append(skills, skill)
	}
	return skills, errors.Join(loadErrs...)
}

func loadSkillFile(path string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	return parseSkillContent(path, string(data)), nil
}

func parseSkillContent(path string, content string) Skill {
	baseName := pathpkg.Base(filepath.ToSlash(path))
	skill := Skill{
		Name:   strings.TrimSuffix(baseName, pathpkg.Ext(baseName)),
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
	if v, ok := frontmatter["keywords"]; ok {
		skill.Keywords = splitCSV(v)
	}
	if v, ok := frontmatter["allowed-tools"]; ok {
		skill.AllowedTools = splitCSV(v)
	}
	if v, ok := frontmatter["argument-hint"]; ok {
		skill.ArgumentHint = v
	}

	return skill
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

	limit := min(len(scored), maxAutoSelectedSkills)
	selected := make([]Skill, 0, limit)
	for i := range limit {
		selected = append(selected, scored[i].skill)
	}
	return selected
}

// SelectForPrompt returns skills that should be injected for a specific turn.
// Explicitly invoked skills are always included before auto-selected skills.
func SelectForPrompt(available []Skill, userPrompt string, explicit []Skill) []Skill {
	selected := make([]Skill, 0, len(explicit)+maxAutoSelectedSkills)
	seen := make(map[string]struct{}, len(explicit)+maxAutoSelectedSkills)

	for _, skill := range explicit {
		key := skillKey(skill.Name)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		selected = append(selected, skill)
	}

	for _, skill := range SelectRelevant(available, userPrompt) {
		key := skillKey(skill.Name)
		if key == "" {
			continue
		}
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		selected = append(selected, skill)
	}

	return selected
}

// FormatPromptSection renders selected skills as additional system instructions.
// Skills are added in order while they fit the budget. The first one is never
// dropped, because it is the skill the user invoked when there is one: if it
// alone is over budget, its body is cut short with a note saying where the
// full text lives.
func FormatPromptSection(selected []Skill) string {
	if len(selected) == 0 {
		return ""
	}

	var builder strings.Builder
	builder.Grow(1024)
	builder.WriteString(skillsSectionHeader)

	for index, skill := range selected {
		remaining := maxInjectedSkillChars - builder.Len() - len(skillsSectionFooter)
		entry := formatSkillEntry(skill)
		if len(entry) > remaining {
			if index > 0 {
				break
			}
			entry = formatTruncatedSkillEntry(skill, remaining)
		}
		builder.WriteString(entry)
	}

	builder.WriteString(skillsSectionFooter)
	return builder.String()
}

// formatTruncatedSkillEntry renders skill in at most limit bytes by shortening
// its body, cutting on a character boundary.
func formatTruncatedSkillEntry(skill Skill, limit int) string {
	note := fmt.Sprintf("\n[Skill truncated to fit the prompt; the full text is in %s.]", skill.Source)
	shortened := skill
	shortened.Content = ""
	budget := max(limit-len(formatSkillEntry(shortened))-len(note), 0)
	shortened.Content = textutil.TruncateHead(strings.TrimSpace(skill.Content), budget) + note
	return formatSkillEntry(shortened)
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
	if len(skill.Keywords) > 0 {
		keywordScore := scoreKeywords(skill.Keywords, lowerPrompt)
		if keywordScore == 0 {
			return 0
		}
		score += keywordScore
	}
	lowerName := strings.ToLower(strings.TrimSpace(skill.Name))
	if lowerName != "" {
		if containsPhrase(lowerPrompt, lowerName) {
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

func scoreKeywords(keywords []string, lowerPrompt string) int {
	score := 0
	for _, keyword := range keywords {
		keyword = strings.ToLower(strings.TrimSpace(keyword))
		if keyword == "" {
			continue
		}
		if containsPhrase(lowerPrompt, keyword) {
			score += 6
		}
	}
	return score
}

// containsPhrase reports whether phrase occurs in text as whole words. A match
// that starts or ends with an ASCII letter or digit must not be glued to
// another one, so the keyword "go" matches "in go" and "/go-review" but not
// "google" or "algorithm". Other characters impose no boundary, which keeps
// keywords from scripts written without spaces, such as Japanese, matching.
func containsPhrase(text, phrase string) bool {
	if phrase == "" {
		return false
	}
	for offset := 0; ; {
		index := strings.Index(text[offset:], phrase)
		if index < 0 {
			return false
		}
		start := offset + index
		end := start + len(phrase)
		gluedBefore := start > 0 && isASCIIWordByte(phrase[0]) && isASCIIWordByte(text[start-1])
		gluedAfter := end < len(text) && isASCIIWordByte(phrase[len(phrase)-1]) && isASCIIWordByte(text[end])
		if !gluedBefore && !gluedAfter {
			return true
		}
		offset = start + 1
	}
}

func isASCIIWordByte(b byte) bool {
	return 'a' <= b && b <= 'z' || 'A' <= b && b <= 'Z' || '0' <= b && b <= '9'
}

func mergeSkills(existing []Skill, next []Skill) []Skill {
	if len(next) == 0 {
		return existing
	}
	index := make(map[string]int, len(existing))
	for i, skill := range existing {
		index[skillKey(skill.Name)] = i
	}
	for _, skill := range next {
		key := skillKey(skill.Name)
		if idx, ok := index[key]; ok {
			existing[idx] = skill
			continue
		}
		index[key] = len(existing)
		existing = append(existing, skill)
	}
	return existing
}

func skillKey(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// LookupByName finds a skill by its slash-command name.
func LookupByName(available []Skill, name string) (Skill, bool) {
	key := skillKey(name)
	if key == "" {
		return Skill{}, false
	}
	for _, skill := range available {
		if skillKey(skill.Name) == key {
			return skill, true
		}
	}
	return Skill{}, false
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
