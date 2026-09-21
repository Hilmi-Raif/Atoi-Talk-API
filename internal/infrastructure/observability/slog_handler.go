package observability

import (
	"context"
	"log/slog"

	"go.opentelemetry.io/otel/trace"
)

type TraceContextHandler struct {
	inner slog.Handler
}

func NewTraceContextHandler(inner slog.Handler) slog.Handler {
	return &TraceContextHandler{
		inner: inner,
	}
}

func (h *TraceContextHandler) Enabled(ctx context.Context, level slog.Level) bool {
	return h.inner.Enabled(ctx, level)
}

func (h *TraceContextHandler) Handle(ctx context.Context, r slog.Record) error {
	span := trace.SpanFromContext(ctx)
	if span.SpanContext().IsValid() {
		r.AddAttrs(
			slog.String("trace_id", span.SpanContext().TraceID().String()),
			slog.String("span_id", span.SpanContext().SpanID().String()),
		)
	}
	return h.inner.Handle(ctx, r)
}

func (h *TraceContextHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return &TraceContextHandler{
		inner: h.inner.WithAttrs(attrs),
	}
}

func (h *TraceContextHandler) WithGroup(name string) slog.Handler {
	return &TraceContextHandler{
		inner: h.inner.WithGroup(name),
	}
}
