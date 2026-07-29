package swarm

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/channyeintun/nami/internal/config"
)

type PromptOverlay struct {
	Role        string
	ProjectRoot string
	Files       []string
	Content     string
}

func LoadRolePromptOverlay(cwd string, role string) (PromptOverlay, error) {
	projectRoot := config.FindProjectRoot(cwd)
	role = normalizeRoleName(role)
	if strings.TrimSpace(projectRoot) == "" || role == "" {
		return PromptOverlay{}, os.ErrNotExist
	}

	baseDir := filepath.Join(projectRoot, ".nami", "swarm")
	loadedFiles := make([]string, 0, 4)
	sections := make([]promptSection, 0, 4)

	appendIfPresent := func(path string) error {
		content, err := readPromptFile(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return err
		}
		loadedFiles = append(loadedFiles, path)
		sections = append(sections, promptSection{Path: path, Content: content})
		return nil
	}

	if err := appendIfPresent(filepath.Join(baseDir, "constitution.md")); err != nil {
		return PromptOverlay{}, err
	}

	constitutionDir := filepath.Join(baseDir, "constitution")
	constitutionFiles, err := markdownFilesUnder(constitutionDir)
	if err != nil {
		return PromptOverlay{}, err
	}
	for _, path := range constitutionFiles {
		if err := appendIfPresent(path); err != nil {
			return PromptOverlay{}, err
		}
	}

	if err := appendIfPresent(filepath.Join(baseDir, "roles", role+".md")); err != nil {
		return PromptOverlay{}, err
	}

	if len(sections) == 0 {
		return PromptOverlay{}, os.ErrNotExist
	}

	return PromptOverlay{
		Role:        role,
		ProjectRoot: projectRoot,
		Files:       loadedFiles,
		Content:     renderPromptOverlay(role, projectRoot, sections),
	}, nil
}

type promptSection struct {
	Path    string
	Content string
}

func markdownFilesUnder(root string) ([]string, error) {
	entries := make([]string, 0)
	if strings.TrimSpace(root) == "" {
		return entries, nil
	}
	if _, err := os.Stat(root); err != nil {
		if os.IsNotExist(err) {
			return entries, nil
		}
		return nil, err
	}
	if err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if strings.EqualFold(filepath.Ext(path), ".md") {
			entries = append(entries, path)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	sort.Strings(entries)
	return entries, nil
}

func readPromptFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return "", os.ErrNotExist
	}
	return content, nil
}

func renderPromptOverlay(role string, projectRoot string, sections []promptSection) string {
	var b strings.Builder
	b.WriteString("Project-local swarm overlay for role \"")
	b.WriteString(role)
	b.WriteString("\".\n")
	b.WriteString("These instructions refine the existing subagent safety baseline and delegated task instructions. Follow them when applicable, but do not violate the base tool, permission, and safety constraints.\n")
	if strings.TrimSpace(projectRoot) != "" {
		b.WriteString("Project root: ")
		b.WriteString(projectRoot)
		b.WriteString("\n")
	}
	for _, section := range sections {
		relPath := strings.TrimPrefix(section.Path, projectRoot)
		relPath = strings.TrimPrefix(relPath, string(filepath.Separator))
		b.WriteString("\n<swarm_overlay_file path=\"")
		b.WriteString(relPath)
		b.WriteString("\">\n")
		b.WriteString(section.Content)
		b.WriteString("\n</swarm_overlay_file>\n")
	}
	return strings.TrimSpace(b.String())
}

func JoinPromptSections(sections ...string) string {
	nonEmpty := make([]string, 0, len(sections))
	for _, section := range sections {
		trimmed := strings.TrimSpace(section)
		if trimmed == "" {
			continue
		}
		nonEmpty = append(nonEmpty, trimmed)
	}
	if len(nonEmpty) == 0 {
		return ""
	}
	return strings.Join(nonEmpty, "\n\n")
}
