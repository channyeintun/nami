package agent

import (
	"fmt"
	"hash/fnv"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/channyeintun/nami/internal/config"
	"github.com/channyeintun/nami/internal/textutil"
)

// MemoryFile represents a loaded instruction or memory index file.
type MemoryFile struct {
	Path      string
	Type      string
	Content   string
	UpdatedAt time.Time
}

// SkippedMemoryFile is an instruction file the loader found but did not load,
// and why.
type SkippedMemoryFile struct {
	Path   string
	Reason string
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
//  1. User memory index: {config root}/memory/MEMORY.md
//  2. Project memory index: {config root}/projects/{slug}/memory/MEMORY.md
//  3. Project instructions: AGENTS.md (walking up from cwd to root)
//  4. Local instructions: AGENTS.local.md (walking up from cwd to root)
//
// Files closer to the working directory have higher priority and are loaded later.
//
// Instruction files that another user could have written are not loaded; the
// second result lists them so the caller can say so.
func LoadMemoryFiles() ([]MemoryFile, []SkippedMemoryFile) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil
	}

	var files []MemoryFile
	var skipped []SkippedMemoryFile
	files = appendConfigMemoryIndexes(files, cwd)
	dirs := walkUpDirs(cwd)

	for _, dir := range slices.Backward(dirs) {
		files, skipped = appendProjectFiles(files, skipped, dir)
	}

	if len(files) > maxMemoryFiles {
		files = files[len(files)-maxMemoryFiles:]
	}

	return files, skipped
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

