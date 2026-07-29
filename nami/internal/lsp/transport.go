package lsp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
	"time"
)

// maxMessageBytes bounds a single server message so a malformed or hostile
// Content-Length header cannot make the engine allocate unbounded memory.
const maxMessageBytes = 32 * 1024 * 1024

const shutdownTimeout = 2 * time.Second

// syncBuffer collects server stderr. The pump goroutine writes to it while
// request error paths read from it, so the buffer needs its own lock.
type syncBuffer struct {
	mu   sync.Mutex
	data bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.data.String()
}

func (c *client) call(ctx context.Context, method string, params any, result any) error {
	id := c.nextRequestID()
	if err := c.writeMessage(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		envelope, err := c.readMessage()
		if err != nil {
			return c.wrapReadError(method, err)
		}
		// Server-initiated requests and notifications share the stream; skip
		// anything that is not the response we are waiting for.
		if envelope.Method != "" {
			continue
		}
		responseID, ok := parseResponseID(envelope.ID)
		if !ok || responseID != id {
			continue
		}
		if envelope.Error != nil {
			return fmt.Errorf("lsp %s failed: %s", method, envelope.Error.Message)
		}
		if result == nil || len(envelope.Result) == 0 || string(envelope.Result) == "null" {
			return nil
		}
		if err := json.Unmarshal(envelope.Result, result); err != nil {
			return fmt.Errorf("decode lsp result for %s: %w", method, err)
		}
		return nil
	}
}

func (c *client) wrapReadError(method string, err error) error {
	stderr := strings.TrimSpace(c.stderr.String())
	if stderr != "" {
		return fmt.Errorf("read lsp response for %s: %w (%s)", method, err, stderr)
	}
	return fmt.Errorf("read lsp response for %s: %w", method, err)
}

func (c *client) notify(method string, params any) error {
	return c.writeMessage(map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	})
}

func (c *client) writeMessage(message any) error {
	data, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode lsp message: %w", err)
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	if _, err := fmt.Fprintf(c.stdin, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = c.stdin.Write(data)
	return err
}

func (c *client) readMessage() (responseEnvelope, error) {
	contentLength, err := c.readHeaders()
	if err != nil {
		return responseEnvelope{}, err
	}
	payload := make([]byte, contentLength)
	if _, err := io.ReadFull(c.stdout, payload); err != nil {
		return responseEnvelope{}, err
	}
	var envelope responseEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return responseEnvelope{}, fmt.Errorf("decode lsp message: %w", err)
	}
	return envelope, nil
}

func (c *client) readHeaders() (int, error) {
	contentLength := 0
	for {
		line, err := c.stdout.ReadString('\n')
		if err != nil {
			return 0, err
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		name, value, ok := strings.Cut(trimmed, ":")
		if !ok || !strings.EqualFold(strings.TrimSpace(name), "Content-Length") {
			continue
		}
		length, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, fmt.Errorf("parse content length %q: %w", strings.TrimSpace(value), err)
		}
		contentLength = length
	}
	if contentLength <= 0 {
		return 0, fmt.Errorf("missing content length in lsp message")
	}
	if contentLength > maxMessageBytes {
		return 0, fmt.Errorf("lsp message of %d bytes exceeds the %d byte limit", contentLength, maxMessageBytes)
	}
	return contentLength, nil
}

func (c *client) nextRequestID() int64 {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	c.nextID++
	return c.nextID
}

// Close asks the server to shut down, then makes sure the process is gone.
func (c *client) Close() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	_ = c.call(shutdownCtx, "shutdown", map[string]any{}, nil)
	_ = c.notify("exit", map[string]any{})
	_ = c.stdin.Close()
	if c.cmd.Process != nil {
		_ = c.cmd.Process.Kill()
	}
	_ = c.cmd.Wait()
	return nil
}

// parseResponseID reads the numeric or string form of a JSON-RPC id. A null id
// belongs to a server-side error that matches no outstanding request, so it is
// rejected rather than decoded as zero.
func parseResponseID(raw json.RawMessage) (int64, bool) {
	if len(raw) == 0 || string(raw) == "null" {
		return 0, false
	}
	var numeric int64
	if err := json.Unmarshal(raw, &numeric); err == nil {
		return numeric, true
	}
	var text string
	if err := json.Unmarshal(raw, &text); err != nil {
		return 0, false
	}
	parsed, err := strconv.ParseInt(text, 10, 64)
	if err != nil {
		return 0, false
	}
	return parsed, true
}
