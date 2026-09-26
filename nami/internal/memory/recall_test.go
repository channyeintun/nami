package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/channyeintun/nami/internal/agent"
)

// memoryIndex writes a MEMORY.md next to the note files it references, because
// index entries are only treated as usable when the note resolves on disk.
func memoryIndex(t *testing.T, fileType string, notes map[string]string, lines ...string) agent.MemoryFile {
	t.Helper()
	dir := t.TempDir()
	for name, body := range notes {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatalf("write note %s: %v", name, err)
		}
	}
	path := filepath.Join(dir, "MEMORY.md")
	content := strings.Join(lines, "\n")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write index: %v", err)
	}
	return agent.MemoryFile{
		Path:      path,
		Type:      fileType,
		Content:   content,
		UpdatedAt: time.Now(),
	}
}

func TestSelectReturnsNothingForBlankPrompt(t *testing.T) {
	file := memoryIndex(t, "project-index",
		map[string]string{"deploy.md": "notes"},
		"- [deploy.md] Deploy runbook (project)")

	results, err := RecallSelector{}.Select(context.Background(), []agent.MemoryFile{file}, "   ")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if results != nil {
		t.Fatalf("results = %+v, want nil", results)
	}
}

func TestSelectMatchesRelevantEntries(t *testing.T) {
	file := memoryIndex(t, "project-index",
		map[string]string{"deploy.md": "x", "styling.md": "y"},
		"- [deploy.md] Deploy runbook for releases (project)",
		"- [styling.md] Styling rules and css conventions (project)",
	)

	results, err := RecallSelector{}.Select(context.Background(), []agent.MemoryFile{file}, "how do we handle a deploy?")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("results = %+v, want one file", results)
	}
	if results[0].Path != file.Path {
		t.Fatalf("path = %q, want %q", results[0].Path, file.Path)
	}
	if len(results[0].Lines) != 1 || !strings.Contains(results[0].Lines[0], "Deploy runbook") {
		t.Fatalf("lines = %#v, want only the deploy entry", results[0].Lines)
	}
	if results[0].Source == "" {
		t.Error("result has no source label")
	}
}

func TestSelectIgnoresNonIndexFiles(t *testing.T) {
	file := memoryIndex(t, "project-note",
		map[string]string{"deploy.md": "x"},
		"- [deploy.md] Deploy runbook (project)")

	results, err := RecallSelector{}.Select(context.Background(), []agent.MemoryFile{file}, "deploy")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if results != nil {
		t.Fatalf("results = %+v, want nil for a non-index file", results)
	}
}

func TestSelectReturnsNothingWhenNoTermMatches(t *testing.T) {
	file := memoryIndex(t, "project-index",
		map[string]string{"deploy.md": "x"},
		"- [deploy.md] Deploy runbook (project)")

	results, err := RecallSelector{}.Select(context.Background(), []agent.MemoryFile{file}, "unrelated kangaroo topic")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if results != nil {
		t.Fatalf("results = %+v, want nil", results)
	}
}

// fillerIndex builds an index of count entries that no deploy prompt matches.
func fillerIndex(t *testing.T, fileType string, count int, extra ...string) agent.MemoryFile {
	t.Helper()
	notes := make(map[string]string, count+len(extra))
	lines := make([]string, 0, count+len(extra))
	for i := range count {
		name := fmt.Sprintf("filler-%02d.md", i)
		notes[name] = "body"
		lines = append(lines, "- ["+name+"] Styling rule "+name+" (project)")
	}
	for _, name := range extra {
		notes[name] = "body"
		lines = append(lines, "- ["+name+"] Deploy runbook (project)")
	}
	return memoryIndex(t, fileType, notes, lines...)
}

// Every index entry is a recall candidate. Capping candidates before scoring
// made entries past the first 32 unrecallable, and a long user index hid the
// project index behind it entirely.
func TestSelectConsidersEveryIndexEntry(t *testing.T) {
	t.Run("entry late in one index", func(t *testing.T) {
		file := fillerIndex(t, "project-index", 40, "deploy.md")
		results, err := RecallSelector{}.Select(context.Background(), []agent.MemoryFile{file}, "how do we deploy?")
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if len(results) != 1 || len(results[0].Lines) != 1 || !strings.Contains(results[0].Lines[0], "deploy.md") {
			t.Fatalf("results = %+v, want the deploy entry", results)
		}
	})

	t.Run("project index behind a long user index", func(t *testing.T) {
		user := fillerIndex(t, "user-index", 40)
		project := fillerIndex(t, "project-index", 0, "deploy.md")
		results, err := RecallSelector{}.Select(context.Background(), []agent.MemoryFile{user, project}, "how do we deploy?")
		if err != nil {
			t.Fatalf("Select: %v", err)
		}
		if len(results) != 1 || results[0].Path != project.Path {
			t.Fatalf("results = %+v, want the project deploy entry", results)
		}
	})
}

func TestSelectMemoryRecallCandidatesLimitsSelections(t *testing.T) {
	candidates := make([]recallCandidate, 0, memoryRecallMaxSelections+5)
	for i := range memoryRecallMaxSelections + 5 {
		candidates = append(candidates, recallCandidate{
			ID:       "m",
			Scope:    "project-index",
			FilePath: "MEMORY.md",
			Line:     "- deployment topic",
			Title:    "deployment topic",
			Index:    i,
		})
	}
	selected := selectMemoryRecallCandidates(candidates, "deployment")
	if len(selected) != memoryRecallMaxSelections {
		t.Fatalf("selected = %d, want the cap of %d", len(selected), memoryRecallMaxSelections)
	}
}

