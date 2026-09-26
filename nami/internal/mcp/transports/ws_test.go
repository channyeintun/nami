package transports

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// serveWSMessages starts a WebSocket server that sends each message in turn.
func serveWSMessages(t *testing.T, messages ...string) string {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer conn.CloseNow()
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		for _, message := range messages {
			if err := conn.Write(ctx, websocket.MessageText, []byte(message)); err != nil {
				return
			}
		}
		// Hold the connection open until the client goes away.
		_, _, _ = conn.Read(ctx)
	}))
	t.Cleanup(server.Close)
	return "ws" + strings.TrimPrefix(server.URL, "http")
}

func jsonRPCResultOfSize(id int, size int) string {
	return fmt.Sprintf(`{"jsonrpc":"2.0","id":%d,"result":{"text":"%s"}}`, id, strings.Repeat("x", size))
}

// Tool results routinely exceed the websocket library's 32 KiB default, so the
// transport must accept large messages, but not unboundedly large ones.
func TestWSReadAcceptsLargeMessagesButNotUnboundedOnes(t *testing.T) {
	endpoint := serveWSMessages(t,
		jsonRPCResultOfSize(1, 1<<20),
		jsonRPCResultOfSize(2, maxWSMessageBytes),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	conn, err := NewWS(Config{URL: endpoint}).Connect(ctx)
	if err != nil {
		t.Fatalf("Connect: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Read(ctx); err != nil {
		t.Fatalf("Read of a 1 MiB message: %v", err)
	}
	if _, err := conn.Read(ctx); err == nil {
		t.Fatal("Read accepted a message over the size limit")
	}
}
