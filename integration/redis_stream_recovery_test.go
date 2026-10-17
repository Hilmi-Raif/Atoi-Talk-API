//go:build integration

package integration

import (
	redisinfra "AtoiTalkAPI/internal/infrastructure/redis"
	"AtoiTalkAPI/internal/messaging/events"
	"AtoiTalkAPI/internal/websocket_gateway/stream"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type recordingRealtimeDelivery struct {
	target  uuid.UUID
	payload []byte
	calls   int
}

func (d *recordingRealtimeDelivery) Deliver(_ context.Context, target uuid.UUID, payload []byte) error {
	d.target = target
	d.payload = payload
	d.calls++
	return nil
}

func TestRedisStreamConsumerDrainsMalformedEntryBeforeValidEntry(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	streamName := "integration:malformed:" + uuid.NewString()
	group := "integration:group:" + uuid.NewString()
	client := redisAdapter.Client()
	t.Cleanup(func() { _ = client.Del(context.Background(), streamName).Err() })
	consumer := redisinfra.NewRedisStreamConsumer(client, streamName, group, "test-consumer")
	require.NoError(t, consumer.EnsureGroup(ctx))
	malformedID := client.XAdd(ctx, &redis.XAddArgs{Stream: streamName, Values: map[string]interface{}{"other": "missing data"}}).Val()
	target := uuid.New()
	validData, err := (events.MessageEvent{ID: uuid.New(), TargetUserID: target, Payload: []byte(`{"type":"message.new"}`)}).Marshal()
	require.NoError(t, err)
	validID := client.XAdd(ctx, &redis.XAddArgs{Stream: streamName, Values: map[string]interface{}{"data": validData}}).Val()
	delivery := &recordingRealtimeDelivery{}
	processor := stream.NewConsumer(consumer, delivery)
	require.NoError(t, processor.Process(ctx))
	require.Zero(t, delivery.calls, "malformed event must not reach a client")
	require.NoError(t, processor.Process(ctx))
	require.Equal(t, 1, delivery.calls)
	require.Equal(t, target, delivery.target)
	require.Equal(t, []byte(`{"type":"message.new"}`), delivery.payload)
	require.Empty(t, client.XPendingExt(ctx, &redis.XPendingExtArgs{Stream: streamName, Group: group, Start: "-", End: "+", Count: 10}).Val())
	require.Len(t, client.XRange(ctx, streamName, malformedID, validID).Val(), 2)
	require.Zero(t, client.XPending(ctx, streamName, group).Val().Count)
}

func TestRedisStreamConsumerRecoversPendingDeliveryAfterRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	streamName := "integration:pending:" + uuid.NewString()
	group := "integration:group:" + uuid.NewString()
	client := redisAdapter.Client()
	t.Cleanup(func() { _ = client.Del(context.Background(), streamName).Err() })
	target := uuid.New()
	payload := []byte(`{"type":"message.new"}`)
	eventData, err := (events.MessageEvent{ID: uuid.New(), TargetUserID: target, Payload: payload}).Marshal()
	require.NoError(t, err)
	require.NoError(t, client.XGroupCreateMkStream(ctx, streamName, group, "0").Err())
	entryID := client.XAdd(ctx, &redis.XAddArgs{Stream: streamName, Values: map[string]interface{}{"data": eventData}}).Val()

	failedDelivery := &failingRealtimeDelivery{}
	first := redisinfra.NewRedisStreamConsumerWithPendingIdle(client, streamName, group, "gateway-before-restart", time.Hour)
	require.ErrorContains(t, stream.NewConsumer(first, failedDelivery).Process(ctx), "simulated delivery failure")
	require.EqualValues(t, 1, client.XPending(ctx, streamName, group).Val().Count)
	require.Equal(t, 1, failedDelivery.calls)
	recovered := &recordingRealtimeDelivery{}
	afterRestart := redisinfra.NewRedisStreamConsumerWithPendingIdle(client, streamName, group, "gateway-after-restart", 0)
	require.NoError(t, stream.NewConsumer(afterRestart, recovered).Process(ctx))
	require.Equal(t, 1, recovered.calls)
	require.Equal(t, target, recovered.target)
	require.Equal(t, payload, recovered.payload)
	require.Zero(t, client.XPending(ctx, streamName, group).Val().Count)
	require.Len(t, client.XRange(ctx, streamName, entryID, entryID).Val(), 1)
}

type failingRealtimeDelivery struct{ calls int }

func (d *failingRealtimeDelivery) Deliver(context.Context, uuid.UUID, []byte) error {
	d.calls++
	return errors.New("simulated delivery failure")
}