func TestScoreMemoryRecallCandidateWeightsFilenameHighest(t *testing.T) {
	filenameHit := recallCandidate{NotePath: "notes/deploy.md", Line: "- unrelated", Title: "unrelated"}
	titleHit := recallCandidate{NotePath: "notes/other.md", Line: "- unrelated", Title: "deploy steps"}
	lineHit := recallCandidate{NotePath: "notes/other.md", Line: "- deploy mentioned here", Title: "unrelated"}

	terms := []string{"deploy"}
	filenameScore := scoreMemoryRecallCandidate(filenameHit, terms)
	titleScore := scoreMemoryRecallCandidate(titleHit, terms)
	lineScore := scoreMemoryRecallCandidate(lineHit, terms)

	if !(filenameScore > titleScore && titleScore > lineScore) {
		t.Fatalf("scores = filename %d, title %d, line %d; want them ranked in that order", filenameScore, titleScore, lineScore)
	}
}

func TestScoreMemoryRecallCandidateBoostsProjectScope(t *testing.T) {
	project := recallCandidate{Scope: "project-index", Title: "deploy"}
	user := recallCandidate{Scope: "user-index", Title: "deploy"}
	if scoreMemoryRecallCandidate(project, []string{"deploy"}) <= scoreMemoryRecallCandidate(user, []string{"deploy"}) {
		t.Fatal("project-scoped memories should outrank user-scoped ones at equal relevance")
	}
}

func TestScoreMemoryRecallCandidateReturnsZeroWithoutMatch(t *testing.T) {
	candidate := recallCandidate{Scope: "project-index", Title: "styling", Line: "- css"}
	if got := scoreMemoryRecallCandidate(candidate, []string{"deploy"}); got != 0 {
		t.Fatalf("score = %d, want 0", got)
	}
}

func TestExtractMemoryRecallTerms(t *testing.T) {
	terms := extractMemoryRecallTerms("Please help me update the internal/tools/bash.go retry logic")
	joined := strings.Join(terms, ",")
	for _, want := range []string{"internal/tools/bash.go", "retry", "logic", "update"} {
		if !strings.Contains(joined, want) {
			t.Errorf("terms %v missing %q", terms, want)
		}
	}
	for _, unwanted := range []string{"the", "please", "help"} {
		for _, term := range terms {
			if term == unwanted {
				t.Errorf("low-signal term %q was kept", unwanted)
			}
		}
	}
}

func TestExtractMemoryRecallTermsDeduplicatesAndCaps(t *testing.T) {
	if got := extractMemoryRecallTerms("retry retry retry"); len(got) != 1 {
		t.Fatalf("terms = %v, want one deduplicated term", got)
	}
	prompt := strings.Repeat("alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi ", 3)
	if got := extractMemoryRecallTerms(prompt); len(got) > memoryRecallMaxTerms {
		t.Fatalf("terms = %d, want at most %d", len(got), memoryRecallMaxTerms)
	}
	if got := extractMemoryRecallTerms("!!! ???"); got != nil {
		t.Fatalf("terms = %v, want nil", got)
	}
}

func TestIsLowSignalTerm(t *testing.T) {
	for _, term := range []string{"a", "in", "the", "file", "project"} {
		if !isLowSignalTerm(term) {
			t.Errorf("isLowSignalTerm(%q) = false, want true", term)
		}
	}
	for _, term := range []string{"retry", "deploy", "bash.go", "internal/tools"} {
		if isLowSignalTerm(term) {
			t.Errorf("isLowSignalTerm(%q) = true, want false", term)
		}
	}
}

func TestBuildMemoryRecallResultsGroupsByFile(t *testing.T) {
	results := buildMemoryRecallResults([]recallCandidate{
		{FilePath: "b/MEMORY.md", Line: "- second file", Index: 1},
		{FilePath: "a/MEMORY.md", Line: "- first entry", Index: 2},
		{FilePath: "a/MEMORY.md", Line: "- earlier entry", Index: 1},
	}, "test source")

	if len(results) != 2 {
		t.Fatalf("results = %+v, want two files", results)
	}
	if results[0].Path != "a/MEMORY.md" {
		t.Fatalf("first path = %q, want the alphabetically first file", results[0].Path)
	}
	if len(results[0].Lines) != 2 || results[0].Lines[0] != "- earlier entry" {
		t.Fatalf("lines = %#v, want index order", results[0].Lines)
	}
	if results[0].Source != "test source" {
		t.Fatalf("source = %q", results[0].Source)
	}
	if got := buildMemoryRecallResults(nil, "x"); got != nil {
		t.Fatalf("results = %+v, want nil", got)
	}
}

func TestScopeRankPrefersProject(t *testing.T) {
	if scopeRank("project-index") >= scopeRank("user-index") {
		t.Fatal("project scope should rank ahead of user scope")
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  ", "value"); got != "value" {
		t.Fatalf("firstNonEmpty = %q", got)
	}
	if got := firstNonEmpty("", " "); got != "" {
		t.Fatalf("firstNonEmpty = %q, want empty", got)
	}
}
