package redis

import (
	"AtoiTalkAPI/internal/messaging/events"
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

type mockUniversalClient struct {
	redis.UniversalClient
	xReadGroupFn           func(ctx context.Context, args *redis.XReadGroupArgs) *redis.XStreamSliceCmd
	xAutoClaimFn           func(ctx context.Context, args *redis.XAutoClaimArgs) *redis.XAutoClaimCmd
	xAckFn                 func(ctx context.Context, stream, group string, ids ...string) *redis.IntCmd
	xGroupCreateMkStreamFn func(ctx context.Context, stream, group, start string) *redis.StatusCmd
}

func (m *mockUniversalClient) XGroupCreateMkStream(ctx context.Context, stream, group, start string) *redis.StatusCmd {
	if m.xGroupCreateMkStreamFn != nil {
		return m.xGroupCreateMkStreamFn(ctx, stream, group, start)
	}
	cmd := redis.NewStatusCmd(ctx)
	cmd.SetVal("OK")
	return cmd
}

func (m *mockUniversalClient) XReadGroup(ctx context.Context, args *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
	if m.xReadGroupFn != nil {
		return m.xReadGroupFn(ctx, args)
	}
	cmd := redis.NewXStreamSliceCmd(ctx)
	cmd.SetVal(nil)
	return cmd
}

func (m *mockUniversalClient) XAutoClaim(ctx context.Context, args *redis.XAutoClaimArgs) *redis.XAutoClaimCmd {
	if m.xAutoClaimFn != nil {
		return m.xAutoClaimFn(ctx, args)
	}
	cmd := redis.NewXAutoClaimCmd(ctx)
	cmd.SetVal([]redis.XMessage{}, "0-0")
	return cmd
}

func (m *mockUniversalClient) XAck(ctx context.Context, stream, group string, ids ...string) *redis.IntCmd {
	if m.xAckFn != nil {
		return m.xAckFn(ctx, stream, group, ids...)
	}
	cmd := redis.NewIntCmd(ctx)
	cmd.SetVal(1)
	return cmd
}

func TestRedisStreamConsumerBuffersBatchMessages(t *testing.T) {
	ctx := context.Background()
	calls := 0

	mockClient := &mockUniversalClient{
		xAutoClaimFn: func(ctx context.Context, args *redis.XAutoClaimArgs) *redis.XAutoClaimCmd {
			cmd := redis.NewXAutoClaimCmd(ctx)
			cmd.SetVal([]redis.XMessage{}, "0-0")
			return cmd
		},
		xReadGroupFn: func(ctx context.Context, args *redis.XReadGroupArgs) *redis.XStreamSliceCmd {
			calls++
			cmd := redis.NewXStreamSliceCmd(ctx)
			if calls == 1 {
				cmd.SetVal([]redis.XStream{
					{
						Stream: "test-stream",
						Messages: []redis.XMessage{
							{ID: "1-0", Values: map[string]interface{}{"data": "msg-1"}},
							{ID: "2-0", Values: map[string]interface{}{"data": "msg-2"}},
							{ID: "3-0", Values: map[string]interface{}{"data": "msg-3"}},
						},
					},
				})
			} else {
				cmd.SetVal(nil)
			}
			return cmd
		},
	}

	consumer := NewRedisStreamConsumer(mockClient, "test-stream", "test-group", "test-c")

	msg1, err := consumer.Receive(ctx)
	require.NoError(t, err)
	require.NotNil(t, msg1)
	require.Equal(t, "1-0", msg1.ID)
	require.Equal(t, []byte("msg-1"), msg1.Data)
	require.Equal(t, 1, calls)

	msg2, err := consumer.Receive(ctx)
	require.NoError(t, err)
	require.NotNil(t, msg2)
	require.Equal(t, "2-0", msg2.ID)
	require.Equal(t, []byte("msg-2"), msg2.Data)
	require.Equal(t, 1, calls, "should drain buffered message without calling XReadGroup again")

	msg3, err := consumer.Receive(ctx)
	require.NoError(t, err)
	require.NotNil(t, msg3)
	require.Equal(t, "3-0", msg3.ID)
	require.Equal(t, []byte("msg-3"), msg3.Data)
	require.Equal(t, 1, calls, "should drain buffered message without calling XReadGroup again")

	msg4, err := consumer.Receive(ctx)
	require.NoError(t, err)
	require.Nil(t, msg4)
	require.Equal(t, 2, calls)
}

func TestRedisStreamConsumerCreatesGroupAtConfiguredStart(t *testing.T) {
	ctx := context.Background()
	var starts []string
	mockClient := &mockUniversalClient{
		xGroupCreateMkStreamFn: func(ctx context.Context, stream, group, start string) *redis.StatusCmd {
			starts = append(starts, start)
			cmd := redis.NewStatusCmd(ctx)
			cmd.SetVal("OK")
			return cmd
		},
	}

	replayConsumer := NewRedisStreamConsumer(mockClient, "events", "workers", "worker-a")
	require.NoError(t, replayConsumer.EnsureGroup(ctx))

	liveConsumer := NewRedisStreamConsumerFrom(mockClient, "events", "realtime-a", "gateway-a", "$")
	require.NoError(t, liveConsumer.EnsureGroup(ctx))

	require.Equal(t, []string{"0", "$"}, starts)
}

func TestRedisStreamPublisherPublishesEventIDOnlyOnce(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	publisher := NewRedisStreamPublisher(client, "events", 1000)
	event := events.MessageEvent{ID: uuid.New(), TargetUserID: uuid.New(), Payload: []byte(`{"type":"message.new"}`)}

	firstID, err := publisher.Publish(ctx, event)
	require.NoError(t, err)
	secondID, err := publisher.Publish(ctx, event)
	require.NoError(t, err)

	require.Equal(t, firstID, secondID)
	require.Equal(t, int64(1), client.XLen(ctx, "events").Val())
}

func TestRedisStreamPublisherGroupsDedupeEntriesByOutbox(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	ctx := context.Background()
	publisher := NewRedisStreamPublisher(client, "events", 1000)
	outboxID := uuid.New()

	for range 2 {
		event := events.MessageEvent{ID: uuid.New(), OutboxID: outboxID, TargetUserID: uuid.New(), Payload: []byte(`{"type":"message.new"}`)}
		_, err := publisher.Publish(ctx, event)
		require.NoError(t, err)
	}

	keys := server.Keys()
	require.Contains(t, keys, "stream:event:published:events:"+outboxID.String())
	require.Len(t, keys, 2)
	require.Equal(t, int64(2), client.HLen(ctx, "stream:event:published:events:"+outboxID.String()).Val())
	require.Equal(t, 24*time.Hour, server.TTL("stream:event:published:events:"+outboxID.String()))
}
