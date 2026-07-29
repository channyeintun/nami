package webfetch

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// localFetcher points a Fetcher at an httptest server. The production Fetcher
// refuses to dial loopback addresses, which is exactly what a test server is,
// so these tests use the default transport and exercise everything above it.
func localFetcher() *Fetcher {
	fetcher := New()
	fetcher.client = &http.Client{
		Timeout: 5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	return fetcher
}

func TestFetchConvertsHTMLToMarkdown(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<html><body><article><h1>Title</h1><p>Body text.</p></article></body></html>`)
	}))
	defer server.Close()

	content, err := localFetcher().Fetch(context.Background(), server.URL)
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if !strings.Contains(content.Markdown, "Title") || !strings.Contains(content.Markdown, "Body text.") {
		t.Fatalf("markdown = %q, want the article content", content.Markdown)
	}
	if content.StatusCode != http.StatusOK {
		t.Errorf("StatusCode = %d, want 200", content.StatusCode)
	}
	if content.Bytes == 0 {
		t.Error("Bytes = 0, want the response size")
	}
}

func TestFetchServesRepeatRequestsFromCache(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "plain body")
	}))
	defer server.Close()

	fetcher := localFetcher()
	for range 3 {
		if _, err := fetcher.Fetch(context.Background(), server.URL); err != nil {
			t.Fatalf("Fetch: %v", err)
		}
	}
	if got := requests.Load(); got != 1 {
		t.Fatalf("server saw %d requests, want 1", got)
	}
}

func TestFetchFollowsSameHostRedirect(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/start", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/final", http.StatusFound)
	})
	mux.HandleFunc("/final", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		fmt.Fprint(w, "arrived")
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	content, err := localFetcher().Fetch(context.Background(), server.URL+"/start")
	if err != nil {
		t.Fatalf("Fetch: %v", err)
	}
	if content.Markdown != "arrived" {
		t.Fatalf("markdown = %q, want %q", content.Markdown, "arrived")
	}
}

func TestFetchRefusesCrossHostRedirect(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "https://example.com/elsewhere", http.StatusFound)
	}))
	defer server.Close()

	_, err := localFetcher().Fetch(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "redirect requires approval") {
		t.Fatalf("Fetch error = %v, want a cross-host redirect refusal", err)
	}
}

func TestFetchRejectsRedirectWithoutLocation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusFound)
	}))
	defer server.Close()

	if _, err := localFetcher().Fetch(context.Background(), server.URL); err == nil {
		t.Fatal("Fetch succeeded on a redirect without Location, want an error")
	}
}

func TestFetchStopsOnRedirectLoop(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer server.Close()

	_, err := localFetcher().Fetch(context.Background(), server.URL+"/loop")
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("Fetch error = %v, want a redirect-limit error", err)
	}
}

func TestFetchReportsErrorStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusForbidden)
	}))
	defer server.Close()

	_, err := localFetcher().Fetch(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("Fetch error = %v, want the status in the message", err)
	}
}

func TestFetchRejectsOversizedBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		chunk := strings.Repeat("a", 1<<20)
		for range (maxContentBytes / (1 << 20)) + 1 {
			if _, err := w.Write([]byte(chunk)); err != nil {
				return
			}
		}
	}))
	defer server.Close()

	_, err := localFetcher().Fetch(context.Background(), server.URL)
	if err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("Fetch error = %v, want a size-limit error", err)
	}
}

func TestFetchHonoursContextCancellation(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-release
	}))
	defer func() {
		close(release)
		server.Close()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := localFetcher().Fetch(ctx, server.URL); err == nil {
		t.Fatal("Fetch succeeded with a cancelled context, want an error")
	}
}

// The production Fetcher must refuse loopback targets even when the URL is
// handed straight to Fetch without going through NormalizeURL.
func TestProductionFetcherBlocksLoopbackDial(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, "should never be reached")
	}))
	defer server.Close()

	if _, err := New().Fetch(context.Background(), server.URL); err == nil {
		t.Fatal("Fetch reached a loopback server, want the dial guard to block it")
	}
}

func TestToMarkdown(t *testing.T) {
	cases := []struct {
		name        string
		body        string
		contentType string
		want        string
	}{
		{"empty body", "   ", "text/html", ""},
		{"plain text passthrough", "just text", "text/plain", "just text"},
		{"html by content type", "<p>hello</p>", "text/html", "hello"},
		{"html by sniffing", "<html><body><p>hi</p></body></html>", "application/octet-stream", "hi"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ToMarkdown(tc.body, tc.contentType)
			if err != nil {
				t.Fatalf("ToMarkdown: %v", err)
			}
			if !strings.Contains(got, tc.want) {
				t.Fatalf("ToMarkdown = %q, want it to contain %q", got, tc.want)
			}
		})
	}
}

func TestLooksLikeHTML(t *testing.T) {
	for _, body := range []string{"<html>", "<!DOCTYPE html>", "  <BODY>x</BODY>"} {
		if !looksLikeHTML(body) {
			t.Errorf("looksLikeHTML(%q) = false, want true", body)
		}
	}
	for _, body := range []string{"", "plain", "{\"json\":true}", "# markdown"} {
		if looksLikeHTML(body) {
			t.Errorf("looksLikeHTML(%q) = true, want false", body)
		}
	}
}
