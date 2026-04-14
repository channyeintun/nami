package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

const (
	// retrievalMaxSnippetBytes is the maximum bytes read from one file.
	retrievalMaxSnippetBytes = 2_000
	// retrievalMaxTotalTokens is the soft token cap for the full retrieval section.
	retrievalMaxTotalTokens = 3_000
	// retrievalMaxCandidates is the maximum number of candidates to score.
	retrievalMaxCandidates = 24
	// retrievalTopN is the maximum number of snippets injected into the prompt.
	retrievalTopN = 4
)

// anchorPattern matches likely file paths in text (relative or absolute).
var anchorPattern = regexp.MustCompile(`(?:^|[\s"'\x60(])([a-zA-Z0-9_./\-]+\.(?:go|ts|tsx|js|jsx|py|rb|rs|java|c|cpp|h|md|yaml|yml|json|toml|sh|sql))(?:[\s"'\x60):]|$)`)

// errorPattern matches common error prefixes.
var errorPattern = regexp.MustCompile(`(?i)(?:error|panic|fatal|fail|undefined|not found|cannot|permission denied)\s*[:;]?\s*([^\n]{0,120})`)

// RetrievalAnchor is an exact match extracted from the current turn context.
type RetrievalAnchor struct {
	FilePath    string
	Symbol      string
	ErrorString string
}

// RetrievalCandidate is a scored file path candidate for live snippet injection.
type RetrievalCandidate struct {
	FilePath string
	Score    int
	Reason   string
}

// LiveSnippet is a small excerpt read live from disk.
type LiveSnippet struct {
	FilePath string
	Content  string
	Source   string
}

// ExtractAnchors extracts exact anchors from the current user prompt, git diff,
// and recent tool output. Results are deduplicated.
func ExtractAnchors(userPrompt, gitStatus, toolOutputs string) []RetrievalAnchor {
	combined := strings.Join([]string{userPrompt, gitStatus, toolOutputs}, "\n")
	anchors := make([]RetrievalAnchor, 0, 8)
	seen := make(map[string]struct{})

	for _, path := range extractFilePathMatches(combined) {
		if _, ok := seen["file:"+path]; ok {
			continue
		}
		seen["file:"+path] = struct{}{}
		anchors = append(anchors, RetrievalAnchor{FilePath: path})
	}

	for _, match := range errorPattern.FindAllStringSubmatch(toolOutputs, -1) {
		if len(match) < 2 {
			continue
		}
		sig := strings.TrimSpace(match[0])
		if sig == "" {
			continue
		}
		if len(sig) > 80 {
			sig = sig[:80]
		}
		if _, ok := seen["err:"+sig]; ok {
			continue
		}
		seen["err:"+sig] = struct{}{}
		anchors = append(anchors, RetrievalAnchor{ErrorString: sig})
	}

	return anchors
}

// ScoreCandidates scores repository file paths based on anchors, git status,
// session-touched files, and structural edges. The second return value is
// the number of new candidates added through structural edge expansion.
func ScoreCandidates(anchors []RetrievalAnchor, cwd string, gitStatusText string, sessionTouched []string) ([]RetrievalCandidate, int) {
	if len(anchors) == 0 && len(sessionTouched) == 0 && strings.TrimSpace(gitStatusText) == "" {
		return nil, 0
	}

	scores := make(map[string]int)
	reasons := make(map[string]string)

	for _, anchor := range anchors {
		if anchor.FilePath == "" {
			continue
		}
		for _, candidate := range resolveFilePath(anchor.FilePath, cwd) {
			addCandidateScore(scores, reasons, candidate, 3, "exact anchor")
		}
	}

	for _, path := range gitStatusPaths(gitStatusText, cwd) {
		addCandidateScore(scores, reasons, path, 4, "staged or modified")
	}

	for _, path := range sessionTouched {
		for _, resolved := range resolveFilePath(path, cwd) {
			addCandidateScore(scores, reasons, resolved, 2, "recently touched")
		}
	}

	scoreErrorAnchors(anchors, scores, reasons)

	// Expand one hop through structural edges from the initial seed set.
	seedPaths := make([]string, 0, len(scores))
	for path := range scores {
		seedPaths = append(seedPaths, path)
	}
	beforeExpand := len(scores)
	expandStructuralEdges(seedPaths, cwd, scores, reasons)
	edgesExpanded := len(scores) - beforeExpand

	candidates := make([]RetrievalCandidate, 0, len(scores))
	for path, score := range scores {
		candidates = append(candidates, RetrievalCandidate{
			FilePath: path,
			Score:    score,
			Reason:   reasons[path],
		})
	}
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].Score != candidates[j].Score {
			return candidates[i].Score > candidates[j].Score
		}
		return candidates[i].FilePath < candidates[j].FilePath
	})
	if len(candidates) > retrievalMaxCandidates {
		candidates = candidates[:retrievalMaxCandidates]
	}
	return candidates, edgesExpanded
}

