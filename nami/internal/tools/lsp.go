package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/channyeintun/nami/internal/lsp"
)

// LSPTool exposes the internal/lsp client as a tool. It owns parameter parsing
// and workspace path resolution; the protocol work lives in internal/lsp.
type LSPTool struct{}

type lspOutput struct {
	Operation   string          `json:"operation"`
	FilePath    string          `json:"filePath,omitempty"`
	Workspace   string          `json:"workspace,omitempty"`
	ResultCount int             `json:"resultCount"`
	Results     []lsp.ResultRow `json:"results"`
}

func NewLSPTool() *LSPTool {
	return &LSPTool{}
}

func (t *LSPTool) Name() string {
	return "lsp"
}

func (t *LSPTool) Description() string {
	return "Use a local Language Server Protocol server for semantic code intelligence including definitions, references, hover, document symbols, workspace symbols, and implementations."
}

func (t *LSPTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"operation": map[string]any{
				"type": "string",
				"enum": []string{
					"go_to_definition",
					"find_references",
					"hover",
					"document_symbols",
					"workspace_symbols",
					"go_to_implementation",
				},
				"description": "The LSP operation to perform.",
			},
			"filePath": map[string]any{
				"type":        "string",
				"description": "Absolute or workspace-relative path for file-based operations.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Optional workspace path hint for workspace_symbols.",
			},
			"line": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "1-based line number for position-based operations.",
			},
			"column": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "1-based column number for position-based operations.",
			},
			"character": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Compatibility alias for column.",
			},
			"query": map[string]any{
				"type":        "string",
				"description": "Workspace symbol query for workspace_symbols.",
			},
			"includeDeclaration": map[string]any{
				"type":        "boolean",
				"description": "Whether declaration sites are included in find_references.",
			},
			"include_declaration": map[string]any{
				"type":        "boolean",
				"description": "Snake_case alias for includeDeclaration.",
			},
			"maxResults": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Optional maximum number of results to return.",
			},
			"max_results": map[string]any{
				"type":        "integer",
				"minimum":     1,
				"description": "Snake_case alias for maxResults.",
			},
		},
		"required": []string{"operation"},
	}
}

func (t *LSPTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *LSPTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *LSPTool) Validate(input ToolInput) error {
	_, err := parseLSPRequest(input.Params)
	return err
}

func (t *LSPTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	request, err := parseLSPRequest(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}
	result, err := lsp.Run(ctx, request)
	if err != nil {
		return ToolOutput{}, err
	}

	encoded, err := json.MarshalIndent(lspOutput{
		Operation:   string(request.Operation),
		FilePath:    request.FilePath,
		Workspace:   result.Workspace,
		ResultCount: len(result.Rows),
		Results:     result.Rows,
	}, "", "  ")
	if err != nil {
		return ToolOutput{}, fmt.Errorf("marshal lsp output: %w", err)
	}
	return ToolOutput{Output: string(encoded)}, nil
}

func parseLSPRequest(params map[string]any) (lsp.Request, error) {
	operation, ok := firstStringParam(params, "operation")
	if !ok || strings.TrimSpace(operation) == "" {
		return lsp.Request{}, fmt.Errorf("lsp requires operation")
	}
	request := lsp.Request{
		Operation:          lsp.NormalizeOperation(operation),
		MaxResults:         firstPositiveIntOrDefault(params, lsp.DefaultMaxResults, "maxResults", "max_results"),
		IncludeDeclaration: firstBoolParam(params, "includeDeclaration", "include_declaration"),
	}
	if filePath, ok := firstStringParam(params, "filePath"); ok {
		request.FilePath = strings.TrimSpace(filePath)
	}
	if pathHint, ok := firstStringParam(params, "path"); ok {
		trimmedPath := strings.TrimSpace(pathHint)
		if request.Operation == lsp.OperationWorkspaceSymbols {
			request.SearchPath = trimmedPath
		} else if request.FilePath == "" {
			request.FilePath = trimmedPath
		}
	}
	if query, ok := stringParam(params, "query"); ok {
		request.Query = strings.TrimSpace(query)
	}
	if line, ok := firstIntParam(params, "line"); ok {
		request.Line = line
	}
	if column, ok := firstIntParam(params, "column", "character"); ok {
		request.Column = column
	}

	if err := lsp.ValidateRequest(request); err != nil {
		return lsp.Request{}, err
	}
	return resolveLSPRequestPaths(request)
}

// resolveLSPRequestPaths turns workspace-relative paths into absolute ones and
// verifies the target file exists before a server is launched.
func resolveLSPRequestPaths(request lsp.Request) (lsp.Request, error) {
	if request.FilePath != "" {
		resolvedPath, err := resolveToolPath(request.FilePath)
		if err != nil {
			return lsp.Request{}, err
		}
		info, err := os.Stat(resolvedPath)
		if err != nil {
			return lsp.Request{}, fmt.Errorf("stat file %q: %w", resolvedPath, err)
		}
		if info.IsDir() {
			return lsp.Request{}, fmt.Errorf("%q is a directory", resolvedPath)
		}
		request.FilePath = resolvedPath
	}
	if request.SearchPath != "" {
		resolvedSearchPath, err := resolveToolPath(request.SearchPath)
		if err != nil {
			return lsp.Request{}, err
		}
		request.SearchPath = resolvedSearchPath
	}
	return request, nil
}

func firstPositiveIntOrDefault(params map[string]any, fallback int, keys ...string) int {
	for _, key := range keys {
		if value, ok := intParam(params, key); ok && value > 0 {
			return value
		}
	}
	return fallback
}
