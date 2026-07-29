package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

type client struct {
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	stdout       *bufio.Reader
	writeMu      sync.Mutex
	nextID       int64
	workspaceDir string
	server       serverConfig
	stderr       syncBuffer
}

// Run starts the language server for the request, performs the query, and shuts
// the server down again.
func Run(ctx context.Context, request Request) (Result, error) {
	if err := ValidateRequest(request); err != nil {
		return Result{}, err
	}
	client, err := newClient(ctx, request)
	if err != nil {
		return Result{}, err
	}
	defer client.Close()

	rows, err := client.run(ctx, request)
	if err != nil {
		return Result{}, err
	}
	return Result{Workspace: client.workspaceDir, Rows: rows}, nil
}

// ValidateRequest reports whether a request carries the fields its operation
// needs, without starting a server.
func ValidateRequest(request Request) error {
	switch request.Operation {
	case OperationDefinition, OperationReferences, OperationHover, OperationImplementation:
		if request.FilePath == "" {
			return fmt.Errorf("lsp %s requires filePath", request.Operation)
		}
		if request.Line < 1 || request.Column < 1 {
			return fmt.Errorf("lsp %s requires positive line and column", request.Operation)
		}
	case OperationDocumentSymbols:
		if request.FilePath == "" {
			return fmt.Errorf("lsp document_symbols requires filePath")
		}
	case OperationWorkspaceSymbols:
		if strings.TrimSpace(request.Query) == "" {
			return fmt.Errorf("lsp workspace_symbols requires query")
		}
	default:
		return fmt.Errorf("unsupported lsp operation %q", request.Operation)
	}
	return nil
}

// NormalizeOperation maps the camelCase and snake_case spellings callers use
// onto the canonical operation names.
func NormalizeOperation(value string) Operation {
	switch strings.TrimSpace(value) {
	case "go_to_definition", "goToDefinition":
		return OperationDefinition
	case "find_references", "findReferences":
		return OperationReferences
	case "hover":
		return OperationHover
	case "document_symbols", "documentSymbol":
		return OperationDocumentSymbols
	case "workspace_symbols", "workspaceSymbol":
		return OperationWorkspaceSymbols
	case "go_to_implementation", "goToImplementation":
		return OperationImplementation
	default:
		return Operation(strings.TrimSpace(value))
	}
}

