package agent

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
)

// ---------------------------------------------------------------------------
// Node and edge types
// ---------------------------------------------------------------------------

type NodeKind int

const (
	NodeFile       NodeKind = iota
	NodeSymbol              // function, class, type, struct, trait, interface
	NodeTest                // test function or test file
	NodeError               // error signature from tool output
	NodeToolResult          // file/symbol mentioned in a tool result
	NodePreference          // durable preference record
)

type GraphNode struct {
	Kind    NodeKind
	Key     string // absolute path for files, "file:symbol" for symbols
	ModTime time.Time
}

type EdgeKind int

const (
	EdgeContains          EdgeKind = iota // file → symbol
	EdgeImports                           // file → file
	EdgeReferences                        // symbol → symbol (lightweight)
	EdgeTestCovers                        // test file ↔ source file
	EdgeToolMentions                      // tool result → file or symbol
	EdgeDiffActive                        // git overlay: file in diff/staging
	EdgeSessionTouched                    // file touched this session
	EdgePreferenceApplies                 // preference → language/dir/tool
)

type GraphEdge struct {
	Target string
	Kind   EdgeKind
	Weight int
}

const (
	edgeWeightImports    = 2
	edgeWeightContains   = 2
	edgeWeightTestCovers = 2
	edgeWeightDiff       = 3
	edgeWeightTouched    = 1
	// edgeWeightSymbolAnchor matches the score of an exact file anchor: a
	// symbol named in the turn points at the file defining it just as
	// directly as naming the file would.
	edgeWeightSymbolAnchor = 3

	// symbolAnchorPrefix keys the node for a symbol named in the turn, as
	// opposed to the "path:symbol" nodes parsed out of files.
	symbolAnchorPrefix = "sym:"

	graphSecondHopPenalty       = 50 // percent
	graphSecondHopMinCandidates = 3
	graphMaxNodesPerFile        = 40
	graphMaxImportsPerFile      = 12
	// graphMaxParseBytes bounds how much of a file is parsed for structure.
	// Imports and most symbols sit near the top of hand-written source; a
	// file past this size is generated code or data.
	graphMaxParseBytes = 1 << 20
)

// ---------------------------------------------------------------------------
// RetrievalGraph
// ---------------------------------------------------------------------------

// RetrievalGraph is a session-scoped in-memory index over repository structure.
// It persists across turns within a single session and is lazily populated.
type RetrievalGraph struct {
	Nodes     map[string]*GraphNode
	Adj       map[string][]GraphEdge // key → outgoing edges
	cwd       string
	goMod     string // cached go module path
	goModRoot string // cached go.mod directory

	// lastGitStatus tracks the previous git status text for overlay invalidation.
	lastGitStatus string

	// fileModTimes caches stat results for lazy invalidation.
	fileModTimes map[string]time.Time
}

// NewRetrievalGraph creates an empty graph rooted at cwd.
func NewRetrievalGraph(cwd string) *RetrievalGraph {
	goMod, goModRoot := findGoModule(cwd)
	return &RetrievalGraph{
		Nodes:        make(map[string]*GraphNode),
		Adj:          make(map[string][]GraphEdge),
		cwd:          cwd,
		goMod:        goMod,
		goModRoot:    goModRoot,
		fileModTimes: make(map[string]time.Time),
	}
}

// ---------------------------------------------------------------------------
// Lazy file ensure + invalidation
// ---------------------------------------------------------------------------

