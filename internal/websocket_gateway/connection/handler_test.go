package connection

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractWebSocketTokenSupportsQueryAndBearerHeader(t *testing.T) {
	queryRequest := httptest.NewRequest("GET", "/ws?token=query-token", nil)
	if got := ExtractWebSocketToken(queryRequest); got != "query-token" {
		t.Fatalf("query token = %q, want query-token", got)
	}

	headerRequest := httptest.NewRequest("GET", "/ws", nil)
	headerRequest.Header.Set("Authorization", "Bearer header-token")
	if got := ExtractWebSocketToken(headerRequest); got != "header-token" {
		t.Fatalf("header token = %q, want header-token", got)
	}
}

func TestRealtimeConsumerGroupIsUniquePerInstanceByDefault(t *testing.T) {
	if got := RealtimeConsumerGroupPrefix("  custom-group  "); got != "custom-group" {
		t.Fatalf("configured prefix = %q, want custom-group", got)
	}
	if got := RealtimeConsumerGroupPrefix(""); got != "realtime" {
		t.Fatalf("default prefix = %q, want realtime", got)
	}
	if got := RealtimeConsumerGroup("", "realtime-a"); got != "realtime-realtime-a" {
		t.Fatalf("default group = %q, want realtime-realtime-a", got)
	}
	if got := RealtimeConsumerGroup("custom-group", "realtime-a"); got != "custom-group-realtime-a" {
		t.Fatalf("configured group = %q, want custom-group-realtime-a", got)
	}
	if got := RealtimeConsumerGroup("", ""); !strings.HasPrefix(got, "realtime-") || len(got) <= len("realtime-") {
		t.Fatalf("empty group should resolve with prefix and dynamic id, got %q", got)
	}
}
