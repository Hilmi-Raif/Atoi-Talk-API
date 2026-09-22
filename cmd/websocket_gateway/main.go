package main

import (
	"AtoiTalkAPI/internal/bootstrap"
	"AtoiTalkAPI/internal/infrastructure/config"
	"AtoiTalkAPI/internal/infrastructure/database/realtimequery"
	"AtoiTalkAPI/internal/infrastructure/redis"
	messagingpubsub "AtoiTalkAPI/internal/messaging/pubsub"
	"AtoiTalkAPI/internal/websocket_gateway/auth"
	"AtoiTalkAPI/internal/websocket_gateway/connection"
	"AtoiTalkAPI/internal/websocket_gateway/presence"
	"AtoiTalkAPI/internal/websocket_gateway/pubsub"
	"AtoiTalkAPI/internal/websocket_gateway/stream"
	"AtoiTalkAPI/internal/websocket_gateway/typing"
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/google/uuid"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	slog.SetDefault(logger)

	cfg := config.LoadAppConfig()
	shutdownTelemetry := bootstrap.InitTelemetry(context.Background(), cfg, cfg.OTelServiceName+"-websocket-gateway")
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

	instanceID := connection.ResolveRealtimeInstanceID(cfg.RealtimeInstanceID)
	group := connection.RealtimeConsumerGroup(cfg.RealtimeConsumerGroup, instanceID)
	redisStream := redis.NewRedisStreamConsumer(redisAdapter.Client(), cfg.MessageEventStream, group, instanceID)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := redisStream.EnsureGroup(ctx); err != nil {
		slog.Error("Failed to initialize realtime Redis Stream group", "error", err)
		os.Exit(1)
	}
	realtimeQuery := realtimequery.New(entClient)

	presenceManager := presence.NewManager(
		redisAdapter.Client(),
		func(ctx context.Context, userID uuid.UUID) error {
			return entClient.User.UpdateOneID(userID).SetLastSeenAt(time.Now().UTC()).Exec(ctx)
		},
		realtimeQuery.Contacts,
		realtimeQuery.IsBlocked,
	)
	gateway := connection.NewGatewayWithPresence(presenceManager)
	go gateway.Run(ctx)
	pubSubListener := pubsub.NewPubSubListener(redisAdapter.Client(), messagingpubsub.BroadcastChannel, gateway)
	go func() {
		if err := pubSubListener.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Error("Realtime Pub/Sub listener stopped", "error", err)
			stop()
		}
	}()
	typingRouter := typing.NewRouterWithBlockChecker(redisAdapter.Client(), realtimeQuery.ChatMembers, realtimeQuery.IsBlocked)
	consumer := stream.NewConsumer(redisStream, gateway)
	go func() {
		if err := consumer.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Error("Realtime stream consumer stopped", "error", err)
			stop()
		}
	}()

	verifier := auth.NewJWTVerifier(entClient, redisAdapter.Client(), cfg.JWTSecret)
	handler := connection.NewHTTPHandlerWithTypingRouter(gateway, verifier, cfg.AppCorsAllowedOrigins, typingRouter)
	server := &http.Server{
		Addr:              fmt.Sprintf(":%s", cfg.RealtimePort),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		slog.Info("Starting WebSocket gateway", "port", cfg.RealtimePort, "stream", cfg.MessageEventStream, "group", group)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			slog.Error("WebSocket gateway stopped", "error", err)
			stop()
		}
	}()

	<-ctx.Done()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		slog.Error("Error shutting down WebSocket gateway", "error", err)
	}
}
