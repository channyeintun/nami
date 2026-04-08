package skills

import "strings"

// ParseFrontmatter extracts YAML frontmatter key-value pairs from markdown.
// Returns (frontmatter map, body after frontmatter).
func ParseFrontmatter(content string) (map[string]string, string) {
	fm := make(map[string]string)

	if !strings.HasPrefix(content, "---") {
		return fm, content
	}

	// Find closing ---
	rest := content[3:]
	idx := strings.Index(rest, "\n---")
	if idx < 0 {
		return fm, content
	}

	fmBlock := rest[:idx]
	body := rest[idx+4:] // skip \n---
	if strings.HasPrefix(body, "\n") {
		body = body[1:]
	}

	// Parse simple key: value pairs
	for _, line := range strings.Split(fmBlock, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])
		fm[key] = val
	}

	return fm, body
}
