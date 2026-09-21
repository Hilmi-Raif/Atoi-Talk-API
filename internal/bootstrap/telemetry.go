package bootstrap

import (
	"AtoiTalkAPI/internal/infrastructure/config"
	"AtoiTalkAPI/internal/infrastructure/observability"
	"context"
	"log/slog"
	"time"
)

func InitTelemetry(ctx context.Context, cfg *config.AppConfig, serviceName string) func() {
	shutdownTelemetry, err := observability.InitTelemetry(ctx, observability.Config{
		Enabled:              cfg.OTelEnabled,
		ServiceName:          serviceName,
		ServiceVersion:       cfg.OTelServiceVersion,
		Environment:          cfg.AppEnv,
		ExporterOTLPEndpoint: cfg.OTelExporterOTLPEndpoint,
		ExporterOTLPInsecure: cfg.OTelExporterOTLPInsecure,
		SamplingRatio:        cfg.OTelSamplingRatio,
		MetricExportInterval: cfg.OTelMetricExportInterval,
	})
	if err != nil {
		slog.Error("Failed to initialize OpenTelemetry", "service", serviceName, "error", err)
		return func() {}
	}

	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := shutdownTelemetry(shutdownCtx); err != nil {
			slog.Error("Error shutting down OpenTelemetry", "service", serviceName, "error", err)
		}
	}
}
