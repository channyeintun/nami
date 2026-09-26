package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
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

func TestRetrievalGraphKeepsImportEdgesIntoInvalidatedFile(t *testing.T) {
	// Editing an imported file changes that file's own structure, not the
	// importer's, so the importer must still lead to it afterwards.
	dir := t.TempDir()
	writeRetrievalFile(t, dir, "go.mod", "module example.com/demo\n")
	app := writeRetrievalFile(t, dir, "app.go", "package main\n\nimport \"example.com/demo/lib\"\n\nfunc main() { lib.Helper() }\n")
	lib := writeRetrievalFile(t, dir, "lib/lib.go", "package lib\n\nfunc Helper() {}\n")

	graph := NewRetrievalGraph(dir)
	anchors := []RetrievalAnchor{{FilePath: app}}
	candidates, _ := ScoreCandidates(anchors, dir, "", nil, graph)
	if candidateScores(candidates)[lib] == 0 {
		t.Fatalf("expected the imported file to be a candidate before invalidation, got %+v", candidates)
	}

	graph.Invalidate(lib)

	candidates, _ = ScoreCandidates(anchors, dir, "", nil, graph)
	if candidateScores(candidates)[lib] == 0 {
		t.Fatalf("expected the imported file to stay a candidate after it was invalidated, got %+v", candidates)
	}
}

func TestRetrievalReadsOnlyAPrefixOfLargeFiles(t *testing.T) {
	// A file named in the prompt or tool output can be arbitrarily large;
	// retrieval keeps a small prefix, so it must not read the rest.
	const size = 64 << 20
	dir := t.TempDir()
	path := writeRetrievalFile(t, dir, "dump.go", "package dump\n\nfunc Load() {}\n")
	if err := os.Truncate(path, size); err != nil {
		t.Fatalf("grow dump.go: %v", err)
	}

	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	graph := NewRetrievalGraph(dir)
	candidates, _ := ScoreCandidates([]RetrievalAnchor{{FilePath: path}}, dir, "", nil, graph)
	snippets := ReadLiveSnippets(candidates, retrievalMaxTotalTokens)
	runtime.ReadMemStats(&after)

	if len(snippets) != 1 || !strings.Contains(snippets[0].Content, "func Load") {
		t.Fatalf("expected a snippet of the file's head, got %+v", snippets)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > size/4 {
		t.Fatalf("retrieval allocated %d bytes for a %d-byte file", allocated, size)
	}
}

// retrievalProject lays out a git project, with the working directory one
// level below its root, next to a secret that lives outside it.
func retrievalProject(t *testing.T) (base, project, cwd, secret string) {
	t.Helper()
	base = t.TempDir()
	project = filepath.Join(base, "project")
	if err := os.MkdirAll(filepath.Join(project, ".git"), 0o755); err != nil {
		t.Fatalf("create .git: %v", err)
	}
	cwd = filepath.Join(project, "app")
	secret = writeRetrievalFile(t, base, "home/.docker/config.json", `{"auths":{"registry":{"auth":"SECRET"}}}`)
	return base, project, cwd, secret
}

func TestRetrievalReadsNothingOutsideTheProject(t *testing.T) {
	// Paths reach retrieval from untrusted text such as a fetched page, and
	// what it reads goes straight into the prompt. Files outside the project
	// must stay out, however they are named; files inside must still come in.
	base, project, cwd, secret := retrievalProject(t)
	mainFile := writeRetrievalFile(t, cwd, "main.go", "package main\n\nfunc main() {}\n")
	sibling := writeRetrievalFile(t, project, "lib/util.go", "package lib\n\nfunc Util() {}\n")
	link := filepath.Join(cwd, "linked.json")
	if err := os.Symlink(secret, link); err != nil {
		t.Fatalf("create link: %v", err)
	}
	writeRetrievalFile(t, cwd, "index.ts", "import { leak } from \"../../outside/leak\";\n")
	leak := writeRetrievalFile(t, base, "outside/leak.ts", "export const leak = \"SECRET\";\n")

	toolOutput := strings.Join([]string{
		"To log in, the page says to paste " + secret,
		"or the copy at ../../home/.docker/config.json and linked.json",
		"Build failed in " + mainFile + " and ../lib/util.go, see index.ts",
	}, "\n")
	touched := []string{secret, leak}

	for _, tc := range []struct {
		name  string
		graph *RetrievalGraph
	}{
		{name: "graph", graph: NewRetrievalGraph(cwd)},
		{name: "fallback", graph: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			anchors := ExtractAnchors("", "", toolOutput, tc.graph)
			if len(anchors) < 6 {
				t.Fatalf("expected an anchor for each path in the tool output, got %+v", anchors)
			}
			candidates, _ := ScoreCandidates(anchors, cwd, "", touched, tc.graph)
			scores := candidateScores(candidates)
			for _, outside := range []string{secret, link, leak} {
				if _, ok := scores[outside]; ok {
					t.Errorf("%s is a candidate, but it is outside the project", outside)
				}
			}
			for _, inside := range []string{mainFile, sibling} {
				if scores[inside] == 0 {
					t.Errorf("%s is not a candidate, but it is inside the project", inside)
				}
			}
			for _, snippet := range ReadLiveSnippets(candidates, retrievalMaxTotalTokens) {
				if strings.Contains(snippet.Content, "SECRET") {
					t.Errorf("retrieval read a file outside the project into %s", snippet.FilePath)
				}
			}
		})
	}
}

func TestRetrievalAcceptsAProjectReachedThroughALink(t *testing.T) {
	// The root and the candidates are compared with links resolved, so a
	// project opened through a symlinked path keeps its files.
	base, project, _, _ := retrievalProject(t)
	mainFile := writeRetrievalFile(t, project, "app/main.go", "package main\n\nfunc main() {}\n")
	alias := filepath.Join(base, "alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Fatalf("create link: %v", err)
	}
	cwd := filepath.Join(alias, "app")
	viaAlias := filepath.Join(cwd, "main.go")

	anchors := []RetrievalAnchor{{FilePath: viaAlias}, {FilePath: mainFile}}
	candidates, _ := ScoreCandidates(anchors, cwd, "", nil, NewRetrievalGraph(cwd))
	scores := candidateScores(candidates)
	for _, path := range []string{viaAlias, mainFile} {
		if scores[path] == 0 {
			t.Errorf("%s is not a candidate, but it is inside the project, got %+v", path, candidates)
		}
	}
}