// EnsureFile lazily indexes a file: if it has not been seen or its mod time
// changed, parse it for symbols, imports, and test relationships.
func (g *RetrievalGraph) EnsureFile(path string) {
	if g == nil || path == "" {
		return
	}
	info, err := os.Stat(path)
	if err != nil {
		return
	}
	if !info.Mode().IsRegular() {
		return
	}
	modTime := info.ModTime()

	if cached, ok := g.fileModTimes[path]; ok && cached.Equal(modTime) {
		return // already indexed and fresh
	}

	// Invalidate stale data, then rebuild.
	g.invalidateFileNodes(path)
	g.fileModTimes[path] = modTime

	// Register file node.
	g.addNode(path, NodeFile, modTime)

	content, _, err := readFileHead(path, graphMaxParseBytes)
	if err != nil {
		return
	}
	ext := strings.ToLower(filepath.Ext(path))

	// Parse language-specific structure.
	switch ext {
	case ".go":
		g.parseGoFile(path, content)
	case ".ts", ".tsx", ".js", ".jsx", ".mjs", ".mts":
		g.parseTSFile(path, content)
	case ".py":
		g.parsePythonFile(path, content)
	case ".rs":
		g.parseRustFile(path, content)
	case ".rb":
		g.parseRubyFile(path, content)
	case ".java":
		g.parseJavaFile(path, content)
	case ".c", ".cpp", ".cc", ".cxx", ".h", ".hpp":
		g.parseCFile(path, content)
	}

	// Register test ↔ source edges.
	g.ensureTestEdges(path)
}

// Invalidate removes all nodes and edges sourced from a file.
func (g *RetrievalGraph) Invalidate(path string) {
	if g == nil || path == "" {
		return
	}
	g.invalidateFileNodes(path)
	delete(g.fileModTimes, path)
}

func (g *RetrievalGraph) invalidateFileNodes(path string) {
	// Drop the file's node, its outgoing edges, and the symbols it contains.
	// Edges other files hold into this file stay: they record those files'
	// own imports and test pairings, which this file changing does not undo.
	symbols := make(map[string]struct{})
	for _, edge := range g.Adj[path] {
		if edge.Kind == EdgeContains {
			symbols[edge.Target] = struct{}{}
		}
	}
	delete(g.Nodes, path)
	delete(g.Adj, path)
	if len(symbols) == 0 {
		return
	}
	for key := range symbols {
		delete(g.Nodes, key)
		delete(g.Adj, key)
	}
	// Remove inbound edges pointing to the removed symbols.
	for src, edges := range g.Adj {
		filtered := edges[:0]
		for _, e := range edges {
			if _, remove := symbols[e.Target]; !remove {
				filtered = append(filtered, e)
			}
		}
		g.Adj[src] = filtered
	}
}

// InvalidateGitOverlay removes stale diff/staging edges and rebuilds from
// the new git status text.
func (g *RetrievalGraph) InvalidateGitOverlay(gitStatusText string) {
	if g == nil {
		return
	}
	if gitStatusText == g.lastGitStatus {
		return
	}
	g.lastGitStatus = gitStatusText

	// Remove all existing diff edges.
	for src, edges := range g.Adj {
		filtered := edges[:0]
		for _, e := range edges {
			if e.Kind != EdgeDiffActive {
				filtered = append(filtered, e)
			}
		}
		g.Adj[src] = filtered
	}

	// Add fresh diff edges.
	for _, path := range gitStatusPaths(gitStatusText, g.cwd) {
		g.EnsureFile(path)
		g.addEdge(path, path, EdgeDiffActive, edgeWeightDiff)
	}
}

// ---------------------------------------------------------------------------
// Seeding + scoring
// ---------------------------------------------------------------------------

// Seed registers anchor nodes into the graph.
func (g *RetrievalGraph) Seed(anchors []RetrievalAnchor, sessionTouched []string) {
	if g == nil {
		return
	}
	for _, anchor := range anchors {
		if anchor.FilePath != "" {
			for _, resolved := range resolveFilePath(anchor.FilePath, g.cwd) {
				g.EnsureFile(resolved)
			}
		}
		if anchor.ErrorString != "" {
			g.addNode("err:"+anchor.ErrorString, NodeError, time.Time{})
		}
	}
	for _, path := range sessionTouched {
		for _, resolved := range resolveFilePath(path, g.cwd) {
			g.EnsureFile(resolved)
			g.addEdge(resolved, resolved, EdgeSessionTouched, edgeWeightTouched)
		}
	}
	// Link symbol anchors last: re-parsing a file above drops its symbol
	// nodes along with every edge into them.
	for _, anchor := range anchors {
		if anchor.Symbol != "" {
			g.linkSymbolAnchor(anchor.Symbol)
		}
	}
}

