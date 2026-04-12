package tools

import (
	"container/list"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"
	"sync"
	"time"
	"unicode"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
)

const (
	defaultWebFetchTimeout         = 60 * time.Second
	maxWebFetchURLLength           = 2000
	maxWebFetchContentBytes  int64 = 10 * 1024 * 1024
	maxWebFetchMarkdownChars       = 100_000
	maxWebFetchCacheBytes    int64 = 50 * 1024 * 1024
	webFetchCacheTTL               = 15 * time.Minute
	maxWebFetchRedirects           = 10
	webFetchUserAgent              = "gocode/0.1 (+https://github.com/channyeintun/gocode)"
)

// WebFetchTool fetches a URL, converts HTML to markdown, and returns prompt-focused content.
type WebFetchTool struct {
	client *http.Client
	cache  *webFetchCache
}

// NewWebFetchTool constructs the web fetch tool.
func NewWebFetchTool() *WebFetchTool {
	return &WebFetchTool{
		client: &http.Client{
			Timeout: defaultWebFetchTimeout,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= maxWebFetchRedirects {
					return errors.New("stopped after too many redirects")
				}
				return http.ErrUseLastResponse
			},
		},
		cache: newWebFetchCache(maxWebFetchCacheBytes, webFetchCacheTTL),
	}
}

func (t *WebFetchTool) Name() string {
	return "web_fetch"
}

func (t *WebFetchTool) Description() string {
	return "Fetch content from a URL, convert HTML to markdown, and return prompt-focused excerpts."
}

func (t *WebFetchTool) InputSchema() any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"url": map[string]any{
				"type":        "string",
				"description": "The URL to fetch.",
			},
			"prompt": map[string]any{
				"type":        "string",
				"description": "What information to extract from the fetched content.",
			},
		},
		"required": []string{"url", "prompt"},
	}
}

func (t *WebFetchTool) Permission() PermissionLevel {
	return PermissionReadOnly
}

func (t *WebFetchTool) Concurrency(input ToolInput) ConcurrencyDecision {
	return ConcurrencyParallel
}

func (t *WebFetchTool) Execute(ctx context.Context, input ToolInput) (ToolOutput, error) {
	rawURL, ok := stringParam(input.Params, "url")
	if !ok || strings.TrimSpace(rawURL) == "" {
		return ToolOutput{}, fmt.Errorf("web_fetch requires url")
	}
	prompt, ok := stringParam(input.Params, "prompt")
	if !ok || strings.TrimSpace(prompt) == "" {
		return ToolOutput{}, fmt.Errorf("web_fetch requires prompt")
	}

	normalizedURL, err := normalizeWebFetchURL(rawURL)
	if err != nil {
		return ToolOutput{}, err
	}

	content, err := t.getMarkdownContent(ctx, normalizedURL)
	if err != nil {
		return ToolOutput{}, err
	}

	result := buildWebFetchResult(normalizedURL, prompt, content)

	// Route substantial fetch results to a search-report artifact.
	const searchReportThreshold = 4000
	if len(result) >= searchReportThreshold {
		if mutation, ok := saveSearchReportArtifact(ctx, normalizedURL, strings.TrimSpace(prompt), result); ok {
			return ToolOutput{Output: result, Artifacts: []ArtifactMutation{mutation}}, nil
		}
	}

	return ToolOutput{Output: result}, nil
}

type webFetchContent struct {
	URL         string
	StatusCode  int
	StatusText  string
	ContentType string
	Bytes       int
	Markdown    string
}

func (t *WebFetchTool) getMarkdownContent(ctx context.Context, rawURL string) (webFetchContent, error) {
	if cached, ok := t.cache.Get(rawURL); ok {
		return cached, nil
	}

	currentURL := rawURL
	for redirectCount := 0; redirectCount <= maxWebFetchRedirects; redirectCount++ {
		content, redirectURL, err := t.fetchOnce(ctx, currentURL)
		if err != nil {
			return webFetchContent{}, err
		}
		if redirectURL == "" {
			t.cache.Set(rawURL, content)
			return content, nil
		}
		if !webFetchPermittedRedirect(currentURL, redirectURL) {
			return webFetchContent{}, fmt.Errorf("web_fetch redirect requires approval: %s -> %s", currentURL, redirectURL)
		}
		currentURL = redirectURL
	}

	return webFetchContent{}, fmt.Errorf("web_fetch exceeded %d redirects", maxWebFetchRedirects)
}

