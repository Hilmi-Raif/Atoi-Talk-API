package config

import (
	"net/http"
	"testing"
	"time"
)

func TestNewHTTPClientConfiguresTimeoutAndTransport(t *testing.T) {
	client := NewHTTPClient()
	if client.Timeout != 30*time.Second {
		t.Fatalf("unexpected timeout: %v", client.Timeout)
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("unexpected transport type: %T", client.Transport)
	}
	if transport.MaxIdleConns != 100 || transport.MaxIdleConnsPerHost != 100 || transport.IdleConnTimeout != 90*time.Second {
		t.Fatalf("unexpected transport settings: %#v", transport)
	}
}
