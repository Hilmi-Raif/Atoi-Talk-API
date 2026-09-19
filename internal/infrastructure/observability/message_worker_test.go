package observability

import (
	"context"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

func TestMessageWorkerMetricsRecordBatchAndBacklog(t *testing.T) {
	reader := metric.NewManualReader()
	provider := metric.NewMeterProvider(metric.WithReader(reader))
	otel.SetMeterProvider(provider)
	defer func() { _ = provider.Shutdown(context.Background()) }()

	metrics := NewMessageWorkerMetrics(provider.Meter("test/message-worker"))
	metrics.RecordBatch(context.Background(), 4, 3, 1, 250*time.Millisecond)
	metrics.RecordFailure(context.Background(), "publish")
	metrics.RecordBacklog(context.Background(), MessageWorkerBacklog{
		Pending:     7,
		Locked:      2,
		Unprojected: 5,
		Unpublished: 6,
	})

	var resourceMetrics metricdata.ResourceMetrics
	if err := reader.Collect(context.Background(), &resourceMetrics); err != nil {
		t.Fatalf("collect metrics: %v", err)
	}

	metricsByName := make(map[string]metricdata.Metrics)
	for _, scope := range resourceMetrics.ScopeMetrics {
		for _, item := range scope.Metrics {
			metricsByName[item.Name] = item
		}
	}

	for _, name := range []string{
		"message_worker.batch.runs",
		"message_worker.batch.duration",
		"message_worker.records.claimed",
		"message_worker.records.processed",
		"message_worker.records.failed",
		"message_worker.failures",
		"message_worker.outbox.pending",
		"message_worker.outbox.locked",
		"message_worker.outbox.unprojected",
		"message_worker.outbox.unpublished",
	} {
		if _, ok := metricsByName[name]; !ok {
			t.Fatalf("expected metric %q", name)
		}
	}

}
