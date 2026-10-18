package main

import (
	"AtoiTalkAPI/internal/bootstrap"
	"AtoiTalkAPI/internal/infrastructure/config"
	"AtoiTalkAPI/internal/infrastructure/observability"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

// @title AtoiTalk API
// @version 1.0
// @securityDefinitions.apikey BearerAuth
// @in header
// @name Authorization
func main() {
	logger := observability.NewLogger(os.Stdout, "production", "api", "unknown")
	slog.SetDefault(logger)

	cfg := config.LoadAppConfig()

	shutdownTelemetry := bootstrap.InitTelemetry(context.Background(), cfg, cfg.OTelServiceName)
	defer shutdownTelemetry()

	client := config.InitEnt(cfg)
	defer func() {
		if err := client.Close(); err != nil {
			slog.Error("Error closing database connection", "error", err)
		}
	}()

	s3Client := config.NewS3Client(cfg)
	if s3Client == nil {
		slog.Error("Failed to initialize S3 client")
	}

	httpClient := config.NewHTTPClient()
	validate := config.NewValidator()
	chiMux := config.NewChi(cfg)

	bootstrap.Init(cfg, client, validate, s3Client, httpClient, chiMux)

	addr := fmt.Sprintf(":%s", cfg.AppPort)
	slog.Info("Starting AtoiTalkAPI", "port", cfg.AppPort)

	server := &http.Server{
		Addr:              addr,
		Handler:           chiMux,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}
	if err := server.ListenAndServe(); err != nil {
		slog.Error("Failed to start server", "error", err)
		os.Exit(1)
	}
}
