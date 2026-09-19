package middleware

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMaxBodySizeAllowsBodyWithinLimit(t *testing.T) {
	handler := MaxBodySize(5)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("12345")))
	if recorder.Code != http.StatusOK || recorder.Body.String() != "12345" {
		t.Fatalf("unexpected response: code=%d body=%q", recorder.Code, recorder.Body.String())
	}
}

func TestMaxBodySizeRejectsBodyAboveLimit(t *testing.T) {
	handler := MaxBodySize(5)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "body too large", http.StatusRequestEntityTooLarge)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("123456")))
	if recorder.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected body limit response, got %d", recorder.Code)
	}
}
