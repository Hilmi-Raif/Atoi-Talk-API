package middleware

import (
	"AtoiTalkAPI/internal/infrastructure/observability"
	"bytes"
	"encoding/json"
	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"github.com/stretchr/testify/require"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPCompletionLogCorrelatesAndSurvivesPanic(t *testing.T) {
	for _, panics := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "panic"}[panics], func(t *testing.T) {
			var output bytes.Buffer
			old := slog.Default()
			slog.SetDefault(observability.NewLogger(&output, "production", "api", "test"))
			defer slog.SetDefault(old)
			router := chi.NewRouter()
			router.Use(chimiddleware.Recoverer)
			router.Use(chimiddleware.RequestID)
			router.Use(HTTPTraceMiddleware("api"))
			router.Get("/items/{id}", func(w http.ResponseWriter, r *http.Request) {
				if panics {
					panic("private panic text")
				}
				w.WriteHeader(http.StatusNoContent)
			})
			req := httptest.NewRequest(http.MethodGet, "/items/private-id?token=secret-value", nil)
			req.Header.Set("X-Request-Id", "request-test")
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, req)
			var entry map[string]any
			require.NoError(t, json.Unmarshal(output.Bytes(), &entry))
			require.Equal(t, "request-test", entry["request_id"])
			require.Equal(t, "/items/{id}", entry["route"])
			expected := http.StatusNoContent
			if panics {
				expected = http.StatusInternalServerError
			}
			require.Equal(t, float64(expected), entry["status"])
			require.Equal(t, expected, recorder.Code)
			require.NotContains(t, output.String(), "secret-value")
			require.NotContains(t, output.String(), "private-id")
			require.NotContains(t, output.String(), "private panic text")
			require.Contains(t, entry["source"].(map[string]any)["function"], "HTTPTraceMiddleware")
		})
	}
}
