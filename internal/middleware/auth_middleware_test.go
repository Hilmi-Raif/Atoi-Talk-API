package middleware

import (
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	repositorymocks "AtoiTalkAPI/internal/repository/mocks"
	servicemocks "AtoiTalkAPI/internal/service/mocks"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

func TestAdminOnlyRejectsMissingUser(t *testing.T) {
	handler := (&AuthMiddleware{}).AdminOnly(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))

	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized response, got %d", recorder.Code)
	}
}

func TestAdminOnlyRejectsNonAdminUser(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(context.WithValue(request.Context(), UserContextKey, &model.UserDTO{
		ID:   uuid.New(),
		Role: string(user.RoleUser),
	}))
	handler := (&AuthMiddleware{}).AdminOnly(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if recorder.Code != http.StatusForbidden {
		t.Fatalf("expected forbidden response, got %d", recorder.Code)
	}
}

func TestAdminOnlyAllowsAdminUser(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request = request.WithContext(context.WithValue(request.Context(), UserContextKey, &model.UserDTO{
		ID:   uuid.New(),
		Role: string(user.RoleAdmin),
	}))
	called := false
	handler := (&AuthMiddleware{}).AdminOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusNoContent)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)

	if !called || recorder.Code != http.StatusNoContent {
		t.Fatalf("expected admin request to continue, called=%v status=%d", called, recorder.Code)
	}
}

func TestVerifyTokenRejectsMissingAndMalformedHeaders(t *testing.T) {
	middleware := (&AuthMiddleware{}).VerifyToken(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	for _, header := range []string{"", "Basic token", "Bearer", "Bearer token extra"} {
		t.Run(header, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Authorization", header)
			middleware.ServeHTTP(recorder, request)
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("expected unauthorized for %q, got %d", header, recorder.Code)
			}
		})
	}
}

func TestVerifyTokenHandlesBlacklistAndVerificationErrors(t *testing.T) {
	tests := []struct {
		name       string
		setup      func(*servicemocks.MockAuthVerifier, *repositorymocks.MockTokenBlacklistStore)
		expectCode int
	}{
		{
			name: "blacklist error",
			setup: func(auth *servicemocks.MockAuthVerifier, session *repositorymocks.MockTokenBlacklistStore) {
				session.EXPECT().IsTokenBlacklisted(mock.Anything, "token").Return(false, errors.New("redis down"))
			},
			expectCode: http.StatusServiceUnavailable,
		},
		{
			name: "blacklisted",
			setup: func(auth *servicemocks.MockAuthVerifier, session *repositorymocks.MockTokenBlacklistStore) {
				session.EXPECT().IsTokenBlacklisted(mock.Anything, "token").Return(true, nil)
			},
			expectCode: http.StatusUnauthorized,
		},
		{
			name: "invalid user",
			setup: func(auth *servicemocks.MockAuthVerifier, session *repositorymocks.MockTokenBlacklistStore) {
				session.EXPECT().IsTokenBlacklisted(mock.Anything, "token").Return(false, nil)
				auth.EXPECT().VerifyUser(mock.Anything, "token").Return(nil, helper.NewUnauthorizedError(""))
			},
			expectCode: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			auth := servicemocks.NewMockAuthVerifier(t)
			session := repositorymocks.NewMockTokenBlacklistStore(t)
			tt.setup(auth, session)
			middleware := (&AuthMiddleware{
				authService: auth,
				sessionRepo: session,
			}).VerifyToken(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
				t.Fatal("next handler should not be called")
			}))
			recorder := httptest.NewRecorder()
			request := httptest.NewRequest(http.MethodGet, "/", nil)
			request.Header.Set("Authorization", "Bearer token")
			middleware.ServeHTTP(recorder, request)
			if recorder.Code != tt.expectCode {
				t.Fatalf("expected status %d, got %d", tt.expectCode, recorder.Code)
			}
		})
	}
}

func TestVerifyTokenStoresUserAndTokenContext(t *testing.T) {
	userContext := &model.UserDTO{ID: uuid.New(), Role: string(user.RoleUser)}
	auth := servicemocks.NewMockAuthVerifier(t)
	session := repositorymocks.NewMockTokenBlacklistStore(t)
	session.EXPECT().IsTokenBlacklisted(mock.Anything, "token").Return(false, nil)
	auth.EXPECT().VerifyUser(mock.Anything, "token").Return(userContext, nil)
	middleware := (&AuthMiddleware{
		authService: auth,
		sessionRepo: session,
	}).VerifyToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got, ok := r.Context().Value(UserContextKey).(*model.UserDTO); !ok || got != userContext {
			t.Fatalf("unexpected user context: %#v", r.Context().Value(UserContextKey))
		}
		if got := r.Context().Value(TokenContextKey); got != "token" {
			t.Fatalf("unexpected token context: %#v", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer token")
	middleware.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected successful response, got %d", recorder.Code)
	}
}

func TestVerifyWSTokenRejectsMissingAndAcceptsValidToken(t *testing.T) {
	missing := (&AuthMiddleware{}).VerifyWSToken(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler should not be called")
	}))
	recorder := httptest.NewRecorder()
	missing.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("expected unauthorized websocket request, got %d", recorder.Code)
	}

	userContext := &model.UserDTO{ID: uuid.New(), Role: string(user.RoleUser)}
	auth := servicemocks.NewMockAuthVerifier(t)
	session := repositorymocks.NewMockTokenBlacklistStore(t)
	session.EXPECT().IsTokenBlacklisted(mock.Anything, "token").Return(false, nil)
	auth.EXPECT().VerifyUser(mock.Anything, "token").Return(userContext, nil)
	valid := (&AuthMiddleware{
		authService: auth,
		sessionRepo: session,
	}).VerifyWSToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Context().Value(UserContextKey); got != userContext {
			t.Fatalf("unexpected websocket user context: %#v", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	recorder = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/?token=token", nil)
	valid.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("expected successful websocket response, got %d", recorder.Code)
	}
}
