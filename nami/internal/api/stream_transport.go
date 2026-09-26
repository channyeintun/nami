package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

// streamIdleTimeout bounds how long a streaming request may go without
// progress: from being sent to its response headers, and between two reads of
// its response body.
const streamIdleTimeout = 5 * time.Minute

// errStreamIdle marks a request that was abandoned because it stopped
// progressing.
var errStreamIdle = errors.New("stream went idle")

// streamingTransport is shared by every streaming client. It wraps
// http.DefaultTransport, which they used before, so they keep pooling
// connections through it.
var streamingTransport = newIdleTimeoutTransport(http.DefaultTransport, streamIdleTimeout)

// newStreamingHTTPClient returns the HTTP client for streamed model responses.
// It deliberately sets no Client.Timeout: that deadline also covers reading
// the body, so it cut off any response still streaming when it expired, and a
// long answer easily streams for more than five minutes. The transport
// abandons a request only once it stops making progress.
func newStreamingHTTPClient() *http.Client {
	return &http.Client{Transport: streamingTransport}
}

// idleTimeoutTransport cancels a request that makes no progress for
// idleTimeout: one that gets no response headers within idleTimeout of being
// sent, which covers connecting, uploading and the provider's time to first
// byte, or whose response body then goes quiet for that long. A stalled
// request fails instead of hanging, while a stream that keeps producing runs
// for as long as it needs.
type idleTimeoutTransport struct {
	base        http.RoundTripper
	idleTimeout time.Duration
}

func newIdleTimeoutTransport(base http.RoundTripper, idleTimeout time.Duration) *idleTimeoutTransport {
	return &idleTimeoutTransport{base: base, idleTimeout: idleTimeout}
}

func (t *idleTimeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(req.Context())
	timer := time.AfterFunc(t.idleTimeout, func() { cancel(errStreamIdle) })
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		timer.Stop()
		err = idleError(ctx, t.idleTimeout, err)
		cancel(nil)
		return nil, err
	}
	timer.Reset(t.idleTimeout)
	resp.Body = &idleTimeoutBody{ctx: ctx, cancel: cancel, body: resp.Body, timeout: t.idleTimeout, timer: timer}
	return resp, nil
}

// idleError explains err when the request was cancelled for going idle, and
// returns it unchanged otherwise.
func idleError(ctx context.Context, timeout time.Duration, err error) error {
	if !errors.Is(context.Cause(ctx), errStreamIdle) {
		return err
	}
	return fmt.Errorf("no data received for %s: %w", timeout, errStreamIdle)
}

// idleTimeoutBody restarts the request's idle timer on every read that
// returns data.
type idleTimeoutBody struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	body    io.ReadCloser
	timeout time.Duration
	timer   *time.Timer
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.timer.Reset(b.timeout)
	}
	if err != nil && err != io.EOF {
		err = idleError(b.ctx, b.timeout, err)
	}
	return n, err
}

func (b *idleTimeoutBody) Close() error {
	b.timer.Stop()
	err := b.body.Close()
	b.cancel(nil)
	return err
}
