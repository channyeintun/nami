package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const defaultGoReferencesLimit = 100

type GoReferencesTool struct{}

type goReferenceMatch struct {
	Path    string `json:"path"`
	Line    int    `json:"line"`
	Column  int    `json:"column"`
	Package string `json:"package"`
	Kind    string `json:"kind"`
	Source  string `json:"source"`
	Score   int    `json:"-"`
}

func NewGoReferencesTool() *GoReferencesTool {
	return &GoReferencesTool{}
}

func (t *GoReferencesTool) Name() string {
	return "go_references"
}

func (t *GoReferencesTool) Description() string {
	return "Find Go identifier references with parser-backed file, line, column, and usage context information."
}

func (t *GoReferencesTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"symbol": map[string]any{
				"type":        "string",
				"description": "The Go identifier name to search for.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Optional file or directory to search in. Defaults to the current working directory.",
			},
			"include_definitions": map[string]any{
				"type":        "boolean",
				"description": "Include declaration sites as well as references. Defaults to false.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Optional maximum number of references to return. Defaults to 100.",
				"minimum":     1,
			},
		},
		"required": []string{"symbol"},
	}
}

func (t *GoReferencesTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *GoReferencesTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *GoReferencesTool) Validate(input ToolInput) error {
	symbol, ok := stringParam(input.Params, "symbol")
	if !ok || strings.TrimSpace(symbol) == "" {
		return fmt.Errorf("go_references requires symbol")
	}
	if value, ok := intParam(input.Params, "max_results"); ok && value < 1 {
		return fmt.Errorf("max_results must be >= 1")
	}
	return nil
}

func (t *GoReferencesTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	symbol, _ := stringParam(input.Params, "symbol")
	symbol = strings.TrimSpace(symbol)
	searchPath, err := resolveSearchPath(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}
	includeDefinitions := boolOrDefault(input.Params, "include_definitions", false)
	maxResults := intOrDefault(input.Params, "max_results", defaultGoReferencesLimit)

	rootPath, err := goDefinitionRoot(searchPath)
	if err != nil {
		return ToolOutput{}, err
	}

	matches := make([]goReferenceMatch, 0, maxResults)
	truncated := false

	collectFromFile := func(filePath string) error {
		if filepath.Ext(filePath) != ".go" {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, filePath, nil, parser.SkipObjectResolution)
		if err != nil {
			return nil
		}
		fileMatches := findGoReferencesInFile(fset, filePath, file.Name.Name, file, symbol, includeDefinitions)
		matches = append(matches, fileMatches...)
		if len(matches) > maxResults {
			matches = matches[:maxResults]
			truncated = true
		}
		return nil
	}

	info, err := os.Stat(searchPath)
	if err != nil {
		return ToolOutput{}, err
	}
	if !info.IsDir() {
		if err := collectFromFile(searchPath); err != nil {
			return ToolOutput{}, err
		}
	} else {
		walkErr := filepath.WalkDir(rootPath, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return nil
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			default:
			}
			if entry.IsDir() {
				if path != rootPath {
					if _, skip := goDefinitionSkippedDirs[entry.Name()]; skip {
						return filepath.SkipDir
					}
				}
				return nil
			}
			if filepath.Ext(path) != ".go" {
				return nil
			}
			if len(matches) >= maxResults {
				truncated = true
				return filepath.SkipAll
			}
			return collectFromFile(path)
		})
		if walkErr != nil && walkErr != filepath.SkipAll {
			return ToolOutput{}, walkErr
		}
	}

	if len(matches) == 0 {
		return ToolOutput{Output: "No Go references found"}, nil
	}

	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].Score != matches[j].Score {
			return matches[i].Score > matches[j].Score
		}
		if matches[i].Path != matches[j].Path {
			return matches[i].Path < matches[j].Path
		}
		if matches[i].Line != matches[j].Line {
			return matches[i].Line < matches[j].Line
		}
		return matches[i].Column < matches[j].Column
	})

	encoded, err := json.MarshalIndent(matches, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal go_references: %w", err)
	}

	return ToolOutput{Output: string(encoded), Truncated: truncated}, nil
}

func findGoReferencesInFile(fset *token.FileSet, filePath, packageName string, file *ast.File, symbol string, includeDefinitions bool) []goReferenceMatch {
	definitions := goDefinitionPositions(fset, file, symbol)
	sourceLines := goSourceLines(filePath)
	matches := make([]goReferenceMatch, 0)
	stack := make([]ast.Node, 0, 16)

	ast.Inspect(file, func(node ast.Node) bool {
		if node == nil {
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
			return true
		}

		parent := ast.Node(nil)
		if len(stack) > 0 {
			parent = stack[len(stack)-1]
		}
		stack = append(stack, node)

		ident, ok := node.(*ast.Ident)
		if !ok || ident.Name != symbol {
			return true
		}

		position := fset.Position(ident.Pos())
		_, isDefinition := definitions[position.Offset]
		if !includeDefinitions {
			if isDefinition {
				return true
			}
		}

		matches = append(matches, goReferenceMatch{
			Path:    filePath,
			Line:    position.Line,
			Column:  position.Column,
			Package: packageName,
			Kind:    goReferenceKind(parent, ident, isDefinition),
			Source:  goSourceLine(sourceLines, position.Line),
			Score:   goReferenceScore(parent, isDefinition),
		})
		return true
	})

	return matches
}

func goDefinitionPositions(fset *token.FileSet, file *ast.File, symbol string) map[int]struct{} {
	positions := make(map[int]struct{})
	for _, decl := range file.Decls {
		switch typed := decl.(type) {
		case *ast.FuncDecl:
			if typed.Name != nil && typed.Name.Name == symbol {
				positions[fset.Position(typed.Name.Pos()).Offset] = struct{}{}
			}
		case *ast.GenDecl:
			for _, spec := range typed.Specs {
				switch specTyped := spec.(type) {
				case *ast.TypeSpec:
					if specTyped.Name != nil && specTyped.Name.Name == symbol {
						positions[fset.Position(specTyped.Name.Pos()).Offset] = struct{}{}
					}
				case *ast.ValueSpec:
					for _, name := range specTyped.Names {
						if name != nil && name.Name == symbol {
							positions[fset.Position(name.Pos()).Offset] = struct{}{}
						}
					}
				}
			}
		}
	}
	return positions
}

func goReferenceKind(parent ast.Node, ident *ast.Ident, isDefinition bool) string {
	if isDefinition {
		return "definition"
	}
	switch typed := parent.(type) {
	case *ast.CallExpr:
		if typed.Fun == ident {
			return "call"
		}
	case *ast.SelectorExpr:
		if typed.Sel == ident {
			return "selector"
		}
	case *ast.AssignStmt:
		return "assignment"
	case *ast.CompositeLit:
		return "composite_literal"
	case *ast.ReturnStmt:
		return "return"
	}
	return "reference"
}

func goReferenceScore(parent ast.Node, isDefinition bool) int {
	if isDefinition {
		return 100
	}
	switch parent.(type) {
	case *ast.CallExpr:
		return 95
	case *ast.SelectorExpr:
		return 90
	case *ast.AssignStmt:
		return 85
	default:
		return 80
	}
}

func goSourceLines(filePath string) []string {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return nil
	}
	return strings.Split(strings.ReplaceAll(string(content), "\r\n", "\n"), "\n")
}

func goSourceLine(lines []string, line int) string {
	if line <= 0 || line > len(lines) {
		return ""
	}
	return strings.TrimSpace(lines[line-1])
}
