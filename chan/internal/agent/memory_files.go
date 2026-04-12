package agent

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"sort"
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

// MemoryRecallResult holds recalled index lines for a specific durable memory file.
type MemoryRecallResult struct {
	Path   string
	Lines  []string
	Source string
}

// MemoryRecallEntrySummary describes one recalled durable memory entry for UI or telemetry surfaces.
type MemoryRecallEntrySummary struct {
	Title     string
	NoteType  string
	Source    string
	IndexPath string
	NotePath  string
	Line      string
}

// MemoryIndexEntry represents one parsed MEMORY.md index entry.
type MemoryIndexEntry struct {
	IndexPath string
	RawLine   string
	Filename  string
	Title     string
	NoteType  string
	NotePath  string
	Order     int
	Issue     string
}

const (
	memoryTypeProject       = "project"
	memoryTypeLocal         = "local"
	memoryTypeProjectIndex  = "project-index"
	memoryTypeUserIndex     = "user-index"
	memoryTypeUserNote      = "user"
	memoryTypeFeedbackNote  = "feedback"
	memoryTypeProjectNote   = "project"
	memoryTypeReferenceNote = "reference"

	maxMemoryFileBytes     = 40_000
	maxMemoryIndexBytes    = 25_000
	maxMemoryIndexLines    = 200
	maxMemoryFiles         = 20
	maxRelevantMemoryLines = 8
	maxRecallTerms         = 12
	maxMemoryNoteLines     = 12
	maxMemoryNoteBytes     = 2_000
)

var nonSlugChars = regexp.MustCompile(`[^a-z0-9]+`)
var recallTokenPattern = regexp.MustCompile(`[a-z0-9][a-z0-9_\-/]{1,}`)

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
func FormatMemoryPrompt(files []MemoryFile, currentUserPrompt string, recalls []MemoryRecallResult) string {
	instructions := make([]MemoryFile, 0, len(files))
	memoryIndexes := make([]MemoryFile, 0, len(files))
	recallByPath := memoryRecallLookup(recalls)
	for _, f := range files {
		switch f.Type {
		case memoryTypeProjectIndex, memoryTypeUserIndex:
			memoryIndexes = append(memoryIndexes, f)
		default:
			instructions = append(instructions, f)
		}
	}

	var b strings.Builder
	writeGuidance := formatMemoryWriteGuidance()
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
			recalledContent := formatRelevantMemoryIndexContent(f, currentUserPrompt, recallByPath[f.Path])
			if strings.TrimSpace(recalledContent) == "" {
				continue
			}
			b.WriteString("<memory_file path=\"")
			b.WriteString(f.Path)
			b.WriteString("\" type=\"")
			b.WriteString(f.Type)
			b.WriteString("\">\n")
			b.WriteString(recalledContent)
			b.WriteString("\n</memory_file>\n\n")
		}
	}

	if writeGuidance != "" {
		if b.Len() > 0 {
			b.WriteString("\n\n")
		}
		b.WriteString(writeGuidance)
	}

	if b.Len() == 0 {
		return ""
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

func userMemoryDirPath() string {
	return filepath.Join(config.ConfigDir(), "memory")
}

func projectMemoryDirPath(cwd string) string {
	projectRoot := findProjectScopeRoot(cwd)
	return filepath.Join(config.ConfigDir(), "projects", projectSlug(projectRoot), "memory")
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

func formatRelevantMemoryIndexContent(file MemoryFile, currentUserPrompt string, recalled MemoryRecallResult) string {
	selectedLines := recalled.Lines
	selectionSource := strings.TrimSpace(recalled.Source)
	if len(selectedLines) == 0 {
		selectedLines = selectRelevantMemoryLines(file.Content, currentUserPrompt)
		selectionSource = "heuristic fallback"
	}
	if len(selectedLines) == 0 {
		return ""
	}

	parts := make([]string, 0, len(selectedLines)+2)
	if note := memoryAgeNote(file.UpdatedAt); note != "" {
		parts = append(parts, note)
	}
	parts = append(parts, formatMemoryIndexValidationWarnings(file)...)
	parts = append(parts, fmt.Sprintf("[memory-recall] Selected %d relevant index entr%s for the current request via %s.", len(selectedLines), pluralSuffix(len(selectedLines), "y", "ies"), selectionSource))
	parts = append(parts, formatRecalledMemoryEntries(file, selectedLines)...)
	return strings.Join(parts, "\n")
}

// ParseMemoryIndexEntries parses canonical MEMORY.md bullet entries and preserves raw-line fallbacks.
func ParseMemoryIndexEntries(file MemoryFile) []MemoryIndexEntry {
	lines := strings.Split(file.Content, "\n")
	entries := make([]MemoryIndexEntry, 0, len(lines))
	for idx, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "[truncated") {
			continue
		}

		entry := MemoryIndexEntry{
			IndexPath: file.Path,
			RawLine:   line,
			Order:     idx,
		}
		if filename, title, noteType, ok := parseMemoryIndexEntryLine(line); ok {
			entry.Filename = filename
			entry.Title = title
			entry.NoteType = noteType
			entry.NotePath, entry.Issue = resolveMemoryNotePath(file.Path, filename)
		} else if looksLikeMalformedMemoryEntry(line) {
			entry.Issue = "Malformed MEMORY.md entry. Expected '- [file.md] Title (type)'."
		}
		entries = append(entries, entry)
	}
	return entries
}

