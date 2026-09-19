package middleware

import (
	"AtoiTalkAPI/ent/user"
	application "AtoiTalkAPI/internal/api/application"
	"AtoiTalkAPI/internal/api/http/response"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/domain/model"
	"AtoiTalkAPI/internal/infrastructure/database/repository"
	"AtoiTalkAPI/internal/infrastructure/observability"
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

type authVerifier = application.AuthVerifier
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
			response.WriteError(w, helper.NewUnauthorizedError(""))
			return
		}

		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 || parts[0] != "Bearer" {
			response.WriteError(w, helper.NewUnauthorizedError(""))
			return
		}

		tokenString := parts[1]

		blacklistCtx, blacklistSpan := observability.StartServiceSpan(r.Context(), "auth.blacklist")
		isBlacklisted, err := m.sessionRepo.IsTokenBlacklisted(blacklistCtx, tokenString)
		if err != nil {
			observability.RecordError(blacklistSpan, err)
		}
		blacklistSpan.End()
		if err != nil {
			response.WriteError(w, helper.NewServiceUnavailableError("Session service unavailable"))
			return
		}

		if isBlacklisted {
			response.WriteError(w, helper.NewUnauthorizedError("Token has been revoked"))
			return
		}

		verifyCtx, verifySpan := observability.StartServiceSpan(r.Context(), "auth.verify_user")
		userContext, err := m.authService.VerifyUser(verifyCtx, tokenString)
		if err != nil {
			observability.RecordError(verifySpan, err)
		}
		verifySpan.End()
		if err != nil {
			response.WriteError(w, err)
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
			response.WriteError(w, helper.NewUnauthorizedError(""))
			return
		}

		if userContext.Role != string(user.RoleAdmin) {
			response.WriteError(w, helper.NewForbiddenError("Admin access required"))
			return
		}

		next.ServeHTTP(w, r)
	})
}
