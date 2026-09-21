package observability

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

type MessageWorkerBacklog struct {
	Pending     int
	Locked      int
	Unprojected int
	Unpublished int
}

type MessageWorkerBacklogStore interface {
	Backlog(context.Context) (MessageWorkerBacklog, error)
}

type MessageWorkerMetrics struct {
	batchRuns        metric.Int64Counter
	batchDuration    metric.Float64Histogram
	claimedRecords   metric.Int64Counter
	processedRecords metric.Int64Counter
	failedRecords    metric.Int64Counter
	failures         metric.Int64Counter
	pending          metric.Int64Gauge
	locked           metric.Int64Gauge
	unprojected      metric.Int64Gauge
	unpublished      metric.Int64Gauge
}

func NewMessageWorkerMetrics(meter metric.Meter) *MessageWorkerMetrics {
	batchRuns, _ := meter.Int64Counter("message_worker.batch.runs")
	batchDuration, _ := meter.Float64Histogram("message_worker.batch.duration", metric.WithUnit("s"))
	claimedRecords, _ := meter.Int64Counter("message_worker.records.claimed")
	processedRecords, _ := meter.Int64Counter("message_worker.records.processed")
	failedRecords, _ := meter.Int64Counter("message_worker.records.failed")
	failures, _ := meter.Int64Counter("message_worker.failures")
	pending, _ := meter.Int64Gauge("message_worker.outbox.pending")
	locked, _ := meter.Int64Gauge("message_worker.outbox.locked")
	unprojected, _ := meter.Int64Gauge("message_worker.outbox.unprojected")
	unpublished, _ := meter.Int64Gauge("message_worker.outbox.unpublished")

	return &MessageWorkerMetrics{
		batchRuns:        batchRuns,
		batchDuration:    batchDuration,
		claimedRecords:   claimedRecords,
		processedRecords: processedRecords,
		failedRecords:    failedRecords,
		failures:         failures,
		pending:          pending,
		locked:           locked,
		unprojected:      unprojected,
		unpublished:      unpublished,
	}
}

func (m *MessageWorkerMetrics) RecordFailure(ctx context.Context, stage string) {
	m.failures.Add(ctx, 1, metric.WithAttributes(attribute.String("message_worker.stage", stage)))
}

func NewDefaultMessageWorkerMetrics() *MessageWorkerMetrics {
	return NewMessageWorkerMetrics(otel.GetMeterProvider().Meter("AtoiTalkAPI/message-worker"))
}

func (m *MessageWorkerMetrics) RecordBatch(ctx context.Context, claimed, processed, failed int, duration time.Duration) {
	m.batchRuns.Add(ctx, 1)
	m.batchDuration.Record(ctx, duration.Seconds())
	if claimed > 0 {
		m.claimedRecords.Add(ctx, int64(claimed))
	}
	if processed > 0 {
		m.processedRecords.Add(ctx, int64(processed))
	}
	if failed > 0 {
		m.failedRecords.Add(ctx, int64(failed))
	}
}

func (m *MessageWorkerMetrics) RecordBacklog(ctx context.Context, backlog MessageWorkerBacklog) {
	attrs := metric.WithAttributes(attribute.String("message_worker.queue", "message_outbox"))
	m.pending.Record(ctx, int64(backlog.Pending), attrs)
	m.locked.Record(ctx, int64(backlog.Locked), attrs)
	m.unprojected.Record(ctx, int64(backlog.Unprojected), attrs)
	m.unpublished.Record(ctx, int64(backlog.Unpublished), attrs)
}