// ReadLiveSnippets reads the top-scoring candidates from disk within the token budget.
func ReadLiveSnippets(candidates []RetrievalCandidate, budgetTokens int) []LiveSnippet {
	if len(candidates) == 0 || budgetTokens <= 0 {
		return nil
	}
	if budgetTokens > retrievalMaxTotalTokens {
		budgetTokens = retrievalMaxTotalTokens
	}

	snippets := make([]LiveSnippet, 0, retrievalTopN)
	usedTokens := 0

	for _, candidate := range candidates {
		if len(snippets) >= retrievalTopN {
			break
		}
		if usedTokens >= budgetTokens {
			break
		}

		content := readFileSnippet(candidate.FilePath)
		if content == "" {
			continue
		}
		tokens := len(content) / 4
		if usedTokens+tokens > budgetTokens && len(snippets) > 0 {
			break
		}

		snippets = append(snippets, LiveSnippet{
			FilePath: candidate.FilePath,
			Content:  content,
			Source:   candidate.Reason,
		})
		usedTokens += tokens
	}

	return snippets
}

// FormatLiveRetrievalSection formats live snippets for injection into the system prompt.
func FormatLiveRetrievalSection(snippets []LiveSnippet) string {
	if len(snippets) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString("<live_context>\n")
	b.WriteString("Live file excerpts selected from the current repository state:\n\n")

	for _, snippet := range snippets {
		b.WriteString("<file path=\"")
		b.WriteString(snippet.FilePath)
		b.WriteString("\" source=\"")
		b.WriteString(snippet.Source)
		b.WriteString("\">\n")
		b.WriteString(snippet.Content)
		b.WriteString("\n</file>\n\n")
	}

	b.WriteString("</live_context>")
	return strings.TrimSpace(b.String())
}

func extractFilePathMatches(text string) []string {
	matches := anchorPattern.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	paths := make([]string, 0, len(matches))
	seen := make(map[string]struct{}, len(matches))
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		path := strings.TrimSpace(match[1])
		if path == "" || path == "." || path == ".." {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		paths = append(paths, path)
	}
	return paths
}

func gitStatusPaths(gitStatusText, cwd string) []string {
	var paths []string
	for _, line := range strings.Split(gitStatusText, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 3 {
			continue
		}
		statusCode := strings.TrimSpace(line[:2])
		if statusCode == "??" {
			continue
		}
		path := parseGitStatusPath(line)
		for _, resolved := range resolveFilePath(path, cwd) {
			paths = append(paths, resolved)
		}
	}
	return paths
}

func parseGitStatusPath(line string) string {
	if len(line) < 3 {
		return ""
	}
	path := strings.TrimSpace(line[2:])
	if idx := strings.LastIndex(path, " -> "); idx >= 0 {
		path = strings.TrimSpace(path[idx+4:])
	}
	return path
}

func scoreErrorAnchors(anchors []RetrievalAnchor, scores map[string]int, reasons map[string]string) {
	if len(scores) == 0 {
		return
	}

	contentCache := make(map[string]string, len(scores))
	for _, anchor := range anchors {
		if strings.TrimSpace(anchor.ErrorString) == "" {
			continue
		}
		terms := extractRecallTerms(anchor.ErrorString)
		if len(terms) == 0 {
			continue
		}
		lowerError := strings.ToLower(anchor.ErrorString)

		for path := range scores {
			delta := 0
			base := strings.ToLower(filepath.Base(path))
			stem := strings.TrimSuffix(base, strings.ToLower(filepath.Ext(base)))
			for _, term := range terms {
				switch {
				case strings.Contains(base, term):
					delta += 3
				case len(term) >= 4 && strings.Contains(stem, term):
					delta += 2
				}
			}

			if delta == 0 {
				content, ok := contentCache[path]
				if !ok {
					content = strings.ToLower(readFileSnippet(path))
					contentCache[path] = content
				}
				for _, term := range terms {
					if len(term) < 4 || content == "" {
						continue
					}
					if strings.Contains(content, term) {
						delta++
					}
					if delta >= 2 {
						break
					}
				}
			}

			if delta == 0 && base != "" && strings.Contains(lowerError, base) {
				delta = 2
			}
			if delta > 4 {
				delta = 4
			}
			if delta > 0 {
				addCandidateScore(scores, reasons, path, delta, "error context")
			}
		}
	}
}

func addCandidateScore(scores map[string]int, reasons map[string]string, path string, delta int, reason string) {
	if path == "" || delta <= 0 {
		return
	}
	scores[path] += delta
	reasons[path] = mergeCandidateReason(reasons[path], reason)
}

func mergeCandidateReason(current, next string) string {
	current = strings.TrimSpace(current)
	next = strings.TrimSpace(next)
	if current == "" {
		return next
	}
	if next == "" || current == next || strings.Contains(current, next) {
		return current
	}
	return current + "; " + next
}

// resolveFilePath attempts to find an absolute path for a potentially relative
// file reference. Returns all plausible resolved paths.
func resolveFilePath(ref, cwd string) []string {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return nil
	}
	ref = strings.TrimPrefix(ref, "file://")

	var results []string
	if filepath.IsAbs(ref) {
		cleaned := filepath.Clean(ref)
		if fileExists(cleaned) {
			results = append(results, cleaned)
		}
		return results
	}

	joined := filepath.Clean(filepath.Join(cwd, ref))
	if fileExists(joined) {
		results = append(results, joined)
	}
	return results
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

