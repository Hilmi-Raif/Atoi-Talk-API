package config

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewChiNotFoundAndMethodNotAllowedResponses(t *testing.T) {
	router := NewChi(&AppConfig{AppEnv: "test", AppCorsAllowedOrigins: []string{"*"}})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/missing", nil))
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "Not Found") {
		t.Fatalf("unexpected not found response: code=%d body=%s", recorder.Code, recorder.Body.String())
	}

	router.Get("/method", func(http.ResponseWriter, *http.Request) {})
	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/method", nil))
	if recorder.Code != http.StatusMethodNotAllowed || !strings.Contains(recorder.Body.String(), "Method Not Allowed") {
		t.Fatalf("unexpected method response: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestNewChiProductionEnvironmentAndRedaction(t *testing.T) {
	router := NewChi(&AppConfig{AppEnv: "production", AppCorsAllowedOrigins: []string{"https://app.example"}})

	router.Get("/token-test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/token-test?token=secret123&other=value", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status: %d", recorder.Code)
	}
}
