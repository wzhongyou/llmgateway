package core

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"
)

const (
	retryMaxAttempts = 2
	retryBaseDelayMs = 100
	retryMaxDelayMs  = 2000
)

// retryCall calls fn up to retryMaxAttempts times for retryable errors,
// using exponential backoff with jitter between attempts.
func retryCall(ctx context.Context, fn func() error) error {
	var lastErr error
	for attempt := range retryMaxAttempts {
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		var pe *ProviderError
		if !errors.As(lastErr, &pe) || !pe.Retryable {
			return lastErr
		}
		if attempt == retryMaxAttempts-1 {
			break
		}
		delay := retryBaseDelayMs * (1 << attempt)
		if delay > retryMaxDelayMs {
			delay = retryMaxDelayMs
		}
		jitter := rand.IntN(delay/2 + 1)
		select {
		case <-time.After(time.Duration(delay+jitter) * time.Millisecond):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return lastErr
}
