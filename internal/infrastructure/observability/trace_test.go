package observability

import (
	"context"
	"errors"
	"testing"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestStartServiceSpanAndRecordError(t *testing.T) {
	exporter := tracetest.NewInMemoryExporter()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(sdktrace.NewSimpleSpanProcessor(exporter)))
	t.Cleanup(func() { _ = provider.Shutdown(context.Background()) })
	otel.SetTracerProvider(provider)

	ctx, span := StartServiceSpan(context.Background(), "service.test.operation")
	recordErr := errors.New("operation failed")
	RecordError(span, recordErr)
	span.End()

	if !span.SpanContext().IsValid() {
		t.Fatal("expected StartServiceSpan to create a valid span context")
	}
	if ctx == nil {
		t.Fatal("expected StartServiceSpan to return a context")
	}

	spans := exporter.GetSpans()
	if len(spans) != 1 {
		t.Fatalf("expected one exported span, got %d", len(spans))
	}
	if spans[0].Status.Code != codes.Error {
		t.Fatalf("expected error status, got %s", spans[0].Status.Code)
	}
	if len(spans[0].Events) != 1 {
		t.Fatalf("expected one error event, got %d", len(spans[0].Events))
	}
}
