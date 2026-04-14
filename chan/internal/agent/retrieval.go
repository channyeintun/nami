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

	// File path anchors.
	for _, match := range anchorPattern.FindAllStringSubmatch(combined, -1) {
		if len(match) < 2 {
			continue
		}
		path := strings.TrimSpace(match[1])
		if path == "" || strings.HasPrefix(path, ".") {
			continue
		}
		if _, ok := seen["file:"+path]; ok {
			continue
		}
		seen["file:"+path] = struct{}{}
		anchors = append(anchors, RetrievalAnchor{FilePath: path})
	}

	// Error string anchors from tool outputs.
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
// and session-touched files. Returns at most retrievalMaxCandidates candidates.
func ScoreCandidates(anchors []RetrievalAnchor, cwd string, gitStatusText string, sessionTouched []string) []RetrievalCandidate {
	if len(anchors) == 0 && len(sessionTouched) == 0 {
		return nil
	}

	scores := make(map[string]int)
	reasons := make(map[string]string)

	// Score from anchors — resolve relative paths against cwd.
	for _, anchor := range anchors {
		if anchor.FilePath == "" {
			continue
		}
		candidates := resolveFilePath(anchor.FilePath, cwd)
		for _, candidate := range candidates {
			scores[candidate] += 3
			reasons[candidate] = "exact anchor"
		}
	}

	// Score staged/modified files from git status.
	for _, line := range strings.Split(gitStatusText, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 3 {
			continue
		}
		// git status --short format: "XY path"
		statusCode := strings.TrimSpace(line[:2])
		path := strings.TrimSpace(line[2:])
		if path == "" || statusCode == "??" {
			continue
		}
		resolved := filepath.Join(cwd, path)
		scores[resolved] += 4
		reasons[resolved] = "staged or modified"
	}

	// Score session-touched files.
	for _, path := range sessionTouched {
		if path == "" {
			continue
		}
		scores[path] += 2
		if reasons[path] == "" {
			reasons[path] = "recently touched"
		}
	}

	// Build and sort candidates.
	candidates := make([]RetrievalCandidate, 0, len(scores))
	for path, score := range scores {
		candidates = append(candidates, RetrievalCandidate{
			FilePath: path,
			Score:    score,
			Reason:   reasons[path],
		})
	}
	sort.SliceStable(candidates, func(i, j int) bool {
		return candidates[i].Score > candidates[j].Score
	})
	if len(candidates) > retrievalMaxCandidates {
		candidates = candidates[:retrievalMaxCandidates]
	}
	return candidates
}

// ReadLiveSnippets reads the top-scoring candidates from disk within the token budget.
func ReadLiveSnippets(candidates []RetrievalCandidate, budgetTokens int) []LiveSnippet {
	if len(candidates) == 0 || budgetTokens <= 0 {
		return nil
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
		// Rough token estimate: 4 chars per token.
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

// resolveFilePath attempts to find an absolute path for a potentially relative
// file reference. Returns all plausible resolved paths.
func resolveFilePath(ref, cwd string) []string {
	var results []string
	// If already absolute.
	if filepath.IsAbs(ref) {
		if fileExists(ref) {
			results = append(results, ref)
		}
		return results
	}
	// Resolve relative to cwd.
	joined := filepath.Join(cwd, ref)
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
		// Truncate at a newline boundary.
		truncated := content[:retrievalMaxSnippetBytes]
		if idx := strings.LastIndex(truncated, "\n"); idx > 0 {
			truncated = truncated[:idx]
		}
		content = strings.TrimSpace(truncated) + "\n[truncated]"
	}
	return content
}
