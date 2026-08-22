package webfetch

import (
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCacheStoresAndReturnsContent(t *testing.T) {
	c := newCache(1024, time.Minute)
	c.Set("https://example.com", Content{URL: "https://example.com", Markdown: "body"})

	got, ok := c.Get("https://example.com")
	if !ok {
		t.Fatal("Get returned miss for a stored key")
	}
	if got.Markdown != "body" {
		t.Fatalf("Markdown = %q, want %q", got.Markdown, "body")
	}
	if _, ok := c.Get("https://other.com"); ok {
		t.Fatal("Get returned a hit for an unknown key")
	}
}

func TestCacheExpiresEntries(t *testing.T) {
	c := newCache(1024, -time.Second)
	c.Set("k", Content{Markdown: "body"})
	if _, ok := c.Get("k"); ok {
		t.Fatal("expired entry was returned")
	}
	if c.usedBytes != 0 {
		t.Fatalf("usedBytes = %d after expiry, want 0", c.usedBytes)
	}
}

func TestCacheEvictsLeastRecentlyUsed(t *testing.T) {
	// Each entry is 10 bytes of markdown, so the budget holds two.
	c := newCache(20, time.Minute)
	c.Set("a", Content{Markdown: strings.Repeat("a", 10)})
	c.Set("b", Content{Markdown: strings.Repeat("b", 10)})

	if _, ok := c.Get("a"); !ok {
		t.Fatal("entry a was evicted too early")
	}

	c.Set("c", Content{Markdown: strings.Repeat("c", 10)})
	if _, ok := c.Get("b"); ok {
		t.Fatal("entry b should have been evicted as least recently used")
	}
	if _, ok := c.Get("a"); !ok {
		t.Fatal("entry a was refreshed by Get and should have survived")
	}
	if _, ok := c.Get("c"); !ok {
		t.Fatal("newest entry is missing")
	}
}

func TestCacheReplacesExistingKeyWithoutLeakingBytes(t *testing.T) {
	c := newCache(1024, time.Minute)
	c.Set("k", Content{Markdown: strings.Repeat("x", 100)})
	c.Set("k", Content{Markdown: "small"})

	if c.recency.Len() != 1 {
		t.Fatalf("recency length = %d, want 1", c.recency.Len())
	}
	if c.usedBytes != int64(len("small")) {
		t.Fatalf("usedBytes = %d, want %d", c.usedBytes, len("small"))
	}
	got, _ := c.Get("k")
	if got.Markdown != "small" {
		t.Fatalf("Markdown = %q, want the replacement value", got.Markdown)
	}
}

func TestCacheIsSafeForConcurrentUse(t *testing.T) {
	c := newCache(4096, time.Minute)
	var wg sync.WaitGroup
	for worker := range 8 {
		wg.Go(func() {
			key := string(rune('a' + worker))
			for range 100 {
				c.Set(key, Content{Markdown: strings.Repeat(key, 8)})
				c.Get(key)
			}
		})
	}
	wg.Wait()
	if c.usedBytes < 0 {
		t.Fatalf("usedBytes = %d, want a non-negative total", c.usedBytes)
	}
}
