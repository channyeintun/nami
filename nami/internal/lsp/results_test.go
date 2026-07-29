package lsp

import (
	"encoding/json"
	"path/filepath"
	"runtime"
	"testing"
)

func TestNormalizeOperation(t *testing.T) {
	cases := map[string]Operation{
		"go_to_definition":     OperationDefinition,
		"goToDefinition":       OperationDefinition,
		"findReferences":       OperationReferences,
		"hover":                OperationHover,
		"documentSymbol":       OperationDocumentSymbols,
		"workspaceSymbol":      OperationWorkspaceSymbols,
		"goToImplementation":   OperationImplementation,
		"  find_references  ":  OperationReferences,
		"something_unexpected": Operation("something_unexpected"),
	}
	for input, want := range cases {
		if got := NormalizeOperation(input); got != want {
			t.Errorf("NormalizeOperation(%q) = %q, want %q", input, got, want)
		}
	}
}

func TestValidateRequest(t *testing.T) {
	cases := []struct {
		name    string
		request Request
		wantErr bool
	}{
		{"definition ok", Request{Operation: OperationDefinition, FilePath: "/a.go", Line: 1, Column: 1}, false},
		{"definition without path", Request{Operation: OperationDefinition, Line: 1, Column: 1}, true},
		{"definition without position", Request{Operation: OperationDefinition, FilePath: "/a.go"}, true},
		{"definition zero column", Request{Operation: OperationDefinition, FilePath: "/a.go", Line: 3, Column: 0}, true},
		{"document symbols ok", Request{Operation: OperationDocumentSymbols, FilePath: "/a.go"}, false},
		{"document symbols without path", Request{Operation: OperationDocumentSymbols}, true},
		{"workspace symbols ok", Request{Operation: OperationWorkspaceSymbols, Query: "Foo"}, false},
		{"workspace symbols blank query", Request{Operation: OperationWorkspaceSymbols, Query: "   "}, true},
		{"unknown operation", Request{Operation: "teleport"}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateRequest(tc.request)
			if tc.wantErr != (err != nil) {
				t.Fatalf("ValidateRequest(%+v) error = %v, wantErr %v", tc.request, err, tc.wantErr)
			}
		})
	}
}

func TestFileURIRoundTrip(t *testing.T) {
	path := filepath.Join(string(filepath.Separator), "tmp", "project", "main.go")
	if runtime.GOOS == "windows" {
		t.Skip("windows drive-letter URIs are covered by fileURIToPath's dedicated branch")
	}
	uri := pathToFileURI(path)
	if got, want := uri, "file:///tmp/project/main.go"; got != want {
		t.Fatalf("pathToFileURI = %q, want %q", got, want)
	}
	if got := fileURIToPath(uri); got != path {
		t.Fatalf("fileURIToPath = %q, want %q", got, path)
	}
}

func TestFileURIToPathPassesThroughNonFileURIs(t *testing.T) {
	if got, want := fileURIToPath("https://example.com/x"), "https://example.com/x"; got != want {
		t.Fatalf("fileURIToPath = %q, want %q", got, want)
	}
}

func TestSymbolKindName(t *testing.T) {
	cases := map[int]string{
		1:  "file",
		12: "function",
		26: "type_parameter",
		0:  "kind_0",
		27: "kind_27",
		-3: "kind_-3",
	}
	for kind, want := range cases {
		if got := symbolKindName(kind); got != want {
			t.Errorf("symbolKindName(%d) = %q, want %q", kind, got, want)
		}
	}
}

func TestExtractHoverContents(t *testing.T) {
	cases := []struct {
		name  string
		value any
		want  string
	}{
		{"nil", nil, ""},
		{"plain string", "docs", "docs"},
		{"markup content", map[string]any{"kind": "markdown", "value": "# Title"}, "# Title"},
		{"marked string keeps language", map[string]any{"language": "go", "value": "func f()"}, "go\nfunc f()"},
		{"map without value", map[string]any{"kind": "markdown"}, ""},
		{"array joins parts", []any{"first", map[string]any{"value": "second"}}, "first\n\nsecond"},
		{"array skips blanks", []any{"first", "  "}, "first"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := extractHoverContents(tc.value); got != tc.want {
				t.Fatalf("extractHoverContents(%#v) = %q, want %q", tc.value, got, tc.want)
			}
		})
	}
}

func TestLocationRowsFromAnyHandlesLinksAndLocations(t *testing.T) {
	links := []map[string]any{{
		"targetUri": "file:///project/a.go",
		"targetRange": map[string]any{
			"start": map[string]any{"line": 4, "character": 2},
			"end":   map[string]any{"line": 4, "character": 8},
		},
		"targetSelectionRange": map[string]any{
			"start": map[string]any{"line": 4, "character": 2},
			"end":   map[string]any{"line": 4, "character": 8},
		},
	}}
	rows := locationRowsFromAny(links, "definition")
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	if rows[0].Line != 5 || rows[0].Column != 3 {
		t.Fatalf("row position = %d:%d, want 5:3 (1-based)", rows[0].Line, rows[0].Column)
	}

	single := map[string]any{
		"uri": "file:///project/b.go",
		"range": map[string]any{
			"start": map[string]any{"line": 0, "character": 0},
			"end":   map[string]any{"line": 0, "character": 1},
		},
	}
	rows = locationRowsFromAny(single, "reference")
	if len(rows) != 1 || rows[0].Kind != "reference" {
		t.Fatalf("rows = %#v, want one reference row", rows)
	}
}

