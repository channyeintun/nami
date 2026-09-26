package skills

import "strings"

// ParseFrontmatter extracts YAML frontmatter key-value pairs from markdown.
// Returns (frontmatter map, body after frontmatter).
func ParseFrontmatter(content string) (map[string]string, string) {
	fm := make(map[string]string)

	// Editors on Windows often start a file with a byte order mark, which
	// would hide the opening delimiter and with it the whole frontmatter.
	content = strings.TrimPrefix(content, "\ufeff")
	if !strings.HasPrefix(content, "---") {
		return fm, content
	}

	// Find closing ---
	rest := content[3:]
	before, after, ok := strings.Cut(rest, "\n---")
	if !ok {
		return fm, content
	}

	fmBlock := before
	// Skip the rest of the closing delimiter's line, CRLF endings included.
	body := strings.TrimPrefix(strings.TrimPrefix(after, "\r"), "\n")

	// Parse simple key: value pairs
	for line := range strings.SplitSeq(fmBlock, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := unquote(strings.TrimSpace(parts[1]))
		fm[key] = val
	}

	return fm, body
}

// unquote removes one pair of matching quotes around a YAML scalar, as in
// description: "Formats code: Go and Rust".
func unquote(value string) string {
	if len(value) >= 2 {
		first, last := value[0], value[len(value)-1]
		if first == last && (first == '"' || first == '\'') {
			return value[1 : len(value)-1]
		}
	}
	return value
}
