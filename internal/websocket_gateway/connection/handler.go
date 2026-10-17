package connection

import (
	"net/http"
	"os"
	"strings"

	"github.com/google/uuid"
)

func ExtractWebSocketToken(r *http.Request) string {
	if token := strings.TrimSpace(r.URL.Query().Get("token")); token != "" {
		return token
	}
	parts := strings.Fields(r.Header.Get("Authorization"))
	if len(parts) == 2 && parts[0] == "Bearer" {
		return parts[1]
	}
	return ""
}

func ResolveRealtimeInstanceID(configured string) string {
	if trimmed := strings.TrimSpace(configured); trimmed != "" {
		return trimmed
	}
	if host, err := os.Hostname(); err == nil && strings.TrimSpace(host) != "" {
		return strings.TrimSpace(host)
	}
	return uuid.NewString()
}

func RealtimeConsumerGroupPrefix(configured string) string {
	prefix := strings.TrimSpace(configured)
	if prefix == "" {
		prefix = "realtime"
	}
	return prefix
}

func RealtimeConsumerGroup(configured, instanceID string) string {
	return RealtimeConsumerGroupPrefix(configured) + "-" + ResolveRealtimeInstanceID(instanceID)
}
