package webfetch

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/channyeintun/nami/internal/textutil"
)

const (
	maxMarkdownChars = 100_000
	maxPassages      = 3
	minKeywordLength = 3
)

// Mode selects how a fetch result is rendered.
type Mode string

const (
	// ModeReport returns a header plus the passages most relevant to a prompt.
	ModeReport Mode = "report"
	// ModeMarkdown returns the converted page markdown as-is.
	ModeMarkdown Mode = "markdown"
)

// ParseMode maps a caller-supplied value onto a Mode, defaulting to a report.
func ParseMode(value string) (Mode, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "", string(ModeReport):
		return ModeReport, nil
	case string(ModeMarkdown):
		return ModeMarkdown, nil
	default:
		return "", fmt.Errorf("web_fetch respond_with must be report or markdown")
	}
}

// Render turns fetched content into the text the model sees.
func Render(rawURL, prompt string, mode Mode, content Content) string {
	markdown := TruncateMarkdown(content.Markdown)
	if mode == ModeMarkdown {
		if markdown == "" {
			return "No readable content returned."
		}
		return markdown
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "Fetched: %s\n", rawURL)
	fmt.Fprintf(&builder, "Status: %s\n", content.StatusText)
	if content.ContentType != "" {
		fmt.Fprintf(&builder, "Content-Type: %s\n", content.ContentType)
	}
	fmt.Fprintf(&builder, "Bytes: %d\n", content.Bytes)
	fmt.Fprintf(&builder, "Prompt: %s\n", strings.TrimSpace(prompt))

	switch passages := RelevantPassages(markdown, prompt); {
	case len(passages) > 0:
		builder.WriteString("\nRelevant excerpts:\n")
		for index, passage := range passages {
			fmt.Fprintf(&builder, "\n%d. %s\n", index+1, passage)
		}
	case markdown != "":
		builder.WriteString("\nContent:\n\n")
		builder.WriteString(markdown)
	default:
		builder.WriteString("\nNo readable content returned.\n")
	}

	return strings.TrimSpace(builder.String())
}

// TruncateMarkdown caps converted page text, cutting on a character boundary so
// the tail of the excerpt is never a broken rune.
func TruncateMarkdown(markdown string) string {
	markdown = strings.TrimSpace(markdown)
	if len(markdown) <= maxMarkdownChars {
		return markdown
	}
	return textutil.TruncateHead(markdown, maxMarkdownChars) + "\n\n[Content truncated due to length...]"
}

// RelevantPassages ranks the lines of a page by how many prompt keywords they
// mention and returns the best few. Pages with no keyword overlap fall back to
// their opening lines.
func RelevantPassages(markdown, prompt string) []string {
	sections := splitSections(markdown)
	if len(sections) == 0 {
		return nil
	}

	keywords := keywordSet(prompt)
	if len(keywords) == 0 {
		return firstSections(sections, maxPassages)
	}

	type scoredSection struct {
		text  string
		score int
		order int
	}
	scored := make([]scoredSection, 0, len(sections))
	for index, section := range sections {
		score := 0
		sectionKeywords := keywordSet(section)
		for keyword := range keywords {
			if _, ok := sectionKeywords[keyword]; ok {
				score++
			}
		}
		if score > 0 {
			scored = append(scored, scoredSection{text: section, score: score, order: index})
		}
	}
	if len(scored) == 0 {
		return firstSections(sections, maxPassages)
	}

	sort.Slice(scored, func(i, j int) bool {
		if scored[i].score != scored[j].score {
			return scored[i].score > scored[j].score
		}
		return scored[i].order < scored[j].order
	})

	result := make([]string, 0, min(maxPassages, len(scored)))
	for _, item := range scored[:min(maxPassages, len(scored))] {
		result = append(result, item.text)
	}
	return result
}

func splitSections(markdown string) []string {
	lines := strings.FieldsFunc(markdown, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	sections := make([]string, 0, len(lines))
	for _, line := range lines {
		if collapsed := strings.Join(strings.Fields(line), " "); collapsed != "" {
			sections = append(sections, collapsed)
		}
	}
	return sections
}

func firstSections(sections []string, limit int) []string {
	result := make([]string, 0, min(limit, len(sections)))
	for _, section := range sections {
		if strings.TrimSpace(section) == "" {
			continue
		}
		result = append(result, section)
		if len(result) == limit {
			break
		}
	}
	return result
}

func keywordSet(text string) map[string]struct{} {
	parts := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	keywords := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if len(part) < minKeywordLength {
			continue
		}
		keywords[part] = struct{}{}
	}
	return keywords
}
