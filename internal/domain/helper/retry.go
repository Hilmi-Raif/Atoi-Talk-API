package helper

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"
)

type RetryableFunc[T any] func() (T, bool, error)

func RetryWithBackoff[T any](operation RetryableFunc[T], maxRetries int, baseDelay time.Duration) (T, error) {
	return RetryWithBackoffContext(context.Background(), operation, maxRetries, baseDelay)
}

func RetryWithBackoffContext[T any](ctx context.Context, operation RetryableFunc[T], maxRetries int, baseDelay time.Duration) (T, error) {
	var err error
	var result T
	var shouldRetry bool

	for i := 0; i <= maxRetries; i++ {
		if err := ctx.Err(); err != nil {
			return result, err
		}

		result, shouldRetry, err = operation()

		if err == nil {
			return result, nil
		}

		if !shouldRetry {
			return result, err
		}

		if i == maxRetries {
			break
		}

		delay := BackoffDelay(i, baseDelay, 0)
		slog.Warn("Operation failed, retrying...", "attempt", i+1, "delay", delay, "error", err)
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return result, ctx.Err()
		case <-timer.C:
		}
	}

	return result, fmt.Errorf("operation failed after %d attempts: %w", maxRetries+1, err)
}

func BackoffDelay(attempt int, baseDelay, maxDelay time.Duration) time.Duration {
	if attempt < 0 {
		attempt = 0
	}

	delay := baseDelay
	for range attempt {
		if maxDelay > 0 && delay >= maxDelay {
			return maxDelay
		}
		if delay > time.Duration(1<<63-1)/2 {
			if maxDelay > 0 {
				return maxDelay
			}
			return time.Duration(1<<63 - 1)
		}
		delay *= 2
	}
	if maxDelay > 0 && delay > maxDelay {
		return maxDelay
	}
	return delay
}

func ShouldRetryHTTP(resp *http.Response, err error) bool {
	if err != nil {
		return true
	}
	if resp == nil {
		return true
	}

	return resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests
}
