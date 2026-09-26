package tools

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/channyeintun/nami/internal/textutil"
)

const fileReadBinarySampleBytes = 8192
const fileReadDefaultLimitLines = 2000
const fileReadMaxLimitLines = 2000
const fileReadMaxOutputBytes = 50 * 1024
const fileReadMaxRenderedLineChars = 2000

// fileReadMaxLineBytes is how much of one line read_file keeps: enough for
// fileReadMaxRenderedLineChars characters of any width plus a CRLF, so lines
// are clipped exactly as before while the rest of a huge line (minified code,
// a JSON dump) is skipped instead of loaded into memory.
const fileReadMaxLineBytes = fileReadMaxRenderedLineChars*utf8.UTFMax + 2
const fileReadNotebookPreviewLines = 80

// notebookReadReserveBytes is kept free of cell content in a notebook read for
// the header and the continuation hint.
const notebookReadReserveBytes = 256

const notebookCellClippedNote = "\n[cell truncated to fit the read limit]"

// FileReadTool reads file contents from disk with bounded line-based pagination.
type FileReadTool struct{}

type fileReadRenderedLine struct {
	lineNo int
	text   string
}

// NewFileReadTool constructs the file read tool.
func NewFileReadTool() *FileReadTool {
	return &FileReadTool{}
}

func (t *FileReadTool) Name() string {
	return "read_file"
}

func (t *FileReadTool) Description() string {
	return "Read the contents of a file. Text files use 1-based line offset and limit. Jupyter notebooks are rendered as cell-aware content using 1-based cell offset and limit. For large files, use grep_search first to find anchors, then read a larger bounded window instead of many tiny slices. Reads are bounded by default; continue truncated reads with offset and limit, and avoid rereading the same unchanged slice."
}

func (t *FileReadTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"filePath": map[string]any{
				"type":        "string",
				"description": "The absolute path to the file to read.",
			},
			"file_path": map[string]any{
				"type":        "string",
				"description": "Compatibility alias for the absolute path to the file to read.",
			},
			"path": map[string]any{
				"type":        "string",
				"description": "Compatibility alias for the absolute path to the file to read.",
			},
			"offset": map[string]any{
				"type":        "integer",
				"description": "Optional 1-based starting line. Defaults to 1.",
				"minimum":     1,
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Optional number of lines to read. Defaults to 2000 and is capped at 2000.",
				"minimum":     1,
				"maximum":     fileReadMaxLimitLines,
			},
		},
		"anyOf": []map[string]any{
			{"required": []string{"filePath"}},
			{"required": []string{"file_path"}},
			{"required": []string{"path"}},
		},
	}
}

func (t *FileReadTool) Validate(input ToolInput) error {
	if err := validateFileReadParams(input.Params); err != nil {
		return err
	}
	filePath, ok := firstStringParam(input.Params, "filePath", "file_path", "path")
	if !ok || strings.TrimSpace(filePath) == "" {
		return fmt.Errorf("read_file requires filePath")
	}
	resolvedPath, err := resolveToolPath(filePath)
	if err != nil {
		return err
	}
	info, err := os.Stat(resolvedPath)
	if err != nil {
		return fmt.Errorf("stat file %q: %w", resolvedPath, err)
	}
	if info.IsDir() {
		return fmt.Errorf("%q is a directory", resolvedPath)
	}
	_, _, err = fileReadRange(input.Params)
	return err
}

