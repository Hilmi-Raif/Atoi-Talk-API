package connection

import (
	"context"
	"encoding/json"
	"testing"

	"AtoiTalkAPI/internal/messaging/events"
	"AtoiTalkAPI/internal/websocket_gateway/typing"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

const legacyPubSubChannel = "events:broadcast"

func TestHTTPHandlerRoutesTypingInputThroughPubSub(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()

	senderID := uuid.New()
	recipientID := uuid.New()
	router := typing.NewRouter(client, func(context.Context, uuid.UUID) ([]uuid.UUID, error) {
		return []uuid.UUID{senderID, recipientID}, nil
	})
	handler := NewHTTPHandlerWithTypingRouter(NewGateway(), nil, nil, router)
	event, err := json.Marshal(events.Event{
		Type: events.EventTyping,
		Meta: &events.EventMeta{ChatID: uuid.New()},
	})
	require.NoError(t, err)

	pubsub := client.Subscribe(context.Background(), legacyPubSubChannel)
	defer pubsub.Close()
	_, err = pubsub.Receive(context.Background())
	require.NoError(t, err)

	require.NoError(t, handler.handleIncomingMessage(context.Background(), senderID, event))
	message, err := pubsub.ReceiveMessage(context.Background())
	require.NoError(t, err)
	var payload struct {
		TargetUserID string `json:"target_user_id"`
		EventData    []byte `json:"event_data"`
	}
	require.NoError(t, json.Unmarshal([]byte(message.Payload), &payload))
	require.Equal(t, recipientID.String(), payload.TargetUserID)
}
