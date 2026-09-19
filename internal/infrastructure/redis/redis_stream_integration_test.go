package redis_test

import (
	redisinfra "AtoiTalkAPI/internal/infrastructure/redis"
	"AtoiTalkAPI/internal/messaging/events"
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRedisStreamPublisherAndConsumerRoundTrip(t *testing.T) {
	if os.Getenv("REDIS_STREAM_INTEGRATION") != "1" {
		t.Skip("set REDIS_STREAM_INTEGRATION=1 to run against Redis")
	}

	addr := os.Getenv("REDIS_STREAM_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Ping(ctx).Err())

	suffix := uuid.NewString()
	stream := "test:events:" + suffix
	group := "test-group:" + suffix
	consumerName := "test-consumer:" + suffix

	event := events.MessageEvent{
		ID:           uuid.New(),
		OutboxID:     uuid.New(),
		Type:         string(events.EventMessageNew),
		MessageID:    uuid.New(),
		ChatID:       uuid.New(),
		SenderID:     uuid.New(),
		TargetUserID: uuid.New(),
		Payload:      []byte(`{"type":"message.new"}`),
	}

	publisher := redisinfra.NewRedisStreamPublisher(client, stream, 1000)
	_, err := publisher.Publish(ctx, event)
	require.NoError(t, err)

	consumer := redisinfra.NewRedisStreamConsumer(client, stream, group, consumerName)
	require.NoError(t, consumer.EnsureGroup(ctx))

	message, err := consumer.Receive(ctx)
	require.NoError(t, err)
	require.NotNil(t, message)

	decoded, err := events.UnmarshalMessageEvent(message.Data)
	require.NoError(t, err)
	require.Equal(t, event, decoded)
	require.NoError(t, consumer.Ack(ctx, message.ID))

	entries, err := client.XPending(ctx, stream, group).Result()
	require.NoError(t, err)
	require.Zero(t, entries.Count)

	require.NoError(t, client.Del(ctx, stream).Err())
}

func TestRedisStreamConsumerReclaimsPendingMessage(t *testing.T) {
	if os.Getenv("REDIS_STREAM_INTEGRATION") != "1" {
		t.Skip("set REDIS_STREAM_INTEGRATION=1 to run against Redis")
	}

	addr := os.Getenv("REDIS_STREAM_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	client := redis.NewClient(&redis.Options{Addr: addr})
	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	require.NoError(t, client.Ping(ctx).Err())

	suffix := uuid.NewString()
	stream := "test:events:" + suffix
	group := "test-group:" + suffix

	event := events.MessageEvent{
		ID:           uuid.New(),
		OutboxID:     uuid.New(),
		Type:         string(events.EventMessageNew),
		MessageID:    uuid.New(),
		ChatID:       uuid.New(),
		SenderID:     uuid.New(),
		TargetUserID: uuid.New(),
		Payload:      []byte(`{"type":"message.new"}`),
	}
	publisher := redisinfra.NewRedisStreamPublisher(client, stream, 1000)
	_, err := publisher.Publish(ctx, event)
	require.NoError(t, err)

	crashed := redisinfra.NewRedisStreamConsumerWithPendingIdle(client, stream, group, "consumer-a-"+suffix, 0)
	require.NoError(t, crashed.EnsureGroup(ctx))
	pending, err := crashed.Receive(ctx)
	require.NoError(t, err)
	require.NotNil(t, pending)

	recovered := redisinfra.NewRedisStreamConsumerWithPendingIdle(client, stream, group, "consumer-b-"+suffix, 0)
	recoveredMessage, err := recovered.Receive(ctx)
	require.NoError(t, err)
	require.NotNil(t, recoveredMessage)
	decoded, err := events.UnmarshalMessageEvent(recoveredMessage.Data)
	require.NoError(t, err)
	require.Equal(t, event, decoded)
	require.NoError(t, recovered.Ack(ctx, recoveredMessage.ID))

	entries, err := client.XPending(ctx, stream, group).Result()
	require.NoError(t, err)
	require.Zero(t, entries.Count)
	require.NoError(t, client.Del(ctx, stream).Err())
}
