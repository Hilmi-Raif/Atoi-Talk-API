package observability

import (
	"context"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"
)

type JobMetrics struct {
	tracer    trace.Tracer
	runs      metric.Int64Counter
	duration  metric.Float64Histogram
	errors    metric.Int64Counter
	completed metric.Int64Counter
}

func NewJobMetrics() *JobMetrics {
	meter := otel.GetMeterProvider().Meter("AtoiTalkAPI/internal/infrastructure/observability")
	runs, _ := meter.Int64Counter("scheduler.job.runs")
	duration, _ := meter.Float64Histogram("scheduler.job.duration", metric.WithUnit("s"))
	errorsCounter, _ := meter.Int64Counter("scheduler.job.errors")
	completed, _ := meter.Int64Counter("scheduler.job.completed")

	return &JobMetrics{
		tracer:    otel.GetTracerProvider().Tracer("AtoiTalkAPI/internal/infrastructure/observability"),
		runs:      runs,
		duration:  duration,
		errors:    errorsCounter,
		completed: completed,
	}
}

func (m *JobMetrics) Run(ctx context.Context, name string, fn func(context.Context) error) error {
	ctx, span := m.tracer.Start(ctx, "scheduler.job."+name,
		trace.WithAttributes(attribute.String("scheduler.job.name", name)),
	)
	defer span.End()

	attrs := metric.WithAttributes(attribute.String("scheduler.job.name", name))
	started := time.Now()
	m.runs.Add(ctx, 1, attrs)
	err := fn(ctx)
	m.duration.Record(ctx, time.Since(started).Seconds(), attrs)
	if err != nil {
		m.errors.Add(ctx, 1, attrs)
		span.RecordError(err)
		span.SetStatus(codes.Error, "job failed")
		slog.ErrorContext(ctx, "Scheduler job failed", "job", name, "error", err)
		return err
	}

	m.completed.Add(ctx, 1, attrs)
	span.SetStatus(codes.Ok, "job completed")
	slog.InfoContext(ctx, "Scheduler job completed", "job", name)
	return nil
}

func (m *JobMetrics) RunWithTimeout(parent context.Context, name string, timeout time.Duration, fn func(context.Context) error) error {
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	return m.Run(ctx, name, fn)
}
