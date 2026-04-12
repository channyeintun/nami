package api

import (
	"context"
	"errors"
	"math"
	"time"
)

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
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	switch apiErr.Type {
	case ErrRateLimit, ErrOverloaded, ErrNetwork:
		return true
	default:
		return false
	}
}

// BackoffDelay calculates exponential backoff delay for attempt n (0-indexed).
func BackoffDelay(policy RetryPolicy, attempt int) time.Duration {
	delay := time.Duration(float64(policy.BaseDelay) * math.Pow(2, float64(attempt)))
	if delay > policy.MaxDelay {
		delay = policy.MaxDelay
	}
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
			var apiErr *APIError
			if errors.As(lastErr, &apiErr) && apiErr.RetryAfter > 0 {
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
