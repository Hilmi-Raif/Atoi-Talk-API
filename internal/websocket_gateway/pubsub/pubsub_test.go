package pubsub

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestPubSubListenerDeliversLegacyEventsToLocalGateway(t *testing.T) {
	redisServer := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: redisServer.Addr()})
	defer client.Close()

	delivery := &testDelivery{data: make(chan []byte, 1)}
	listener := NewPubSubListener(client, "events:broadcast", delivery)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	listenerErr := make(chan error, 1)
	go func() { listenerErr <- listener.Run(ctx) }()
	select {
	case <-listener.Ready():
	case <-time.After(time.Second):
		t.Fatal("Pub/Sub listener did not subscribe")
	}

	payload, err := json.Marshal(pubSubPayload{
		TargetUserID: uuid.New().String(),
		EventData:    []byte(`{"type":"typing"}`),
	})
	require.NoError(t, err)
	require.NoError(t, client.Publish(ctx, "events:broadcast", payload).Err())

	select {
	case got := <-delivery.data:
		require.JSONEq(t, `{"type":"typing"}`, string(got))
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for Pub/Sub delivery")
	}

	cancel()
	select {
	case err := <-listenerErr:
		require.NoError(t, err)
	case <-time.After(time.Second):
		t.Fatal("Pub/Sub listener did not stop")
	}
}

type testDelivery struct {
	data chan []byte
}

func (d *testDelivery) DeliverString(_ context.Context, _ string, data []byte) error {
	d.data <- data
	return nil
}

type pubSubPayload = Payload
