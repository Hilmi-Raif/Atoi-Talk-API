package middleware

import (
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	chimiddleware "github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/propagation"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const (
	tracerName = "AtoiTalkAPI/http"
	meterName  = "AtoiTalkAPI/http"
)

func HTTPTraceMiddleware(serviceName string) func(next http.Handler) http.Handler {
	tracer := otel.GetTracerProvider().Tracer(tracerName)
	meter := otel.GetMeterProvider().Meter(meterName)

	durationHistogram, _ := meter.Float64Histogram(
		"http.server.request.duration",
		metric.WithDescription("Duration of HTTP server requests in seconds"),
		metric.WithUnit("s"),
	)

	activeRequestsGauge, _ := meter.Int64UpDownCounter(
		"http.server.active_requests",
		metric.WithDescription("Number of active in-flight HTTP requests"),
		metric.WithUnit("{request}"),
	)

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			startTime := time.Now()

			ctx := otel.GetTextMapPropagator().Extract(r.Context(), propagation.HeaderCarrier(r.Header))

			spanName := fmt.Sprintf("%s %s", r.Method, r.URL.Path)

			ctx, span := tracer.Start(ctx, spanName,
				trace.WithSpanKind(trace.SpanKindServer),
				trace.WithAttributes(
					semconv.HTTPRequestMethodKey.String(r.Method),
					semconv.URLPath(r.URL.Path),
					semconv.ClientAddress(r.RemoteAddr),
					semconv.UserAgentOriginal(r.UserAgent()),
				),
			)
			defer span.End()

			if activeRequestsGauge != nil {
				activeRequestsGauge.Add(ctx, 1)
				defer activeRequestsGauge.Add(ctx, -1)
			}

			ww := chimiddleware.NewWrapResponseWriter(w, r.ProtoMajor)

			r = r.WithContext(ctx)
			defer func() {
				panicValue := recover()
				rctx := chi.RouteContext(r.Context())
				if rctx != nil && rctx.RoutePattern() != "" {
					pattern := rctx.RoutePattern()
					span.SetName(fmt.Sprintf("%s %s", r.Method, pattern))
					span.SetAttributes(semconv.HTTPRoute(pattern))
				}

				status := ww.Status()
				if panicValue != nil {
					status = http.StatusInternalServerError
				}
				if status == 0 {
					status = http.StatusOK
				}
				span.SetAttributes(semconv.HTTPResponseStatusCode(status))

				if status >= 500 {
					span.SetStatus(codes.Error, fmt.Sprintf("HTTP %d", status))
				} else {
					span.SetStatus(codes.Ok, "")
				}

				elapsed := time.Since(startTime).Seconds()
				route := "unmatched"
				if rctx != nil && rctx.RoutePattern() != "" {
					route = rctx.RoutePattern()
				}
				level := slog.LevelInfo
				if status >= 500 {
					level = slog.LevelError
				}
				slog.Log(ctx, level, "HTTP request completed", "request_id", chimiddleware.GetReqID(ctx), "method", r.Method, "route", route, "status", status, "duration_ms", elapsed*1000)
				if durationHistogram != nil {
					routeAttr := ""
					if rctx != nil {
						routeAttr = rctx.RoutePattern()
					}
					durationHistogram.Record(ctx, elapsed, metric.WithAttributes(
						attribute.String("http.method", r.Method),
						attribute.String("http.route", routeAttr),
						attribute.Int("http.status_code", status),
					))
				}
				if panicValue != nil {
					panic(panicValue)
				}
			}()
			next.ServeHTTP(ww, r)
		})
	}
}
