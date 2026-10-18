package main

import (
	"AtoiTalkAPI/internal/bootstrap"
	"AtoiTalkAPI/internal/infrastructure/config"
	"AtoiTalkAPI/internal/infrastructure/object_storage"
	"AtoiTalkAPI/internal/infrastructure/observability"
	"AtoiTalkAPI/internal/infrastructure/redis"
	"AtoiTalkAPI/internal/message_worker"
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	logger := observability.NewLogger(os.Stdout, "production", "message-worker", "unknown")
	slog.SetDefault(logger)

	cfg := config.LoadAppConfig()
	shutdownTelemetry := bootstrap.InitTelemetry(context.Background(), cfg, cfg.OTelServiceName+"-message-worker")
	defer shutdownTelemetry()

	cfg.DBMigrate = false
	entClient := config.InitEnt(cfg)
	defer func() {
		if err := entClient.Close(); err != nil {
			slog.Error("Error closing database connection", "error", err)
		}
	}()

	redisAdapter, err := redis.NewRedisAdapter(cfg)
	if err != nil {
		slog.Error("Failed to initialize Redis client", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := redisAdapter.Client().Close(); err != nil {
			slog.Error("Error closing Redis connection", "error", err)
		}
	}()

	s3Client := config.NewS3Client(cfg)
	if s3Client == nil {
		slog.Error("Failed to initialize S3 client")
		os.Exit(1)
	}
	storageAdapter := objectstorage.NewStorageAdapter(cfg, s3Client, config.NewHTTPClient())

	store := messageworker.NewEntOutboxStoreWithURLGenerator(entClient, storageAdapter)
	store.EnablePush(cfg.PushEnabled)
	publisher := redis.NewRedisStreamPublisherWithDedupeTTL(
		redisAdapter.Client(),
		cfg.MessageEventStream,
		int64(cfg.MessageEventStreamMaxLen),
		messageEventDedupeTTL(cfg),
	)
	worker := messageworker.NewWorker(store, publisher, messageworker.ConfigFromAppConfig(cfg))

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("Starting message worker", "stream", cfg.MessageEventStream)
	if err := worker.Run(ctx, nil); err != nil {
		slog.Error("Message worker stopped with error", "error", err)
		os.Exit(1)
	}
	slog.Info("Message worker stopped")
}

func messageEventDedupeTTL(cfg *config.AppConfig) time.Duration {
	retentionDays := cfg.MessageOutboxRetentionDays
	if retentionDays <= 0 {
		retentionDays = 7
	}
	return time.Duration(retentionDays) * 24 * time.Hour
}
