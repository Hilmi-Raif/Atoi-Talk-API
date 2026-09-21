package presence

import (
	"context"
	"encoding/json"
	"testing"

	"AtoiTalkAPI/internal/messaging/events"
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

	pubsub := client.Subscribe(context.Background(), legacyPubSubChannel)
	defer pubsub.Close()
	_, err := pubsub.Receive(context.Background())
	require.NoError(t, err)

	manager.Connected(context.Background(), userID)
	require.Equal(t, "true", client.Get(context.Background(), "online:"+userID.String()).Val())

	message, err := pubsub.ReceiveMessage(context.Background())
	require.NoError(t, err)
	var payload PubSubPayload
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
