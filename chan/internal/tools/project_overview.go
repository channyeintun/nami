package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const (
	defaultProjectOverviewLimit   = 5000
	defaultProjectOverviewResults = 8
)

var projectOverviewSkippedDirs = map[string]struct{}{
	".git":         {},
	".next":        {},
	".turbo":       {},
	"build":        {},
	"coverage":     {},
	"dist":         {},
	"node_modules": {},
	"target":       {},
	"vendor":       {},
}

var projectOverviewManifestNames = map[string]struct{}{
	"Cargo.toml":        {},
	"Dockerfile":        {},
	"Makefile":          {},
	"README":            {},
	"README.md":         {},
	"README.txt":        {},
	"build.gradle":      {},
	"compose.yaml":      {},
	"compose.yml":       {},
	"deno.json":         {},
	"deno.jsonc":        {},
	"go.mod":            {},
	"go.work":           {},
	"package-lock.json": {},
	"package.json":      {},
	"pnpm-lock.yaml":    {},
	"pyproject.toml":    {},
	"requirements.txt":  {},
	"tsconfig.json":     {},
	"vite.config.ts":    {},
	"yarn.lock":         {},
	"bun.lock":          {},
	"bun.lockb":         {},
}

type projectOverview struct {
	RootPath         string                   `json:"root_path"`
	FilesScanned     int                      `json:"files_scanned"`
	Truncated        bool                     `json:"truncated"`
	ManifestFiles    []string                 `json:"manifest_files,omitempty"`
	TopLevelSections []projectSectionSummary  `json:"top_level_sections,omitempty"`
	LanguageCounts   []projectLanguageSummary `json:"language_counts,omitempty"`
	NotableFiles     []string                 `json:"notable_files,omitempty"`
}

type projectSectionSummary struct {
	Name  string `json:"name"`
	Files int    `json:"files"`
}

type projectLanguageSummary struct {
	Language string `json:"language"`
	Files    int    `json:"files"`
}

// ProjectOverviewTool provides a compact structural summary of a repository.
type ProjectOverviewTool struct{}

func NewProjectOverviewTool() *ProjectOverviewTool {
	return &ProjectOverviewTool{}
}

func (t *ProjectOverviewTool) Name() string {
	return "project_overview"
}

func (t *ProjectOverviewTool) Description() string {
	return "Summarize a repository's structure, manifests, dominant languages, and top-level sections."
}

func (t *ProjectOverviewTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "Optional file or directory to inspect. Defaults to the current working directory.",
			},
			"max_files": map[string]any{
				"type":        "integer",
				"description": "Optional cap on scanned files. Defaults to 5000.",
				"minimum":     1,
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Optional number of language and section rows to return. Defaults to 8.",
				"minimum":     1,
			},
		},
	}
}

func (t *ProjectOverviewTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *ProjectOverviewTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *ProjectOverviewTool) Validate(input ToolInput) error {
	if value, ok := intParam(input.Params, "max_files"); ok && value < 1 {
		return fmt.Errorf("max_files must be >= 1")
	}
	if value, ok := intParam(input.Params, "max_results"); ok && value < 1 {
		return fmt.Errorf("max_results must be >= 1")
	}
	return nil
}

func (t *ProjectOverviewTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	searchPath, err := resolveSearchPath(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}

	rootPath, err := normalizeOverviewRoot(searchPath)
	if err != nil {
		return ToolOutput{}, err
	}

	maxFiles := intOrDefault(input.Params, "max_files", defaultProjectOverviewLimit)
	maxResults := intOrDefault(input.Params, "max_results", defaultProjectOverviewResults)

	manifestSet := make(map[string]struct{})
	notableSet := make(map[string]struct{})
	topLevelCounts := make(map[string]int)
	languageCounts := make(map[string]int)
	filesScanned := 0
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

		filesScanned++
		if filesScanned > maxFiles {
			truncated = true
			return filepath.SkipAll
		}

		relPath, err := filepath.Rel(rootPath, path)
		if err != nil {
			return nil
		}
		baseName := filepath.Base(path)
		if _, ok := projectOverviewManifestNames[baseName]; ok {
			manifestSet[filepath.ToSlash(relPath)] = struct{}{}
		}
		if isNotableOverviewFile(baseName, relPath) {
			notableSet[filepath.ToSlash(relPath)] = struct{}{}
		}
		section := topLevelSection(relPath)
		topLevelCounts[section]++
		language := languageForOverviewFile(baseName, filepath.Ext(baseName))
		if language != "" {
			languageCounts[language]++
		}
		return nil
	})
	if walkErr != nil && walkErr != filepath.SkipAll {
		return ToolOutput{}, walkErr
	}
	if walkErr == ctx.Err() {
		return ToolOutput{}, ctx.Err()
	}

	summary := projectOverview{
		RootPath:         rootPath,
		FilesScanned:     min(filesScanned, maxFiles),
		Truncated:        truncated,
		ManifestFiles:    sortedKeys(manifestSet),
		TopLevelSections: sortSectionSummaries(topLevelCounts, maxResults),
		LanguageCounts:   sortLanguageSummaries(languageCounts, maxResults),
		NotableFiles:     limitedStrings(sortedKeys(notableSet), maxResults),
	}

	encoded, err := json.MarshalIndent(summary, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal project_overview: %w", err)
	}

	return ToolOutput{Output: string(encoded), Truncated: truncated}, nil
}

