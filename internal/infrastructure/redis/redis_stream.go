package redis

import (
	"AtoiTalkAPI/internal/messaging/events"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

const (
	defaultRedisStreamMaxLen = 10000
	defaultRedisStreamGroup  = "message-workers"
	defaultRedisStreamBlock  = 5 * time.Second
	defaultRedisStreamIdle   = 30 * time.Second
)

type RedisStreamPublisher struct {
	client redis.UniversalClient
	stream string
	maxLen int64
}

func NewRedisStreamPublisher(client redis.UniversalClient, stream string, maxLen int64) *RedisStreamPublisher {
	if maxLen <= 0 {
		maxLen = defaultRedisStreamMaxLen
	}
	return &RedisStreamPublisher{client: client, stream: stream, maxLen: maxLen}
}

func (p *RedisStreamPublisher) Publish(ctx context.Context, event events.MessageEvent) (string, error) {
	data, err := event.MarshalContext(ctx)
	if err != nil {
		return "", fmt.Errorf("marshal message event: %w", err)
	}

	return p.client.XAdd(ctx, &redis.XAddArgs{
		Stream: p.stream,
		MaxLen: p.maxLen,
		Approx: true,
		Values: map[string]interface{}{"data": data},
	}).Result()
}

type RedisStreamConsumer struct {
	client   redis.UniversalClient
	stream   string
	group    string
	consumer string
	minIdle  time.Duration
}

func NewRedisStreamConsumer(client redis.UniversalClient, stream, group, consumer string) *RedisStreamConsumer {
	return NewRedisStreamConsumerWithPendingIdle(client, stream, group, consumer, defaultRedisStreamIdle)
}

func NewRedisStreamConsumerWithPendingIdle(client redis.UniversalClient, stream, group, consumer string, minIdle time.Duration) *RedisStreamConsumer {
	if group == "" {
		group = defaultRedisStreamGroup
	}
	if consumer == "" {
		consumer = "consumer"
	}
	if minIdle < 0 {
		minIdle = defaultRedisStreamIdle
	}
	return &RedisStreamConsumer{client: client, stream: stream, group: group, consumer: consumer, minIdle: minIdle}
}

func (c *RedisStreamConsumer) EnsureGroup(ctx context.Context) error {
	err := c.client.XGroupCreateMkStream(ctx, c.stream, c.group, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("create Redis Stream consumer group: %w", err)
	}
	return nil
}

func (c *RedisStreamConsumer) Receive(ctx context.Context) (*events.StreamMessage, error) {
	if c.minIdle >= 0 {
		claimed, _, err := c.client.XAutoClaim(ctx, &redis.XAutoClaimArgs{
			Stream:   c.stream,
			Group:    c.group,
			Consumer: c.consumer,
			MinIdle:  c.minIdle,
			Start:    "0-0",
			Count:    10,
		}).Result()
		if err != nil && !errors.Is(err, redis.Nil) {
			return nil, fmt.Errorf("claim pending Redis Stream messages: %w", err)
		}
		if message, err := streamMessageFromRedis(claimed); err != nil {
			return nil, err
		} else if message != nil {
			return message, nil
		}
	}

	result, err := c.client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    c.group,
		Consumer: c.consumer,
		Streams:  []string{c.stream, ">"},
		Count:    10,
		Block:    defaultRedisStreamBlock,
		NoAck:    false,
	}).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return nil, nil
		}
		return nil, fmt.Errorf("read Redis Stream: %w", err)
	}
	for _, stream := range result {
		if message, err := streamMessageFromRedis(stream.Messages); err != nil {
			return nil, err
		} else if message != nil {
			return message, nil
		}
	}
	return nil, nil
}

func streamMessageFromRedis(messages []redis.XMessage) (*events.StreamMessage, error) {
	if len(messages) == 0 {
		return nil, nil
	}
	message := messages[0]
	data, ok := message.Values["data"]
	if !ok {
		return nil, fmt.Errorf("redis stream message %s has no data", message.ID)
	}
	payload, ok := data.(string)
	if !ok {
		if bytes, ok := data.([]byte); ok {
			return &events.StreamMessage{ID: message.ID, Data: bytes}, nil
		}
		return nil, fmt.Errorf("redis stream message %s data has invalid type", message.ID)
	}
	return &events.StreamMessage{ID: message.ID, Data: []byte(payload)}, nil
}

func (c *RedisStreamConsumer) Ack(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("ack Redis Stream message: empty message ID")
	}
	if err := c.client.XAck(ctx, c.stream, c.group, id).Err(); err != nil {
		return fmt.Errorf("ack Redis Stream message %s: %w", id, err)
	}
	return nil
}
