package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"testing"
	"time"
)

func mkdirAll(path string) error {
	return os.MkdirAll(path, 0o755)
}

func writeFile(path, content string) error {
	return os.WriteFile(path, []byte(content), 0o644)
}

func framed(payload string) string {
	return fmt.Sprintf("Content-Length: %d\r\n\r\n%s", len(payload), payload)
}

func readerClient(stream string) *client {
	return &client{stdout: bufio.NewReader(strings.NewReader(stream))}
}

func TestReadMessageDecodesFramedPayload(t *testing.T) {
	c := readerClient(framed(`{"jsonrpc":"2.0","id":7,"result":{"ok":true}}`))
	envelope, err := c.readMessage()
	if err != nil {
		t.Fatalf("readMessage: %v", err)
	}
	id, ok := parseResponseID(envelope.ID)
	if !ok || id != 7 {
		t.Fatalf("id = %d (ok=%v), want 7", id, ok)
	}
	if string(envelope.Result) != `{"ok":true}` {
		t.Fatalf("result = %s", envelope.Result)
	}
}

func TestReadMessageIgnoresUnknownHeaders(t *testing.T) {
	payload := `{"jsonrpc":"2.0","id":1,"result":null}`
	stream := fmt.Sprintf("Content-Type: application/vscode-jsonrpc\r\nContent-Length: %d\r\nmalformed-header\r\n\r\n%s", len(payload), payload)
	if _, err := readerClient(stream).readMessage(); err != nil {
		t.Fatalf("readMessage: %v", err)
	}
}

func TestReadMessageRejectsMissingContentLength(t *testing.T) {
	if _, err := readerClient("Content-Type: text/plain\r\n\r\n{}").readMessage(); err == nil {
		t.Fatal("readMessage succeeded without Content-Length, want error")
	}
}

func TestReadMessageRejectsUnparsableContentLength(t *testing.T) {
	if _, err := readerClient("Content-Length: abc\r\n\r\n{}").readMessage(); err == nil {
		t.Fatal("readMessage succeeded with a non-numeric length, want error")
	}
}

// A server that advertises an enormous body must not make the engine allocate
// that much memory before it reads a single byte.
func TestReadMessageRejectsOversizedContentLength(t *testing.T) {
	stream := fmt.Sprintf("Content-Length: %d\r\n\r\n{}", maxMessageBytes+1)
	_, err := readerClient(stream).readMessage()
	if err == nil {
		t.Fatal("readMessage accepted an oversized message, want error")
	}
	if !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("error = %v, want a size-limit error", err)
	}
}

func TestReadMessageRejectsTruncatedBody(t *testing.T) {
	if _, err := readerClient("Content-Length: 64\r\n\r\n{}").readMessage(); err == nil {
		t.Fatal("readMessage accepted a truncated body, want error")
	}
}

func TestParseResponseID(t *testing.T) {
	cases := []struct {
		raw   string
		want  int64
		valid bool
	}{
		{`12`, 12, true},
		{`"34"`, 34, true},
		{`"abc"`, 0, false},
		{`null`, 0, false},
		{``, 0, false},
		{`{}`, 0, false},
	}
	for _, tc := range cases {
		got, ok := parseResponseID(json.RawMessage(tc.raw))
		if ok != tc.valid || got != tc.want {
			t.Errorf("parseResponseID(%q) = %d, %v; want %d, %v", tc.raw, got, ok, tc.want, tc.valid)
		}
	}
}

// pipeClient wires a client to an in-process fake server so request framing,
// id matching, and notification skipping can be exercised without a real LSP
// binary on PATH.
func pipeClient(t *testing.T) (*client, *client) {
	t.Helper()
	clientReader, serverWriter := io.Pipe()
	serverReader, clientWriter := io.Pipe()
	t.Cleanup(func() {
		_ = clientWriter.Close()
		_ = serverWriter.Close()
	})
	return &client{stdin: clientWriter, stdout: bufio.NewReader(clientReader)},
		&client{stdin: serverWriter, stdout: bufio.NewReader(serverReader)}
}

