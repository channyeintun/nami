package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"
)

const (
	// streamResponseHeaderTimeout bounds how long a provider may take to start
	// answering a streaming request, up to its response headers.
	streamResponseHeaderTimeout = 5 * time.Minute

	// streamIdleTimeout bounds the silence between two reads of a streaming
	// response body.
	streamIdleTimeout = 5 * time.Minute
)

// errStreamIdle marks a stream that was abandoned because it stopped sending.
var errStreamIdle = errors.New("stream went idle")

// streamingTransport is shared by every streaming client so that they pool
// connections together, as they did on http.DefaultTransport.
var streamingTransport = newIdleTimeoutTransport(newStreamingBaseTransport(), streamIdleTimeout)

// newStreamingHTTPClient returns the HTTP client for streamed model responses.
// It deliberately sets no Client.Timeout: that deadline also covers reading
// the body, so it cut off any response still streaming when it expired, and a
// long answer easily streams for more than five minutes. The transport bounds
// the wait for the response headers and the silence between reads instead.
func newStreamingHTTPClient() *http.Client {
	return &http.Client{Transport: streamingTransport}
}

func newStreamingBaseTransport() *http.Transport {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = streamResponseHeaderTimeout
	return transport
}

// idleTimeoutTransport cancels a request once its response body has gone
// quiet for idleTimeout, so a stalled stream fails instead of hanging.
type idleTimeoutTransport struct {
	base        http.RoundTripper
	idleTimeout time.Duration
}

func newIdleTimeoutTransport(base http.RoundTripper, idleTimeout time.Duration) *idleTimeoutTransport {
	return &idleTimeoutTransport{base: base, idleTimeout: idleTimeout}
}

func (t *idleTimeoutTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx, cancel := context.WithCancelCause(req.Context())
	resp, err := t.base.RoundTrip(req.WithContext(ctx))
	if err != nil {
		cancel(nil)
		return nil, err
	}
	resp.Body = newIdleTimeoutBody(ctx, cancel, resp.Body, t.idleTimeout)
	return resp, nil
}

// idleTimeoutBody restarts its timer on every read that returns data and
// cancels the request when the timer runs out.
type idleTimeoutBody struct {
	ctx     context.Context
	cancel  context.CancelCauseFunc
	body    io.ReadCloser
	timeout time.Duration
	timer   *time.Timer
}

func newIdleTimeoutBody(ctx context.Context, cancel context.CancelCauseFunc, body io.ReadCloser, timeout time.Duration) *idleTimeoutBody {
	return &idleTimeoutBody{
		ctx:     ctx,
		cancel:  cancel,
		body:    body,
		timeout: timeout,
		timer:   time.AfterFunc(timeout, func() { cancel(errStreamIdle) }),
	}
}

func (b *idleTimeoutBody) Read(p []byte) (int, error) {
	n, err := b.body.Read(p)
	if n > 0 {
		b.timer.Reset(b.timeout)
	}
	if err != nil && err != io.EOF && errors.Is(context.Cause(b.ctx), errStreamIdle) {
		err = fmt.Errorf("no data received for %s: %w", b.timeout, errStreamIdle)
	}
	return n, err
}

func (b *idleTimeoutBody) Close() error {
	b.timer.Stop()
	err := b.body.Close()
	b.cancel(nil)
	return err
}
