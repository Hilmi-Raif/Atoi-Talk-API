package observability

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

type Config struct {
	Enabled              bool
	ServiceName          string
	ServiceVersion       string
	Environment          string
	ExporterOTLPEndpoint string
	ExporterOTLPInsecure bool
	SamplingRatio        float64
	MetricExportInterval time.Duration
}

type ShutdownFunc func(context.Context) error

func InitTelemetry(ctx context.Context, cfg Config) (ShutdownFunc, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
			semconv.DeploymentEnvironment(cfg.Environment),
		),
		resource.WithHost(),
		resource.WithOS(),
		resource.WithProcess(),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create otel resource: %w", err)
	}

	var grpcOpts []grpc.DialOption
	if cfg.ExporterOTLPInsecure {
		grpcOpts = append(grpcOpts, grpc.WithTransportCredentials(insecure.NewCredentials()))
	}

	traceExporterOpts := []otlptracegrpc.Option{
		otlptracegrpc.WithEndpoint(cfg.ExporterOTLPEndpoint),
		otlptracegrpc.WithDialOption(grpcOpts...),
	}
	if cfg.ExporterOTLPInsecure {
		traceExporterOpts = append(traceExporterOpts, otlptracegrpc.WithInsecure())
	}

	traceExporter, err := otlptracegrpc.New(ctx, traceExporterOpts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create otlp trace exporter: %w", err)
	}

	sampler := sdktrace.AlwaysSample()
	if cfg.SamplingRatio < 1.0 {
		sampler = sdktrace.ParentBased(sdktrace.TraceIDRatioBased(cfg.SamplingRatio))
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(sampler),
	)
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	metricExporterOpts := []otlpmetricgrpc.Option{
		otlpmetricgrpc.WithEndpoint(cfg.ExporterOTLPEndpoint),
		otlpmetricgrpc.WithDialOption(grpcOpts...),
	}
	if cfg.ExporterOTLPInsecure {
		metricExporterOpts = append(metricExporterOpts, otlpmetricgrpc.WithInsecure())
	}

	metricExporter, err := otlpmetricgrpc.New(ctx, metricExporterOpts...)
	if err != nil {
		_ = tp.Shutdown(ctx)
		return nil, fmt.Errorf("failed to create otlp metric exporter: %w", err)
	}

	readerOpts := []sdkmetric.PeriodicReaderOption{}
	if cfg.MetricExportInterval > 0 {
		readerOpts = append(readerOpts, sdkmetric.WithInterval(cfg.MetricExportInterval))
	}

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(sdkmetric.NewPeriodicReader(metricExporter, readerOpts...)),
		sdkmetric.WithResource(res),
	)
	otel.SetMeterProvider(mp)

	slog.Info("OpenTelemetry initialized",
		"service", cfg.ServiceName,
		"endpoint", cfg.ExporterOTLPEndpoint,
		"sampler_ratio", cfg.SamplingRatio,
	)

	shutdown := func(shutdownCtx context.Context) error {
		var errs []error
		if err := tp.Shutdown(shutdownCtx); err != nil {
			errs = append(errs, fmt.Errorf("failed to shutdown trace provider: %w", err))
		}
		if err := mp.Shutdown(shutdownCtx); err != nil {
			errs = append(errs, fmt.Errorf("failed to shutdown meter provider: %w", err))
		}
		return errors.Join(errs...)
	}

	return shutdown, nil
}
