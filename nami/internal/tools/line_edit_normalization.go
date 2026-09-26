package tools

import "strings"

// lineEndingText is file content prepared for line-based edits. Edits are
// located in normalized, where every CRLF is folded to LF — that is how
// read_file shows a file and how the model writes its snippets — and are then
// spliced into the original, so everything outside an edit keeps its exact
// bytes: CRLF, mixed line endings and stray carriage returns included.
type lineEndingText struct {
	original   string
	normalized string
	// origin[i] is the offset in original that normalized[i] came from, and
	// origin[len(normalized)] is len(original).
	origin []int
	// lineEnding is the file's predominant line ending, used for the line
	// breaks that edits insert.
	lineEnding string
}

// textEdit replaces normalized[start:end] with text, which uses "\n" line
// breaks.
type textEdit struct {
	start int
	end   int
	text  string
}

func newLineEndingText(original string) lineEndingText {
	var normalized strings.Builder
	normalized.Grow(len(original))
	origin := make([]int, 0, len(original)+1)
	for i := 0; i < len(original); i++ {
		start := i
		if original[i] == '\r' && i+1 < len(original) && original[i+1] == '\n' {
			i++
		}
		normalized.WriteByte(original[i])
		origin = append(origin, start)
	}
	origin = append(origin, len(original))

	lineEnding := "\n"
	crlf := strings.Count(original, "\r\n")
	if crlf > strings.Count(original, "\n")-crlf {
		lineEnding = "\r\n"
	}

	return lineEndingText{
		original:   original,
		normalized: normalized.String(),
		origin:     origin,
		lineEnding: lineEnding,
	}
}

// apply splices edits, sorted by start and not overlapping, into the original
// content and returns the result. A file that ended with a line break still
// does, unless the edits emptied it.
func (t lineEndingText) apply(edits []textEdit) string {
	var result strings.Builder
	next := 0
	for _, edit := range edits {
		result.WriteString(t.original[next:t.origin[edit.start]])
		result.WriteString(strings.ReplaceAll(edit.text, "\n", t.lineEnding))
		next = t.origin[edit.end]
	}
	result.WriteString(t.original[next:])

	updated := result.String()
	if updated != "" && endsWithLineBreak(t.original) && !endsWithLineBreak(updated) {
		updated += t.lineEnding
	}
	return updated
}

func endsWithLineBreak(content string) bool {
	return strings.HasSuffix(content, "\n") || strings.HasSuffix(content, "\r")
}

// literalEdits replaces the first limit non-overlapping occurrences of old in
// content, scanning left to right as strings.Replace does. old must not be
// empty.
func literalEdits(content, old, new string, limit int) []textEdit {
	edits := make([]textEdit, 0, limit)
	offset := 0
	for len(edits) < limit {
		index := strings.Index(content[offset:], old)
		if index < 0 {
			break
		}
		start := offset + index
		edits = append(edits, textEdit{start: start, end: start + len(old), text: new})
		offset = start + len(old)
	}
	return edits
}
