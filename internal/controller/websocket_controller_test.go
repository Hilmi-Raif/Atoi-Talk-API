package controller

import (
	"AtoiTalkAPI/internal/config"
	"net/http"
	"testing"
)

func TestNormalizeOrigin(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "normalizes scheme and host", raw: " HTTPS://Example.COM/path ", want: "https://example.com"},
		{name: "rejects relative origin", raw: "/relative", want: ""},
		{name: "rejects malformed origin", raw: "://bad", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := normalizeOrigin(tt.raw); got != tt.want {
				t.Fatalf("normalizeOrigin(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestWebSocketControllerCheckOrigin(t *testing.T) {
	controller := NewWebSocketController(nil, &config.AppConfig{AppCorsAllowedOrigins: []string{"https://allowed.example"}})

	for _, tt := range []struct {
		name   string
		origin string
		want   bool
	}{
		{name: "allows configured origin", origin: "https://ALLOWED.example/path", want: true},
		{name: "rejects unconfigured origin", origin: "https://other.example", want: false},
		{name: "allows missing origin", origin: "", want: true},
		{name: "rejects malformed origin", origin: "://bad", want: false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			req := &http.Request{Header: http.Header{}}
			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}
			if got := controller.checkOrigin(req); got != tt.want {
				t.Fatalf("checkOrigin(%q) = %v, want %v", tt.origin, got, tt.want)
			}
		})
	}
}

func TestWebSocketControllerCheckOriginAllowsWildcard(t *testing.T) {
	controller := NewWebSocketController(nil, &config.AppConfig{AppCorsAllowedOrigins: []string{"*"}})
	if !controller.checkOrigin(&http.Request{Header: http.Header{"Origin": []string{"https://any.example"}}}) {
		t.Fatal("wildcard origin should be allowed")
	}
}