func newClient(ctx context.Context, request Request) (*client, error) {
	server, workspaceDir, err := resolveServerConfig(request)
	if err != nil {
		return nil, err
	}
	if _, err := exec.LookPath(server.Command); err != nil {
		return nil, fmt.Errorf("lsp server %q is not installed or not on PATH", server.Command)
	}

	cmd := exec.CommandContext(ctx, server.Command, server.Args...)
	cmd.Dir = workspaceDir
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("open stdin for %s: %w", server.Name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("open stdout for %s: %w", server.Name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("open stderr for %s: %w", server.Name, err)
	}

	c := &client{
		cmd:          cmd,
		stdin:        stdin,
		stdout:       bufio.NewReader(stdout),
		workspaceDir: workspaceDir,
		server:       server,
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s: %w", server.Name, err)
	}
	go func() {
		_, _ = io.Copy(&c.stderr, stderr)
	}()
	if err := c.initialize(ctx); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

func (c *client) run(ctx context.Context, request Request) ([]ResultRow, error) {
	if request.FilePath != "" {
		if err := c.didOpen(request.FilePath); err != nil {
			return nil, err
		}
	}

	maxResults := request.MaxResults
	if maxResults <= 0 {
		maxResults = DefaultMaxResults
	}

	switch request.Operation {
	case OperationDefinition:
		return c.locationQuery(ctx, "textDocument/definition", "definition", request, maxResults)
	case OperationImplementation:
		return c.locationQuery(ctx, "textDocument/implementation", "implementation", request, maxResults)
	case OperationReferences:
		var locations []location
		params := c.positionParams(request)
		params["context"] = map[string]any{"includeDeclaration": request.IncludeDeclaration}
		if err := c.call(ctx, "textDocument/references", params, &locations); err != nil {
			return nil, err
		}
		return limitResults(rowsFromLocations(locations, "reference"), maxResults), nil
	case OperationHover:
		return c.hover(ctx, request)
	case OperationDocumentSymbols:
		var raw json.RawMessage
		if err := c.call(ctx, "textDocument/documentSymbol", map[string]any{
			"textDocument": map[string]any{"uri": pathToFileURI(request.FilePath)},
		}, &raw); err != nil {
			return nil, err
		}
		return limitResults(documentSymbolRows(raw, request.FilePath), maxResults), nil
	case OperationWorkspaceSymbols:
		var symbols []symbolInformation
		if err := c.call(ctx, "workspace/symbol", map[string]any{"query": request.Query}, &symbols); err != nil {
			return nil, err
		}
		return limitResults(rowsFromWorkspaceSymbols(symbols), maxResults), nil
	default:
		return nil, fmt.Errorf("unsupported lsp operation %q", request.Operation)
	}
}

func (c *client) locationQuery(ctx context.Context, method, kind string, request Request, maxResults int) ([]ResultRow, error) {
	var raw any
	if err := c.call(ctx, method, c.positionParams(request), &raw); err != nil {
		return nil, err
	}
	return limitResults(locationRowsFromAny(raw, kind), maxResults), nil
}

func (c *client) hover(ctx context.Context, request Request) ([]ResultRow, error) {
	var result hoverResult
	if err := c.call(ctx, "textDocument/hover", c.positionParams(request), &result); err != nil {
		return nil, err
	}
	contents := strings.TrimSpace(extractHoverContents(result.Contents))
	if contents == "" {
		return []ResultRow{}, nil
	}
	row := ResultRow{Kind: "hover", Contents: contents}
	if result.Range != nil {
		applyRange(&row, result.Range)
	}
	return []ResultRow{row}, nil
}

func (c *client) initialize(ctx context.Context) error {
	var result map[string]any
	params := map[string]any{
		"processId": os.Getpid(),
		"rootUri":   pathToFileURI(c.workspaceDir),
		"rootPath":  c.workspaceDir,
		"clientInfo": map[string]any{
			"name": "nami",
		},
		"workspaceFolders": []workspaceFolder{{
			URI:  pathToFileURI(c.workspaceDir),
			Name: filepath.Base(c.workspaceDir),
		}},
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"hover":          map[string]any{"contentFormat": []string{"markdown", "plaintext"}},
				"definition":     map[string]any{"linkSupport": true},
				"implementation": map[string]any{"linkSupport": true},
				"references":     map[string]any{},
				"documentSymbol": map[string]any{"hierarchicalDocumentSymbolSupport": true},
			},
			"workspace": map[string]any{
				"workspaceFolders": true,
			},
		},
	}
	if err := c.call(ctx, "initialize", params, &result); err != nil {
		return fmt.Errorf("initialize %s: %w", c.server.Name, err)
	}
	if err := c.notify("initialized", map[string]any{}); err != nil {
		return fmt.Errorf("notify initialized: %w", err)
	}
	return nil
}

func (c *client) didOpen(filePath string) error {
	content, err := os.ReadFile(filePath)
	if err != nil {
		return fmt.Errorf("read file for didOpen %q: %w", filePath, err)
	}
	params := map[string]any{
		"textDocument": map[string]any{
			"uri":        pathToFileURI(filePath),
			"languageId": languageIDForPath(filePath, c.server),
			"version":    1,
			"text":       string(content),
		},
	}
	if err := c.notify("textDocument/didOpen", params); err != nil {
		return fmt.Errorf("notify didOpen: %w", err)
	}
	return nil
}

// positionParams converts the request's 1-based position to the 0-based
// position the protocol uses.
func (c *client) positionParams(request Request) map[string]any {
	return map[string]any{
		"textDocument": map[string]any{"uri": pathToFileURI(request.FilePath)},
		"position": map[string]any{
			"line":      request.Line - 1,
			"character": request.Column - 1,
		},
	}
}
