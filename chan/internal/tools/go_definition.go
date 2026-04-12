package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const defaultGoDefinitionLimit = 20

var goDefinitionSkippedDirs = map[string]struct{}{
	".git":         {},
	"build":        {},
	"dist":         {},
	"node_modules": {},
	"vendor":       {},
}

type GoDefinitionTool struct{}

type goDefinitionMatch struct {
	Path      string `json:"path"`
	Line      int    `json:"line"`
	Column    int    `json:"column"`
	Package   string `json:"package"`
	Kind      string `json:"kind"`
	Signature string `json:"signature"`
	Score     int    `json:"-"`
}

func NewGoDefinitionTool() *GoDefinitionTool {
	return &GoDefinitionTool{}
}

func (t *GoDefinitionTool) Name() string {
	return "go_definition"
}

func (t *GoDefinitionTool) Description() string {
	return "Find Go symbol definitions using the Go parser, returning precise file, line, column, package, and signature information."
}

func (t *GoDefinitionTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"symbol": map[string]any{
				"type":        "string",
				"description": "The Go symbol name to resolve.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Optional file or directory to search in. Defaults to the current working directory.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"description": "Optional maximum number of definitions to return. Defaults to 20.",
				"minimum":     1,
			},
		},
		"required": []string{"symbol"},
	}
}

func (t *GoDefinitionTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *GoDefinitionTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *GoDefinitionTool) Validate(input ToolInput) error {
	symbol, ok := stringParam(input.Params, "symbol")
	if !ok || strings.TrimSpace(symbol) == "" {
		return fmt.Errorf("go_definition requires symbol")
	}
	if value, ok := intParam(input.Params, "max_results"); ok && value < 1 {
		return fmt.Errorf("max_results must be >= 1")
	}
	return nil
}

func (t *GoDefinitionTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	symbol, _ := stringParam(input.Params, "symbol")
	symbol = strings.TrimSpace(symbol)
	searchPath, err := resolveSearchPath(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}
	maxResults := intOrDefault(input.Params, "max_results", defaultGoDefinitionLimit)

	rootPath, err := goDefinitionRoot(searchPath)
	if err != nil {
		return ToolOutput{}, err
	}

	matches := make([]goDefinitionMatch, 0, maxResults)
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

		matches = append(matches, findGoDefinitionsInFile(fset, filePath, file.Name.Name, file, symbol)...)
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
		return ToolOutput{Output: "No Go definitions found"}, nil
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
		return ToolOutput{}, fmt.Errorf("marshal go_definition: %w", err)
	}

	return ToolOutput{Output: string(encoded), Truncated: truncated}, nil
}

func goDefinitionRoot(searchPath string) (string, error) {
	info, err := os.Stat(searchPath)
	if err != nil {
		return "", fmt.Errorf("stat path %q: %w", searchPath, err)
	}
	if info.IsDir() {
		return searchPath, nil
	}
	return filepath.Dir(searchPath), nil
}

func findGoDefinitionsInFile(fset *token.FileSet, filePath, packageName string, file *ast.File, symbol string) []goDefinitionMatch {
	matches := make([]goDefinitionMatch, 0)
	for _, decl := range file.Decls {
		switch typed := decl.(type) {
		case *ast.FuncDecl:
			if typed.Name == nil || typed.Name.Name != symbol {
				continue
			}
			kind := "func"
			score := 100
			if typed.Recv != nil && len(typed.Recv.List) > 0 {
				kind = "method"
				score = 110
			}
			matches = append(matches, goDefinitionMatch{
				Path:      filePath,
				Line:      fset.Position(typed.Name.Pos()).Line,
				Column:    fset.Position(typed.Name.Pos()).Column,
				Package:   packageName,
				Kind:      kind,
				Signature: formatGoFuncDecl(fset, typed),
				Score:     score,
			})
		case *ast.GenDecl:
			for _, spec := range typed.Specs {
				specMatch, ok := goDefinitionSpecMatch(fset, filePath, packageName, typed, spec, symbol)
				if ok {
					matches = append(matches, specMatch)
				}
			}
		}
	}
	return matches
}

func goDefinitionSpecMatch(fset *token.FileSet, filePath, packageName string, decl *ast.GenDecl, spec ast.Spec, symbol string) (goDefinitionMatch, bool) {
	switch typed := spec.(type) {
	case *ast.TypeSpec:
		if typed.Name == nil || typed.Name.Name != symbol {
			return goDefinitionMatch{}, false
		}
		return goDefinitionMatch{
			Path:      filePath,
			Line:      fset.Position(typed.Name.Pos()).Line,
			Column:    fset.Position(typed.Name.Pos()).Column,
			Package:   packageName,
			Kind:      "type",
			Signature: formatGoTypeSpec(fset, typed),
			Score:     95,
		}, true
	case *ast.ValueSpec:
		for _, name := range typed.Names {
			if name == nil || name.Name != symbol {
				continue
			}
			kind := strings.ToLower(decl.Tok.String())
			return goDefinitionMatch{
				Path:      filePath,
				Line:      fset.Position(name.Pos()).Line,
				Column:    fset.Position(name.Pos()).Column,
				Package:   packageName,
				Kind:      kind,
				Signature: formatGoValueSpec(fset, decl, typed, name.Name),
				Score:     90,
			}, true
		}
	}
	return goDefinitionMatch{}, false
}

func formatGoFuncDecl(fset *token.FileSet, decl *ast.FuncDecl) string {
	var buf bytes.Buffer
	buf.WriteString("func ")
	if decl.Recv != nil && len(decl.Recv.List) > 0 {
		buf.WriteString("(")
		for index, field := range decl.Recv.List {
			if index > 0 {
				buf.WriteString(", ")
			}
			if len(field.Names) > 0 {
				buf.WriteString(field.Names[0].Name)
				buf.WriteString(" ")
			}
			buf.WriteString(formatGoNode(fset, field.Type))
		}
		buf.WriteString(") ")
	}
	buf.WriteString(decl.Name.Name)
	buf.WriteString(formatGoNode(fset, decl.Type))
	return strings.TrimSpace(buf.String())
}

func formatGoTypeSpec(fset *token.FileSet, spec *ast.TypeSpec) string {
	return strings.TrimSpace("type " + spec.Name.Name + " " + formatGoNode(fset, spec.Type))
}

func formatGoValueSpec(fset *token.FileSet, decl *ast.GenDecl, spec *ast.ValueSpec, symbol string) string {
	var buf bytes.Buffer
	buf.WriteString(strings.ToLower(decl.Tok.String()))
	buf.WriteString(" ")
	buf.WriteString(symbol)
	if spec.Type != nil {
		buf.WriteString(" ")
		buf.WriteString(formatGoNode(fset, spec.Type))
	}
	if len(spec.Values) > 0 {
		buf.WriteString(" = ")
		for index, value := range spec.Values {
			if index > 0 {
				buf.WriteString(", ")
			}
			buf.WriteString(formatGoNode(fset, value))
		}
	}
	return strings.TrimSpace(buf.String())
}

func formatGoNode(fset *token.FileSet, node any) string {
	if node == nil {
		return ""
	}
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, node); err != nil {
		return ""
	}
	return strings.TrimSpace(buf.String())
}
