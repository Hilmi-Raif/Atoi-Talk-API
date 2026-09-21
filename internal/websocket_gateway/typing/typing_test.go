package typing

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"AtoiTalkAPI/internal/messaging/events"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestTypingRouterPublishesOnlyToOtherChatMembers(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()

	senderID := uuid.New()
	recipientID := uuid.New()
	router := NewRouter(client, func(context.Context, uuid.UUID) ([]uuid.UUID, error) {
		return []uuid.UUID{senderID, recipientID}, nil
	})
	pubsub := client.Subscribe(context.Background(), "events:broadcast")
	defer pubsub.Close()
	_, err := pubsub.Receive(context.Background())
	require.NoError(t, err)
	event := events.Event{
		Type: events.EventTyping,
		Meta: &events.EventMeta{ChatID: uuid.New()},
	}

	require.NoError(t, router.Publish(context.Background(), senderID, event))

	message, err := pubsub.ReceiveMessage(context.Background())
	require.NoError(t, err)
	var payload PubSubPayload
	require.NoError(t, json.Unmarshal([]byte(message.Payload), &payload))
	require.Equal(t, recipientID.String(), payload.TargetUserID)

	var delivered events.Event
	require.NoError(t, json.Unmarshal(payload.EventData, &delivered))
	require.Equal(t, events.EventTyping, delivered.Type)
	require.Equal(t, senderID, delivered.Meta.SenderID)
	require.NotZero(t, delivered.Meta.Timestamp)

	require.NoError(t, router.Publish(context.Background(), senderID, event))
	select {
	case <-pubsub.Channel():
		t.Fatal("typing event was published during the throttle window")
	case <-time.After(100 * time.Millisecond):
	}
}

func TestTypingRouterSkipsBlockedRecipients(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()

	senderID := uuid.New()
	blockedRecipientID := uuid.New()
	allowedRecipientID := uuid.New()
	router := NewRouterWithBlockChecker(client, func(context.Context, uuid.UUID) ([]uuid.UUID, error) {
		return []uuid.UUID{senderID, blockedRecipientID, allowedRecipientID}, nil
	}, func(_ context.Context, from, to uuid.UUID) (bool, error) {
		return from == senderID && to == blockedRecipientID, nil
	})
	pubsub := client.Subscribe(context.Background(), legacyPubSubChannel)
	defer pubsub.Close()
	_, err := pubsub.Receive(context.Background())
	require.NoError(t, err)

	event := events.Event{
		Type: events.EventTyping,
		Meta: &events.EventMeta{ChatID: uuid.New()},
	}
	require.NoError(t, router.Publish(context.Background(), senderID, event))

	message, err := pubsub.ReceiveMessage(context.Background())
	require.NoError(t, err)
	var payload PubSubPayload
	require.NoError(t, json.Unmarshal([]byte(message.Payload), &payload))
	require.Equal(t, allowedRecipientID.String(), payload.TargetUserID)

	select {
	case message := <-pubsub.Channel():
		t.Fatalf("blocked recipient received typing event: %s", message.Payload)
	case <-time.After(100 * time.Millisecond):
	}
}