func normalizeOverviewRoot(searchPath string) (string, error) {
	info, err := os.Stat(searchPath)
	if err != nil {
		return "", fmt.Errorf("stat path %q: %w", searchPath, err)
	}
	if info.IsDir() {
		return searchPath, nil
	}
	return filepath.Dir(searchPath), nil
}

func shouldSkipOverviewDir(name, path, rootPath string) bool {
	if path == rootPath {
		return false
	}
	_, skip := projectOverviewSkippedDirs[name]
	return skip
}

func isNotableOverviewFile(baseName, relPath string) bool {
	if strings.EqualFold(baseName, "README.md") || strings.EqualFold(baseName, "README") {
		return true
	}
	if strings.EqualFold(baseName, "main.go") || strings.EqualFold(baseName, "main.py") || strings.EqualFold(baseName, "main.ts") {
		return true
	}
	if strings.HasSuffix(strings.ToLower(relPath), "/main.go") || strings.HasSuffix(strings.ToLower(relPath), "/main.ts") {
		return true
	}
	return false
}

func topLevelSection(relPath string) string {
	clean := filepath.ToSlash(relPath)
	parts := strings.Split(clean, "/")
	if len(parts) <= 1 {
		return "."
	}
	return parts[0]
}

func languageForOverviewFile(baseName, extension string) string {
	switch strings.ToLower(extension) {
	case ".go":
		return "Go"
	case ".ts", ".tsx":
		return "TypeScript"
	case ".js", ".jsx", ".mjs", ".cjs":
		return "JavaScript"
	case ".py":
		return "Python"
	case ".rs":
		return "Rust"
	case ".java":
		return "Java"
	case ".rb":
		return "Ruby"
	case ".php":
		return "PHP"
	case ".c", ".h":
		return "C"
	case ".cc", ".cpp", ".cxx", ".hpp", ".hh":
		return "C++"
	case ".cs":
		return "C#"
	case ".swift":
		return "Swift"
	case ".kt":
		return "Kotlin"
	case ".scala":
		return "Scala"
	case ".sh", ".bash", ".zsh":
		return "Shell"
	case ".sql":
		return "SQL"
	case ".md":
		return "Markdown"
	case ".json":
		return "JSON"
	case ".yaml", ".yml":
		return "YAML"
	case ".toml":
		return "TOML"
	case ".css", ".scss", ".sass":
		return "CSS"
	case ".html":
		return "HTML"
	}
	if baseName == "Dockerfile" {
		return "Docker"
	}
	if baseName == "Makefile" {
		return "Make"
	}
	return ""
}

func sortSectionSummaries(counts map[string]int, limit int) []projectSectionSummary {
	summaries := make([]projectSectionSummary, 0, len(counts))
	for name, files := range counts {
		summaries = append(summaries, projectSectionSummary{Name: name, Files: files})
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Files != summaries[j].Files {
			return summaries[i].Files > summaries[j].Files
		}
		return summaries[i].Name < summaries[j].Name
	})
	if len(summaries) > limit {
		summaries = summaries[:limit]
	}
	return summaries
}

func sortLanguageSummaries(counts map[string]int, limit int) []projectLanguageSummary {
	summaries := make([]projectLanguageSummary, 0, len(counts))
	for name, files := range counts {
		summaries = append(summaries, projectLanguageSummary{Language: name, Files: files})
	}
	sort.Slice(summaries, func(i, j int) bool {
		if summaries[i].Files != summaries[j].Files {
			return summaries[i].Files > summaries[j].Files
		}
		return summaries[i].Language < summaries[j].Language
	})
	if len(summaries) > limit {
		summaries = summaries[:limit]
	}
	return summaries
}

func sortedKeys(items map[string]struct{}) []string {
	keys := make([]string, 0, len(items))
	for key := range items {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func limitedStrings(items []string, limit int) []string {
	if len(items) > limit {
		return items[:limit]
	}
	return items
}
