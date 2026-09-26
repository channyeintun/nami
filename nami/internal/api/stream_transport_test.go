package api

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestStreamingClientReadsStreamsLongerThanTheIdleTimeout(t *testing.T) {
	const chunks = 10
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		for i := range chunks {
			fmt.Fprintf(w, "data: %d\n\n", i)
			w.(http.Flusher).Flush()
			time.Sleep(20 * time.Millisecond)
		}
	}))
	defer server.Close()

	// The whole response takes about twice the idle timeout, but no gap
	// between chunks comes close to it, so the stream must be read in full.
	client := &http.Client{Transport: newIdleTimeoutTransport(http.DefaultTransport, 100*time.Millisecond)}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if got := strings.Count(string(body), "data:"); got != chunks {
		t.Fatalf("read %d chunks, want %d", got, chunks)
	}
}

// stallingServer answers with body, if any, and then holds the response open
// until the client gives up or the test ends.
func stallingServer(t *testing.T, body string) *httptest.Server {
	t.Helper()
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if body != "" {
			io.WriteString(w, body)
			w.(http.Flusher).Flush()
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	// Cleanups run last in, first out: the handler is released before the
	// server waits for it to finish.
	t.Cleanup(server.Close)
	t.Cleanup(func() { close(release) })
	return server
}

func TestStreamingClientAbandonsAStalledStream(t *testing.T) {
	server := stallingServer(t, "data: first\n\n")

	client := &http.Client{Transport: newIdleTimeoutTransport(http.DefaultTransport, 50*time.Millisecond)}
	resp, err := client.Get(server.URL)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	defer resp.Body.Close()

	done := make(chan error, 1)
	go func() {
		_, err := io.ReadAll(resp.Body)
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errStreamIdle) {
			t.Fatalf("read error = %v, want the idle timeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a stalled stream was never abandoned")
	}
}

func TestStreamingClientAbandonsARequestThatNeverAnswers(t *testing.T) {
	server := stallingServer(t, "")

	// Everything before the response headers, from connecting through
	// sending the request to the provider's first byte, counts against the
	// same limit.
	client := &http.Client{Transport: newIdleTimeoutTransport(http.DefaultTransport, 50*time.Millisecond)}
	done := make(chan error, 1)
	go func() {
		resp, err := client.Get(server.URL)
		if err == nil {
			resp.Body.Close()
		}
		done <- err
	}()
	select {
	case err := <-done:
		if !errors.Is(err, errStreamIdle) {
			t.Fatalf("Get error = %v, want the idle timeout", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("a request that never answered was never abandoned")
	}
}

func TestStreamingClientLeavesCallerCancellationAlone(t *testing.T) {
	server := stallingServer(t, "data: first\n\n")

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	client := &http.Client{Transport: newIdleTimeoutTransport(http.DefaultTransport, time.Minute)}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	cancel()
	// A cancelled request is not an idle stream and must not be reported as one.
	if _, err := io.ReadAll(resp.Body); err == nil || errors.Is(err, errStreamIdle) {
		t.Fatalf("read error = %v, want the caller's cancellation", err)
	}
}

func TestStreamingHTTPClientHasNoOverallDeadline(t *testing.T) {
	// Client.Timeout also bounds reading the body, which would cut off any
	// response still streaming when it expired.
	if timeout := newStreamingHTTPClient().Timeout; timeout != 0 {
		t.Fatalf("streaming client Timeout = %v, want none", timeout)
	}
}
