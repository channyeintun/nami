package webfetch

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	htmltomarkdown "github.com/JohannesKaufmann/html-to-markdown/v2"
	"github.com/channyeintun/nami/internal/readability"
)

const (
	defaultTimeout         = 60 * time.Second
	maxContentBytes  int64 = 10 * 1024 * 1024
	maxCacheBytes    int64 = 50 * 1024 * 1024
	cacheTTL               = 15 * time.Minute
	maxRedirects           = 10
	defaultUserAgent       = "nami/0.1 (+https://github.com/channyeintun/nami)"
)

// Content is a fetched page after HTML-to-markdown conversion.
type Content struct {
	URL         string
	StatusCode  int
	StatusText  string
	ContentType string
	Bytes       int
	Markdown    string
}

// Fetcher retrieves pages and caches converted markdown between calls.
type Fetcher struct {
	client *http.Client
	cache  *cache
}

// New builds a Fetcher whose transport refuses to connect to private or local
// addresses and does not follow redirects on its own — redirects are resolved
// explicitly so each hop can be vetted.
func New() *Fetcher {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.DialContext = guardedDialer().DialContext

	return &Fetcher{
		client: &http.Client{
			Timeout:   defaultTimeout,
			Transport: transport,
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= maxRedirects {
					return errors.New("stopped after too many redirects")
				}
				return http.ErrUseLastResponse
			},
		},
		cache: newCache(maxCacheBytes, cacheTTL),
	}
}

// Fetch returns the markdown for a URL, following same-site redirects and
// serving repeat requests from the cache.
func (f *Fetcher) Fetch(ctx context.Context, rawURL string) (Content, error) {
	if cached, ok := f.cache.Get(rawURL); ok {
		return cached, nil
	}

	currentURL := rawURL
	for range maxRedirects + 1 {
		content, redirectURL, err := f.fetchOnce(ctx, currentURL)
		if err != nil {
			return Content{}, err
		}
		if redirectURL == "" {
			f.cache.Set(rawURL, content)
			return content, nil
		}
		if !permittedRedirect(currentURL, redirectURL) {
			return Content{}, fmt.Errorf("web_fetch redirect requires approval: %s -> %s", currentURL, redirectURL)
		}
		currentURL = redirectURL
	}

	return Content{}, fmt.Errorf("web_fetch exceeded %d redirects", maxRedirects)
}

// fetchOnce performs a single request. A non-empty second return value means
// the server asked for a redirect that the caller must decide about.
func (f *Fetcher) fetchOnce(ctx context.Context, rawURL string) (Content, string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return Content{}, "", fmt.Errorf("create web fetch request: %w", err)
	}
	req.Header.Set("Accept", "text/markdown, text/html, text/plain, */*")
	req.Header.Set("User-Agent", defaultUserAgent)

	resp, err := f.client.Do(req)
	if err != nil {
		return Content{}, "", fmt.Errorf("execute web fetch request: %w", err)
	}
	defer resp.Body.Close()

	if isRedirectStatus(resp.StatusCode) {
		location := strings.TrimSpace(resp.Header.Get("Location"))
		if location == "" {
			return Content{}, "", fmt.Errorf("redirect missing Location header")
		}
		redirectURL, err := resolveRedirect(rawURL, location)
		if err != nil {
			return Content{}, "", err
		}
		return Content{}, redirectURL, nil
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return Content{}, "", fmt.Errorf("web_fetch returned status %s", resp.Status)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, maxContentBytes+1))
	if err != nil {
		return Content{}, "", fmt.Errorf("read web fetch response: %w", err)
	}
	if int64(len(body)) > maxContentBytes {
		return Content{}, "", fmt.Errorf("web_fetch response exceeded %d bytes", maxContentBytes)
	}

	contentType := strings.ToLower(resp.Header.Get("Content-Type"))
	markdown, err := ToMarkdown(string(body), contentType)
	if err != nil {
		return Content{}, "", err
	}

	return Content{
		URL:         rawURL,
		StatusCode:  resp.StatusCode,
		StatusText:  resp.Status,
		ContentType: contentType,
		Bytes:       len(body),
		Markdown:    markdown,
	}, "", nil
}

// ToMarkdown converts an HTML body to markdown after stripping navigation and
// boilerplate. Non-HTML bodies pass through unchanged.
func ToMarkdown(body, contentType string) (string, error) {
	body = strings.TrimSpace(body)
	if body == "" {
		return "", nil
	}
	if !strings.Contains(contentType, "text/html") && !looksLikeHTML(body) {
		return body, nil
	}

	markdown, err := htmltomarkdown.ConvertString(readability.ExtractHTMLForMarkdown(body))
	if err != nil {
		return "", fmt.Errorf("convert html to markdown: %w", err)
	}
	return strings.TrimSpace(markdown), nil
}

func looksLikeHTML(body string) bool {
	body = strings.ToLower(strings.TrimSpace(body))
	return strings.Contains(body, "<html") || strings.Contains(body, "<!doctype html") || strings.Contains(body, "<body")
}
