package observability

import (
	"context"
	"testing"
)

func TestInitTelemetryDisabled(t *testing.T) {
	cfg := Config{
		Enabled: false,
	}

	shutdown, err := InitTelemetry(context.Background(), cfg)
	if err != nil {
		t.Fatalf("expected no error when otel is disabled, got: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown function")
	}
	if err := shutdown(context.Background()); err != nil {
		t.Fatalf("expected clean shutdown, got: %v", err)
	}
}

func TestInitTelemetryEnabled(t *testing.T) {
	cfg := Config{
		Enabled:              true,
		ServiceName:          "atoitalk-test",
		ServiceVersion:       "1.0.0",
		Environment:          "test",
		ExporterOTLPEndpoint: "localhost:4317",
		ExporterOTLPInsecure: true,
		SamplingRatio:        0.5,
	}

	shutdown, err := InitTelemetry(context.Background(), cfg)
	if err != nil {
		t.Fatalf("expected successful telemetry initialization, got: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown function")
	}

	_ = shutdown(context.Background())
}

func TestInitTelemetryFullSamplingAndSecure(t *testing.T) {
	cfg := Config{
		Enabled:              true,
		ServiceName:          "atoitalk-test-secure",
		ServiceVersion:       "1.0.0",
		Environment:          "production",
		ExporterOTLPEndpoint: "localhost:4317",
		ExporterOTLPInsecure: false,
		SamplingRatio:        1.0,
	}

	shutdown, err := InitTelemetry(context.Background(), cfg)
	if err != nil {
		t.Fatalf("expected successful telemetry initialization, got: %v", err)
	}
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown function")
	}

	_ = shutdown(context.Background())
}