// linkSymbolAnchor connects the node for a symbol named in the turn to every
// parsed symbol of that name, so Score's expansion from the anchor reaches the
// files that define it.
func (g *RetrievalGraph) linkSymbolAnchor(symbol string) {
	anchorKey := symbolAnchorPrefix + symbol
	g.addNode(anchorKey, NodeSymbol, time.Time{})
	for key := range g.Nodes {
		if g.isParsedSymbol(key, symbol) {
			g.addEdge(anchorKey, key, EdgeReferences, edgeWeightSymbolAnchor)
		}
	}
}

// isParsedSymbol reports whether key is a symbol or test node parsed out of a
// file under the given name, excluding the anchor nodes Seed adds.
func (g *RetrievalGraph) isParsedSymbol(key, symbol string) bool {
	node, ok := g.Nodes[key]
	if !ok || (node.Kind != NodeSymbol && node.Kind != NodeTest) {
		return false
	}
	return strings.HasSuffix(key, ":"+symbol) && !strings.HasPrefix(key, symbolAnchorPrefix)
}

// Score walks the graph from anchor nodes and returns scored file candidates.
// Returns (candidates, edgesExpanded).
func (g *RetrievalGraph) Score(anchors []RetrievalAnchor, gitStatusText string, sessionTouched []string) ([]RetrievalCandidate, int) {
	if g == nil {
		return nil, 0
	}

	scores := make(map[string]int)
	reasons := make(map[string]string)

	// Direct anchor scores.
	for _, anchor := range anchors {
		if anchor.FilePath != "" {
			for _, resolved := range resolveFilePath(anchor.FilePath, g.cwd) {
				addCandidateScore(scores, reasons, resolved, 3, "exact anchor")
			}
		}
	}

	// Git status scores.
	for _, path := range gitStatusPaths(gitStatusText, g.cwd) {
		addCandidateScore(scores, reasons, path, 4, "staged or modified")
	}

	// Session touched.
	for _, path := range sessionTouched {
		for _, resolved := range resolveFilePath(path, g.cwd) {
			addCandidateScore(scores, reasons, resolved, 2, "recently touched")
		}
	}

	// Error context scoring (reuse existing logic).
	scoreErrorAnchors(anchors, scores, reasons)

	// 1-hop expansion through graph edges.
	seedKeys := make([]string, 0, len(scores))
	for path := range scores {
		seedKeys = append(seedKeys, path)
	}
	// Also include symbol anchors as seeds.
	for _, anchor := range anchors {
		if anchor.Symbol != "" {
			seedKeys = append(seedKeys, symbolAnchorPrefix+anchor.Symbol)
		}
	}

	beforeExpand := len(scores)
	hop1Targets := g.expandHop(seedKeys, scores, reasons, 100)

	// 2-hop: only if 1-hop set is sparse.
	fileCandidateCount := 0
	for path := range scores {
		if n, ok := g.Nodes[path]; ok && n.Kind == NodeFile {
			fileCandidateCount++
		}
	}
	if fileCandidateCount < graphSecondHopMinCandidates && len(hop1Targets) > 0 {
		g.expandHop(hop1Targets, scores, reasons, graphSecondHopPenalty)
	}

	edgesExpanded := len(scores) - beforeExpand

	candidates := make([]RetrievalCandidate, 0, len(scores))
	for path, score := range scores {
		// Only file nodes are useful as snippet candidates.
		if n, ok := g.Nodes[path]; ok && n.Kind != NodeFile {
			continue
		}
		if !fileExists(path) {
			continue
		}
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

// expandHop walks one hop from the given keys and adds scores.
// weightPercent scales edge weights (100 = full, 50 = half for 2nd hop).
// Returns the new keys discovered this hop for potential further expansion.
func (g *RetrievalGraph) expandHop(keys []string, scores map[string]int, reasons map[string]string, weightPercent int) []string {
	var newKeys []string
	for _, key := range keys {
		for _, edge := range g.Adj[key] {
			// A file's own symbols say nothing new about the file. Crediting
			// the file once per symbol it contains would rank candidates by
			// symbol count instead of by the signals that seeded them.
			if edge.Kind == EdgeContains {
				continue
			}
			target := edge.Target
			weight := edge.Weight * weightPercent / 100
			if weight <= 0 {
				weight = 1
			}
			reason := edgeKindReason(edge.Kind)
			wasNew := scores[target] == 0
			addCandidateScore(scores, reasons, target, weight, reason)

			// For symbol/test nodes, also score their containing file.
			if n, ok := g.Nodes[target]; ok && (n.Kind == NodeSymbol || n.Kind == NodeTest) {
				g.scoreSymbolFile(target, scores, reasons, weight, reason)
			}

			if wasNew {
				newKeys = append(newKeys, target)
			}
		}
	}
	return newKeys
}

// scoreSymbolFile finds the file containing a symbol and scores it.
func (g *RetrievalGraph) scoreSymbolFile(symbolKey string, scores map[string]int, reasons map[string]string, weight int, reason string) {
	// Symbol keys have inbound EdgeContains from their file.
	// Walk all adjacencies to find files that contain this symbol.
	for fileKey, edges := range g.Adj {
		for _, e := range edges {
			if e.Target == symbolKey && e.Kind == EdgeContains {
				addCandidateScore(scores, reasons, fileKey, weight, reason)
				return
			}
		}
	}
}

func edgeKindReason(kind EdgeKind) string {
	switch kind {
	case EdgeContains:
		return "contains symbol"
	case EdgeImports:
		return "imported by anchor"
	case EdgeReferences:
		return "references symbol"
	case EdgeTestCovers:
		return "test covers"
	case EdgeToolMentions:
		return "tool output"
	case EdgeDiffActive:
		return "staged or modified"
	case EdgeSessionTouched:
		return "recently touched"
	case EdgePreferenceApplies:
		return "preference"
	default:
		return "graph edge"
	}
}

// ---------------------------------------------------------------------------
// Graph helpers
// ---------------------------------------------------------------------------

func (g *RetrievalGraph) addNode(key string, kind NodeKind, modTime time.Time) {
	if _, exists := g.Nodes[key]; exists {
		return
	}
	g.Nodes[key] = &GraphNode{Kind: kind, Key: key, ModTime: modTime}
}

func (g *RetrievalGraph) addEdge(source, target string, kind EdgeKind, weight int) {
	// Avoid duplicate edges.
	for _, e := range g.Adj[source] {
		if e.Target == target && e.Kind == kind {
			return
		}
	}
	g.Adj[source] = append(g.Adj[source], GraphEdge{Target: target, Kind: kind, Weight: weight})
}

// ---------------------------------------------------------------------------
// Test edge detection (multi-language)
// ---------------------------------------------------------------------------

func (g *RetrievalGraph) ensureTestEdges(path string) {
	pairs := testSourcePairs(path)
	for _, pair := range pairs {
		if fileExists(pair) {
			g.EnsureFile(pair)
			g.addEdge(path, pair, EdgeTestCovers, edgeWeightTestCovers)
			g.addEdge(pair, path, EdgeTestCovers, edgeWeightTestCovers)
		}
	}
}

// testSourcePairs returns the counterpart file(s) for a given path.
func testSourcePairs(path string) []string {
	base := filepath.Base(path)
	dir := filepath.Dir(path)

	switch {
	// Go: foo_test.go ↔ foo.go
	case strings.HasSuffix(base, "_test.go"):
		return []string{filepath.Join(dir, strings.TrimSuffix(base, "_test.go")+".go")}
	case strings.HasSuffix(base, ".go"):
		return []string{filepath.Join(dir, strings.TrimSuffix(base, ".go")+"_test.go")}

	// TS/JS: foo.test.ts ↔ foo.ts, foo.spec.ts ↔ foo.ts, etc.
	case hasAnySuffix(base, ".test.ts", ".test.tsx", ".test.js", ".test.jsx"):
		src := stripTestSuffix(base, ".test")
		return []string{filepath.Join(dir, src)}
	case hasAnySuffix(base, ".spec.ts", ".spec.tsx", ".spec.js", ".spec.jsx"):
		src := stripTestSuffix(base, ".spec")
		return []string{filepath.Join(dir, src)}
	case hasAnySuffix(base, ".ts", ".tsx", ".js", ".jsx"):
		ext := filepath.Ext(base)
		stem := strings.TrimSuffix(base, ext)
		return []string{
			filepath.Join(dir, stem+".test"+ext),
			filepath.Join(dir, stem+".spec"+ext),
		}

	// Python: test_foo.py ↔ foo.py, foo_test.py ↔ foo.py
	case strings.HasSuffix(base, ".py") && strings.HasPrefix(base, "test_"):
		return []string{filepath.Join(dir, strings.TrimPrefix(base, "test_"))}
	case strings.HasSuffix(base, "_test.py"):
		return []string{filepath.Join(dir, strings.TrimSuffix(base, "_test.py")+".py")}
	case strings.HasSuffix(base, ".py"):
		stem := strings.TrimSuffix(base, ".py")
		return []string{
			filepath.Join(dir, "test_"+base),
			filepath.Join(dir, stem+"_test.py"),
		}

	// Ruby: foo_spec.rb ↔ foo.rb
	case strings.HasSuffix(base, "_spec.rb"):
		return []string{filepath.Join(dir, strings.TrimSuffix(base, "_spec.rb")+".rb")}
	case strings.HasSuffix(base, ".rb"):
		return []string{filepath.Join(dir, strings.TrimSuffix(base, ".rb")+"_spec.rb")}

	// Java: FooTest.java ↔ Foo.java
	case strings.HasSuffix(base, "Test.java"):
		return []string{filepath.Join(dir, strings.TrimSuffix(base, "Test.java")+".java")}
	case strings.HasSuffix(base, ".java"):
		return []string{filepath.Join(dir, strings.TrimSuffix(base, ".java")+"Test.java")}
	}

	return nil
}

func hasAnySuffix(s string, suffixes ...string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

func stripTestSuffix(filename, marker string) string {
	ext := filepath.Ext(filename)
	stem := strings.TrimSuffix(filename, ext)
	stem = strings.TrimSuffix(stem, marker)
	return stem + ext
}

// ---------------------------------------------------------------------------
// Language parsers (line-by-line regex, not AST)
// ---------------------------------------------------------------------------

// --- Go ---

var goFuncPattern = regexp.MustCompile(`^func\s+(?:\([^)]+\)\s+)?([A-Z]\w+)`)
var goTypePattern = regexp.MustCompile(`^type\s+([A-Z]\w+)`)
var goTestFuncPattern = regexp.MustCompile(`^func\s+(Test\w+)\s*\(`)

func (g *RetrievalGraph) parseGoFile(path, content string) {
	symCount := 0
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)

		if m := goTestFuncPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			key := path + ":" + m[1]
			g.addNode(key, NodeTest, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
		} else if m := goFuncPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			key := path + ":" + m[1]
			g.addNode(key, NodeSymbol, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
		} else if m := goTypePattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			key := path + ":" + m[1]
			g.addNode(key, NodeSymbol, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
		}

		if symCount >= graphMaxNodesPerFile {
			break
		}
	}

	// Import edges.
	g.parseGoImportEdges(path, content)
}

func (g *RetrievalGraph) parseGoImportEdges(filePath, content string) {
	imports := parseGoImports(content)
	added := 0
	for _, imp := range imports {
		if added >= graphMaxImportsPerFile {
			break
		}
		if g.goMod == "" || !strings.HasPrefix(imp, g.goMod) {
			continue
		}
		relPkg := strings.TrimPrefix(imp, g.goMod)
		relPkg = strings.TrimPrefix(relPkg, "/")
		pkgDir := filepath.Join(g.goModRoot, relPkg)
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
			target := filepath.Join(pkgDir, name)
			g.addNode(target, NodeFile, time.Time{})
			g.addEdge(filePath, target, EdgeImports, edgeWeightImports)
			added++
			if added >= graphMaxImportsPerFile {
				break
			}
		}
	}
}

