package pubsub

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

func TestPublishToUserUsesSharedBroadcastEnvelope(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	listener := client.Subscribe(context.Background(), BroadcastChannel)
	t.Cleanup(func() { _ = listener.Close() })
	_, err := listener.Receive(context.Background())
	require.NoError(t, err)
	targetID := uuid.New()

	require.NoError(t, PublishToUser(context.Background(), client, BroadcastChannel, targetID, events.Event{Type: events.EventTyping}))

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	message, err := listener.ReceiveMessage(ctx)
	require.NoError(t, err)
	var payload Payload
	require.NoError(t, json.Unmarshal([]byte(message.Payload), &payload))
	require.Equal(t, targetID.String(), payload.TargetUserID)
	var event events.Event
	require.NoError(t, json.Unmarshal(payload.EventData, &event))
	require.Equal(t, events.EventTyping, event.Type)
}
