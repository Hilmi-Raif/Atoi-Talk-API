package main

import (
	"AtoiTalkAPI/internal/bootstrap"
	"AtoiTalkAPI/internal/infrastructure/config"
	"AtoiTalkAPI/internal/infrastructure/fcm"
	"AtoiTalkAPI/internal/infrastructure/observability"
	"AtoiTalkAPI/internal/infrastructure/redis"
	"AtoiTalkAPI/internal/push_worker"
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"
)

func main() {
	slog.SetDefault(observability.NewLogger(os.Stdout, "production", "push-worker", "unknown"))
	cfg := config.LoadAppConfig()
	if !cfg.PushEnabled {
		slog.Info("Push notifications disabled")
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	shutdown := bootstrap.InitTelemetry(ctx, cfg, cfg.OTelServiceName+"-push-worker")
	defer shutdown()
	cfg.DBMigrate = false
	client := config.InitEnt(cfg)
	defer func() { _ = client.Close() }()
	cache, err := redis.NewRedisAdapter(cfg)
	if err != nil {
		slog.Error("Initialize push session store", "error", err)
		os.Exit(1)
	}
	defer func() { _ = cache.Client().Close() }()
	sender, err := fcm.NewFCMAdapter(ctx, cfg.FCMProjectID)
	if err != nil {
		slog.Error("Initialize FCM", "error", err)
		os.Exit(1)
	}
	worker := pushworker.Worker{Store: &pushworker.Store{Client: client, Dialect: "postgres"}, Sender: sender, Redis: cache.Client()}
	if err = worker.Run(ctx); err != nil {
		slog.Error("Push worker stopped", "error", err)
		os.Exit(1)
	}
}
