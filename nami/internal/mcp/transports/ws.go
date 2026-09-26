package transports

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/coder/websocket"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// maxWSMessageBytes bounds one inbound message. The library default of 32 KiB
// is far too small for real tool results, but no limit at all would let a
// server exhaust the engine's memory with a single message; this matches the
// bound the LSP transport uses.
const maxWSMessageBytes = 32 << 20

type wsTransport struct {
	endpoint string
	client   *websocket.DialOptions
}

type wsConn struct {
	conn      *websocket.Conn
	writeMu   sync.Mutex
	closeOnce sync.Once
	closeErr  error
}

func NewWS(definition Config) sdkmcp.Transport {
	return &wsTransport{
		endpoint: definition.URL,
		client: &websocket.DialOptions{
			HTTPClient: newHeaderHTTPClient(nil),
			HTTPHeader: cloneHeaders(definition.Headers),
		},
	}
}

func (t *wsTransport) Connect(ctx context.Context) (sdkmcp.Connection, error) {
	conn, _, err := websocket.Dial(ctx, t.endpoint, t.client)
	if err != nil {
		return nil, err
	}
	conn.SetReadLimit(maxWSMessageBytes)
	return &wsConn{conn: conn}, nil
}

func (c *wsConn) Read(ctx context.Context) (jsonrpc.Message, error) {
	_, data, err := c.conn.Read(ctx)
	if err != nil {
		c.shutdownNow()
		if isNormalWebsocketClosure(err) {
			return nil, io.EOF
		}
		return nil, err
	}
	message, err := jsonrpc.DecodeMessage(data)
	if err != nil {
		c.shutdownNow()
		return nil, fmt.Errorf("decode websocket message: %w", err)
	}
	return message, nil
}

func (c *wsConn) Write(ctx context.Context, msg jsonrpc.Message) error {
	data, err := jsonrpc.EncodeMessage(msg)
	if err != nil {
		return err
	}

	c.writeMu.Lock()
	defer c.writeMu.Unlock()

	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
		c.shutdownNow()
		return err
	}
	return nil
}

func (c *wsConn) Close() error {
	c.closeOnce.Do(func() {
		c.closeErr = c.conn.Close(websocket.StatusNormalClosure, "")
	})
	return c.closeErr
}

func (c *wsConn) SessionID() string {
	return ""
}

func (c *wsConn) shutdownNow() {
	c.closeOnce.Do(func() {
		c.closeErr = c.conn.CloseNow()
	})
}

func isNormalWebsocketClosure(err error) bool {
	status := websocket.CloseStatus(err)
	if status == -1 {
		return errors.Is(err, io.EOF)
	}
	return status == websocket.StatusNormalClosure || status == websocket.StatusGoingAway
}
