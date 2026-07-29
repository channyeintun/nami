package tools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"strings"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/channyeintun/nami/internal/readability"
)

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
		focusedHTML := readability.ExtractHTMLForMarkdown(body)
		markdown, err := htmltomarkdown.ConvertString(focusedHTML)
		if err != nil {
			return "", fmt.Errorf("convert html to markdown: %w", err)
		}
		return strings.TrimSpace(markdown), nil
	}

	return body, nil
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