// appendProjectFiles loads the instruction files in one directory of the walk
// up from the working directory. The walk reaches every ancestor, shared ones
// such as /tmp included, so a file another user could have written is skipped
// rather than read into the system prompt.
func appendProjectFiles(files []MemoryFile, skipped []SkippedMemoryFile, dir string) ([]MemoryFile, []SkippedMemoryFile) {
	for _, instructions := range []struct {
		name     string
		fileType string
	}{
		{name: "AGENTS.md", fileType: memoryTypeProject},
		{name: "AGENTS.local.md", fileType: memoryTypeLocal},
	} {
		path := filepath.Join(dir, instructions.name)
		if reason := instructionFileDistrust(path); reason != "" {
			skipped = append(skipped, SkippedMemoryFile{Path: path, Reason: reason})
			continue
		}
		content, err := readMemoryFile(path)
		if err != nil {
			continue
		}
		files = append(files, MemoryFile{Path: path, Type: instructions.fileType, Content: content, UpdatedAt: fileUpdatedAt(path)})
	}
	return files, skipped
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

// readMemoryFile and readMemoryIndex read at most twice the bytes they keep:
// enough that trimming whitespace never marks a file that fits as truncated,
// without reading a large file whole or blocking on a FIFO.
func readMemoryFile(path string) (string, error) {
	data, more, err := readFileHead(path, 2*maxMemoryFileBytes)
	if err != nil {
		return "", err
	}
	content := strings.TrimSpace(data)
	if content == "" {
		return "", os.ErrNotExist
	}
	if more || len(content) > maxMemoryFileBytes {
		content = textutil.TruncateHead(content, maxMemoryFileBytes) + "\n[truncated]"
	}
	return content, nil
}

func readMemoryIndex(path string) (string, error) {
	data, more, err := readFileHead(path, 2*maxMemoryIndexBytes)
	if err != nil {
		return "", err
	}
	content := strings.TrimSpace(data)
	if content == "" {
		return "", os.ErrNotExist
	}

	lines := strings.Split(content, "\n")
	truncated := more
	if len(lines) > maxMemoryIndexLines {
		lines = lines[:maxMemoryIndexLines]
		truncated = true
	}
	content = strings.TrimSpace(strings.Join(lines, "\n"))
	if len(content) > maxMemoryIndexBytes {
		content = strings.TrimSpace(textutil.TruncateHead(content, maxMemoryIndexBytes))
		truncated = true
	}
	if truncated {
		content += "\n[truncated memory index]"
	}
	return content, nil
}

// FormatMemoryInstructionPrompt renders session-stable instruction content.
func FormatMemoryInstructionPrompt(files []MemoryFile) string {
	instructions := make([]MemoryFile, 0, len(files))
	for _, f := range files {
		switch f.Type {
		case memoryTypeProjectIndex, memoryTypeUserIndex:
			continue
		default:
			instructions = append(instructions, f)
		}
	}

	var b strings.Builder
	writeGuidance := formatMemoryWriteGuidance()
	if len(instructions) > 0 {
		b.WriteString("Project instructions below. Follow exactly when applicable. These override default behavior.\n\n")

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

// FormatRelevantMemoryPrompt renders recalled durable memory snippets relevant to the current turn.
func FormatRelevantMemoryPrompt(files []MemoryFile, currentUserPrompt string, recalls []MemoryRecallResult) string {
	memoryIndexes := make([]MemoryFile, 0, len(files))
	recallByPath := memoryRecallLookup(recalls)
	for _, f := range files {
		switch f.Type {
		case memoryTypeProjectIndex, memoryTypeUserIndex:
			memoryIndexes = append(memoryIndexes, f)
		}
	}

	var b strings.Builder

	if len(memoryIndexes) > 0 {
		renderedIndexes := make([]string, 0, len(memoryIndexes))
		for _, f := range memoryIndexes {
			recall, ok := recallByPath[f.Path]
			if !ok {
				continue
			}
			recalledContent := formatRelevantMemoryIndexContent(f, currentUserPrompt, recall)
			if strings.TrimSpace(recalledContent) == "" {
				continue
			}

			var section strings.Builder
			section.WriteString("<memory_file path=\"")
			section.WriteString(f.Path)
			section.WriteString("\" type=\"")
			section.WriteString(f.Type)
			section.WriteString("\">\n")
			section.WriteString(recalledContent)
			section.WriteString("\n</memory_file>")
			renderedIndexes = append(renderedIndexes, section.String())
		}

		if len(renderedIndexes) > 0 {
			b.WriteString("Durable memory recalled for this request: non-derivable user/project guidance such as workflow constraints and style decisions. Treat entries as selectively relevant context, not unconditional instructions. Memory reflects what was true when it was written: verify drift-prone facts (file paths, commands, code claims) against the live repo when that is cheap, and if you rely on an unverified memory-derived fact in an answer, say it comes from memory and may be stale.\n\n")
			for _, section := range renderedIndexes {
				b.WriteString(section)
				b.WriteString("\n\n")
			}
		}
	}

	if b.Len() == 0 {
		return ""
	}

	return strings.TrimSpace(b.String())
}

// FormatMemoryPrompt renders all loaded memory prompt sections.
func FormatMemoryPrompt(files []MemoryFile, currentUserPrompt string, recalls []MemoryRecallResult) string {
	return strings.TrimSpace(joinPromptSections([]string{
		FormatMemoryInstructionPrompt(files),
		FormatRelevantMemoryPrompt(files, currentUserPrompt, recalls),
	}))
}

func userMemoryIndexPath() string {
	return filepath.Join(config.MemoryDir(), "MEMORY.md")
}

func projectMemoryIndexPath(cwd string) string {
	projectRoot := findProjectScopeRoot(cwd)
	return filepath.Join(config.ProjectsDir(), projectSlug(projectRoot), "memory", "MEMORY.md")
}

func userMemoryDirPath() string {
	return config.MemoryDir()
}

func projectMemoryDirPath(cwd string) string {
	projectRoot := findProjectScopeRoot(cwd)
	return filepath.Join(config.ProjectsDir(), projectSlug(projectRoot), "memory")
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

func formatRelevantMemoryIndexContent(file MemoryFile, currentUserPrompt string, recalled MemoryRecallResult) string {
	selectedLines := recalled.Lines
	selectionSource := strings.TrimSpace(recalled.Source)
	if len(selectedLines) == 0 {
		return ""
	}
	if selectionSource == "" {
		selectionSource = "deterministic selection"
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

const memoryNoteOutsideIssue = "Referenced memory note resolves outside the memory directory and was skipped."

func resolveMemoryNotePath(indexPath, filename string) (string, string) {
	baseDir := filepath.Clean(filepath.Dir(indexPath))
	resolved := filepath.Clean(filepath.Join(baseDir, filename))
	if !pathWithinBaseDir(baseDir, resolved) {
		return "", memoryNoteOutsideIssue
	}
	info, err := os.Stat(resolved)
	if err != nil {
		if os.IsNotExist(err) {
			return resolved, "Referenced memory note file does not exist."
		}
		return resolved, fmt.Sprintf("Referenced memory note could not be read: %v", err)
	}
	if !info.Mode().IsRegular() {
		return resolved, "Referenced memory note is not a regular file."
	}
	// The lexical check above cannot see symlinks, and a link inside the
	// memory directory can lead anywhere on disk. Compare the real paths too.
	within, err := realPathWithinBaseDir(baseDir, resolved)
	if err != nil {
		return resolved, fmt.Sprintf("Referenced memory note could not be read: %v", err)
	}
	if !within {
		return "", memoryNoteOutsideIssue
	}
	return resolved, ""
}

// realPathWithinBaseDir is pathWithinBaseDir after resolving symlinks in both
// paths, so it holds for where a read of candidate actually lands.
func realPathWithinBaseDir(baseDir, candidate string) (bool, error) {
	realBase, err := filepath.EvalSymlinks(baseDir)
	if err != nil {
		return false, err
	}
	realCandidate, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return false, err
	}
	return pathWithinBaseDir(realBase, realCandidate), nil
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

	rawTitle, rawNoteType, ok := strings.CutLast(remaining, " (")
	if !ok {
		return "", "", "", false
	}
	title = strings.TrimSpace(rawTitle)
	noteType = strings.TrimSpace(strings.TrimSuffix(rawNoteType, ")"))
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
		excerpt = strings.TrimSpace(textutil.TruncateHead(excerpt, maxMemoryNoteBytes)) + "\n[truncated memory note]"
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

// memoryFileTemplate renders the example note shown in the stable system
// prompt. It must not embed the current time: that prompt is the cached
// prefix, and a changing timestamp makes every new query miss the cache.
func memoryFileTemplate(memoryType, title string) string {
	var b strings.Builder
	b.WriteString("---\n")
	b.WriteString("title: ")
	b.WriteString(title)
	b.WriteString("\n")
	b.WriteString("type: ")
	b.WriteString(memoryType)
	b.WriteString("\n")
	b.WriteString("updated_at: <current UTC time, RFC 3339>\n")
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