func (t *FileReadTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *FileReadTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *FileReadTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	filePath, ok := firstStringParam(input.Params, "filePath", "file_path", "path")
	if !ok || strings.TrimSpace(filePath) == "" {
		return ToolOutput{}, fmt.Errorf("read_file requires filePath")
	}
	filePath, err := resolveToolPath(filePath)
	if err != nil {
		return ToolOutput{}, err
	}

	info, err := os.Stat(filePath)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("stat file %q: %w", filePath, err)
	}
	if info.IsDir() {
		return ToolOutput{}, fmt.Errorf("%q is a directory", filePath)
	}

	offset, limit, err := fileReadRange(input.Params)
	if err != nil {
		return ToolOutput{}, err
	}
	if readState := GetGlobalFileReadState(); readState != nil && readState.SeenUnchanged(filePath, offset, limit, info) {
		stub := fmt.Sprintf("[File unchanged since last read: %s (offset=%d limit=%d).]", filePath, offset, limit)
		recordFileReadMetric(FileReadMetric{RequestedOffset: offset, RequestedLimit: limit, BytesReturned: len(stub), UnchangedHit: true})
		return ToolOutput{Output: stub, FilePath: filePath, Preview: textutil.TruncateHead(stub, PreviewChars)}, nil
	}

	if isNotebookFile(filePath) {
		return executeNotebookRead(ctx, filePath, offset, limit)
	}

	file, err := os.Open(filePath)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("open file %q: %w", filePath, err)
	}
	defer file.Close()

	sample := make([]byte, fileReadBinarySampleBytes)
	readCount, readErr := file.Read(sample)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		return ToolOutput{}, fmt.Errorf("sample file %q: %w", filePath, readErr)
	}
	if _, err := file.Seek(0, 0); err != nil {
		return ToolOutput{}, fmt.Errorf("rewind file %q: %w", filePath, err)
	}
	if isLikelyBinaryFile(filePath, sample[:readCount]) {
		return ToolOutput{
			Output:   fmt.Sprintf("%s: binary or image-like file detected; read_file only supports text files and skipped this read for safety", filePath),
			IsError:  true,
			FilePath: filePath,
		}, nil
	}

	reader := bufio.NewReader(file)
	lineNo := 0
	partial := false
	lineClipped := false
	nextOffset := offset
	lines := make([]fileReadRenderedLine, 0, min(limit, 128))
	outputBytes := 0

	for {
		select {
		case <-ctx.Done():
			return ToolOutput{}, ctx.Err()
		default:
		}

		rawLine, readErr := readLineBounded(reader, fileReadMaxLineBytes)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return ToolOutput{}, fmt.Errorf("read file %q: %w", filePath, readErr)
		}
		if errors.Is(readErr, io.EOF) && rawLine == "" {
			break
		}

		lineNo++
		if lineNo < offset {
			if errors.Is(readErr, io.EOF) {
				break
			}
			continue
		}
		if len(lines) >= limit {
			partial = true
			nextOffset = lineNo
			break
		}

		trimmedLine := strings.TrimSuffix(rawLine, "\n")
		trimmedLine = strings.TrimSuffix(trimmedLine, "\r")
		renderedText, wasClipped := clipRenderedLine(trimmedLine)
		lineClipped = lineClipped || wasClipped
		renderedLine := fmt.Sprintf("%d\t%s", lineNo, renderedText)
		addedBytes := len(renderedLine)
		if outputBytes > 0 {
			addedBytes++
		}
		if outputBytes+addedBytes > fileReadMaxOutputBytes {
			partial = true
			nextOffset = lineNo
			break
		}

		lines = append(lines, fileReadRenderedLine{lineNo: lineNo, text: renderedLine})
		outputBytes += addedBytes
		nextOffset = lineNo + 1

		if errors.Is(readErr, io.EOF) {
			break
		}
	}

	if len(lines) == 0 {
		message := fmt.Sprintf("%s: no content in requested range", filePath)
		recordFileReadMetric(FileReadMetric{RequestedOffset: offset, RequestedLimit: limit, BytesReturned: len(message)})
		return ToolOutput{Output: message, FilePath: filePath, ReadOffset: offset, ReadLimit: limit}, nil
	}

	output := renderReadOutput(lines, partial, nextOffset, limit)
	recordFileReadMetric(FileReadMetric{RequestedOffset: offset, RequestedLimit: limit, LinesReturned: len(lines), BytesReturned: len(output), Truncated: partial || lineClipped})

	return ToolOutput{
		Output:     output,
		Truncated:  partial || lineClipped,
		FilePath:   filePath,
		ReadOffset: offset,
		ReadLimit:  limit,
		Preview:    textutil.TruncateHead(output, PreviewChars),
	}, nil
}

type notebookFile struct {
	Cells []notebookCell `json:"cells"`
}

type notebookCell struct {
	CellType       string `json:"cell_type"`
	Source         any    `json:"source"`
	ExecutionCount any    `json:"execution_count"`
	Metadata       struct {
		Language string `json:"language"`
	} `json:"metadata"`
}