// --- TypeScript / JavaScript ---

var tsExportFuncPattern = regexp.MustCompile(`(?:export\s+)?(?:async\s+)?function\s+([A-Za-z_$]\w*)`)
var tsExportClassPattern = regexp.MustCompile(`(?:export\s+)?class\s+([A-Z]\w*)`)
var tsExportConstPattern = regexp.MustCompile(`export\s+(?:const|let|var)\s+([A-Za-z_$]\w*)`)
var tsImportPattern = regexp.MustCompile(`(?:import|require)\s*(?:\(?\s*['"]([^'"]+)['"]|.+from\s+['"]([^'"]+)['"])`)

func (g *RetrievalGraph) parseTSFile(path, content string) {
	symCount := 0
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)

		if m := tsExportClassPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			key := path + ":" + m[1]
			g.addNode(key, NodeSymbol, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
		} else if m := tsExportFuncPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			key := path + ":" + m[1]
			g.addNode(key, NodeSymbol, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
		} else if m := tsExportConstPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			key := path + ":" + m[1]
			g.addNode(key, NodeSymbol, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
		}

		if symCount >= graphMaxNodesPerFile {
			break
		}
	}

	g.parseTSImportEdges(path, content)
}

func (g *RetrievalGraph) parseTSImportEdges(filePath, content string) {
	dir := filepath.Dir(filePath)
	added := 0
	for line := range strings.SplitSeq(content, "\n") {
		if added >= graphMaxImportsPerFile {
			break
		}
		m := tsImportPattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) < 3 {
			continue
		}
		importPath := m[1]
		if importPath == "" {
			importPath = m[2]
		}
		if importPath == "" || !strings.HasPrefix(importPath, ".") {
			continue // skip node_modules / bare specifiers
		}
		resolved := resolveTSImport(dir, importPath)
		if resolved == "" {
			continue
		}
		g.addNode(resolved, NodeFile, time.Time{})
		g.addEdge(filePath, resolved, EdgeImports, edgeWeightImports)
		added++
	}
}