func looksLikeMalformedMemoryEntry(line string) bool {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "- ")
	trimmed = strings.TrimPrefix(trimmed, "* ")
	if trimmed == "" {
		return false
	}
	return strings.Contains(trimmed, "[") || strings.Contains(trimmed, "]") || strings.Contains(trimmed, "(") || strings.Contains(trimmed, ")")
}

func resolveMemoryNotePath(indexPath, filename string) (string, string) {
	baseDir := filepath.Clean(filepath.Dir(indexPath))
	resolved := filepath.Clean(filepath.Join(baseDir, filename))
	if !pathWithinBaseDir(baseDir, resolved) {
		return "", "Referenced memory note resolves outside the memory directory and was skipped."
	}
	if _, err := os.Stat(resolved); err != nil {
		if os.IsNotExist(err) {
			return resolved, "Referenced memory note file does not exist."
		}
		return resolved, fmt.Sprintf("Referenced memory note could not be read: %v", err)
	}
	return resolved, ""
}

func pathWithinBaseDir(baseDir, candidate string) bool {
	if baseDir == candidate {
		return true
	}
	baseWithSep := baseDir + string(filepath.Separator)
	return strings.HasPrefix(candidate, baseWithSep)
}

func parseMemoryIndexEntryLine(line string) (filename, title, noteType string, ok bool) {
	trimmed := strings.TrimSpace(line)
	trimmed = strings.TrimPrefix(trimmed, "- ")
	trimmed = strings.TrimPrefix(trimmed, "* ")
	if !strings.HasPrefix(trimmed, "[") {
		return "", "", "", false
	}

	closeIdx := strings.Index(trimmed, "]")
	if closeIdx <= 1 {
		return "", "", "", false
	}
	filename = strings.TrimSpace(trimmed[1:closeIdx])
	remaining := strings.TrimSpace(trimmed[closeIdx+1:])
	if filename == "" || remaining == "" || !strings.HasSuffix(remaining, ")") {
		return "", "", "", false
	}

	openTypeIdx := strings.LastIndex(remaining, " (")
	if openTypeIdx <= 0 {
		return "", "", "", false
	}
	title = strings.TrimSpace(remaining[:openTypeIdx])
	noteType = strings.TrimSpace(remaining[openTypeIdx+2 : len(remaining)-1])
	if title == "" || !isKnownMemoryNoteType(noteType) {
		return "", "", "", false
	}
	return filename, title, noteType, true
}

func isKnownMemoryNoteType(value string) bool {
	switch strings.TrimSpace(value) {
	case memoryTypeUserNote, memoryTypeFeedbackNote, memoryTypeProjectNote, memoryTypeReferenceNote:
		return true
	default:
		return false
	}
}

