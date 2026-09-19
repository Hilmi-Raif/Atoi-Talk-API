package main

import (
	"AtoiTalkAPI/internal/bootstrap"
	"AtoiTalkAPI/internal/infrastructure/config"
	"AtoiTalkAPI/internal/scheduler"
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := config.LoadAppConfig()

	shutdownTelemetry := bootstrap.InitTelemetry(context.Background(), cfg, cfg.OTelServiceName+"-scheduler")
	defer shutdownTelemetry()

	cfg.DBMigrate = false

	entClient := config.InitEnt(cfg)
	defer func() {
		if err := entClient.Close(); err != nil {
			slog.Error("Error closing database connection", "error", err)
		}
	}()

	s3Client := config.NewS3Client(cfg)
	if s3Client == nil {
		slog.Error("Failed to initialize S3 client")
		os.Exit(1)
	}

	srv := scheduler.New(cfg, entClient, s3Client)

	srv.Start()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	slog.Info("Shutting down scheduler...")
	srv.Stop()
}
