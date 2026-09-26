package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// serveStream starts a test server that answers every request with body as a
// streamed response, and points the provider clients at it without the
// custom base URL warning.
func serveStream(t *testing.T, body string) *httptest.Server {
	t.Helper()
	t.Setenv("NAMI_ALLOW_CUSTOM_BASE_URL", "1")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(server.Close)
	return server
}

// drainStream runs one request and collects every event, failing the test on
// any error.
func drainStream(t *testing.T, client LLMClient, req ModelRequest) []ModelEvent {
	t.Helper()
	stream, err := client.Stream(context.Background(), req)
	if err != nil {
		t.Fatalf("Stream: %v", err)
	}
	var events []ModelEvent
	for event, err := range stream {
		if err != nil {
			t.Fatalf("stream error after %d events: %v", len(events), err)
		}
		events = append(events, event)
	}
	return events
}

// usageEvents returns the usage reported by a stream, in order.
func usageEvents(events []ModelEvent) []Usage {
	var usages []Usage
	for _, event := range events {
		if event.Type == ModelEventUsage && event.Usage != nil {
			usages = append(usages, *event.Usage)
		}
	}
	return usages
}

// eventTypes lists the event types of a stream, in order.
func eventTypes(events []ModelEvent) []ModelEventType {
	types := make([]ModelEventType, 0, len(events))
	for _, event := range events {
		types = append(types, event.Type)
	}
	return types
}