func formatRecalledMemoryEntries(file MemoryFile, selectedLines []string) []string {
	entries := ParseMemoryIndexEntries(file)
	entryByLine := make(map[string]MemoryIndexEntry, len(entries))
	for _, entry := range entries {
		entryByLine[entry.RawLine] = entry
	}

	parts := make([]string, 0, len(selectedLines)*2)
	for _, line := range selectedLines {
		parts = append(parts, line)
		entry, ok := entryByLine[line]
		if !ok || strings.TrimSpace(entry.NotePath) == "" {
			continue
		}
		if strings.TrimSpace(entry.Issue) != "" {
			parts = append(parts, fmt.Sprintf("[memory-index-warning] %s Entry: %s", entry.Issue, entry.RawLine))
			continue
		}
		excerpt := loadMemoryNoteExcerpt(entry.NotePath)
		if excerpt == "" {
			parts = append(parts, fmt.Sprintf("[memory-index-warning] Referenced memory note could not be loaded. Entry: %s", entry.RawLine))
			continue
		}
		parts = append(parts, fmt.Sprintf("<memory_note path=\"%s\" type=\"%s\">\n%s\n</memory_note>", entry.NotePath, entry.NoteType, excerpt))
	}
	return parts
}

func formatMemoryIndexValidationWarnings(file MemoryFile) []string {
	entries := ParseMemoryIndexEntries(file)
	issues := make([]string, 0, 3)
	invalidCount := 0
	for _, entry := range entries {
		if strings.TrimSpace(entry.Issue) == "" {
			continue
		}
		invalidCount++
		if len(issues) < 3 {
			issues = append(issues, fmt.Sprintf("[memory-index-warning] %s Entry: %s", entry.Issue, entry.RawLine))
		}
	}
	if invalidCount == 0 {
		return nil
	}
	if invalidCount > len(issues) {
		issues = append(issues, fmt.Sprintf("[memory-index-warning] %d additional invalid MEMORY.md entr%s were skipped.", invalidCount-len(issues), pluralSuffix(invalidCount-len(issues), "y", "ies")))
	}
	return issues
}

func loadMemoryNoteExcerpt(path string) string {
	content, err := readMemoryFile(path)
	if err != nil {
		return ""
	}
	content = stripMemoryFrontmatter(content)
	if strings.TrimSpace(content) == "" {
		return ""
	}

	lines := strings.Split(content, "\n")
	if len(lines) > maxMemoryNoteLines {
		lines = lines[:maxMemoryNoteLines]
	}
	excerpt := strings.TrimSpace(strings.Join(lines, "\n"))
	if len(excerpt) > maxMemoryNoteBytes {
		excerpt = strings.TrimSpace(excerpt[:maxMemoryNoteBytes]) + "\n[truncated memory note]"
	}
	return excerpt
}

func stripMemoryFrontmatter(content string) string {
	lines := strings.Split(content, "\n")
	if len(lines) < 3 || strings.TrimSpace(lines[0]) != "---" {
		return content
	}
	for idx := 1; idx < len(lines); idx++ {
		if strings.TrimSpace(lines[idx]) == "---" {
			return strings.TrimSpace(strings.Join(lines[idx+1:], "\n"))
		}
	}
	return content
}

func memoryRecallLookup(recalls []MemoryRecallResult) map[string]MemoryRecallResult {
	if len(recalls) == 0 {
		return nil
	}
	lookup := make(map[string]MemoryRecallResult, len(recalls))
	for _, recall := range recalls {
		if strings.TrimSpace(recall.Path) == "" {
			continue
		}
		lookup[recall.Path] = MemoryRecallResult{
			Path:   recall.Path,
			Lines:  append([]string(nil), recall.Lines...),
			Source: recall.Source,
		}
	}
	return lookup
}

