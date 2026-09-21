package main

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/ent/userblock"
	"AtoiTalkAPI/internal/bootstrap"
	"AtoiTalkAPI/internal/infrastructure/config"
	"AtoiTalkAPI/internal/infrastructure/redis"
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

	group := connection.RealtimeConsumerGroup(cfg.RealtimeConsumerGroup, cfg.RealtimeInstanceID)
	redisStream := redis.NewRedisStreamConsumer(redisAdapter.Client(), cfg.MessageEventStream, group, cfg.RealtimeInstanceID)
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := redisStream.EnsureGroup(ctx); err != nil {
		slog.Error("Failed to initialize realtime Redis Stream group", "error", err)
		os.Exit(1)
	}

	presenceManager := presence.NewManager(
		redisAdapter.Client(),
		func(ctx context.Context, userID uuid.UUID) error {
			return entClient.User.UpdateOneID(userID).SetLastSeenAt(time.Now().UTC()).Exec(ctx)
		},
		func(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
			privateChats, err := entClient.PrivateChat.Query().Where(
				privatechat.Or(privatechat.User1ID(userID), privatechat.User2ID(userID)),
			).Select(privatechat.FieldUser1ID, privatechat.FieldUser2ID).All(ctx)
			if err != nil {
				return nil, err
			}
			contacts := make([]uuid.UUID, 0, len(privateChats))
			for _, privateChat := range privateChats {
				if privateChat.User1ID != nil && *privateChat.User1ID == userID && privateChat.User2ID != nil {
					contacts = append(contacts, *privateChat.User2ID)
				} else if privateChat.User2ID != nil && *privateChat.User2ID == userID && privateChat.User1ID != nil {
					contacts = append(contacts, *privateChat.User1ID)
				}
			}
			return contacts, nil
		},
		func(ctx context.Context, senderID, targetID uuid.UUID) (bool, error) {
			return entClient.UserBlock.Query().Where(
				userblock.Or(userblock.BlockerID(senderID), userblock.BlockedID(senderID)),
				userblock.Or(userblock.BlockerID(targetID), userblock.BlockedID(targetID)),
			).Exist(ctx)
		},
	)
	gateway := connection.NewGatewayWithPresence(presenceManager)
	go gateway.Run(ctx)
	pubSubListener := pubsub.NewPubSubListener(redisAdapter.Client(), "events:broadcast", gateway)
	go func() {
		if err := pubSubListener.Run(ctx); err != nil && ctx.Err() == nil {
			slog.Error("Realtime Pub/Sub listener stopped", "error", err)
			stop()
		}
	}()
	typingRouter := typing.NewRouterWithBlockChecker(redisAdapter.Client(), func(ctx context.Context, chatID uuid.UUID) ([]uuid.UUID, error) {
		chatEntity, err := entClient.Chat.Query().Where(chat.ID(chatID), chat.DeletedAtIsNil()).Select(chat.FieldType).Only(ctx)
		if err != nil {
			return nil, err
		}
		switch chatEntity.Type {
		case chat.TypePrivate:
			privateEntity, err := entClient.PrivateChat.Query().Where(privatechat.ChatID(chatID)).Select(privatechat.FieldUser1ID, privatechat.FieldUser2ID).Only(ctx)
			if err != nil {
				return nil, err
			}
			members := make([]uuid.UUID, 0, 2)
			if privateEntity.User1ID != nil {
				members = append(members, *privateEntity.User1ID)
			}
			if privateEntity.User2ID != nil {
				members = append(members, *privateEntity.User2ID)
			}
			return members, nil
		case chat.TypeGroup:
			groupID, err := entClient.GroupChat.Query().Where(groupchat.ChatID(chatID)).OnlyID(ctx)
			if err != nil {
				return nil, err
			}
			members, err := entClient.GroupMember.Query().Where(groupmember.GroupChatID(groupID)).Select(groupmember.FieldUserID).All(ctx)
			if err != nil {
				return nil, err
			}
			userIDs := make([]uuid.UUID, 0, len(members))
			for _, member := range members {
				userIDs = append(userIDs, member.UserID)
			}
			return userIDs, nil
		default:
			return nil, nil
		}
	}, func(ctx context.Context, senderID, targetID uuid.UUID) (bool, error) {
		return entClient.UserBlock.Query().Where(
			userblock.Or(
				userblock.BlockerID(senderID),
				userblock.BlockedID(senderID),
			),
			userblock.Or(
				userblock.BlockerID(targetID),
				userblock.BlockedID(targetID),
			),
		).Exist(ctx)
	})
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
