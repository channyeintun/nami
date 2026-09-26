package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestRetrievalGraphSymbolAnchorScoresDefiningFile(t *testing.T) {
	dir := t.TempDir()
	widget := writeRetrievalFile(t, dir, "widget.go", goFileWithFuncs("RenderWidget"))
	other := writeRetrievalFile(t, dir, "other.go", goFileWithFuncs("Unrelated"))

	graph := NewRetrievalGraph(dir)
	touched := []string{widget, other}
	// An earlier turn touched both files, so the graph knows their symbols.
	graph.Seed(nil, touched)

	anchors := ExtractAnchors("Why does RenderWidget panic?", "", "", graph)
	if len(anchors) != 1 || anchors[0].Symbol != "RenderWidget" {
		t.Fatalf("expected a single RenderWidget symbol anchor, got %+v", anchors)
	}
	candidates, _ := ScoreCandidates(anchors, dir, "", touched, graph)
	scores := candidateScores(candidates)
	if scores[widget] <= scores[other] {
		t.Fatalf("expected the file defining the anchored symbol to outrank the other touched file, got widget=%d other=%d", scores[widget], scores[other])
	}
}

func TestRetrievalGraphForgetsSymbolAnchorsOnceTheSymbolIsGone(t *testing.T) {
	dir := t.TempDir()
	widget := writeRetrievalFile(t, dir, "widget.go", goFileWithFuncs("RenderWidget"))

	graph := NewRetrievalGraph(dir)
	touched := []string{widget}
	graph.Seed(nil, touched)
	anchors := ExtractAnchors("Why does RenderWidget panic?", "", "", graph)
	ScoreCandidates(anchors, dir, "", touched, graph)

	writeRetrievalFile(t, dir, "widget.go", goFileWithFuncs("DrawGadget"))
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(widget, later, later); err != nil {
		t.Fatalf("bump widget.go mod time: %v", err)
	}
	graph.Seed(nil, touched)

	if symbols := graph.ExtractSymbolAnchors("Why does RenderWidget panic?"); len(symbols) != 0 {
		t.Fatalf("expected no symbol anchors once no file defines RenderWidget, got %v", symbols)
	}
}
