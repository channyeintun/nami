package debuglog

import (
	"encoding/json"
	"io"
)

// IPCWriter wraps an io.Writer and logs every NDJSON line emitted.
type IPCWriter struct {
	inner io.Writer
}

// NewIPCWriter returns a writer that logs outbound IPC frames.
func NewIPCWriter(w io.Writer) *IPCWriter {
	return &IPCWriter{inner: w}
}

func (w *IPCWriter) Write(p []byte) (int, error) {
	n, err := w.inner.Write(p)
	if n > 0 {
		raw := string(p[:n])
		// Try to extract the event type for easier grep.
		eventType := extractIPCType(raw)
		logRaw := raw
		if len(logRaw) > 500 {
			logRaw = logRaw[:500] + "...(truncated)"
		}
		Log("ipc", "emit", map[string]any{
			"type":  eventType,
			"bytes": n,
			"raw":   logRaw,
		})
	}
	return n, err
}

// IPCReader wraps an io.Reader and logs every inbound chunk.
type IPCReader struct {
	inner io.Reader
}

// NewIPCReader returns a reader that logs inbound IPC messages.
func NewIPCReader(r io.Reader) *IPCReader {
	return &IPCReader{inner: r}
}

func (r *IPCReader) Read(p []byte) (int, error) {
	n, err := r.inner.Read(p)
	if n > 0 {
		raw := string(p[:n])
		msgType := extractIPCType(raw)
		logRaw := raw
		if len(logRaw) > 500 {
			logRaw = logRaw[:500] + "...(truncated)"
		}
		Log("ipc", "recv", map[string]any{
			"type":  msgType,
			"bytes": n,
			"raw":   logRaw,
		})
	}
	return n, err
}

// extractIPCType attempts to extract the "type" field from a JSON line.
func extractIPCType(raw string) string {
	var partial struct {
		Type string `json:"type"`
	}
	if json.Unmarshal([]byte(raw), &partial) == nil && partial.Type != "" {
		return partial.Type
	}
	return "unknown"
}
