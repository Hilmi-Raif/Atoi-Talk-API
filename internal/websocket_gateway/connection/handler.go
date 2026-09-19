package connection

import (
	"net/http"
	"strings"
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

func RealtimeConsumerGroup(configured, instanceID string) string {
	if strings.TrimSpace(configured) != "" {
		return configured
	}
	return "realtime-" + instanceID
}
