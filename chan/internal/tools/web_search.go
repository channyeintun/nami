package tools

import (
	"context"
	"fmt"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

const (
	defaultWebSearchTimeout = 15 * time.Second
	defaultWebSearchLimit   = 5
	maxWebSearchLimit       = 10
	maxWebSearchBodyBytes   = 2 * 1024 * 1024
	duckDuckGoHTMLURL       = "https://html.duckduckgo.com/html/"
)

var webSearchResultAnchorPattern = regexp.MustCompile(`(?is)<a[^>]*class="[^"]*result__a[^"]*"[^>]*href="([^"]+)"[^>]*>(.*?)</a>`)
var webSearchTagPattern = regexp.MustCompile(`(?is)<[^>]+>`)

// WebSearchTool searches the public web and returns result titles with URLs.
type WebSearchTool struct {
	client   *http.Client
	endpoint string
}

// NewWebSearchTool constructs the web search tool.
func NewWebSearchTool() *WebSearchTool {
	return &WebSearchTool{
		client:   &http.Client{Timeout: defaultWebSearchTimeout},
		endpoint: duckDuckGoHTMLURL,
	}
}

func (t *WebSearchTool) Name() string {
	return "web_search"
}

func (t *WebSearchTool) Description() string {
	return "Search the public web for current information and return result titles with URLs."
}

func (t *WebSearchTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"query": map[string]any{
				"type":        "string",
				"description": "The search query to execute.",
			},
			"allowed_domains": map[string]any{
				"type":        "array",
				"description": "Optional list of domains that results must belong to.",
				"items":       map[string]any{"type": "string"},
			},
			"blocked_domains": map[string]any{
				"type":        "array",
				"description": "Optional list of domains that results must not belong to.",
				"items":       map[string]any{"type": "string"},
			},
			"limit": map[string]any{
				"type":        "integer",
				"description": "Optional maximum number of results to return. Defaults to 5 and caps at 10.",
				"minimum":     1,
				"maximum":     maxWebSearchLimit,
			},
		},
		"required": []string{"query"},
	}
}

func (t *WebSearchTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *WebSearchTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *WebSearchTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	query, ok := stringParam(input.Params, "query")
	if !ok || strings.TrimSpace(query) == "" {
		return ToolOutput{}, fmt.Errorf("web_search requires query")
	}

	allowedDomains := stringSliceParam(input.Params, "allowed_domains")
	blockedDomains := stringSliceParam(input.Params, "blocked_domains")
	if len(allowedDomains) > 0 && len(blockedDomains) > 0 {
		return ToolOutput{}, fmt.Errorf("web_search cannot use allowed_domains and blocked_domains together")
	}

	limit := intOrDefault(input.Params, "limit", defaultWebSearchLimit)
	if limit < 1 || limit > maxWebSearchLimit {
		return ToolOutput{}, fmt.Errorf("limit must be between 1 and %d", maxWebSearchLimit)
	}

	body, err := t.fetchSearchResults(ctx, query)
	if err != nil {
		return ToolOutput{}, err
	}

	results := extractWebSearchResults(body, allowedDomains, blockedDomains, limit)
	if len(results) == 0 {
		return ToolOutput{Output: fmt.Sprintf("No web results found for %q", query)}, nil
	}

	var builder strings.Builder
	fmt.Fprintf(&builder, "Web search results for %q:\n", query)
	for index, result := range results {
		fmt.Fprintf(&builder, "\n%d. %s\n   URL: %s", index+1, result.Title, result.URL)
	}

	return ToolOutput{Output: builder.String()}, nil
}

func (t *WebSearchTool) fetchSearchResults(ctx context.Context, query string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, t.endpoint, nil)
	if err != nil {
		return "", fmt.Errorf("create web search request: %w", err)
	}

	values := req.URL.Query()
	values.Set("q", query)
	req.URL.RawQuery = values.Encode()
	req.Header.Set("User-Agent", "gocode/0.1 (+https://github.com/channyeintun/gocode)")

	resp, err := t.client.Do(req)
	if err != nil {
		return "", fmt.Errorf("execute web search request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("web search returned status %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWebSearchBodyBytes))
	if err != nil {
		return "", fmt.Errorf("read web search response: %w", err)
	}
	return string(body), nil
}

