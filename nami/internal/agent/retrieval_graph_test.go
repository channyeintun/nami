package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeRetrievalFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("create dir for %s: %v", name, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return path
}

func goFileWithFuncs(names ...string) string {
	var b strings.Builder
	b.WriteString("package demo\n")
	for _, name := range names {
		fmt.Fprintf(&b, "\nfunc %s() {}\n", name)
	}
	return b.String()
}

func candidateScores(candidates []RetrievalCandidate) map[string]int {
	scores := make(map[string]int, len(candidates))
	for _, candidate := range candidates {
		scores[candidate.FilePath] = candidate.Score
	}
	return scores
}

func TestRetrievalGraphScoreDoesNotGrowWithSymbolCount(t *testing.T) {
	// Two files carrying the same signal must score the same; a file must not
	// earn points from the symbols it contains itself.
	dir := t.TempDir()
	many := make([]string, 30)
	for i := range many {
		many[i] = fmt.Sprintf("Handler%02d", i)
	}
	small := writeRetrievalFile(t, dir, "small.go", goFileWithFuncs("Only"))
	large := writeRetrievalFile(t, dir, "large.go", goFileWithFuncs(many...))

	graph := NewRetrievalGraph(dir)
	touched := []string{small, large}
	candidates, _ := ScoreCandidates(nil, dir, "", touched, graph)
	scores := candidateScores(candidates)
	if scores[small] == 0 || scores[small] != scores[large] {
		t.Fatalf("expected equally touched files to score the same, got small=%d large=%d", scores[small], scores[large])
	}
}
