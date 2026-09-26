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

func TestStreamingClientAbandonsAStalledStream(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

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

func TestStreamingClientLeavesCallerCancellationAlone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "data: first\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer server.Close()

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
