package debuglog

import (
	"io"
)

// SSEReaderProxy wraps an io.Reader to log raw SSE bytes.
type SSEReaderProxy struct {
	inner    io.Reader
	provider string
}

// NewSSEReaderProxy returns a reader that logs every Read before delegating.
func NewSSEReaderProxy(inner io.Reader, provider string) *SSEReaderProxy {
	return &SSEReaderProxy{inner: inner, provider: provider}
}

func (r *SSEReaderProxy) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		raw := string(p[:n])
		// Truncate large payloads in log to avoid bloat.
		logRaw := raw
		if len(logRaw) > 2048 {
			logRaw = logRaw[:2048] + "...(truncated)"
		}
		Log("sse", "read", map[string]any{
			"provider": r.provider,
			"bytes":    n,
			"raw":      logRaw,
		})
	}
	if err != nil {
		Log("sse", "read_err", map[string]any{
			"provider": r.provider,
			"error":    err.Error(),
		})
	}
	return n, err
}

// Close delegates to the inner reader if it implements io.Closer.
func (r *SSEReaderProxy) Close() error {
	if c, ok := r.inner.(io.Closer); ok {
		return c.Close()
	}
	return nil
}
