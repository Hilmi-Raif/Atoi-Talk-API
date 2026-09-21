package middleware

import (
	"AtoiTalkAPI/internal/api/http/response"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/domain/model"
	"AtoiTalkAPI/internal/infrastructure/config"
	"AtoiTalkAPI/internal/infrastructure/database/repository"
	"fmt"
	"log/slog"
	"math"
	"net"
	"net/http"
	"strings"
	"time"
)

type rateLimitStore = repository.RateLimiter

type RateLimitMiddleware struct {
	repo    rateLimitStore
	enabled bool
}

func NewRateLimitMiddleware(repo rateLimitStore, cfg *config.AppConfig) *RateLimitMiddleware {
	return &RateLimitMiddleware{
		repo:    repo,
		enabled: cfg.RateLimitEnabled,
	}
}

func (m *RateLimitMiddleware) Limit(keyName string, limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !m.enabled {
				next.ServeHTTP(w, r)
				return
			}

			var identifier string
			var keyPrefix string

			userContext, ok := r.Context().Value(UserContextKey).(*model.UserDTO)
			if ok && userContext != nil {
				identifier = userContext.ID.String()
				keyPrefix = "ratelimit:user"
			} else {

				identifier = m.getIP(r)
				keyPrefix = "ratelimit:ip"
			}

			key := fmt.Sprintf("%s:%s:%s", keyPrefix, keyName, identifier)

			allowed, ttl, err := m.repo.Allow(r.Context(), key, limit, window)
			if err != nil {
				slog.Error("Rate limit check failed", "error", err)
				response.WriteError(w, helper.NewServiceUnavailableError("Rate limiting service unavailable"))
				return
			}

			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
			w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", int(ttl.Seconds())))

			if !allowed {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(math.Ceil(ttl.Seconds()))))

				response.WriteError(w, helper.NewTooManyRequestsError("Rate limit exceeded. Please try again later."))
				return
			}

			ctx := helper.WithClientFingerprint(r.Context(), identifier)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func (m *RateLimitMiddleware) getIP(r *http.Request) string {
	remoteIP := parseIP(r.RemoteAddr)
	if remoteIP == nil {
		return r.RemoteAddr
	}
	return remoteIP.String()
}

func parseIP(remoteAddr string) net.IP {
	if remoteAddr == "" {
		return nil
	}

	host := remoteAddr
	if parsedHost, _, err := net.SplitHostPort(remoteAddr); err == nil {
		host = parsedHost
	}

	host = strings.Trim(host, "[]")
	return net.ParseIP(host)
}
