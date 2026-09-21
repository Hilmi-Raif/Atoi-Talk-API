package middleware

import (
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/domain/model"
	repositorymocks "AtoiTalkAPI/internal/infrastructure/database/repository/mocks"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

func TestGetIPUntrustedRemote(t *testing.T) {
	m := &RateLimitMiddleware{}

	req, _ := http.NewRequest(http.MethodGet, "/", nil)
	req.RemoteAddr = "198.51.100.20:1234"

	got := m.getIP(req)
	want := "198.51.100.20"
	if got != want {
		t.Fatalf("expected %s, got %s", want, got)
	}
}

func TestLimitDisabledSkipsStoreAndCallsNext(t *testing.T) {
	fake := repositorymocks.NewMockRateLimiter(t)
	handler := (&RateLimitMiddleware{repo: fake, enabled: false}).Limit("login", 1, time.Minute)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected disabled middleware to call next, got %d", recorder.Code)
	}
}

func TestLimitAllowsRequestAndSetsFingerprint(t *testing.T) {
	fake := repositorymocks.NewMockRateLimiter(t)
	fake.EXPECT().Allow(mock.Anything, "ratelimit:ip:login:198.51.100.20", 3, time.Minute).Return(true, 10*time.Second, nil)
	middleware := (&RateLimitMiddleware{repo: fake, enabled: true}).Limit("login", 3, time.Minute)
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
	handler := (&RateLimitMiddleware{repo: fake, enabled: true}).Limit("message", 2, time.Minute)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
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
	handler := (&RateLimitMiddleware{repo: fake, enabled: true}).Limit("login", 3, time.Minute)(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
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
