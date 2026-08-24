package helper

import (
	"errors"
	"net/http"
	"testing"
)

func TestRetryWithBackoffStopsAndReturnsFinalError(t *testing.T) {
	called := 0
	wantErr := errors.New("failed")
	_, err := RetryWithBackoff(func() (string, bool, error) {
		called++
		return "", true, wantErr
	}, 0, 0)
	if called != 1 || !errors.Is(err, wantErr) {
		t.Fatalf("expected one attempt and wrapped error, calls=%d err=%v", called, err)
	}

	called = 0
	result, err := RetryWithBackoff(func() (string, bool, error) {
		called++
		return "", false, wantErr
	}, 3, 0)
	if called != 1 || result != "" || !errors.Is(err, wantErr) {
		t.Fatalf("expected non-retryable error, calls=%d result=%q err=%v", called, result, err)
	}

	result, err = RetryWithBackoff(func() (string, bool, error) {
		return "ok", false, nil
	}, 3, 0)
	if err != nil || result != "ok" {
		t.Fatalf("expected success, result=%q err=%v", result, err)
	}
}

func TestRetryWithBackoffRetriesThenSucceeds(t *testing.T) {
	attempts := 0
	result, err := RetryWithBackoff(func() (string, bool, error) {
		attempts++
		if attempts == 1 {
			return "", true, errors.New("temporary")
		}
		return "ok", false, nil
	}, 2, 0)
	if err != nil || result != "ok" || attempts != 2 {
		t.Fatalf("expected retry success, attempts=%d result=%q err=%v", attempts, result, err)
	}
}

func TestShouldRetryHTTP(t *testing.T) {
	for _, tt := range []struct {
		name string
		resp *http.Response
		err  error
		want bool
	}{
		{"transport error", nil, errors.New("network"), true},
		{"nil response", nil, nil, true},
		{"server error", &http.Response{StatusCode: http.StatusBadGateway}, nil, true},
		{"rate limited", &http.Response{StatusCode: http.StatusTooManyRequests}, nil, true},
		{"client error", &http.Response{StatusCode: http.StatusBadRequest}, nil, false},
		{"success", &http.Response{StatusCode: http.StatusOK}, nil, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldRetryHTTP(tt.resp, tt.err); got != tt.want {
				t.Fatalf("expected %v, got %v", tt.want, got)
			}
		})
	}
}
