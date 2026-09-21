package bootstrap

import (
	"AtoiTalkAPI/internal/infrastructure/config"
	"context"
	"testing"
)

func TestInitTelemetryDisabled(t *testing.T) {
	cfg := &config.AppConfig{
		OTelEnabled: false,
	}

	shutdown := InitTelemetry(context.Background(), cfg, "test-service")
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown function")
	}
	shutdown()
}

func TestInitTelemetryEnabled(t *testing.T) {
	cfg := &config.AppConfig{
		OTelEnabled:              true,
		OTelServiceName:          "test-service",
		OTelServiceVersion:       "0.1.0",
		AppEnv:                   "test",
		OTelExporterOTLPEndpoint: "localhost:4317",
		OTelExporterOTLPInsecure: true,
		OTelSamplingRatio:        1.0,
	}

	shutdown := InitTelemetry(context.Background(), cfg, "test-service")
	if shutdown == nil {
		t.Fatal("expected non-nil shutdown function")
	}
	shutdown()
}