type webSearchResult struct {
	Title string
	URL   string
}

func extractWebSearchResults(body string, allowedDomains, blockedDomains []string, limit int) []webSearchResult {
	matches := webSearchResultAnchorPattern.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		return nil
	}

	allowed := normalizeDomainRules(allowedDomains)
	blocked := normalizeDomainRules(blockedDomains)
	seen := make(map[string]struct{})
	results := make([]webSearchResult, 0, min(limit, len(matches)))

	for _, match := range matches {
		if len(match) < 3 {
			continue
		}

		resolvedURL, err := resolveSearchResultURL(match[1])
		if err != nil || resolvedURL == "" {
			continue
		}
		if !domainAllowed(resolvedURL, allowed, blocked) {
			continue
		}
		if _, ok := seen[resolvedURL]; ok {
			continue
		}

		title := sanitizeSearchTitle(match[2])
		if title == "" {
			continue
		}

		seen[resolvedURL] = struct{}{}
		results = append(results, webSearchResult{Title: title, URL: resolvedURL})
		if len(results) == limit {
			break
		}
	}

	return results
}

func resolveSearchResultURL(rawHref string) (string, error) {
	rawHref = strings.TrimSpace(html.UnescapeString(rawHref))
	if rawHref == "" {
		return "", nil
	}

	parsed, err := url.Parse(rawHref)
	if err != nil {
		return "", err
	}
	if redirectURL := parsed.Query().Get("uddg"); redirectURL != "" {
		decoded, err := url.QueryUnescape(redirectURL)
		if err == nil {
			rawHref = decoded
		}
	}

	parsed, err = url.Parse(rawHref)
	if err != nil {
		return "", err
	}
	if parsed.Scheme == "" && strings.HasPrefix(rawHref, "//") {
		parsed.Scheme = "https"
		return parsed.String(), nil
	}
	if parsed.Scheme == "http" || parsed.Scheme == "https" {
		return parsed.String(), nil
	}
	return "", nil
}

func sanitizeSearchTitle(raw string) string {
	clean := webSearchTagPattern.ReplaceAllString(raw, " ")
	clean = html.UnescapeString(clean)
	return strings.Join(strings.Fields(clean), " ")
}

func normalizeDomainRules(domains []string) []string {
	if len(domains) == 0 {
		return nil
	}
	unique := make(map[string]struct{}, len(domains))
	for _, domain := range domains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		domain = strings.TrimPrefix(domain, "https://")
		domain = strings.TrimPrefix(domain, "http://")
		domain = strings.TrimPrefix(domain, "www.")
		domain = strings.TrimSuffix(domain, "/")
		if domain != "" {
			unique[domain] = struct{}{}
		}
	}
	normalized := make([]string, 0, len(unique))
	for domain := range unique {
		normalized = append(normalized, domain)
	}
	sort.Strings(normalized)
	return normalized
}

func domainAllowed(rawURL string, allowedDomains, blockedDomains []string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := strings.ToLower(parsed.Hostname())
	host = strings.TrimPrefix(host, "www.")
	if host == "" {
		return false
	}

	if len(allowedDomains) > 0 && !matchesAnyDomain(host, allowedDomains) {
		return false
	}
	if len(blockedDomains) > 0 && matchesAnyDomain(host, blockedDomains) {
		return false
	}
	return true
}

func matchesAnyDomain(host string, domains []string) bool {
	for _, domain := range domains {
		if host == domain || strings.HasSuffix(host, "."+domain) {
			return true
		}
	}
	return false
}

func stringSliceParam(params map[string]any, key string) []string {
	value, ok := params[key]
	if !ok {
		return nil
	}
	switch v := value.(type) {
	case []string:
		return append([]string(nil), v...)
	case []any:
		items := make([]string, 0, len(v))
		for _, item := range v {
			if text, ok := item.(string); ok && strings.TrimSpace(text) != "" {
				items = append(items, text)
			}
		}
		return items
	default:
		return nil
	}
}