// SummarizeMemoryRecalls maps recalled MEMORY.md lines back to parsed metadata for compact UI surfaces.
func SummarizeMemoryRecalls(files []MemoryFile, recalls []MemoryRecallResult) []MemoryRecallEntrySummary {
	if len(files) == 0 || len(recalls) == 0 {
		return nil
	}

	fileByPath := make(map[string]MemoryFile, len(files))
	for _, file := range files {
		fileByPath[file.Path] = file
	}

	summaries := make([]MemoryRecallEntrySummary, 0, len(recalls)*2)
	seen := make(map[string]struct{}, len(recalls)*2)
	for _, recall := range recalls {
		file, ok := fileByPath[recall.Path]
		if !ok {
			continue
		}
		entryByLine := make(map[string]MemoryIndexEntry)
		for _, entry := range ParseMemoryIndexEntries(file) {
			entryByLine[entry.RawLine] = entry
		}

		for _, rawLine := range recall.Lines {
			line := strings.TrimSpace(rawLine)
			if line == "" {
				continue
			}
			key := recall.Path + "\n" + line
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}

			summary := MemoryRecallEntrySummary{
				Title:     memoryRecallSummaryTitle(line),
				Source:    strings.TrimSpace(recall.Source),
				IndexPath: recall.Path,
				Line:      line,
			}
			if entry, ok := entryByLine[line]; ok {
				if strings.TrimSpace(entry.Title) != "" {
					summary.Title = entry.Title
				}
				summary.NoteType = entry.NoteType
				summary.NotePath = entry.NotePath
			}
			summaries = append(summaries, summary)
		}
	}

	return summaries
}

func memoryRecallSummaryTitle(line string) string {
	if _, title, _, ok := parseMemoryIndexEntryLine(line); ok && strings.TrimSpace(title) != "" {
		return title
	}
	trimmed := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(line, "- "), "* "))
	if trimmed == "" {
		return "recalled memory"
	}
	return trimmed
}

func selectRelevantMemoryLines(content, currentUserPrompt string) []string {
	lines := strings.Split(content, "\n")
	terms := extractRecallTerms(currentUserPrompt)
	if len(terms) == 0 {
		return fallbackMemoryLines(lines)
	}

	type scoredLine struct {
		line  string
		score int
		idx   int
	}

	scored := make([]scoredLine, 0, len(lines))
	for idx, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "[truncated") {
			continue
		}
		score := scoreMemoryLine(line, terms)
		if score <= 0 {
			continue
		}
		scored = append(scored, scoredLine{line: line, score: score, idx: idx})
	}

	if len(scored) == 0 {
		return fallbackMemoryLines(lines)
	}

	sort.SliceStable(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].idx < scored[j].idx
	})

	limit := min(maxRelevantMemoryLines, len(scored))
	selected := scored[:limit]
	sort.SliceStable(selected, func(i, j int) bool {
		return selected[i].idx < selected[j].idx
	})

	result := make([]string, 0, limit)
	for _, item := range selected {
		result = append(result, item.line)
	}
	return result
}

func fallbackMemoryLines(lines []string) []string {
	result := make([]string, 0, maxRelevantMemoryLines)
	for _, raw := range lines {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "[truncated") {
			continue
		}
		result = append(result, line)
		if len(result) >= maxRelevantMemoryLines {
			break
		}
	}
	return result
}

func extractRecallTerms(prompt string) []string {
	matches := recallTokenPattern.FindAllString(strings.ToLower(prompt), -1)
	if len(matches) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(matches))
	terms := make([]string, 0, min(maxRecallTerms, len(matches)))
	for _, match := range matches {
		if isLowSignalRecallTerm(match) {
			continue
		}
		if _, ok := seen[match]; ok {
			continue
		}
		seen[match] = struct{}{}
		terms = append(terms, match)
		if len(terms) >= maxRecallTerms {
			break
		}
	}
	return terms
}

func isLowSignalRecallTerm(term string) bool {
	if len(term) < 3 {
		return true
	}
	switch term {
	case "the", "and", "for", "with", "from", "into", "that", "this", "when", "then", "than", "have", "will", "want", "need", "make", "adds", "add", "use", "using", "used", "show", "help", "continue", "please":
		return true
	default:
		return false
	}
}

