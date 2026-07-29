// Package lsp speaks the Language Server Protocol over stdio to locally
// installed servers. It owns process startup, framing, and result shaping so
// callers only deal with a Request and plain result rows.
package lsp

import "encoding/json"

// Operation is a supported semantic query.
type Operation string

const (
	OperationDefinition       Operation = "go_to_definition"
	OperationReferences       Operation = "find_references"
	OperationHover            Operation = "hover"
	OperationDocumentSymbols  Operation = "document_symbols"
	OperationWorkspaceSymbols Operation = "workspace_symbols"
	OperationImplementation   Operation = "go_to_implementation"
)

// DefaultMaxResults caps result rows when a caller does not specify a limit.
const DefaultMaxResults = 100

// Request describes one semantic query. FilePath and SearchPath must already be
// absolute; this package does not resolve workspace-relative paths.
type Request struct {
	Operation          Operation
	FilePath           string
	Line               int
	Column             int
	Query              string
	IncludeDeclaration bool
	MaxResults         int
	SearchPath         string
}

// ResultRow is one normalized result. Line and Column are 1-based.
type ResultRow struct {
	Kind          string `json:"kind"`
	Name          string `json:"name,omitempty"`
	Detail        string `json:"detail,omitempty"`
	SymbolKind    string `json:"symbolKind,omitempty"`
	FilePath      string `json:"filePath,omitempty"`
	Line          int    `json:"line,omitempty"`
	Column        int    `json:"column,omitempty"`
	EndLine       int    `json:"endLine,omitempty"`
	EndColumn     int    `json:"endColumn,omitempty"`
	ContainerName string `json:"containerName,omitempty"`
	Contents      string `json:"contents,omitempty"`
	Signature     string `json:"signature,omitempty"`
}

// Result is the outcome of a request, including the workspace the server ran in.
type Result struct {
	Workspace string
	Rows      []ResultRow
}

type responseEnvelope struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type position struct {
	Line      int `json:"line"`
	Character int `json:"character"`
}

type textRange struct {
	Start position `json:"start"`
	End   position `json:"end"`
}

type location struct {
	URI   string    `json:"uri"`
	Range textRange `json:"range"`
}

type locationLink struct {
	TargetURI            string    `json:"targetUri"`
	TargetRange          textRange `json:"targetRange"`
	TargetSelectionRange textRange `json:"targetSelectionRange"`
}

type markupContent struct {
	Kind  string `json:"kind"`
	Value string `json:"value"`
}

type markedString struct {
	Language string `json:"language"`
	Value    string `json:"value"`
}

type hoverResult struct {
	Contents any        `json:"contents"`
	Range    *textRange `json:"range,omitempty"`
}

type documentSymbol struct {
	Name           string           `json:"name"`
	Detail         string           `json:"detail"`
	Kind           int              `json:"kind"`
	Range          textRange        `json:"range"`
	SelectionRange textRange        `json:"selectionRange"`
	Children       []documentSymbol `json:"children,omitempty"`
}

type symbolInformation struct {
	Name          string   `json:"name"`
	Kind          int      `json:"kind"`
	Location      location `json:"location"`
	ContainerName string   `json:"containerName,omitempty"`
}

type workspaceFolder struct {
	URI  string `json:"uri"`
	Name string `json:"name"`
}
