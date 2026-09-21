package observability

import (
	"context"
	"errors"
	"testing"
)

func TestJobMetricsRunReturnsJobError(t *testing.T) {
	metrics := NewJobMetrics()
	wantErr := errors.New("job failed")

	if err := metrics.Run(context.Background(), "entity_cleanup", func(context.Context) error {
		return wantErr
	}); !errors.Is(err, wantErr) {
		t.Fatalf("expected job error %v, got %v", wantErr, err)
	}
}

func TestJobMetricsRunAcceptsSuccessfulJob(t *testing.T) {
	metrics := NewJobMetrics()
	if err := metrics.Run(context.Background(), "media_cleanup", func(context.Context) error {
		return nil
	}); err != nil {
		t.Fatalf("expected successful job, got %v", err)
	}
}
