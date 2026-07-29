package tools

import (
	"bytes"
	"fmt"
	"sync"

	"github.com/channyeintun/nami/internal/textutil"
)

const backgroundCommandMaxOutputBytes = 256 * 1024

type boundedOutput struct {
	mu               sync.Mutex
	data             []byte
	readOffset       int
	droppedUnreadLen int
}

type unreadOutputSummary struct {
	HasUnread   bool
	UnreadBytes int
	Preview     string
}

func (b *boundedOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	b.data = append(b.data, p...)
	if len(b.data) > backgroundCommandMaxOutputBytes {
		trim := len(b.data) - backgroundCommandMaxOutputBytes
		if b.readOffset < trim {
			b.droppedUnreadLen += trim - b.readOffset
		}
		b.data = append([]byte(nil), b.data[trim:]...)
		if b.readOffset > trim {
			b.readOffset -= trim
		} else {
			b.readOffset = 0
		}
	}
	return len(p), nil
}

func (b *boundedOutput) ReadDelta() string {
	b.mu.Lock()
	defer b.mu.Unlock()

	hadDroppedOutput := b.droppedUnreadLen > 0
	droppedUnreadLen := b.droppedUnreadLen
	b.droppedUnreadLen = 0

	if b.readOffset >= len(b.data) {
		if hadDroppedOutput {
			return fmt.Sprintf("[Older buffered output was dropped before it could be read (%d bytes)]", droppedUnreadLen)
		}
		return ""
	}
	delta := bytes.TrimSpace(b.data[b.readOffset:])
	b.readOffset = len(b.data)
	if hadDroppedOutput {
		if len(delta) == 0 {
			return fmt.Sprintf("[Older buffered output was dropped before it could be read (%d bytes)]", droppedUnreadLen)
		}
		return fmt.Sprintf("[Older buffered output was dropped before it could be read (%d bytes)]\n%s", droppedUnreadLen, delta)
	}
	return string(delta)
}

func (b *boundedOutput) unreadSummary(limit int) unreadOutputSummary {
	b.mu.Lock()
	defer b.mu.Unlock()

	if b.readOffset >= len(b.data) {
		return unreadOutputSummary{}
	}

	unread := bytes.TrimSpace(b.data[b.readOffset:])
	if len(unread) == 0 {
		return unreadOutputSummary{}
	}

	previewBytes := unread
	truncated := false
	if limit > 0 && len(previewBytes) > limit {
		previewBytes = textutil.TruncateTailBytes(previewBytes, limit)
		truncated = true
	}
	preview := string(previewBytes)
	if truncated {
		preview = "[...]" + preview
	}
	if b.droppedUnreadLen > 0 {
		preview = fmt.Sprintf("[older unread output dropped: %d bytes]\n%s", b.droppedUnreadLen, preview)
	}

	return unreadOutputSummary{
		HasUnread:   true,
		UnreadBytes: len(unread),
		Preview:     preview,
	}
}

func (b *boundedOutput) tail(limit int) string {
	b.mu.Lock()
	defer b.mu.Unlock()

	if len(b.data) == 0 {
		return ""
	}

	tail := bytes.TrimSpace(b.data)
	if len(tail) == 0 {
		return ""
	}

	truncated := false
	if limit > 0 && len(tail) > limit {
		tail = textutil.TruncateTailBytes(tail, limit)
		truncated = true
	}

	text := string(tail)
	if truncated {
		return "[...]" + text
	}
	return text
}
