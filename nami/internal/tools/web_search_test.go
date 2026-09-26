package tools

import (
	"net/url"
	"testing"
)

// DuckDuckGo wraps each result in a redirect whose uddg parameter holds the
// target URL, query-escaped once. Decoding it a second time corrupted targets
// that contain escapes of their own: an encoded "+" became a space and a
// literal "%" made the URL unparseable, which silently dropped the result.
func TestResolveSearchResultURLDecodesTheRedirectOnce(t *testing.T) {
	redirect := func(target string) string {
		return "//duckduckgo.com/l/?uddg=" + url.QueryEscape(target) + "&amp;rut=abc123"
	}
	cases := []struct {
		name string
		href string
		want string
	}{
		{name: "plain target", href: redirect("https://www.python.org/"), want: "https://www.python.org/"},
		{name: "encoded plus", href: redirect("https://example.com/search?q=c%2B%2B&lang=en"), want: "https://example.com/search?q=c%2B%2B&lang=en"},
		{name: "encoded ampersand", href: redirect("https://example.com/?q=a%26b"), want: "https://example.com/?q=a%26b"},
		{name: "literal percent", href: redirect("https://en.wikipedia.org/wiki/100%25_Pure"), want: "https://en.wikipedia.org/wiki/100%25_Pure"},
		{name: "encoded slash", href: redirect("https://example.com/files/a%2Fb"), want: "https://example.com/files/a%2Fb"},
		{name: "direct link", href: "https://go.dev/doc/", want: "https://go.dev/doc/"},
		{name: "protocol relative", href: "//example.com/page", want: "https://example.com/page"},
		{name: "unsupported scheme", href: "javascript:alert(1)", want: ""},
	}
	for _, tc := range cases {
		got, err := resolveSearchResultURL(tc.href)
		if err != nil {
			t.Errorf("%s: resolveSearchResultURL(%q) error: %v", tc.name, tc.href, err)
			continue
		}
		if got != tc.want {
			t.Errorf("%s: resolveSearchResultURL(%q) = %q, want %q", tc.name, tc.href, got, tc.want)
		}
	}
}