func executeNotebookRead(ctx context.Context, filePath string, offset, limit int) (ToolOutput, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return ToolOutput{}, fmt.Errorf("read notebook %q: %w", filePath, err)
	}
	var notebook notebookFile
	if err := json.Unmarshal(data, &notebook); err != nil {
		return ToolOutput{}, fmt.Errorf("parse notebook %q: %w", filePath, err)
	}
	if len(notebook.Cells) == 0 {
		message := fmt.Sprintf("%s: notebook has no cells", filePath)
		return ToolOutput{Output: message, FilePath: filePath, ReadOffset: offset, ReadLimit: limit, Preview: message}, nil
	}

	startIndex := max(0, offset-1)
	if startIndex >= len(notebook.Cells) {
		message := fmt.Sprintf("%s: no notebook cells in requested range", filePath)
		return ToolOutput{Output: message, FilePath: filePath, ReadOffset: offset, ReadLimit: limit, Preview: message}, nil
	}
	endIndex := min(len(notebook.Cells), startIndex+limit)

	// Cells fill the byte budget, leaving room for the header and the
	// continuation hint.
	budget := fileReadMaxOutputBytes - notebookReadReserveBytes
	cells := make([]string, 0, endIndex-startIndex)
	used := 0
	clipped := false
	for index := startIndex; index < endIndex; index++ {
		select {
		case <-ctx.Done():
			return ToolOutput{}, ctx.Err()
		default:
		}
		section := renderNotebookCell(index+1, notebook.Cells[index])
		if used+2+len(section) > budget {
			// A read always returns at least one cell, clipped if it has to
			// be: returning none would send the model back to this same
			// offset forever.
			if len(cells) == 0 {
				cells = append(cells, textutil.TruncateHead(section, budget-len(notebookCellClippedNote))+notebookCellClippedNote)
				clipped = true
			}
			break
		}
		cells = append(cells, section)
		used += 2 + len(section)
	}

	lastShown := startIndex + len(cells)
	partial := lastShown < len(notebook.Cells)
	parts := append([]string{fmt.Sprintf("Notebook cells %d-%d of %d", startIndex+1, lastShown, len(notebook.Cells))}, cells...)
	if partial {
		parts = append(parts, fmt.Sprintf("[Partial notebook read. Continue with offset=%d limit=%d.]", lastShown+1, limit))
	}
	output := strings.Join(parts, "\n\n")
	return ToolOutput{
		Output:     output,
		Truncated:  partial || clipped,
		FilePath:   filePath,
		ReadOffset: offset,
		ReadLimit:  limit,
		Preview:    textutil.TruncateHead(output, PreviewChars),
	}, nil
}

func renderNotebookCell(cellNumber int, cell notebookCell) string {
	header := fmt.Sprintf("[Cell %d] %s", cellNumber, normalizeNotebookCellType(cell.CellType))
	if language := notebookCellLanguage(cell); language != "" {
		header += fmt.Sprintf(" language=%s", language)
	}
	if execution := notebookExecutionCount(cell.ExecutionCount); execution != "" {
		header += fmt.Sprintf(" execution=%s", execution)
	}
	source := renderNotebookCellSource(cell.Source)
	if source == "" {
		return header + "\n[empty]"
	}
	lines := strings.Split(source, "\n")
	clipped := false
	if len(lines) > fileReadNotebookPreviewLines {
		lines = lines[:fileReadNotebookPreviewLines]
		clipped = true
	}
	for index, line := range lines {
		lines[index], _ = clipRenderedLine(line)
	}
	body := strings.Join(lines, "\n")
	if clipped {
		body += "\n[cell output truncated]"
	}
	return header + "\n" + body
}

func normalizeNotebookCellType(cellType string) string {
	cellType = strings.TrimSpace(strings.ToLower(cellType))
	if cellType == "" {
		return "unknown"
	}
	return cellType
}

func notebookCellLanguage(cell notebookCell) string {
	if language := strings.TrimSpace(cell.Metadata.Language); language != "" {
		return language
	}
	if strings.EqualFold(strings.TrimSpace(cell.CellType), "markdown") {
		return "markdown"
	}
	return ""
}

func notebookExecutionCount(value any) string {
	switch v := value.(type) {
	case float64:
		return strconv.Itoa(int(v))
	case int:
		return strconv.Itoa(v)
	case int64:
		return strconv.FormatInt(v, 10)
	case string:
		return strings.TrimSpace(v)
	default:
		return ""
	}
}

