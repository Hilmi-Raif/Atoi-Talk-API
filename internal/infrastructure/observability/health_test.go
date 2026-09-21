package observability

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/trace"
	oteltrace "go.opentelemetry.io/otel/trace"
)

func TestLivenessProbe(t *testing.T) {
	handler := NewHealthHandler(nil, nil)
	r := chi.NewRouter()
	r.Get("/livez", handler.Live)

	req := httptest.NewRequest(http.MethodGet, "/livez", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200 for livez, got: %d", rr.Code)
	}
}

func TestHealthzSummary(t *testing.T) {
	handler := NewHealthHandler(nil, nil)
	r := chi.NewRouter()
	r.Get("/healthz", handler.Health)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200 for healthz, got: %d", rr.Code)
	}
}

func TestReadinessProbeHealthy(t *testing.T) {
	dbCheck := func(ctx context.Context) error { return nil }
	redisCheck := func(ctx context.Context) error { return nil }

	handler := NewHealthHandler(dbCheck, redisCheck)
	r := chi.NewRouter()
	r.Get("/readyz", handler.Ready)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200 for healthy readyz, got: %d", rr.Code)
	}
}

func TestReadinessProbePropagatesDependencyCheckSpan(t *testing.T) {
	previousProvider := otel.GetTracerProvider()
	tp := trace.NewTracerProvider()
	otel.SetTracerProvider(tp)
	t.Cleanup(func() {
		_ = tp.Shutdown(context.Background())
		otel.SetTracerProvider(previousProvider)
	})

	spanSeen := false
	dbCheck := func(ctx context.Context) error {
		spanSeen = oteltrace.SpanContextFromContext(ctx).IsValid()
		return nil
	}
	handler := NewHealthHandler(dbCheck, nil)
	r := chi.NewRouter()
	r.Get("/readyz", handler.Ready)

	r.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if !spanSeen {
		t.Fatal("expected readiness dependency check to receive an active span context")
	}
}

func TestReadinessProbeUnhealthyDB(t *testing.T) {
	dbCheck := func(ctx context.Context) error { return errors.New("db connection timeout") }
	redisCheck := func(ctx context.Context) error { return nil }

	handler := NewHealthHandler(dbCheck, redisCheck)
	r := chi.NewRouter()
	r.Get("/readyz", handler.Ready)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503 for unhealthy readyz, got: %d", rr.Code)
	}
}

func TestReadinessProbeUnhealthyRedis(t *testing.T) {
	dbCheck := func(ctx context.Context) error { return nil }
	redisCheck := func(ctx context.Context) error { return errors.New("redis unavailable") }

	handler := NewHealthHandler(dbCheck, redisCheck)
	r := chi.NewRouter()
	r.Get("/readyz", handler.Ready)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503 for unhealthy redis readyz, got: %d", rr.Code)
	}
}

func TestPprofMountedWhenRegistered(t *testing.T) {
	r := chi.NewRouter()
	RegisterPprofRoutes(r)

	req := httptest.NewRequest(http.MethodGet, "/debug/pprof/", nil)
	rr := httptest.NewRecorder()

	r.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected status 200 for pprof index, got: %d", rr.Code)
	}
}