func resolveTSImport(dir, importPath string) string {
	base := filepath.Join(dir, importPath)
	// Try exact, then with extensions.
	for _, ext := range []string{"", ".ts", ".tsx", ".js", ".jsx", "/index.ts", "/index.tsx", "/index.js", "/index.jsx"} {
		candidate := base + ext
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

// --- Python ---

var pyDefPattern = regexp.MustCompile(`^(?:async\s+)?def\s+([A-Za-z_]\w*)`)
var pyClassPattern = regexp.MustCompile(`^class\s+([A-Z]\w*)`)
var pyImportFromPattern = regexp.MustCompile(`^from\s+(\.\S+)\s+import`)
var pyImportPattern = regexp.MustCompile(`^import\s+(\.\S+)`)

func (g *RetrievalGraph) parsePythonFile(path, content string) {
	symCount := 0
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)

		if m := pyClassPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			key := path + ":" + m[1]
			g.addNode(key, NodeSymbol, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
		} else if m := pyDefPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			name := m[1]
			kind := NodeSymbol
			if strings.HasPrefix(name, "test_") {
				kind = NodeTest
			}
			key := path + ":" + name
			g.addNode(key, kind, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
		}

		if symCount >= graphMaxNodesPerFile {
			break
		}
	}

	g.parsePythonImportEdges(path, content)
}

