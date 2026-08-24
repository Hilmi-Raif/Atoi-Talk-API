package middleware

import (
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/repository"
	"AtoiTalkAPI/internal/service"
	"context"
	"net/http"
	"strings"
)

type contextKey string

const (
	UserContextKey  contextKey = "userContext"
	TokenContextKey contextKey = "tokenContext"
)

type AuthMiddleware struct {
	authService authVerifier
	sessionRepo sessionBlacklistStore
}

type authVerifier = service.AuthVerifier
type sessionBlacklistStore = repository.TokenBlacklistStore

func NewAuthMiddleware(authService authVerifier, sessionRepo sessionBlacklistStore) *AuthMiddleware {
	return &AuthMiddleware{
		authService: authService,
		sessionRepo: sessionRepo,
	}
}

func (m *AuthMiddleware) VerifyToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			helper.WriteError(w, helper.NewUnauthorizedError(""))
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			helper.WriteError(w, helper.NewUnauthorizedError(""))
			return
		}

		tokenString := parts[1]

		isBlacklisted, err := m.sessionRepo.IsTokenBlacklisted(r.Context(), tokenString)
		if err != nil {
			helper.WriteError(w, helper.NewServiceUnavailableError("Session service unavailable"))
			return
		}

		if isBlacklisted {
			helper.WriteError(w, helper.NewUnauthorizedError("Token has been revoked"))
			return
		}

		userContext, err := m.authService.VerifyUser(r.Context(), tokenString)
		if err != nil {
			helper.WriteError(w, err)
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, userContext)
		ctx = context.WithValue(ctx, TokenContextKey, tokenString)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *AuthMiddleware) VerifyWSToken(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		tokenString := r.URL.Query().Get("token")
		if tokenString == "" {
			helper.WriteError(w, helper.NewUnauthorizedError(""))
			return
		}

		isBlacklisted, err := m.sessionRepo.IsTokenBlacklisted(r.Context(), tokenString)
		if err != nil {
			helper.WriteError(w, helper.NewServiceUnavailableError("Session service unavailable"))
			return
		}

		if isBlacklisted {
			helper.WriteError(w, helper.NewUnauthorizedError("Token has been revoked"))
			return
		}

		userContext, err := m.authService.VerifyUser(r.Context(), tokenString)
		if err != nil {
			helper.WriteError(w, err)
			return
		}

		ctx := context.WithValue(r.Context(), UserContextKey, userContext)
		ctx = context.WithValue(ctx, TokenContextKey, tokenString)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (m *AuthMiddleware) AdminOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userContext, ok := r.Context().Value(UserContextKey).(*model.UserDTO)
		if !ok {
			helper.WriteError(w, helper.NewUnauthorizedError(""))
			return
		}

		if userContext.Role != string(user.RoleAdmin) {
			helper.WriteError(w, helper.NewForbiddenError("Admin access required"))
			return
		}

		next.ServeHTTP(w, r)
	})
}
