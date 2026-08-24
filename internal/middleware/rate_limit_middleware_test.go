package middleware

import (
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	repositorymocks "AtoiTalkAPI/internal/repository/mocks"
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

func TestGetIPUntrustedRemote(t *testing.T) {
	m := &RateLimitMiddleware{
		trustedProxyCIDRs: parseTrustedProxyCIDRs([]string{"10.0.0.0/8"}),
	}

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.20:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.1")

	got := m.getIP(req)
	want := "198.51.100.20"
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestLimitAllowsRequestAndSetsFingerprint(t *testing.T) {
	fake := repositorymocks.NewMockRateLimiter(t)
	fake.EXPECT().Allow(mock.Anything, "ratelimit:ip:login:198.51.100.20", 3, time.Minute).Return(true, 10*time.Second, nil)
	middleware := (&RateLimitMiddleware{repo: fake}).Limit("login", 3, time.Minute)
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := helper.ClientFingerprintFromContext(r.Context()); got != "198.51.100.20" {
			t.Fatalf("expected client fingerprint, got %v", got)
		}
		w.WriteHeader(http.StatusNoContent)
	})
	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.20:1234"
	middleware(next).ServeHTTP(recorder, req)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("unexpected allowed request: code=%d", recorder.Code)
	}
}

func TestLimitUsesUserIdentifierAndRejectsExceededRequest(t *testing.T) {
	userID := uuid.New()
	fake := repositorymocks.NewMockRateLimiter(t)
	fake.EXPECT().Allow(mock.Anything, "ratelimit:user:message:"+userID.String(), 2, time.Minute).Return(false, 1500*time.Millisecond, nil)
	handler := (&RateLimitMiddleware{repo: fake}).Limit("message", 2, time.Minute)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))
	req := httptest.NewRequest(http.MethodPost, "/", nil)
	req = req.WithContext(context.WithValue(req.Context(), UserContextKey, &model.UserDTO{ID: userID}))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, req)
	if recorder.Code != http.StatusTooManyRequests || recorder.Header().Get("Retry-After") != "2" {
		t.Fatalf("unexpected rate limit response: code=%d retry=%q", recorder.Code, recorder.Header().Get("Retry-After"))
	}
}

func TestLimitReturnsServiceUnavailableWhenStoreFails(t *testing.T) {
	fake := repositorymocks.NewMockRateLimiter(t)
	fake.EXPECT().Allow(mock.Anything, "ratelimit:ip:login:198.51.100.20", 3, time.Minute).Return(false, 0, errors.New("redis unavailable"))
	handler := (&RateLimitMiddleware{repo: fake}).Limit("login", 3, time.Minute)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.RemoteAddr = "198.51.100.20:1234"
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected service unavailable response, got %d", recorder.Code)
	}
}

func TestGetIPTrustedProxyUsesRightMostUntrusted(t *testing.T) {
	m := &RateLimitMiddleware{
		trustedProxyCIDRs: parseTrustedProxyCIDRs([]string{"10.0.0.0/8"}),
	}

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"

	req.Header.Set("X-Forwarded-For", "1.1.1.1, 2.2.2.2, 198.51.100.10")

	got := m.getIP(req)
	want := "198.51.100.10"
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestGetIPTrustedProxySkipsTrustedChain(t *testing.T) {
	m := &RateLimitMiddleware{
		trustedProxyCIDRs: parseTrustedProxyCIDRs([]string{"10.0.0.0/8"}),
	}

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Forwarded-For", "203.0.113.10, 10.1.1.1")

	got := m.getIP(req)
	want := "203.0.113.10"
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestGetIPTrustedProxyFallbackToXRealIP(t *testing.T) {
	m := &RateLimitMiddleware{
		trustedProxyCIDRs: parseTrustedProxyCIDRs([]string{"10.0.0.0/8"}),
	}

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Real-IP", "198.51.100.11")

	got := m.getIP(req)
	want := "198.51.100.11"
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestGetIPTrustedProxyIgnoresInvalidXRealIP(t *testing.T) {
	m := &RateLimitMiddleware{
		trustedProxyCIDRs: parseTrustedProxyCIDRs([]string{"10.0.0.0/8"}),
	}

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "10.0.0.1:1234"
	req.Header.Set("X-Real-IP", "not-an-ip")

	if got := m.getIP(req); got != "10.0.0.1" {
		t.Fatalf("expected remote proxy address fallback, got %s", got)
	}
}

func TestParseTrustedProxyCIDRsSkipsInvalidEntries(t *testing.T) {
	networks := parseTrustedProxyCIDRs([]string{"not-a-cidr", "10.0.0.0/8"})
	if len(networks) != 1 || !networks[0].Contains(net.ParseIP("10.1.2.3")) {
		t.Fatalf("expected only valid CIDR to remain: %+v", networks)
	}
}