func TestLocationRowsFromAnyReturnsNilForUnknownShapes(t *testing.T) {
	if rows := locationRowsFromAny(map[string]any{"unexpected": true}, "definition"); rows != nil {
		t.Fatalf("rows = %#v, want nil", rows)
	}
}

func TestDocumentSymbolRowsFlattensChildren(t *testing.T) {
	raw, err := json.Marshal([]documentSymbol{{
		Name:           "Server",
		Kind:           23,
		Range:          textRange{Start: position{Line: 1}, End: position{Line: 9}},
		SelectionRange: textRange{Start: position{Line: 1, Character: 5}, End: position{Line: 1, Character: 11}},
		Children: []documentSymbol{{
			Name:           "Start",
			Kind:           6,
			SelectionRange: textRange{Start: position{Line: 3, Character: 1}, End: position{Line: 3, Character: 6}},
		}},
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rows := documentSymbolRows(raw, "/project/server.go")
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2", len(rows))
	}
	var child ResultRow
	for _, row := range rows {
		if row.Name == "Start" {
			child = row
		}
	}
	if child.ContainerName != "Server" {
		t.Fatalf("child container = %q, want %q", child.ContainerName, "Server")
	}
	if child.SymbolKind != "method" {
		t.Fatalf("child kind = %q, want %q", child.SymbolKind, "method")
	}
}

func TestDocumentSymbolRowsFallsBackToSymbolInformation(t *testing.T) {
	raw, err := json.Marshal([]symbolInformation{{
		Name:     "Top",
		Kind:     12,
		Location: location{URI: "file:///project/a.go", Range: textRange{Start: position{Line: 2}}},
	}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	rows := documentSymbolRows(raw, "/project/a.go")
	if len(rows) != 1 || rows[0].SymbolKind != "function" {
		t.Fatalf("rows = %#v, want one function row", rows)
	}
}

func TestSortRowsOrdersByFileThenPosition(t *testing.T) {
	rows := sortRows([]ResultRow{
		{FilePath: "b.go", Line: 1},
		{FilePath: "a.go", Line: 5, Column: 2},
		{FilePath: "a.go", Line: 5, Column: 1},
		{FilePath: "a.go", Line: 2},
	})
	want := []struct {
		path   string
		line   int
		column int
	}{
		{"a.go", 2, 0},
		{"a.go", 5, 1},
		{"a.go", 5, 2},
		{"b.go", 1, 0},
	}
	for i, expected := range want {
		if rows[i].FilePath != expected.path || rows[i].Line != expected.line || rows[i].Column != expected.column {
			t.Fatalf("row %d = %+v, want %+v", i, rows[i], expected)
		}
	}
}

func TestLimitResults(t *testing.T) {
	rows := []ResultRow{{Name: "a"}, {Name: "b"}, {Name: "c"}}
	if got := limitResults(rows, 0); len(got) != 3 {
		t.Errorf("limitResults(rows, 0) = %d rows, want 3", len(got))
	}
	if got := limitResults(rows, 2); len(got) != 2 {
		t.Errorf("limitResults(rows, 2) = %d rows, want 2", len(got))
	}
	if got := limitResults(rows, 10); len(got) != 3 {
		t.Errorf("limitResults(rows, 10) = %d rows, want 3", len(got))
	}
}

func TestLanguageIDForPath(t *testing.T) {
	typescript := serverConfigs[1]
	if got := languageIDForPath("/project/app.js", typescript); got != "javascript" {
		t.Errorf("languageIDForPath(.js) = %q, want javascript", got)
	}
	if got := languageIDForPath("/project/app.tsx", typescript); got != "typescript" {
		t.Errorf("languageIDForPath(.tsx) = %q, want typescript", got)
	}
	if got := languageIDForPath("/project/main.go", serverConfigs[0]); got != "go" {
		t.Errorf("languageIDForPath(.go) = %q, want go", got)
	}
}

func TestDetectWorkspaceRoot(t *testing.T) {
	root := t.TempDir()
	nested := filepath.Join(root, "internal", "pkg")
	if err := mkdirAll(nested); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := writeFile(filepath.Join(root, "go.mod"), "module example.com/x\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	if got := detectWorkspaceRoot(nested, []string{"go.mod"}); got != root {
		t.Fatalf("detectWorkspaceRoot = %q, want %q", got, root)
	}
	if got := detectWorkspaceRoot(nested, []string{"never-present.marker"}); got != nested {
		t.Fatalf("detectWorkspaceRoot without marker = %q, want %q", got, nested)
	}
}

func TestResolveServerConfigRejectsUnknownExtension(t *testing.T) {
	if _, _, err := resolveServerConfig(Request{FilePath: "/project/notes.txt"}); err == nil {
		t.Fatal("resolveServerConfig succeeded for .txt, want error")
	}
}

func TestResolveServerConfigMatchesExtension(t *testing.T) {
	root := t.TempDir()
	if err := writeFile(filepath.Join(root, "go.mod"), "module example.com/x\n"); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}
	server, workspace, err := resolveServerConfig(Request{FilePath: filepath.Join(root, "main.go")})
	if err != nil {
		t.Fatalf("resolveServerConfig: %v", err)
	}
	if server.Name != "gopls" {
		t.Errorf("server = %q, want gopls", server.Name)
	}
	if workspace != root {
		t.Errorf("workspace = %q, want %q", workspace, root)
	}
}
