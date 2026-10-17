package presence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"AtoiTalkAPI/internal/messaging/events"
	messagingpubsub "AtoiTalkAPI/internal/messaging/pubsub"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestPresenceManagerPublishesFilteredOnlineAndOfflineEvents(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()

	userID := uuid.New()
	recipientID := uuid.New()
	updated := make(chan uuid.UUID, 1)
	manager := NewManager(
		client,
		"gateway-1",
		func(_ context.Context, id uuid.UUID) error {
			updated <- id
			return nil
		},
		func(context.Context, uuid.UUID) ([]uuid.UUID, error) {
			return []uuid.UUID{recipientID}, nil
		},
		func(context.Context, uuid.UUID, uuid.UUID) (bool, error) {
			return false, nil
		},
	)

	pubsub := client.Subscribe(context.Background(), messagingpubsub.BroadcastChannel)
	defer pubsub.Close()
	_, err := pubsub.Receive(context.Background())
	require.NoError(t, err)

	manager.Connected(context.Background(), userID)
	require.Equal(t, int64(1), client.Exists(context.Background(), "online:"+userID.String()).Val())

	message, err := pubsub.ReceiveMessage(context.Background())
	require.NoError(t, err)
	var payload messagingpubsub.Payload
	require.NoError(t, json.Unmarshal([]byte(message.Payload), &payload))
	var event events.Event
	require.NoError(t, json.Unmarshal(payload.EventData, &event))
	require.Equal(t, recipientID.String(), payload.TargetUserID)
	require.Equal(t, events.EventUserOnline, event.Type)

	manager.Disconnected(context.Background(), userID)
	require.Equal(t, userID, <-updated)
	require.Equal(t, redis.Nil, client.Get(context.Background(), "online:"+userID.String()).Err())

	message, err = pubsub.ReceiveMessage(context.Background())
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal([]byte(message.Payload), &payload))
	require.NoError(t, json.Unmarshal(payload.EventData, &event))
	require.Equal(t, events.EventUserOffline, event.Type)
}

func TestPresenceManagerRefreshExtendsOnlineTTL(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()

	userID := uuid.New()
	manager := NewManager(client, "gateway-1", nil, nil, nil)
	ctx := context.Background()

	manager.Connected(ctx, userID)
	redisServer.FastForward(60 * time.Second)
	manager.Refresh(ctx, userID)
	redisServer.FastForward(20 * time.Second)

	require.Equal(t, int64(1), client.Exists(ctx, "online:"+userID.String()).Val())
}

func TestPresenceManagerKeepsUserOnlineWhileAnotherInstanceIsConnected(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()

	ctx := context.Background()
	userID := uuid.New()
	lastSeenCalls := 0
	newManager := func(instanceID string) *PresenceManager {
		return NewManager(client, instanceID, func(context.Context, uuid.UUID) error {
			lastSeenCalls++
			return nil
		}, nil, nil)
	}
	first := newManager("gateway-1")
	second := newManager("gateway-2")

	first.Connected(ctx, userID)
	second.Connected(ctx, userID)
	first.Disconnected(ctx, userID)

	require.Equal(t, int64(1), client.Exists(ctx, "online:"+userID.String()).Val())
	require.Equal(t, 0, lastSeenCalls)

	second.Disconnected(ctx, userID)

	require.Equal(t, int64(0), client.Exists(ctx, "online:"+userID.String()).Val())
	require.Equal(t, 1, lastSeenCalls)

	second.Disconnected(ctx, userID)
	require.Equal(t, 1, lastSeenCalls)
}

func TestPresenceManagerReconcilesExpiredInstanceOnce(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()

	ctx := context.Background()
	userID := uuid.New()
	lastSeenCalls := 0
	manager := NewManager(client, "gateway-1", func(context.Context, uuid.UUID) error {
		lastSeenCalls++
		return nil
	}, nil, nil)

	manager.Connected(ctx, userID)
	key := "presence:instances:" + userID.String()
	require.NoError(t, client.ZAddArgs(ctx, key, redis.ZAddArgs{XX: true, Members: []redis.Z{{Score: 0, Member: "gateway-1"}}}).Err())
	require.NoError(t, client.Del(ctx, "online:"+userID.String()).Err())

	require.NoError(t, manager.ReconcileExpired(ctx))
	require.Equal(t, 1, lastSeenCalls)
	require.Equal(t, int64(0), client.Exists(ctx, key).Val())

	require.NoError(t, manager.ReconcileExpired(ctx))
	require.Equal(t, 1, lastSeenCalls)
}

func TestPresenceManagerDoesNotReconcileWhileAnotherInstanceIsLive(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()

	ctx := context.Background()
	userID := uuid.New()
	lastSeenCalls := 0
	first := NewManager(client, "gateway-1", func(context.Context, uuid.UUID) error {
		lastSeenCalls++
		return nil
	}, nil, nil)
	second := NewManager(client, "gateway-2", nil, nil, nil)

	first.Connected(ctx, userID)
	redisServer.FastForward(60 * time.Second)
	second.Connected(ctx, userID)
	redisServer.FastForward(20 * time.Second)

	require.NoError(t, first.ReconcileExpired(ctx))
	require.Zero(t, lastSeenCalls)
	require.Equal(t, int64(1), client.Exists(ctx, "online:"+userID.String()).Val())
}

func TestPresenceReconcileLockOnlyReleasesCurrentOwner(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()

	ctx := context.Background()
	const key = "presence:reconcile:lock"
	require.NoError(t, client.Set(ctx, key, "new-owner", 0).Err())

	released, err := releaseReconcileLockScript.Run(ctx, client, []string{key}, "old-owner").Int64()
	require.NoError(t, err)
	require.Zero(t, released)
	require.Equal(t, "new-owner", client.Get(ctx, key).Val())

	released, err = releaseReconcileLockScript.Run(ctx, client, []string{key}, "new-owner").Int64()
	require.NoError(t, err)
	require.Equal(t, int64(1), released)
	require.Equal(t, redis.Nil, client.Get(ctx, key).Err())
}

func TestPresenceReconcileDoesNotClaimMissingLease(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()

	userID := uuid.New()
	claimed, err := reconcileScript.Run(
		context.Background(),
		client,
		[]string{"presence:instances:" + userID.String(), "online:" + userID.String()},
	).Int64()
	require.NoError(t, err)
	require.Zero(t, claimed)
}

func TestPresenceManagerLogsLastSeenUpdateFailure(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()
	var logs bytes.Buffer
	previousLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logs, nil)))
	t.Cleanup(func() { slog.SetDefault(previousLogger) })
	userID := uuid.New()
	manager := NewManager(client, "gateway-1", func(context.Context, uuid.UUID) error {
		return errors.New("database unavailable")
	}, nil, nil)

	manager.Connected(context.Background(), userID)
	manager.Disconnected(context.Background(), userID)

	require.True(t, strings.Contains(logs.String(), "failed to update last seen"), logs.String())
	require.True(t, strings.Contains(logs.String(), userID.String()), logs.String())
}
