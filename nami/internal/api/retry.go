package api

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// maxRetryAfter caps how long a server-requested retry delay is honoured; a
// longer one falls back to the normal backoff rather than stalling the turn.
const maxRetryAfter = 60 * time.Second

// APIErrorType classifies API errors for retry decisions.
type APIErrorType int

const (
	ErrUnknown       APIErrorType = iota
	ErrPromptTooLong              // trigger compaction
	ErrRateLimit                  // exponential backoff
	ErrOverloaded                 // retry with delay
	ErrMaxTokens                  // output truncated
	ErrAuth                       // do not retry
	ErrNetwork                    // retry
)

// APIError wraps an API error with classification.
type APIError struct {
	Type       APIErrorType
	StatusCode int
	Message    string
	RetryAfter time.Duration // server-specified retry delay (0 = use backoff)
	Err        error
}

func (e *APIError) Error() string {
	return e.Message
}

func (e *APIError) Unwrap() error {
	return e.Err
}

// networkError reports a failed exchange with a provider. Error() shows only
// the message, so the cause goes into it as well as into Err.
func networkError(action string, err error) *APIError {
	return &APIError{Type: ErrNetwork, Message: fmt.Sprintf("%s: %v", action, err), Err: err}
}

// incompleteStreamError reports a stream that ended cleanly before the final
// event every complete response carries, so the response was cut short.
func incompleteStreamError(provider, finalEvent string) *APIError {
	return &APIError{
		Type:    ErrNetwork,
		Message: fmt.Sprintf("%s stream ended before %s; the response is incomplete", provider, finalEvent),
	}
}

// RetryPolicy defines retry behavior per error class.
type RetryPolicy struct {
	MaxAttempts int
	BaseDelay   time.Duration
	MaxDelay    time.Duration
}

// DefaultRetryPolicy returns the standard retry policy.
func DefaultRetryPolicy() RetryPolicy {
	return RetryPolicy{
		MaxAttempts: 3,
		BaseDelay:   1 * time.Second,
		MaxDelay:    16 * time.Second,
	}
}

// ShouldRetry returns whether an error is retryable.
func ShouldRetry(err error) bool {
	apiErr, ok := errors.AsType[*APIError](err)
	if !ok {
		return false
	}
	switch apiErr.Type {
	case ErrRateLimit, ErrOverloaded, ErrNetwork:
		return true
	default:
		return false
	}
}

// retryAfterHeaderDelay reads the retry delay a server asked for, from the
// retry-after-ms header OpenAI sends or from Retry-After, which holds either
// seconds or an HTTP date. It returns 0 when there is none.
func retryAfterHeaderDelay(header http.Header) time.Duration {
	if value := strings.TrimSpace(header.Get("Retry-After-Ms")); value != "" {
		if millis, err := strconv.ParseFloat(value, 64); err == nil && millis > 0 {
			return time.Duration(millis * float64(time.Millisecond))
		}
	}
	value := strings.TrimSpace(header.Get("Retry-After"))
	if value == "" {
		return 0
	}
	if seconds, err := strconv.ParseFloat(value, 64); err == nil {
		if seconds <= 0 {
			return 0
		}
		return time.Duration(seconds * float64(time.Second))
	}
	if at, err := http.ParseTime(value); err == nil {
		return max(time.Until(at), 0)
	}
	return 0
}

// withRetryAfter records on err the retry delay the server asked for, so that
// RetryWithBackoff waits that long instead of its own backoff. A delay past
// maxRetryAfter is not recorded.
func withRetryAfter(err error, delay time.Duration) error {
	apiErr, ok := errors.AsType[*APIError](err)
	if !ok || delay <= 0 || delay > maxRetryAfter {
		return err
	}
	apiErr.RetryAfter = delay
	return err
}

// BackoffDelay calculates exponential backoff delay for attempt n (0-indexed).
func BackoffDelay(policy RetryPolicy, attempt int) time.Duration {
	delay := min(time.Duration(float64(policy.BaseDelay)*math.Pow(2, float64(attempt))), policy.MaxDelay)
	return delay
}

// RetryWithBackoff executes fn with exponential backoff on retryable errors.
func RetryWithBackoff(ctx context.Context, policy RetryPolicy, fn func() error) error {
	var lastErr error
	for attempt := range policy.MaxAttempts {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		if !ShouldRetry(lastErr) {
			return lastErr
		}
		if attempt < policy.MaxAttempts-1 {
			delay := BackoffDelay(policy, attempt)
			// Prefer server-specified retry delay when present.
			if apiErr, ok := errors.AsType[*APIError](lastErr); ok && apiErr.RetryAfter > 0 {
				delay = apiErr.RetryAfter
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}
	}
	return lastErr
}
