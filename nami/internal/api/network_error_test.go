package api

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// failingReader stands in for a response body whose connection broke.
type failingReader struct{ err error }

func (r failingReader) Read([]byte) (int, error) { return 0, r.err }

func TestReadSSEReportsWhyTheStreamBroke(t *testing.T) {
	cause := errors.New("connection reset by peer")
	err := readSSE(context.Background(), failingReader{err: cause}, func(string, string) error { return nil })

	apiErr, ok := errors.AsType[*APIError](err)
	if !ok || apiErr.Type != ErrNetwork {
		t.Fatalf("err = %#v, want a network APIError", err)
	}
	if !errors.Is(err, cause) {
		t.Fatalf("err = %v, want it to wrap the cause", err)
	}
	// Error() is what reaches the user, and readSSE serves every provider.
	if message := err.Error(); !strings.Contains(message, cause.Error()) || strings.Contains(message, "anthropic") {
		t.Fatalf("message = %q, want the cause and no provider name", message)
	}
}

func TestNetworkErrorKeepsItsCauseInTheMessage(t *testing.T) {
	cause := errors.New("dial tcp 127.0.0.1:11434: connect: connection refused")
	err := networkError("Ollama request failed", cause)

	if err.Type != ErrNetwork || !errors.Is(err, cause) {
		t.Fatalf("err = %#v, want a network APIError wrapping the cause", err)
	}
	// APIError.Error shows only the message, so the cause has to be in it.
	if want := "Ollama request failed: " + cause.Error(); err.Error() != want {
		t.Fatalf("message = %q, want %q", err.Error(), want)
	}
}
