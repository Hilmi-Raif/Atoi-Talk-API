package adapter

import (
	"AtoiTalkAPI/internal/config"
	"bytes"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

func TestCaptchaAdapterVerifyRejectsOversizedResponse(t *testing.T) {
	httpClient := &http.Client{
		Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(strings.Repeat("a", maxCaptchaResponseBytes+1))),
				Header:     make(http.Header),
			}, nil
		}),
	}
	adapter := NewCaptchaAdapter(&config.AppConfig{TurnstileSecretKey: "secret"}, httpClient)

	err := adapter.Verify("token", "127.0.0.1")
	if err == nil || !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("expected oversized response error, got %v", err)
	}
}

func TestCaptchaAdapterVerifyAcceptsSuccessResponse(t *testing.T) {
	adapter := newCaptchaAdapterWithResponse(`{"success":true}`)
	if err := adapter.Verify("token", "127.0.0.1"); err != nil {
		t.Fatalf("expected successful verification, got %v", err)
	}
}

func TestCaptchaAdapterVerifyRejectsFailedVerification(t *testing.T) {
	adapter := newCaptchaAdapterWithResponse(`{"success":false,"error-codes":["invalid-input-response"]}`)
	err := adapter.Verify("token", "127.0.0.1")
	if err == nil || !strings.Contains(err.Error(), "invalid-input-response") {
		t.Fatalf("expected provider error, got %v", err)
	}
}

func TestCaptchaAdapterVerifyRejectsMalformedResponse(t *testing.T) {
	adapter := newCaptchaAdapterWithResponse("not-json")
	err := adapter.Verify("token", "127.0.0.1")
	if err == nil || !strings.Contains(err.Error(), "failed to unmarshal") {
		t.Fatalf("expected JSON error, got %v", err)
	}
}

func TestCaptchaAdapterVerifyRejectsProviderStatus(t *testing.T) {
	adapter := &CaptchaAdapter{
		cfg: &config.AppConfig{TurnstileSecretKey: "secret"},
		httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader("bad gateway")),
				Header:     make(http.Header),
			}, nil
		})},
	}
	err := adapter.Verify("token", "127.0.0.1")
	if err == nil || !strings.Contains(err.Error(), "status: 502") {
		t.Fatalf("expected provider status error, got %v", err)
	}
}

func TestCaptchaAdapterVerifyRetriesRetryableStatus(t *testing.T) {
	attempts := 0
	adapter := &CaptchaAdapter{
		cfg: &config.AppConfig{TurnstileSecretKey: "secret"},
		httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			attempts++
			if attempts == 1 {
				return &http.Response{
					StatusCode: http.StatusBadGateway,
					Body:       io.NopCloser(strings.NewReader("temporary failure")),
					Header:     make(http.Header),
				}, nil
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"success":true}`)),
				Header:     make(http.Header),
			}, nil
		})},
	}

	if err := adapter.Verify("token", "127.0.0.1"); err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected two verification attempts, got %d", attempts)
	}
}

func TestCaptchaAdapterVerifyReturnsTransportError(t *testing.T) {
	adapter := &CaptchaAdapter{
		cfg: &config.AppConfig{TurnstileSecretKey: "secret"},
		httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return nil, errors.New("network unavailable")
		})},
	}
	err := adapter.Verify("token", "127.0.0.1")
	if err == nil || !strings.Contains(err.Error(), "network unavailable") {
		t.Fatalf("expected transport error, got %v", err)
	}
}

func TestCaptchaAdapterVerifyRejectsNonRetryableStatus(t *testing.T) {
	adapter := &CaptchaAdapter{
		cfg: &config.AppConfig{TurnstileSecretKey: "secret"},
		httpClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadRequest,
				Body:       io.NopCloser(strings.NewReader("bad request")),
				Header:     make(http.Header),
			}, nil
		})},
	}
	err := adapter.Verify("token", "127.0.0.1")
	if err == nil || !strings.Contains(err.Error(), "status: 400") {
		t.Fatalf("expected 400 status error, got %v", err)
	}
}

func newCaptchaAdapterWithResponse(response string) *CaptchaAdapter {
	return &CaptchaAdapter{
		cfg: &config.AppConfig{TurnstileSecretKey: "secret"},
		httpClient: &http.Client{Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
			if req.Header.Get("Content-Type") != "application/json" {
				return nil, errors.New("unexpected request content type")
			}
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(bytes.NewBufferString(response)),
				Header:     make(http.Header),
			}, nil
		})},
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