func (g *RetrievalGraph) parsePythonImportEdges(filePath, content string) {
	dir := filepath.Dir(filePath)
	added := 0
	for line := range strings.SplitSeq(content, "\n") {
		if added >= graphMaxImportsPerFile {
			break
		}
		trimmed := strings.TrimSpace(line)
		var relImport string
		if m := pyImportFromPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			relImport = m[1]
		} else if m := pyImportPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			relImport = m[1]
		}
		if relImport == "" || !strings.HasPrefix(relImport, ".") {
			continue
		}
		resolved := resolvePythonImport(dir, relImport)
		if resolved == "" {
			continue
		}
		g.addNode(resolved, NodeFile, time.Time{})
		g.addEdge(filePath, resolved, EdgeImports, edgeWeightImports)
		added++
	}
}

func resolvePythonImport(dir, relImport string) string {
	// Count leading dots for relative depth.
	dots := 0
	for _, c := range relImport {
		if c == '.' {
			dots++
		} else {
			break
		}
	}
	module := relImport[dots:]
	base := dir
	for i := 1; i < dots; i++ {
		base = filepath.Dir(base)
	}
	modulePath := strings.ReplaceAll(module, ".", string(filepath.Separator))
	for _, candidate := range []string{
		filepath.Join(base, modulePath+".py"),
		filepath.Join(base, modulePath, "__init__.py"),
	} {
		if fileExists(candidate) {
			return candidate
		}
	}
	return ""
}

