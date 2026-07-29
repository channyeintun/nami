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

func TestExtractHTMLForMarkdownHandlesNestedArticles(t *testing.T) {
	html := `<html><body><main><section><article><p>` +
		strings.Repeat("Deeply nested but genuine article prose. ", 20) +
		`</p></article></section></main></body></html>`

	if got := ExtractHTMLForMarkdown(html); !strings.Contains(got, "genuine article prose") {
		t.Fatalf("nested article lost: %q", got)
	}
}
