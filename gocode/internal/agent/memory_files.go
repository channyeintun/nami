package agent

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/channyeintun/gocode/internal/config"
)

// MemoryFile represents a loaded instruction or memory index file.
type MemoryFile struct {
	Path      string
	Type      string
	Content   string
	UpdatedAt time.Time
}

const (
	memoryTypeProject      = "project"
	memoryTypeLocal        = "local"
	memoryTypeProjectIndex = "project-index"
	memoryTypeUserIndex    = "user-index"

	maxMemoryFileBytes  = 40_000
	maxMemoryIndexBytes = 25_000
	maxMemoryIndexLines = 200
	maxMemoryFiles      = 20
)

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)

// LoadMemoryFiles discovers and loads shared instruction files and durable memory indexes.
//
// Priority order:
//  1. User memory index: ~/.config/gocode/memory/MEMORY.md
//  2. Project memory index: ~/.config/gocode/projects/{slug}/memory/MEMORY.md
//  3. Project instructions: AGENTS.md (walking up from cwd to root)
//  4. Local instructions: AGENTS.local.md (walking up from cwd to root)
//
// Files closer to the working directory have higher priority and are loaded later.
func LoadMemoryFiles() []MemoryFile {
	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}

	var files []MemoryFile
	files = appendConfigMemoryIndexes(files, cwd)
	dirs := walkUpDirs(cwd)

	for i := len(dirs) - 1; i >= 0; i-- {
		dir := dirs[i]
		files = appendProjectFiles(files, dir)
	}

	if len(files) > maxMemoryFiles {
		files = files[len(files)-maxMemoryFiles:]
	}

	return files
}

func appendConfigMemoryIndexes(files []MemoryFile, cwd string) []MemoryFile {
	if content, err := readMemoryIndex(userMemoryIndexPath()); err == nil {
		files = append(files, MemoryFile{
			Path:      userMemoryIndexPath(),
			Type:      memoryTypeUserIndex,
			Content:   content,
			UpdatedAt: fileUpdatedAt(userMemoryIndexPath()),
		})
	}

	projectIndexPath := projectMemoryIndexPath(cwd)
	if content, err := readMemoryIndex(projectIndexPath); err == nil {
		files = append(files, MemoryFile{
			Path:      projectIndexPath,
			Type:      memoryTypeProjectIndex,
			Content:   content,
			UpdatedAt: fileUpdatedAt(projectIndexPath),
		})
	}

	return files
}

func appendProjectFiles(files []MemoryFile, dir string) []MemoryFile {
	if content, err := readMemoryFile(filepath.Join(dir, "AGENTS.md")); err == nil {
		path := filepath.Join(dir, "AGENTS.md")
		files = append(files, MemoryFile{Path: path, Type: memoryTypeProject, Content: content, UpdatedAt: fileUpdatedAt(path)})
	}

	if content, err := readMemoryFile(filepath.Join(dir, "AGENTS.local.md")); err == nil {
		path := filepath.Join(dir, "AGENTS.local.md")
		files = append(files, MemoryFile{Path: path, Type: memoryTypeLocal, Content: content, UpdatedAt: fileUpdatedAt(path)})
	}

	return files
}

func walkUpDirs(start string) []string {
	var dirs []string
	dir := filepath.Clean(start)
	for {
		dirs = append(dirs, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return dirs
}

func readMemoryFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return "", os.ErrNotExist
	}
	if len(content) > maxMemoryFileBytes {
		content = content[:maxMemoryFileBytes] + "\n[truncated]"
	}
	return content, nil
}

func readMemoryIndex(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return "", os.ErrNotExist
	}

	lines := strings.Split(content, "\n")
	truncated := false
	if len(lines) > maxMemoryIndexLines {
		lines = lines[:maxMemoryIndexLines]
		truncated = true
	}
	content = strings.TrimSpace(strings.Join(lines, "\n"))
	if len(content) > maxMemoryIndexBytes {
		content = strings.TrimSpace(content[:maxMemoryIndexBytes])
		truncated = true
	}
	if truncated {
		content += "\n[truncated memory index]"
	}
	return content, nil
}