func (t *WebFetchTool) fetchOnce(ctx context.Context, rawURL string) (webFetchContent, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return webFetchContent{}, "", fmt.Errorf("create web fetch request: %w", err)
	}
	req.Header.Set("Accept", "text/markdown, text/html, text/plain, */*")
	req.Header.Set("User-Agent", webFetchUserAgent)

	resp, err := t.client.Do(req)
	if err != nil {
		return webFetchContent{}, "", fmt.Errorf("execute web fetch request: %w", err)
	}
	defer resp.Body.Close()

	if isRedirectStatus(resp.StatusCode) {
		location := strings.TrimSpace(resp.Header.Get("Location"))
		if location == "" {
			return webFetchContent{}, "", fmt.Errorf("redirect missing Location header")
		}
		redirectURL, err := resolveWebFetchRedirect(rawURL, location)
		if err != nil {
			return webFetchContent{}, "", err
		}
		return webFetchContent{}, redirectURL, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return webFetchContent{}, "", fmt.Errorf("web_fetch returned status %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxWebFetchContentBytes+1))
	if err != nil {
		return webFetchContent{}, "", fmt.Errorf("read web fetch response: %w", err)
	}
	if int64(len(body)) > maxWebFetchContentBytes {
		return webFetchContent{}, "", fmt.Errorf("web_fetch response exceeded %d bytes", maxWebFetchContentBytes)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	markdown, err := t.toMarkdown(string(body), contentType)
	if err != nil {
		return webFetchContent{}, "", err
	}

	return webFetchContent{
		URL:         rawURL,
		StatusCode:  resp.StatusCode,
		StatusText:  resp.Status,
		ContentType: contentType,
		Bytes:       len(body),
		Markdown:    markdown,
	}, "", nil
}

func (t *WebFetchTool) toMarkdown(body string, contentType string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", nil
	}

	if strings.Contains(contentType, "text/html") || looksLikeHTML(body) {
		markdown, err := htmltomarkdown.ConvertString(body)
		if err != nil {
			return "", fmt.Errorf("convert html to markdown: %w", err)
		}
		return strings.TrimSpace(markdown), nil
	}

	return body, nil
}

func buildWebFetchResult(rawURL, prompt string, content webFetchContent) string {
	truncatedContent := content.Markdown
	if len(truncatedContent) > maxWebFetchMarkdownChars {
		truncatedContent = truncatedContent[:maxWebFetchMarkdownChars] + "\n\n[Content truncated due to length...]"
	}

	passages := extractRelevantPassages(truncatedContent, prompt)

	var builder strings.Builder
	fmt.Fprintf(&builder, "Fetched: %s\n", rawURL)
	fmt.Fprintf(&builder, "Status: %s\n", content.StatusText)
	if content.ContentType != "" {
		fmt.Fprintf(&builder, "Content-Type: %s\n", content.ContentType)
	}
	fmt.Fprintf(&builder, "Bytes: %d\n", content.Bytes)
	fmt.Fprintf(&builder, "Prompt: %s\n", strings.TrimSpace(prompt))

	if len(passages) > 0 {
		builder.WriteString("\nRelevant excerpts:\n")
		for index, passage := range passages {
			fmt.Fprintf(&builder, "\n%d. %s\n", index+1, passage)
		}
	} else if truncatedContent != "" {
		builder.WriteString("\nContent:\n\n")
		builder.WriteString(truncatedContent)
	} else {
		builder.WriteString("\nNo readable content returned.\n")
	}

	return strings.TrimSpace(builder.String())
}

func extractRelevantPassages(markdown string, prompt string) []string {
	sections := splitWebFetchSections(markdown)
	if len(sections) == 0 {
		return nil
	}

	keywords := keywordSet(prompt)
	if len(keywords) == 0 {
		return firstNonEmptySections(sections, 3)
	}

	type scoredSection struct {
		text  string
		score int
	}

	scored := make([]scoredSection, 0, len(sections))
	for _, section := range sections {
		sectionKeywords := keywordSet(section)
		score := 0
		for keyword := range keywords {
			if _, ok := sectionKeywords[keyword]; ok {
				score++
			}
		}
		if score > 0 {
			scored = append(scored, scoredSection{text: section, score: score})
		}
	}

	if len(scored) == 0 {
		return firstNonEmptySections(sections, 3)
	}

	for left := 0; left < len(scored)-1; left++ {
		for right := left + 1; right < len(scored); right++ {
			if scored[right].score > scored[left].score {
				scored[left], scored[right] = scored[right], scored[left]
			}
		}
	}

	result := make([]string, 0, min(3, len(scored)))
	for _, item := range scored {
		result = append(result, item.text)
		if len(result) == 3 {
			break
		}
	}
	return result
}

func splitWebFetchSections(markdown string) []string {
	rawSections := strings.FieldsFunc(markdown, func(r rune) bool {
		return r == '\n' || r == '\r'
	})
	sections := make([]string, 0, len(rawSections))
	for _, section := range rawSections {
		section = strings.Join(strings.Fields(section), " ")
		if section != "" {
			sections = append(sections, section)
		}
	}
	return sections
}

func firstNonEmptySections(sections []string, limit int) []string {
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
	text = strings.ToLower(text)
	parts := strings.FieldsFunc(text, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsNumber(r)
	})
	keywords := make(map[string]struct{}, len(parts))
	for _, part := range parts {
		if len(part) < 3 {
			continue
		}
		keywords[part] = struct{}{}
	}
	return keywords
}

