package observability

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/pprof"
	"time"

	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type CheckFunc func(ctx context.Context) error

type HealthResponse struct {
	Status    string            `json:"status"`
	Timestamp string            `json:"timestamp"`
	Checks    map[string]string `json:"checks,omitempty"`
}

type HealthHandler struct {
	dbCheck    CheckFunc
	redisCheck CheckFunc
	tracer     trace.Tracer
	duration   metric.Float64Histogram
	errors     metric.Int64Counter
}

func NewHealthHandler(dbCheck CheckFunc, redisCheck CheckFunc) *HealthHandler {
	meter := otel.GetMeterProvider().Meter("AtoiTalkAPI/health")
	duration, _ := meter.Float64Histogram(
		"health.check.duration",
		metric.WithDescription("Duration of health dependency checks in seconds"),
		metric.WithUnit("s"),
	)
	errorsCounter, _ := meter.Int64Counter(
		"health.check.errors",
		metric.WithDescription("Number of failed health dependency checks"),
	)

	return &HealthHandler{
		dbCheck:    dbCheck,
		redisCheck: redisCheck,
		tracer:     otel.GetTracerProvider().Tracer("AtoiTalkAPI/health"),
		duration:   duration,
		errors:     errorsCounter,
	}
}

func (h *HealthHandler) Health(w http.ResponseWriter, r *http.Request) {
	writeJSONResponse(w, http.StatusOK, HealthResponse{
		Status:    "healthy",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *HealthHandler) Live(w http.ResponseWriter, r *http.Request) {
	writeJSONResponse(w, http.StatusOK, HealthResponse{
		Status:    "alive",
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

func (h *HealthHandler) Ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	checks := make(map[string]string)
	allHealthy := true

	if h.dbCheck != nil {
		if err := h.runCheck(ctx, "database", h.dbCheck); err != nil {
			checks["database"] = err.Error()
			allHealthy = false
		} else {
			checks["database"] = "ok"
		}
	}

	if h.redisCheck != nil {
		if err := h.runCheck(ctx, "redis", h.redisCheck); err != nil {
			checks["redis"] = err.Error()
			allHealthy = false
		} else {
			checks["redis"] = "ok"
		}
	}

	status := "ready"
	statusCode := http.StatusOK
	if !allHealthy {
		status = "not_ready"
		statusCode = http.StatusServiceUnavailable
	}

	writeJSONResponse(w, statusCode, HealthResponse{
		Status:    status,
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		Checks:    checks,
	})
}

func (h *HealthHandler) runCheck(ctx context.Context, dependency string, check CheckFunc) error {
	checkCtx, span := h.tracer.Start(ctx, "health.check."+dependency)
	defer span.End()

	start := time.Now()
	err := check(checkCtx)
	h.duration.Record(checkCtx, time.Since(start).Seconds(), metric.WithAttributes(
		attribute.String("health.dependency", dependency),
	))
	if err != nil {
		h.errors.Add(checkCtx, 1, metric.WithAttributes(attribute.String("health.dependency", dependency)))
		span.RecordError(err)
		span.SetStatus(codes.Error, "health check failed")
		return err
	}

	span.SetStatus(codes.Ok, "health check passed")
	return nil
}

func RegisterPprofRoutes(r chi.Router) {
	r.Route("/debug/pprof", func(r chi.Router) {
		r.HandleFunc("/", pprof.Index)
		r.HandleFunc("/cmdline", pprof.Cmdline)
		r.HandleFunc("/profile", pprof.Profile)
		r.HandleFunc("/symbol", pprof.Symbol)
		r.HandleFunc("/trace", pprof.Trace)
		r.Handle("/allocs", pprof.Handler("allocs"))
		r.Handle("/block", pprof.Handler("block"))
		r.Handle("/goroutine", pprof.Handler("goroutine"))
		r.Handle("/heap", pprof.Handler("heap"))
		r.Handle("/mutex", pprof.Handler("mutex"))
		r.Handle("/threadcreate", pprof.Handler("threadcreate"))
	})
}

func writeJSONResponse(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}
