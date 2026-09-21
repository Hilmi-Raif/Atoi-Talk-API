package helper

import (
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"time"
)

type RetryableFunc[T any] func() (T, bool, error)

func RetryWithBackoff[T any](operation RetryableFunc[T], maxRetries int, baseDelay time.Duration) (T, error) {
	var err error
	var result T
	var shouldRetry bool

	for i := 0; i <= maxRetries; i++ {
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

		delay := baseDelay * time.Duration(math.Pow(2, float64(i)))
		slog.Warn("Operation failed, retrying...", "attempt", i+1, "delay", delay, "error", err)
		time.Sleep(delay)
	}

	return result, fmt.Errorf("operation failed after %d attempts: %w", maxRetries+1, err)
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