func renderNotebookCellSource(source any) string {
	switch value := source.(type) {
	case string:
		return strings.TrimSpace(strings.ReplaceAll(value, "\r\n", "\n"))
	case []any:
		parts := make([]string, 0, len(value))
		for _, item := range value {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.TrimSpace(strings.ReplaceAll(strings.Join(parts, ""), "\r\n", "\n"))
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}

func validateFileReadParams(params map[string]any) error {
	allowed := map[string]struct{}{
		"filePath":  {},
		"file_path": {},
		"path":      {},
		"offset":    {},
		"limit":     {},
	}
	for key := range params {
		if _, ok := allowed[key]; ok {
			continue
		}
		return fmt.Errorf("read_file does not accept %q; use filePath with optional offset and limit", key)
	}
	return nil
}

func isLikelyBinaryFile(filePath string, sample []byte) bool {
	if len(sample) == 0 {
		return false
	}
	if isNotebookFile(filePath) {
		return false
	}
	switch strings.ToLower(filepath.Ext(filePath)) {
	case ".png", ".jpg", ".jpeg", ".gif", ".webp", ".bmp", ".ico", ".tif", ".tiff", ".pdf", ".zip", ".gz", ".tar", ".jar", ".exe", ".dll", ".so", ".dylib", ".woff", ".woff2", ".ttf", ".otf":
		return true
	}
	if bytes.IndexByte(sample, 0) >= 0 {
		return true
	}
	if !utf8.Valid(trimIncompleteRune(sample)) {
		return true
	}
	controlBytes := 0
	for _, b := range sample {
		if b < 0x20 && b != '\n' && b != '\r' && b != '\t' && b != '\f' {
			controlBytes++
		}
	}
	return controlBytes > len(sample)/10
}

// trimIncompleteRune drops a multi-byte character cut off at the end of sample.
// Callers sniff a fixed-size prefix of the file, and that boundary can land
// inside a character of perfectly valid UTF-8 text.
func trimIncompleteRune(sample []byte) []byte {
	for back := 1; back < utf8.UTFMax && back <= len(sample); back++ {
		start := len(sample) - back
		if !utf8.RuneStart(sample[start]) {
			continue
		}
		if utf8.FullRune(sample[start:]) {
			return sample
		}
		return sample[:start]
	}
	return sample
}

func isNotebookFile(filePath string) bool {
	return strings.EqualFold(filepath.Ext(strings.TrimSpace(filePath)), ".ipynb")
}

func fileReadRange(params map[string]any) (int, int, error) {
	offset := 1
	if value, ok := intParam(params, "offset"); ok {
		if value < 1 {
			return 0, 0, fmt.Errorf("offset must be >= 1")
		}
		offset = value
	}

	limit := fileReadDefaultLimitLines
	if value, ok := intParam(params, "limit"); ok {
		if value < 1 {
			return 0, 0, fmt.Errorf("limit must be >= 1")
		}
		limit = min(value, fileReadMaxLimitLines)
	}
	return offset, limit, nil
}

// readLineBounded reads the next line including its newline, keeping at most
// limit bytes of it and discarding the rest. Like ReadString, it returns the
// final line with io.EOF when the file does not end in a newline.
func readLineBounded(reader *bufio.Reader, limit int) (string, error) {
	var line []byte
	for {
		chunk, err := reader.ReadSlice('\n')
		if room := limit - len(line); room > 0 {
			line = append(line, chunk[:min(len(chunk), room)]...)
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return string(line), err
		}
	}
}

func clipRenderedLine(line string) (string, bool) {
	if utf8.RuneCountInString(line) <= fileReadMaxRenderedLineChars {
		return line, false
	}
	trimTo := max(1, fileReadMaxRenderedLineChars-3)
	var builder strings.Builder
	runeCount := 0
	for _, r := range line {
		if runeCount >= trimTo {
			break
		}
		builder.WriteRune(r)
		runeCount++
	}
	builder.WriteString("...")
	return builder.String(), true
}

func renderReadOutput(lines []fileReadRenderedLine, partial bool, nextOffset, limit int) string {
	if !partial {
		return joinRenderedReadLines(lines)
	}
	for {
		hint := fmt.Sprintf("[Partial read. Continue with offset=%d limit=%d.]", nextOffset, limit)
		body := joinRenderedReadLines(lines)
		candidate := hint
		if body != "" {
			candidate = body + "\n\n" + hint
		}
		if len(candidate) <= fileReadMaxOutputBytes || len(lines) == 0 {
			return candidate
		}
		nextOffset = lines[len(lines)-1].lineNo
		lines = lines[:len(lines)-1]
	}
}

func joinRenderedReadLines(lines []fileReadRenderedLine) string {
	if len(lines) == 0 {
		return ""
	}
	parts := make([]string, 0, len(lines))
	for _, line := range lines {
		parts = append(parts, line.text)
	}
	return strings.Join(parts, "\n")
}

func intParam(params map[string]any, key string) (int, bool) {
	value, ok := params[key]
	if !ok {
		return 0, false
	}
	switch v := value.(type) {
	case int:
		return v, true
	case int64:
		return int(v), true
	case float64:
		return int(v), true
	case string:
		parsed, err := strconv.Atoi(v)
		if err == nil {
			return parsed, true
		}
	}
	return 0, false
}
