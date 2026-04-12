package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	defaultDependencyOverviewResults   = 12
	defaultDependencyOverviewManifests = 24
)

var dependencyOverviewManifestNames = map[string]string{
	"go.mod":           "Go",
	"package.json":     "Node.js",
	"pyproject.toml":   "Python",
	"requirements.txt": "Python",
	"Cargo.toml":       "Rust",
	"Gemfile":          "Ruby",
}

var quotedStringPattern = regexp.MustCompile(`"([^"\\]+)"|'([^'\\]+)'`)
var gemfileLinePattern = regexp.MustCompile(`^\s*gem\s+["']([^"']+)["']`)
var pep508NamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*`)

type dependencyOverview struct {
	RootPath          string                       `json:"root_path"`
	ManifestCount     int                          `json:"manifest_count"`
	DependencyCount   int                          `json:"dependency_count"`
	Truncated         bool                         `json:"truncated,omitempty"`
	Ecosystems        []dependencyEcosystemSummary `json:"ecosystems,omitempty"`
	ManifestSummaries []dependencyManifestSummary  `json:"manifests,omitempty"`
}

type dependencyManifestSummary struct {
	Path      string                     `json:"path"`
	Ecosystem string                     `json:"ecosystem"`
	Sections  []dependencySectionSummary `json:"sections,omitempty"`
}

type dependencySectionSummary struct {
	Name         string   `json:"name"`
	Count        int      `json:"count"`
	Dependencies []string `json:"dependencies,omitempty"`
}

type dependencyEcosystemSummary struct {
	Ecosystem       string `json:"ecosystem"`
	ManifestCount   int    `json:"manifest_count"`
	DependencyCount int    `json:"dependency_count"`
}

type dependencyOverviewTool struct{}

func NewDependencyOverviewTool() *dependencyOverviewTool {
	return &dependencyOverviewTool{}
}

func (t *dependencyOverviewTool) Name() string {
	return "dependency_overview"
}

func (t *dependencyOverviewTool) Description() string {
	return "Summarize dependencies from common manifests such as go.mod, package.json, pyproject.toml, requirements.txt, Cargo.toml, and Gemfile."
}

func (t *dependencyOverviewTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Optional file or directory to inspect. Defaults to the current working directory.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Optional cap on dependency names returned per section. Defaults to 12.",
				"minimum":     1,
			},
			"max_manifests": map[string]any{
				"type":        "integer",
				"description": "Optional cap on manifest files scanned. Defaults to 24.",
				"minimum":     1,
			},
		},
	}
}

func (t *dependencyOverviewTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *dependencyOverviewTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *dependencyOverviewTool) Validate(input ToolInput) error {
	if value, ok := intParam(input.Params, "max_results"); ok && value < 1 {
		return fmt.Errorf("max_results must be >= 1")
	}
	if value, ok := intParam(input.Params, "max_manifests"); ok && value < 1 {
		return fmt.Errorf("max_manifests must be >= 1")
	}
	return nil
}

func (t *dependencyOverviewTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	searchPath, err := resolveSearchPath(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}

	rootPath, err := normalizeOverviewRoot(searchPath)
	if err != nil {
		return ToolOutput{}, err
	}

	maxResults := intOrDefault(input.Params, "max_results", defaultDependencyOverviewResults)
	maxManifests := intOrDefault(input.Params, "max_manifests", defaultDependencyOverviewManifests)

	manifestPaths, truncated, err := discoverDependencyManifestPaths(ctx, rootPath, maxManifests)
	if err != nil {
		return ToolOutput{}, err
	}

	summaries := make([]dependencyManifestSummary, 0, len(manifestPaths))
	ecosystemCounts := make(map[string]int)
	ecosystemDeps := make(map[string]int)
	totalDependencies := 0

	for _, manifestPath := range manifestPaths {
		summary, err := summarizeDependencyManifest(rootPath, manifestPath, maxResults)
		if err != nil {
			continue
		}
		if len(summary.Sections) == 0 {
			continue
		}
		summaries = append(summaries, summary)
		ecosystemCounts[summary.Ecosystem]++
		for _, section := range summary.Sections {
			totalDependencies += section.Count
			ecosystemDeps[summary.Ecosystem] += section.Count
		}
	}

	overview := dependencyOverview{
		RootPath:          rootPath,
		ManifestCount:     len(summaries),
		DependencyCount:   totalDependencies,
		Truncated:         truncated,
		Ecosystems:        sortDependencyEcosystems(ecosystemCounts, ecosystemDeps),
		ManifestSummaries: summaries,
	}

	encoded, err := json.MarshalIndent(overview, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal dependency_overview: %w", err)
	}

	return ToolOutput{Output: string(encoded), Truncated: truncated}, nil
}

func discoverDependencyManifestPaths(ctx context.Context, rootPath string, maxManifests int) ([]string, bool, error) {
	paths := make([]string, 0, maxManifests)
	truncated := false

	walkErr := filepath.WalkDir(rootPath, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if entry.IsDir() {
			if shouldSkipOverviewDir(entry.Name(), path, rootPath) {
				return filepath.SkipDir
			}
			return nil
		}

		if _, ok := dependencyOverviewManifestNames[filepath.Base(path)]; !ok {
			return nil
		}
		paths = append(paths, path)
		if len(paths) >= maxManifests {
			truncated = true
			return filepath.SkipAll
		}
		return nil
	})
	if walkErr != nil && walkErr != filepath.SkipAll {
		return nil, false, walkErr
	}
	sort.Strings(paths)
	return paths, truncated, nil
}

func summarizeDependencyManifest(rootPath, manifestPath string, maxResults int) (dependencyManifestSummary, error) {
	relPath, err := filepath.Rel(rootPath, manifestPath)
	if err != nil {
		return dependencyManifestSummary{}, err
	}
	contentBytes, err := os.ReadFile(manifestPath)
	if err != nil {
		return dependencyManifestSummary{}, err
	}
	content := string(contentBytes)
	base := filepath.Base(manifestPath)
	ecosystem := dependencyOverviewManifestNames[base]
	sections := parseDependencySections(base, content)
	return dependencyManifestSummary{
		Path:      filepath.ToSlash(relPath),
		Ecosystem: ecosystem,
		Sections:  summarizeDependencySections(sections, maxResults),
	}, nil
}

func parseDependencySections(baseName, content string) map[string][]string {
	switch baseName {
	case "go.mod":
		return parseGoModDependencies(content)
	case "package.json":
		return parsePackageJSONDependencies(content)
	case "pyproject.toml":
		return parsePyProjectDependencies(content)
	case "requirements.txt":
		return parseRequirementsDependencies(content)
	case "Cargo.toml":
		return parseCargoDependencies(content)
	case "Gemfile":
		return parseGemfileDependencies(content)
	default:
		return nil
	}
}

func summarizeDependencySections(sections map[string][]string, maxResults int) []dependencySectionSummary {
	if len(sections) == 0 {
		return nil
	}
	keys := make([]string, 0, len(sections))
	for key, deps := range sections {
		if len(deps) == 0 {
			continue
		}
		keys = append(keys, key)
	}
	sort.Strings(keys)
	result := make([]dependencySectionSummary, 0, len(keys))
	for _, key := range keys {
		deps := dedupeSortedStrings(sections[key])
		result = append(result, dependencySectionSummary{
			Name:         key,
			Count:        len(deps),
			Dependencies: limitedStrings(deps, maxResults),
		})
	}
	return result
}

func sortDependencyEcosystems(manifestCounts, dependencyCounts map[string]int) []dependencyEcosystemSummary {
	items := make([]dependencyEcosystemSummary, 0, len(manifestCounts))
	for ecosystem, manifests := range manifestCounts {
		items = append(items, dependencyEcosystemSummary{
			Ecosystem:       ecosystem,
			ManifestCount:   manifests,
			DependencyCount: dependencyCounts[ecosystem],
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].DependencyCount != items[j].DependencyCount {
			return items[i].DependencyCount > items[j].DependencyCount
		}
		return items[i].Ecosystem < items[j].Ecosystem
	})
	return items
}

func parseGoModDependencies(content string) map[string][]string {
	sections := map[string][]string{}
	inRequireBlock := false
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "//") {
			continue
		}
		if strings.HasPrefix(line, "require (") {
			inRequireBlock = true
			continue
		}
		if inRequireBlock && line == ")" {
			inRequireBlock = false
			continue
		}
		if strings.HasPrefix(line, "require ") {
			module := parseGoModRequireLine(strings.TrimSpace(strings.TrimPrefix(line, "require ")))
			if module != "" {
				section := "require"
				if strings.Contains(line, "// indirect") {
					section = "require_indirect"
				}
				sections[section] = append(sections[section], module)
			}
			continue
		}
		if inRequireBlock {
			module := parseGoModRequireLine(line)
			if module != "" {
				section := "require"
				if strings.Contains(line, "// indirect") {
					section = "require_indirect"
				}
				sections[section] = append(sections[section], module)
			}
		}
	}
	return sections
}

func parseGoModRequireLine(line string) string {
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimSpace(fields[0])
}

func parsePackageJSONDependencies(content string) map[string][]string {
	type packageJSON struct {
		Dependencies         map[string]any `json:"dependencies"`
		DevDependencies      map[string]any `json:"devDependencies"`
		PeerDependencies     map[string]any `json:"peerDependencies"`
		OptionalDependencies map[string]any `json:"optionalDependencies"`
	}
	var payload packageJSON
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return nil
	}
	sections := map[string][]string{}
	sections["dependencies"] = mapKeys(payload.Dependencies)
	sections["devDependencies"] = mapKeys(payload.DevDependencies)
	sections["peerDependencies"] = mapKeys(payload.PeerDependencies)
	sections["optionalDependencies"] = mapKeys(payload.OptionalDependencies)
	return sections
}

func parsePyProjectDependencies(content string) map[string][]string {
	sections := map[string][]string{}
	currentSection := ""
	pendingKey := ""
	var pendingValues []string
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.Trim(line, "[]")
			pendingKey = ""
			pendingValues = nil
			continue
		}

		if pendingKey != "" {
			pendingValues = append(pendingValues, parseQuotedStrings(line)...)
			if strings.Contains(line, "]") {
				sections[pendingKey] = append(sections[pendingKey], normalizePythonDependencyNames(pendingValues)...)
				pendingKey = ""
				pendingValues = nil
			}
			continue
		}

		key, value, ok := splitTomlAssignment(line)
		if !ok {
			continue
		}
		sectionKey := pyProjectSectionKey(currentSection, key)
		if sectionKey == "" {
			continue
		}
		if strings.HasPrefix(strings.TrimSpace(value), "[") && !strings.Contains(value, "]") {
			pendingKey = sectionKey
			pendingValues = parseQuotedStrings(value)
			continue
		}
		sections[sectionKey] = append(sections[sectionKey], normalizePythonDependencyNames(parseQuotedStrings(value))...)
	}
	return sections
}

func pyProjectSectionKey(section, key string) string {
	section = strings.TrimSpace(section)
	key = strings.TrimSpace(key)
	switch {
	case section == "project" && key == "dependencies":
		return "project.dependencies"
	case section == "project.optional-dependencies":
		return "project.optional-dependencies." + key
	case section == "dependency-groups":
		return "dependency-groups." + key
	default:
		return ""
	}
}

func parseRequirementsDependencies(content string) map[string][]string {
	deps := make([]string, 0)
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "-") {
			continue
		}
		line = strings.SplitN(line, " #", 2)[0]
		if name := normalizePythonDependencyName(line); name != "" {
			deps = append(deps, name)
		}
	}
	return map[string][]string{"requirements": deps}
}

func parseCargoDependencies(content string) map[string][]string {
	sections := map[string][]string{}
	currentSection := ""
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			currentSection = strings.Trim(line, "[]")
			continue
		}
		if !strings.Contains(currentSection, "dependencies") {
			continue
		}
		key, _, ok := splitTomlAssignment(line)
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		if key != "" {
			sections[currentSection] = append(sections[currentSection], key)
		}
	}
	return sections
}

func parseGemfileDependencies(content string) map[string][]string {
	deps := make([]string, 0)
	for _, rawLine := range strings.Split(content, "\n") {
		line := strings.TrimSpace(rawLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		match := gemfileLinePattern.FindStringSubmatch(line)
		if len(match) == 2 {
			deps = append(deps, match[1])
		}
	}
	return map[string][]string{"gem": deps}
}

func splitTomlAssignment(line string) (string, string, bool) {
	parts := strings.SplitN(line, "=", 2)
	if len(parts) != 2 {
		return "", "", false
	}
	return strings.TrimSpace(parts[0]), strings.TrimSpace(parts[1]), true
}

func parseQuotedStrings(value string) []string {
	matches := quotedStringPattern.FindAllStringSubmatch(value, -1)
	result := make([]string, 0, len(matches))
	for _, match := range matches {
		candidate := ""
		if len(match) > 1 && match[1] != "" {
			candidate = match[1]
		} else if len(match) > 2 {
			candidate = match[2]
		}
		candidate = strings.TrimSpace(candidate)
		if candidate != "" {
			result = append(result, candidate)
		}
	}
	return result
}

func normalizePythonDependencyNames(values []string) []string {
	result := make([]string, 0, len(values))
	for _, value := range values {
		if name := normalizePythonDependencyName(value); name != "" {
			result = append(result, name)
		}
	}
	return result
}

func normalizePythonDependencyName(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if match := pep508NamePattern.FindString(value); match != "" {
		return match
	}
	return ""
}

func mapKeys(items map[string]any) []string {
	if len(items) == 0 {
		return nil
	}
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func dedupeSortedStrings(items []string) []string {
	if len(items) == 0 {
		return nil
	}
	sorted := append([]string(nil), items...)
	sort.Strings(sorted)
	result := make([]string, 0, len(sorted))
	for _, item := range sorted {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		if len(result) > 0 && result[len(result)-1] == item {
			continue
		}
		result = append(result, item)
	}
	return result
}
