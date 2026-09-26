package readability

import (
	"strings"
	"testing"
)

func TestExtractHTMLForMarkdownKeepsArticleAndDropsChrome(t *testing.T) {
	html := `<html><body>
		<nav id="nav"><a href="/a">Home</a><a href="/b">About</a></nav>
		<div class="sidebar"><a href="/ad">Sponsored</a></div>
		<article id="content">
			<p>` + strings.Repeat("This is the real article body with enough prose to score well. ", 12) + `</p>
			<p>` + strings.Repeat("A second substantial paragraph of article text. ", 12) + `</p>
		</article>
		<footer>Copyright notice</footer>
	</body></html>`

	got := ExtractHTMLForMarkdown(html)

	if !strings.Contains(got, "the real article body") {
		t.Fatalf("article text was dropped: %q", got)
	}
	for _, boilerplate := range []string{"Sponsored", "Copyright notice"} {
		if strings.Contains(got, boilerplate) {
			t.Errorf("boilerplate %q survived extraction", boilerplate)
		}
	}
}

func TestExtractHTMLForMarkdownReturnsInputWhenUnparseable(t *testing.T) {
	// The extractor is a best-effort improvement; it must never lose content it
	// cannot make sense of.
	for _, input := range []string{"", "not html at all", "<p>short</p>"} {
		if got := ExtractHTMLForMarkdown(input); got == "" && input != "" {
			t.Errorf("ExtractHTMLForMarkdown(%q) returned empty", input)
		}
	}
}

func TestExtractHTMLForMarkdownPrefersDenserCandidate(t *testing.T) {
	// A link-heavy block should lose to a prose block of similar size.
	html := `<html><body>
		<div id="links">` + strings.Repeat(`<a href="/x">link</a> `, 60) + `</div>
		<div id="prose"><p>` + strings.Repeat("Real sentences of article prose here. ", 30) + `</p></div>
	</body></html>`

	got := ExtractHTMLForMarkdown(html)
	if !strings.Contains(got, "Real sentences of article prose") {
		t.Fatalf("prose block not selected: %q", got)
	}
}

func TestExtractHTMLForMarkdownIsDeterministic(t *testing.T) {
	html := `<html><body><article><p>` +
		strings.Repeat("Stable article content for repeated extraction. ", 20) +
		`</p></article></body></html>`

	first := ExtractHTMLForMarkdown(html)
	for range 20 {
		if got := ExtractHTMLForMarkdown(html); got != first {
			t.Fatal("ExtractHTMLForMarkdown returned different output across runs")
		}
	}
}

// Single-cell layout tables are collapsed into a paragraph or div. Moving the
// cell's children used to panic in the HTML package, which took the whole
// engine down on any page that still uses a table for layout.
func TestExtractHTMLForMarkdownCollapsesSingleCellTables(t *testing.T) {
	prose := strings.Repeat("Article prose that carries the page content. ", 8)
	cases := []struct {
		name string
		html string
		want string
	}{
		{
			name: "layout table inside the article",
			html: `<html><body><div><p>` + prose + `</p><table><tr><td>Cell note about the release.</td></tr></table><p>` + prose + `</p></div></body></html>`,
			want: "Cell note about the release.",
		},
		{
			name: "whole page in one cell",
			html: `<html><body><table><tr><td><p>` + prose + `</p><p>` + prose + `</p></td></tr></table></body></html>`,
			want: "Article prose that carries the page content.",
		},
		{
			name: "short page in one cell",
			html: `<html><body><table><tbody><tr><td>Short note about the release.</td></tr></tbody></table></body></html>`,
			want: "Short note about the release.",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ExtractHTMLForMarkdown(tc.html)
			if !strings.Contains(got, tc.want) {
				t.Fatalf("cell content %q was lost: %q", tc.want, got)
			}
		})
	}
}

func TestExtractHTMLForMarkdownHandlesNestedArticles(t *testing.T) {
	html := `<html><body><main><section><article><p>` +
		strings.Repeat("Deeply nested but genuine article prose. ", 20) +
		`</p></article></section></main></body></html>`

	if got := ExtractHTMLForMarkdown(html); !strings.Contains(got, "genuine article prose") {
		t.Fatalf("nested article lost: %q", got)
	}
}
