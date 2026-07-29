package webfetch

import (
	"container/list"
	"sync"
	"time"
)

// cache is a size-bounded, TTL-expiring LRU of converted pages.
type cache struct {
	mu        sync.Mutex
	maxBytes  int64
	ttl       time.Duration
	usedBytes int64
	entries   map[string]*list.Element
	recency   *list.List
}

type cacheEntry struct {
	key       string
	value     Content
	size      int64
	expiresAt time.Time
}

func newCache(maxBytes int64, ttl time.Duration) *cache {
	return &cache{
		maxBytes: maxBytes,
		ttl:      ttl,
		entries:  make(map[string]*list.Element),
		recency:  list.New(),
	}
}

func (c *cache) Get(key string) (Content, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	element, ok := c.entries[key]
	if !ok {
		return Content{}, false
	}
	entry := element.Value.(*cacheEntry)
	if time.Now().After(entry.expiresAt) {
		c.removeElement(element)
		return Content{}, false
	}
	c.recency.MoveToFront(element)
	return entry.value, true
}

func (c *cache) Set(key string, value Content) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if element, ok := c.entries[key]; ok {
		c.removeElement(element)
	}

	entry := &cacheEntry{
		key:       key,
		value:     value,
		size:      int64(len(value.Markdown) + len(value.ContentType) + len(value.StatusText) + len(value.URL)),
		expiresAt: time.Now().Add(c.ttl),
	}
	c.entries[key] = c.recency.PushFront(entry)
	c.usedBytes += entry.size

	for c.usedBytes > c.maxBytes && c.recency.Len() > 0 {
		c.removeElement(c.recency.Back())
	}
}

func (c *cache) removeElement(element *list.Element) {
	if element == nil {
		return
	}
	entry := element.Value.(*cacheEntry)
	delete(c.entries, entry.key)
	c.usedBytes -= entry.size
	c.recency.Remove(element)
	if c.usedBytes < 0 {
		c.usedBytes = 0
	}
}
