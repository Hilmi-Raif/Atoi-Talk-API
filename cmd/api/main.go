package main

import (
	"AtoiTalkAPI/internal/bootstrap"
	"AtoiTalkAPI/internal/config"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := config.LoadAppConfig()

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