// FormatMemoryPrompt renders loaded instruction files into a system prompt section.
func FormatMemoryPrompt(files []MemoryFile) string {
	if len(files) == 0 {
		return ""
	}

	instructions := make([]MemoryFile, 0, len(files))
	memoryIndexes := make([]MemoryFile, 0, len(files))
	for _, f := range files {
		switch f.Type {
		case memoryTypeProjectIndex, memoryTypeUserIndex:
			memoryIndexes = append(memoryIndexes, f)
		default:
			instructions = append(instructions, f)
		}
	}

	var b strings.Builder
	if len(instructions) > 0 {
		b.WriteString("Project instructions are shown below. Be sure to adhere to these instructions. IMPORTANT: These instructions override default behavior and should be followed exactly when applicable.\n\n")

		for _, f := range instructions {
			b.WriteString("<memory_file path=\"")
			b.WriteString(f.Path)
			b.WriteString("\" type=\"")
			b.WriteString(f.Type)
			b.WriteString("\">\n")
			b.WriteString(f.Content)
			b.WriteString("\n</memory_file>\n\n")
		}
	}

	if len(memoryIndexes) > 0 {
		b.WriteString("Durable memory indexes are shown below. Treat them as selectively relevant context, not as unconditional instructions. Prefer recent, project-specific entries when they help, and verify details against the live repository when needed.\n\n")

		for _, f := range memoryIndexes {
			b.WriteString("<memory_file path=\"")
			b.WriteString(f.Path)
			b.WriteString("\" type=\"")
			b.WriteString(f.Type)
			b.WriteString("\">\n")
			b.WriteString(formatMemoryIndexContent(f))
			b.WriteString("\n</memory_file>\n\n")
		}
	}

	return strings.TrimSpace(b.String())
}

func userMemoryIndexPath() string {
	return filepath.Join(config.ConfigDir(), "memory", "MEMORY.md")
}

func projectMemoryIndexPath(cwd string) string {
	projectRoot := findProjectScopeRoot(cwd)
	return filepath.Join(config.ConfigDir(), "projects", projectSlug(projectRoot), "memory", "MEMORY.md")
}

func findProjectScopeRoot(start string) string {
	for _, dir := range walkUpDirs(start) {
		if _, err := os.Stat(filepath.Join(dir, ".git")); err == nil {
			return dir
		}
	}
	return filepath.Clean(start)
}

func projectSlug(root string) string {
	cleaned := filepath.Clean(root)
	base := strings.ToLower(filepath.Base(cleaned))
	base = nonSlugChars.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" || base == "." || base == string(filepath.Separator) {
		base = "project"
	}
	if len(base) > 32 {
		base = strings.Trim(base[:32], "-")
	}

	hasher := fnv.New32a()
	_, _ = hasher.Write([]byte(cleaned))
	return fmt.Sprintf("%s-%08x", base, hasher.Sum32())
}

func fileUpdatedAt(path string) time.Time {
	info, err := os.Stat(path)
	if err != nil {
		return time.Time{}
	}
	return info.ModTime()
}

func formatMemoryIndexContent(file MemoryFile) string {
	note := memoryAgeNote(file.UpdatedAt)
	if note == "" {
		return file.Content
	}
	return note + "\n" + file.Content
}

func memoryAgeNote(updatedAt time.Time) string {
	if updatedAt.IsZero() {
		return ""
	}

	age := time.Since(updatedAt)
	if age < 48*time.Hour {
		return ""
	}

	return fmt.Sprintf("[staleness-warning] This memory index was last updated %s ago. Treat it as historical context and verify important details against the live repository.", formatMemoryAge(age))
}

func formatMemoryAge(age time.Duration) string {
	if age < 7*24*time.Hour {
		days := int(age / (24 * time.Hour))
		if days <= 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}

	weeks := int(age / (7 * 24 * time.Hour))
	if weeks < 5 {
		if weeks <= 1 {
			return "1 week"
		}
		return fmt.Sprintf("%d weeks", weeks)
	}

	months := int(age / (30 * 24 * time.Hour))
	if months <= 1 {
		return "1 month"
	}
	return fmt.Sprintf("%d months", months)
}
