package middleware

import (
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/repository"
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
	repo              rateLimitStore
	trustedProxyCIDRs []*net.IPNet
}

func NewRateLimitMiddleware(repo rateLimitStore, cfg *config.AppConfig) *RateLimitMiddleware {
	return &RateLimitMiddleware{
		repo:              repo,
		trustedProxyCIDRs: parseTrustedProxyCIDRs(cfg.TrustedProxyCIDRs),
	}
}

func (m *RateLimitMiddleware) Limit(keyName string, limit int, window time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
				helper.WriteError(w, helper.NewServiceUnavailableError("Rate limiting service unavailable"))
				return
			}

			w.Header().Set("X-RateLimit-Limit", fmt.Sprintf("%d", limit))
			w.Header().Set("X-RateLimit-Reset", fmt.Sprintf("%d", int(ttl.Seconds())))

			if !allowed {
				w.Header().Set("X-RateLimit-Remaining", "0")
				w.Header().Set("Retry-After", fmt.Sprintf("%d", int(math.Ceil(ttl.Seconds()))))

				helper.WriteError(w, helper.NewTooManyRequestsError("Rate limit exceeded. Please try again later."))
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

	if m.isTrustedProxy(remoteIP) {
		if forwardedIP := m.clientIPFromXForwardedFor(r.Header.Get("X-Forwarded-For"), remoteIP); forwardedIP != "" {
			return forwardedIP
		}

		if realIP := parseIPString(r.Header.Get("X-Real-IP")); realIP != "" {
			parsedRealIP := parseIP(realIP)
			if parsedRealIP != nil && !m.isTrustedProxy(parsedRealIP) {
				return parsedRealIP.String()
			}
		}
	}

	return remoteIP.String()
}

func (m *RateLimitMiddleware) isTrustedProxy(ip net.IP) bool {
	for _, network := range m.trustedProxyCIDRs {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

func parseTrustedProxyCIDRs(cidrs []string) []*net.IPNet {
	if len(cidrs) == 0 {
		return nil
	}

	out := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, err := net.ParseCIDR(strings.TrimSpace(cidr))
		if err != nil {
			slog.Warn("Ignoring invalid trusted proxy CIDR", "cidr", cidr, "error", err)
			continue
		}
		out = append(out, network)
	}

	return out
}

func (m *RateLimitMiddleware) clientIPFromXForwardedFor(xForwardedFor string, remoteIP net.IP) string {
	forwardedIPs := parseForwardedIPs(xForwardedFor)
	if len(forwardedIPs) == 0 {
		return ""
	}

	chain := make([]net.IP, 0, len(forwardedIPs)+1)
	chain = append(chain, forwardedIPs...)
	chain = append(chain, remoteIP)

	for i := len(chain) - 1; i >= 0; i-- {
		if !m.isTrustedProxy(chain[i]) {
			return chain[i].String()
		}
	}

	return forwardedIPs[0].String()
}

func parseForwardedIPs(xForwardedFor string) []net.IP {
	if xForwardedFor == "" {
		return nil
	}

	parts := strings.Split(xForwardedFor, ",")
	ips := make([]net.IP, 0, len(parts))
	for _, part := range parts {
		if ip := parseIP(strings.TrimSpace(part)); ip != nil {
			ips = append(ips, ip)
		}
	}

	return ips
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

func parseIPString(value string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}

	if ip := parseIP(trimmed); ip != nil {
		return ip.String()
	}

	return ""
}