func readFileSnippet(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	content := strings.TrimSpace(string(data))
	if content == "" {
		return ""
	}
	if len(content) > retrievalMaxSnippetBytes {
		truncated := content[:retrievalMaxSnippetBytes]
		if idx := strings.LastIndex(truncated, "\n"); idx > 0 {
			truncated = truncated[:idx]
		}
		content = strings.TrimSpace(truncated) + "\n[truncated]"
	}
	return content
}

// ---------------------------------------------------------------------------
// Structural edge expansion
// ---------------------------------------------------------------------------

// goImportQuotePattern matches a quoted import path inside an import block.
var goImportQuotePattern = regexp.MustCompile(`"([^"]+)"`)

// expandStructuralEdges adds candidates reachable via one-hop structural edges
// from the initial seed file paths. It handles test ↔ source association and
// Go local-package imports.
func expandStructuralEdges(seedPaths []string, cwd string, scores map[string]int, reasons map[string]string) {
	modulePath, moduleRoot := findGoModule(cwd)

	for _, path := range seedPaths {
		expandTestEdge(path, scores, reasons)
		if strings.HasSuffix(path, ".go") && modulePath != "" {
			expandGoImports(path, modulePath, moduleRoot, scores, reasons)
		}
	}
}

// expandTestEdge associates a Go source file with its *_test.go counterpart
// and vice versa. Extends naturally to other language conventions.
func expandTestEdge(path string, scores map[string]int, reasons map[string]string) {
	switch {
	case strings.HasSuffix(path, "_test.go"):
		source := strings.TrimSuffix(path, "_test.go") + ".go"
		if fileExists(source) {
			addCandidateScore(scores, reasons, source, 2, "test covers")
		}
	case strings.HasSuffix(path, ".go"):
		test := strings.TrimSuffix(path, ".go") + "_test.go"
		if fileExists(test) {
			addCandidateScore(scores, reasons, test, 2, "test covers")
		}
	case strings.HasSuffix(path, ".ts") && !strings.HasSuffix(path, ".test.ts"):
		test := strings.TrimSuffix(path, ".ts") + ".test.ts"
		if fileExists(test) {
			addCandidateScore(scores, reasons, test, 2, "test covers")
		}
	case strings.HasSuffix(path, ".test.ts"):
		source := strings.TrimSuffix(path, ".test.ts") + ".ts"
		if fileExists(source) {
			addCandidateScore(scores, reasons, source, 2, "test covers")
		}
	}
}

const maxImportEdgesPerFile = 8

// expandGoImports parses Go import statements from a source file and resolves
// local-package imports to files on disk, adding them as candidates.
func expandGoImports(filePath, modulePath, moduleRoot string, scores map[string]int, reasons map[string]string) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return
	}

	imports := parseGoImports(string(data))
	added := 0
	for _, imp := range imports {
		if added >= maxImportEdgesPerFile {
			break
		}
		if !strings.HasPrefix(imp, modulePath) {
			continue
		}
		relPkg := strings.TrimPrefix(imp, modulePath)
		relPkg = strings.TrimPrefix(relPkg, "/")
		pkgDir := filepath.Join(moduleRoot, relPkg)
		entries, err := os.ReadDir(pkgDir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() {
				continue
			}
			name := entry.Name()
			if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
				continue
			}
			candidate := filepath.Join(pkgDir, name)
			if _, alreadyScored := scores[candidate]; alreadyScored {
				continue
			}
			addCandidateScore(scores, reasons, candidate, 1, "imported by anchor")
			added++
			if added >= maxImportEdgesPerFile {
				break
			}
		}
	}
}

// parseGoImports returns the imported package paths from a Go source file.
func parseGoImports(content string) []string {
	var imports []string
	inBlock := false
	for _, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "import (") {
			inBlock = true
			continue
		}
		if inBlock && trimmed == ")" {
			inBlock = false
			continue
		}
		if inBlock {
			if m := goImportQuotePattern.FindStringSubmatch(trimmed); len(m) >= 2 {
				imports = append(imports, m[1])
			}
			continue
		}
		if strings.HasPrefix(trimmed, "import ") {
			if m := goImportQuotePattern.FindStringSubmatch(trimmed); len(m) >= 2 {
				imports = append(imports, m[1])
			}
		}
	}
	return imports
}

// findGoModule walks up from startDir looking for go.mod and returns
// (module path, module root directory). Returns ("", "") if not found.
func findGoModule(startDir string) (string, string) {
	dir := startDir
	for {
		modFile := filepath.Join(dir, "go.mod")
		data, err := os.ReadFile(modFile)
		if err == nil {
			for _, line := range strings.Split(string(data), "\n") {
				line = strings.TrimSpace(line)
				if strings.HasPrefix(line, "module ") {
					return strings.TrimSpace(strings.TrimPrefix(line, "module")), dir
				}
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", ""
}