func scoreMemoryLine(line string, terms []string) int {
	lower := strings.ToLower(line)
	score := 0
	for _, term := range terms {
		if strings.Contains(lower, term) {
			score++
		}
	}
	if strings.HasPrefix(line, "#") && score > 0 {
		score++
	}
	if strings.HasPrefix(line, "-") || strings.HasPrefix(line, "*") {
		score++
	}
	return score
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

func formatMemoryWriteGuidance() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	projectMemoryDir := projectMemoryDirPath(cwd)
	projectIndexPath := projectMemoryIndexPath(cwd)
	userMemoryDir := userMemoryDirPath()
	userIndexPath := userMemoryIndexPath()

	var b strings.Builder
	b.WriteString("Durable memory write guidance:\n")
	b.WriteString("- Use existing file tools to create or update memory files. Do not treat AGENTS.md or AGENTS.local.md as long-term memory storage.\n")
	b.WriteString("- Project memory files belong under: ")
	b.WriteString(projectMemoryDir)
	b.WriteString("\n")
	b.WriteString("- Project memory index path: ")
	b.WriteString(projectIndexPath)
	b.WriteString("\n")
	b.WriteString("- User-global memory files belong under: ")
	b.WriteString(userMemoryDir)
	b.WriteString("\n")
	b.WriteString("- User-global memory index path: ")
	b.WriteString(userIndexPath)
	b.WriteString("\n")
	b.WriteString("- Memory file types: user, feedback, project, reference. Prefer short Markdown files with YAML frontmatter that records at least title, type, and updated_at.\n")
	b.WriteString("- Canonical project memory filename example: ")
	b.WriteString(filepath.Join(projectMemoryDir, suggestedMemoryFilename(memoryTypeProjectNote, "Example project note")))
	b.WriteString("\n")
	b.WriteString("- Canonical user memory filename example: ")
	b.WriteString(filepath.Join(userMemoryDir, suggestedMemoryFilename(memoryTypeUserNote, "Example user preference")))
	b.WriteString("\n")
	b.WriteString("- When writing a new memory file, also add or update a concise entry in the appropriate MEMORY.md index so future recall can find it.\n")
	b.WriteString("- Store only durable, non-derivable guidance. If a fact can be re-derived from the repository state, prefer not to save it as memory.\n")
	b.WriteString("- Recommended memory file template:\n")
	b.WriteString(indentLines(memoryFileTemplate(memoryTypeProjectNote, "Example project note"), "  "))
	b.WriteString("\n")
	b.WriteString("- Recommended MEMORY.md index entry format:\n")
	b.WriteString(indentLines(memoryIndexEntryTemplate(memoryTypeProjectNote, "Example project note", suggestedMemoryFilename(memoryTypeProjectNote, "Example project note")), "  "))

	return strings.TrimSpace(b.String())
}

func suggestedMemoryFilename(memoryType, title string) string {
	slug := strings.ToLower(strings.TrimSpace(title))
	slug = nonSlugChars.ReplaceAllString(slug, "-")
	slug = strings.Trim(slug, "-")
	if slug == "" {
		slug = "note"
	}
	if len(slug) > 48 {
		slug = strings.Trim(slug[:48], "-")
	}
	return fmt.Sprintf("%s-%s.md", memoryType, slug)
}

func memoryFileTemplate(memoryType, title string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("title: ")
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString("type: ")
	b.WriteString(memoryType)
	b.WriteString("\n")
	b.WriteString("updated_at: ")
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteString("\n")
	b.WriteString("---\n\n")
	b.WriteString("- Durable note summary\n")
	b.WriteString("- Why it matters\n")
	b.WriteString("- Trigger or verification cue\n")
	return b.String()
}

func memoryIndexEntryTemplate(memoryType, title, filename string) string {
	return fmt.Sprintf("- [%s] %s (%s)", filename, title, memoryType)
}

func indentLines(value, indent string) string {
	if strings.TrimSpace(value) == "" {
		return ""
	}
	lines := strings.Split(strings.TrimRight(value, "\n"), "\n")
	for i := range lines {
		lines[i] = indent + lines[i]
	}
	return strings.Join(lines, "\n")
}

func pluralSuffix(count int, singular, plural string) string {
	if count == 1 {
		return singular
	}
	return plural
}