// --- Rust ---

var rustFnPattern = regexp.MustCompile(`^(?:pub\s+)?(?:async\s+)?fn\s+([a-z_]\w*)`)
var rustStructPattern = regexp.MustCompile(`^(?:pub\s+)?(?:struct|enum|trait)\s+([A-Z]\w*)`)
var rustImplPattern = regexp.MustCompile(`^impl(?:<[^>]*>)?\s+([A-Z]\w*)`)
var rustUsePattern = regexp.MustCompile(`^(?:pub\s+)?use\s+(?:crate|super)::([^;{]+)`)
var rustTestAttr = regexp.MustCompile(`#\[(?:tokio::)?test`)

func (g *RetrievalGraph) parseRustFile(path, content string) {
	symCount := 0
	nextIsTest := false
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)

		if rustTestAttr.MatchString(trimmed) {
			nextIsTest = true
			continue
		}

		if m := rustStructPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			key := path + ":" + m[1]
			g.addNode(key, NodeSymbol, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
			nextIsTest = false
		} else if m := rustImplPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			key := path + ":" + m[1]
			g.addNode(key, NodeSymbol, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
			nextIsTest = false
		} else if m := rustFnPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			kind := NodeSymbol
			if nextIsTest {
				kind = NodeTest
			}
			key := path + ":" + m[1]
			g.addNode(key, kind, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
			nextIsTest = false
		} else {
			nextIsTest = false
		}

		if symCount >= graphMaxNodesPerFile {
			break
		}
	}

	g.parseRustUseEdges(path, content)
}

func (g *RetrievalGraph) parseRustUseEdges(filePath, content string) {
	dir := filepath.Dir(filePath)
	added := 0
	for line := range strings.SplitSeq(content, "\n") {
		if added >= graphMaxImportsPerFile {
			break
		}
		m := rustUsePattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) < 2 {
			continue
		}
		// Try to resolve crate-relative use to a file.
		parts := strings.SplitN(strings.TrimSpace(m[1]), "::", 2)
		moduleName := strings.TrimSpace(parts[0])
		if moduleName == "" {
			continue
		}
		for _, candidate := range []string{
			filepath.Join(dir, moduleName+".rs"),
			filepath.Join(dir, moduleName, "mod.rs"),
		} {
			if fileExists(candidate) {
				g.addNode(candidate, NodeFile, time.Time{})
				g.addEdge(filePath, candidate, EdgeImports, edgeWeightImports)
				added++
				break
			}
		}
	}
}

// --- Ruby ---

var rubyDefPattern = regexp.MustCompile(`^(?:def\s+)([a-z_]\w*[!?=]?)`)
var rubyClassPattern = regexp.MustCompile(`^(?:class|module)\s+([A-Z]\w*)`)
var rubyRequirePattern = regexp.MustCompile(`require(?:_relative)?\s+['"]([^'"]+)['"]`)

func (g *RetrievalGraph) parseRubyFile(path, content string) {
	symCount := 0
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)

		if m := rubyClassPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			key := path + ":" + m[1]
			g.addNode(key, NodeSymbol, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
		} else if m := rubyDefPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			key := path + ":" + m[1]
			g.addNode(key, NodeSymbol, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
		}

		if symCount >= graphMaxNodesPerFile {
			break
		}
	}

	g.parseRubyRequireEdges(path, content)
}

func (g *RetrievalGraph) parseRubyRequireEdges(filePath, content string) {
	dir := filepath.Dir(filePath)
	added := 0
	for line := range strings.SplitSeq(content, "\n") {
		if added >= graphMaxImportsPerFile {
			break
		}
		trimmed := strings.TrimSpace(line)
		if !strings.Contains(trimmed, "require_relative") {
			continue
		}
		m := rubyRequirePattern.FindStringSubmatch(trimmed)
		if len(m) < 2 {
			continue
		}
		target := filepath.Join(dir, m[1])
		if !strings.HasSuffix(target, ".rb") {
			target += ".rb"
		}
		if fileExists(target) {
			g.addNode(target, NodeFile, time.Time{})
			g.addEdge(filePath, target, EdgeImports, edgeWeightImports)
			added++
		}
	}
}

