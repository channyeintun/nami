package lsp

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

func limitResults(rows []ResultRow, maxResults int) []ResultRow {
	if maxResults <= 0 || len(rows) <= maxResults {
		return rows
	}
	return rows[:maxResults]
}

// locationRowsFromAny normalizes the several shapes a definition or
// implementation response may take: a list of links, a list of locations, or a
// single one of either.
func locationRowsFromAny(raw any, kind string) []ResultRow {
	data, err := json.Marshal(raw)
	if err != nil {
		return nil
	}
	var links []locationLink
	if err := json.Unmarshal(data, &links); err == nil && len(links) > 0 {
		rows := make([]ResultRow, 0, len(links))
		for _, link := range links {
			rows = append(rows, rowFromLocationLink(link, kind))
		}
		return sortRows(rows)
	}
	var locations []location
	if err := json.Unmarshal(data, &locations); err == nil && len(locations) > 0 {
		return rowsFromLocations(locations, kind)
	}
	var single location
	if err := json.Unmarshal(data, &single); err == nil && single.URI != "" {
		return rowsFromLocations([]location{single}, kind)
	}
	var link locationLink
	if err := json.Unmarshal(data, &link); err == nil && link.TargetURI != "" {
		return []ResultRow{rowFromLocationLink(link, kind)}
	}
	return nil
}

func rowFromLocationLink(link locationLink, kind string) ResultRow {
	row := ResultRow{Kind: kind, FilePath: fileURIToPath(link.TargetURI)}
	applyRange(&row, &link.TargetSelectionRange)
	if row.Line == 0 {
		applyRange(&row, &link.TargetRange)
	}
	return row
}

func rowsFromLocations(locations []location, kind string) []ResultRow {
	rows := make([]ResultRow, 0, len(locations))
	for _, item := range locations {
		row := ResultRow{Kind: kind, FilePath: fileURIToPath(item.URI)}
		applyRange(&row, &item.Range)
		rows = append(rows, row)
	}
	return sortRows(rows)
}

func rowsFromWorkspaceSymbols(symbols []symbolInformation) []ResultRow {
	rows := make([]ResultRow, 0, len(symbols))
	for _, symbol := range symbols {
		row := ResultRow{
			Kind:          "symbol",
			Name:          symbol.Name,
			SymbolKind:    symbolKindName(symbol.Kind),
			FilePath:      fileURIToPath(symbol.Location.URI),
			ContainerName: symbol.ContainerName,
		}
		applyRange(&row, &symbol.Location.Range)
		rows = append(rows, row)
	}
	return sortRows(rows)
}

func documentSymbolRows(raw json.RawMessage, filePath string) []ResultRow {
	var symbols []documentSymbol
	if err := json.Unmarshal(raw, &symbols); err == nil && len(symbols) > 0 {
		rows := make([]ResultRow, 0, len(symbols))
		flattenDocumentSymbols(&rows, symbols, filePath, "")
		return sortRows(rows)
	}
	var symbolInfos []symbolInformation
	if err := json.Unmarshal(raw, &symbolInfos); err == nil && len(symbolInfos) > 0 {
		return rowsFromWorkspaceSymbols(symbolInfos)
	}
	return nil
}

func flattenDocumentSymbols(rows *[]ResultRow, symbols []documentSymbol, filePath, container string) {
	for _, symbol := range symbols {
		row := ResultRow{
			Kind:          "symbol",
			Name:          symbol.Name,
			Detail:        symbol.Detail,
			SymbolKind:    symbolKindName(symbol.Kind),
			FilePath:      filePath,
			ContainerName: container,
		}
		applyRange(&row, &symbol.SelectionRange)
		if row.Line == 0 {
			applyRange(&row, &symbol.Range)
		}
		*rows = append(*rows, row)
		nextContainer := symbol.Name
		if container != "" {
			nextContainer = container + "." + symbol.Name
		}
		flattenDocumentSymbols(rows, symbol.Children, filePath, nextContainer)
	}
}

func sortRows(rows []ResultRow) []ResultRow {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].FilePath != rows[j].FilePath {
			return rows[i].FilePath < rows[j].FilePath
		}
		if rows[i].Line != rows[j].Line {
			return rows[i].Line < rows[j].Line
		}
		if rows[i].Column != rows[j].Column {
			return rows[i].Column < rows[j].Column
		}
		return rows[i].Name < rows[j].Name
	})
	return rows
}

// applyRange converts a 0-based protocol range onto a 1-based result row.
func applyRange(row *ResultRow, source *textRange) {
	if row == nil || source == nil {
		return
	}
	row.Line = source.Start.Line + 1
	row.Column = source.Start.Character + 1
	row.EndLine = source.End.Line + 1
	row.EndColumn = source.End.Character + 1
}

// extractHoverContents flattens the MarkupContent, MarkedString, and array
// shapes hover responses are allowed to use into plain text.
func extractHoverContents(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case map[string]any:
		return hoverContentsFromMap(typed)
	case []any:
		parts := make([]string, 0, len(typed))
		for _, part := range typed {
			if text := strings.TrimSpace(extractHoverContents(part)); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n\n")
	}

	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	var markup markupContent
	if err := json.Unmarshal(data, &markup); err == nil && strings.TrimSpace(markup.Value) != "" {
		return markup.Value
	}
	var marked markedString
	if err := json.Unmarshal(data, &marked); err == nil && strings.TrimSpace(marked.Value) != "" {
		return joinMarkedString(marked.Language, marked.Value)
	}
	return string(data)
}

func hoverContentsFromMap(value map[string]any) string {
	text, ok := value["value"].(string)
	if !ok {
		return ""
	}
	language, _ := value["language"].(string)
	return joinMarkedString(language, text)
}

func joinMarkedString(language, value string) string {
	if strings.TrimSpace(language) == "" || strings.TrimSpace(value) == "" {
		return value
	}
	return language + "\n" + value
}

func pathToFileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: filepath.ToSlash(filepath.Clean(path))}).String()
}

func fileURIToPath(value string) string {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Scheme != "file" {
		return value
	}
	path := filepath.FromSlash(parsed.Path)
	if runtime.GOOS == "windows" && len(path) >= 3 && path[0] == '\\' && path[2] == ':' {
		path = path[1:]
	}
	return filepath.Clean(path)
}

func symbolKindName(kind int) string {
	names := []string{
		"file", "module", "namespace", "package", "class", "method", "property",
		"field", "constructor", "enum", "interface", "function", "variable",
		"constant", "string", "number", "boolean", "array", "object", "key",
		"null", "enum_member", "struct", "event", "operator", "type_parameter",
	}
	if kind < 1 || kind > len(names) {
		return fmt.Sprintf("kind_%d", kind)
	}
	return names[kind-1]
}