func TestCallMatchesResponseByID(t *testing.T) {
	c, server := pipeClient(t)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		request, err := server.readMessage()
		if err != nil {
			t.Errorf("server readMessage: %v", err)
			return
		}
		id, _ := parseResponseID(request.ID)
		// A notification and a stale response arrive first; both must be skipped.
		_ = server.notify("window/logMessage", map[string]any{"message": "starting"})
		_ = server.writeMessage(map[string]any{"jsonrpc": "2.0", "id": id + 99, "result": "stale"})
		_ = server.writeMessage(map[string]any{"jsonrpc": "2.0", "id": id, "result": map[string]any{"value": "fresh"}})
	}()

	var result map[string]string
	if err := c.call(context.Background(), "textDocument/hover", map[string]any{}, &result); err != nil {
		t.Fatalf("call: %v", err)
	}
	if result["value"] != "fresh" {
		t.Fatalf("result = %#v, want value=fresh", result)
	}
	wg.Wait()
}

func TestCallReturnsServerError(t *testing.T) {
	c, server := pipeClient(t)
	go func() {
		request, err := server.readMessage()
		if err != nil {
			return
		}
		id, _ := parseResponseID(request.ID)
		_ = server.writeMessage(map[string]any{
			"jsonrpc": "2.0",
			"id":      id,
			"error":   map[string]any{"code": -32601, "message": "method not found"},
		})
	}()

	err := c.call(context.Background(), "textDocument/hover", map[string]any{}, nil)
	if err == nil || !strings.Contains(err.Error(), "method not found") {
		t.Fatalf("call error = %v, want the server message", err)
	}
}

func TestCallStopsOnCancelledContext(t *testing.T) {
	c, server := pipeClient(t)
	go func() {
		_, _ = server.readMessage()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.call(ctx, "initialize", map[string]any{}, nil); err == nil {
		t.Fatal("call succeeded with a cancelled context, want error")
	}
}

func TestCallReportsServerStderr(t *testing.T) {
	c, server := pipeClient(t)
	_, _ = c.stderr.Write([]byte("gopls crashed"))
	go func() {
		_, _ = server.readMessage()
		_ = server.stdin.Close()
	}()

	err := c.call(context.Background(), "initialize", map[string]any{}, nil)
	if err == nil || !strings.Contains(err.Error(), "gopls crashed") {
		t.Fatalf("call error = %v, want the captured stderr", err)
	}
}

func TestNextRequestIDIsMonotonicUnderConcurrency(t *testing.T) {
	c := &client{}
	const workers = 32
	ids := make(chan int64, workers)
	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ids <- c.nextRequestID()
		}()
	}
	wg.Wait()
	close(ids)

	seen := make(map[int64]bool, workers)
	for id := range ids {
		if seen[id] {
			t.Fatalf("request id %d handed out twice", id)
		}
		seen[id] = true
	}
	if len(seen) != workers {
		t.Fatalf("unique ids = %d, want %d", len(seen), workers)
	}
}

func TestSyncBufferIsSafeForConcurrentUse(t *testing.T) {
	var buffer syncBuffer
	done := make(chan struct{})
	go func() {
		defer close(done)
		for range 200 {
			_, _ = buffer.Write([]byte("x"))
		}
	}()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case <-done:
			if got := len(buffer.String()); got != 200 {
				t.Fatalf("buffer length = %d, want 200", got)
			}
			return
		case <-deadline:
			t.Fatal("timed out waiting for concurrent writes")
		default:
			_ = buffer.String()
		}
	}
}

func TestRunRejectsInvalidRequestBeforeStartingServer(t *testing.T) {
	if _, err := Run(context.Background(), Request{Operation: OperationHover}); err == nil {
		t.Fatal("Run succeeded for an invalid request, want error")
	}
}