// --- Java ---

var javaClassPattern = regexp.MustCompile(`^(?:public\s+)?(?:abstract\s+)?(?:class|interface|enum)\s+([A-Z]\w*)`)
var javaMethodPattern = regexp.MustCompile(`^\s+(?:public|protected|private|static|\s)*\s+\w+\s+([a-z]\w*)\s*\(`)

func (g *RetrievalGraph) parseJavaFile(path, content string) {
	symCount := 0
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)

		if m := javaClassPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			key := path + ":" + m[1]
			g.addNode(key, NodeSymbol, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
		} else if m := javaMethodPattern.FindStringSubmatch(line); len(m) >= 2 {
			key := path + ":" + m[1]
			g.addNode(key, NodeSymbol, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
		}

		if symCount >= graphMaxNodesPerFile {
			break
		}
	}
	// Java import resolution requires package→directory mapping that is too
	// project-specific for a generic parser. Skipped — test edges suffice.
}

// --- C / C++ ---

var cFuncPattern = regexp.MustCompile(`^(?:static\s+)?(?:inline\s+)?(?:const\s+)?\w[\w*\s]+\s+([a-zA-Z_]\w*)\s*\(`)
var cIncludeLocalPattern = regexp.MustCompile(`^#include\s+"([^"]+)"`)

func (g *RetrievalGraph) parseCFile(path, content string) {
	symCount := 0
	for line := range strings.SplitSeq(content, "\n") {
		trimmed := strings.TrimSpace(line)

		if m := cFuncPattern.FindStringSubmatch(trimmed); len(m) >= 2 {
			name := m[1]
			// Skip common C keywords that match the pattern.
			if name == "if" || name == "for" || name == "while" || name == "switch" || name == "return" || name == "sizeof" {
				continue
			}
			key := path + ":" + name
			g.addNode(key, NodeSymbol, time.Time{})
			g.addEdge(path, key, EdgeContains, edgeWeightContains)
			symCount++
		}

		if symCount >= graphMaxNodesPerFile {
			break
		}
	}

	g.parseCIncludeEdges(path, content)
}

func (g *RetrievalGraph) parseCIncludeEdges(filePath, content string) {
	dir := filepath.Dir(filePath)
	added := 0
	for line := range strings.SplitSeq(content, "\n") {
		if added >= graphMaxImportsPerFile {
			break
		}
		m := cIncludeLocalPattern.FindStringSubmatch(strings.TrimSpace(line))
		if len(m) < 2 {
			continue
		}
		target := filepath.Join(dir, m[1])
		if fileExists(target) {
			g.addNode(target, NodeFile, time.Time{})
			g.addEdge(filePath, target, EdgeImports, edgeWeightImports)
			added++
		}
	}
}

// ---------------------------------------------------------------------------
// Symbol anchor extraction
// ---------------------------------------------------------------------------

// symbolAnchorPattern matches likely exported symbol names in text.
var symbolAnchorPattern = regexp.MustCompile(`\b([A-Z][a-zA-Z0-9_]{2,})\b`)

// ExtractSymbolAnchors scans text for identifiers that match known graph symbol nodes.
func (g *RetrievalGraph) ExtractSymbolAnchors(text string) []string {
	if g == nil || text == "" {
		return nil
	}

	matches := symbolAnchorPattern.FindAllString(text, 50)
	if len(matches) == 0 {
		return nil
	}

	var symbols []string
	seen := make(map[string]struct{})
	for _, match := range matches {
		if _, ok := seen[match]; ok {
			continue
		}
		// Only symbols parsed from a file count: an anchor node left by an
		// earlier turn would otherwise keep matching after its file changed.
		for key := range g.Nodes {
			if g.isParsedSymbol(key, match) {
				seen[match] = struct{}{}
				symbols = append(symbols, match)
				break
			}
		}
	}
	return symbols
}