func normalizeWebFetchURL(rawURL string) (string, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return "", fmt.Errorf("web_fetch requires url")
	}
	if len(rawURL) > maxWebFetchURLLength {
		return "", fmt.Errorf("web_fetch url exceeds %d characters", maxWebFetchURLLength)
	}

	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid url %q: %w", rawURL, err)
	}
	if parsed.Scheme == "" {
		return "", fmt.Errorf("web_fetch requires an absolute url")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("web_fetch only supports http and https urls")
	}
	if parsed.User != nil {
		return "", fmt.Errorf("web_fetch does not allow credentials in urls")
	}
	if parsed.Hostname() == "" {
		return "", fmt.Errorf("web_fetch requires a hostname")
	}
	if err := validateWebFetchHost(parsed.Hostname()); err != nil {
		return "", err
	}
	if parsed.Scheme == "http" {
		parsed.Scheme = "https"
		return parsed.String(), nil
	}
	return parsed.String(), nil
}

func validateWebFetchHost(host string) error {
	host = strings.TrimSpace(host)
	if host == "" {
		return fmt.Errorf("web_fetch requires a hostname")
	}

	if ip, err := netip.ParseAddr(host); err == nil {
		if !ip.IsGlobalUnicast() || ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsMulticast() {
			return fmt.Errorf("web_fetch blocks private or local addresses")
		}
		return nil
	}

	if strings.EqualFold(host, "localhost") || !strings.Contains(host, ".") {
		return fmt.Errorf("web_fetch requires a public hostname")
	}

	addrs, err := net.DefaultResolver.LookupNetIP(context.Background(), "ip", host)
	if err != nil {
		return fmt.Errorf("resolve host %q: %w", host, err)
	}
	if len(addrs) == 0 {
		return fmt.Errorf("web_fetch could not resolve %q", host)
	}
	for _, addr := range addrs {
		if !addr.IsGlobalUnicast() || addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsMulticast() {
			return fmt.Errorf("web_fetch blocks private or local addresses")
		}
	}
	return nil
}

func isRedirectStatus(statusCode int) bool {
	switch statusCode {
	case http.StatusMovedPermanently, http.StatusFound, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		return true
	default:
		return false
	}
}

func resolveWebFetchRedirect(baseURL, location string) (string, error) {
	base, err := url.Parse(baseURL)
	if err != nil {
		return "", fmt.Errorf("parse redirect base url: %w", err)
	}
	redirectURL, err := url.Parse(location)
	if err != nil {
		return "", fmt.Errorf("parse redirect url: %w", err)
	}
	return base.ResolveReference(redirectURL).String(), nil
}

func webFetchPermittedRedirect(originalURL, redirectURL string) bool {
	original, err := url.Parse(originalURL)
	if err != nil {
		return false
	}
	redirected, err := url.Parse(redirectURL)
	if err != nil {
		return false
	}
	if redirected.Scheme != original.Scheme {
		return false
	}
	if redirected.Port() != original.Port() {
		return false
	}
	if redirected.User != nil {
		return false
	}
	return stripWww(original.Hostname()) == stripWww(redirected.Hostname())
}

func stripWww(host string) string {
	return strings.TrimPrefix(strings.ToLower(host), "www.")
}

func looksLikeHTML(body string) bool {
	body = strings.ToLower(strings.TrimSpace(body))
	return strings.Contains(body, "<html") || strings.Contains(body, "<!doctype html") || strings.Contains(body, "<body")
}

type webFetchCache struct {
	mu        sync.Mutex
	maxBytes  int64
	ttl       time.Duration
	usedBytes int64
	entries   map[string]*list.Element
	recency   *list.List
}

type webFetchCacheEntry struct {
	key       string
	value     webFetchContent
	size      int64
	expiresAt time.Time
}

func newWebFetchCache(maxBytes int64, ttl time.Duration) *webFetchCache {
	return &webFetchCache{
		maxBytes: maxBytes,
		ttl:      ttl,
		entries:  make(map[string]*list.Element),
		recency:  list.New(),
	}
}

func (c *webFetchCache) Get(key string) (webFetchContent, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	element, ok := c.entries[key]
	if !ok {
		return webFetchContent{}, false
	}
	entry := element.Value.(*webFetchCacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.removeElement(element)
		return webFetchContent{}, false
	}
	c.recency.MoveToFront(element)
	return entry.value, true
}

func (c *webFetchCache) Set(key string, value webFetchContent) {
	size := int64(len(value.Markdown))
	if size <= 0 {
		size = 1
	}
	if size > c.maxBytes {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.entries[key]; ok {
		c.removeElement(existing)
	}

	entry := &webFetchCacheEntry{
		key:       key,
		value:     value,
		size:      size,
		expiresAt: time.Now().Add(c.ttl),
	}
	element := c.recency.PushFront(entry)
	c.entries[key] = element
	c.usedBytes += size

	for c.usedBytes > c.maxBytes {
		oldest := c.recency.Back()
		if oldest == nil {
			break
		}
		c.removeElement(oldest)
	}
}

func (c *webFetchCache) removeElement(element *list.Element) {
	entry := element.Value.(*webFetchCacheEntry)
	delete(c.entries, entry.key)
	c.recency.Remove(element)
	c.usedBytes -= entry.size
	if c.usedBytes < 0 {
		c.usedBytes = 0
	}
}
