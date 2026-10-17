package redis

import (
	"AtoiTalkAPI/internal/messaging/events"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	defaultRedisStreamMaxLen = 10000
	defaultRedisStreamGroup  = "message-workers"
	defaultRedisStreamBlock  = 5 * time.Second
	defaultRedisStreamIdle   = 30 * time.Second
)

var publishMessageEventScript = redis.NewScript(`
local existing
if ARGV[4] ~= '' then
  existing = redis.call('HGET', KEYS[2], ARGV[4])
else
  existing = redis.call('GET', KEYS[2])
end
if existing then
  return existing
end
local id = redis.call('XADD', KEYS[1], 'MAXLEN', '~', ARGV[1], '*', 'data', ARGV[2])
if ARGV[4] ~= '' then
  redis.call('HSET', KEYS[2], ARGV[4], id)
  redis.call('EXPIRE', KEYS[2], ARGV[3])
else
  redis.call('SET', KEYS[2], id, 'EX', ARGV[3])
end
return id
`)

const publishedEventDedupeTTL = 24 * time.Hour

type RedisStreamPublisher struct {
	client    redis.UniversalClient
	stream    string
	maxLen    int64
	dedupeTTL time.Duration
}

func NewRedisStreamPublisher(client redis.UniversalClient, stream string, maxLen int64) *RedisStreamPublisher {
	return NewRedisStreamPublisherWithDedupeTTL(client, stream, maxLen, publishedEventDedupeTTL)
}

func NewRedisStreamPublisherWithDedupeTTL(client redis.UniversalClient, stream string, maxLen int64, dedupeTTL time.Duration) *RedisStreamPublisher {
	if maxLen <= 0 {
		maxLen = defaultRedisStreamMaxLen
	}
	if dedupeTTL <= 0 {
		dedupeTTL = publishedEventDedupeTTL
	}
	return &RedisStreamPublisher{client: client, stream: stream, maxLen: maxLen, dedupeTTL: dedupeTTL}
}

func (p *RedisStreamPublisher) Publish(ctx context.Context, event events.MessageEvent) (string, error) {
	data, err := event.MarshalContext(ctx)
	if err != nil {
		return "", fmt.Errorf("marshal message event: %w", err)
	}
	if event.ID != uuid.Nil {
		dedupeKey := "stream:event:published:" + p.stream + ":" + event.ID.String()
		dedupeField := ""
		if event.OutboxID != uuid.Nil {
			dedupeKey = "stream:event:published:" + p.stream + ":" + event.OutboxID.String()
			dedupeField = event.ID.String()
		}
		id, err := publishMessageEventScript.Run(
			ctx,
			p.client,
			[]string{p.stream, dedupeKey},
			p.maxLen,
			data,
			int64(p.dedupeTTL/time.Second),
			dedupeField,
		).Text()
		if err != nil {
			return "", fmt.Errorf("publish message event: %w", err)
		}
		return id, nil
	}

	return p.client.XAdd(ctx, &redis.XAddArgs{
		Stream: p.stream,
		MaxLen: p.maxLen,
		Approx: true,
		Values: map[string]interface{}{"data": data},
	}).Result()
}

type RedisStreamConsumer struct {
	client     redis.UniversalClient
	stream     string
	group      string
	consumer   string
	groupStart string
	minIdle    time.Duration
	buffer     []*events.StreamMessage
}

func NewRedisStreamConsumer(client redis.UniversalClient, stream, group, consumer string) *RedisStreamConsumer {
	return newRedisStreamConsumer(client, stream, group, consumer, "0", defaultRedisStreamIdle)
}

func NewRedisStreamConsumerFrom(client redis.UniversalClient, stream, group, consumer, groupStart string) *RedisStreamConsumer {
	return newRedisStreamConsumer(client, stream, group, consumer, groupStart, defaultRedisStreamIdle)
}

func NewRedisStreamConsumerWithPendingIdle(client redis.UniversalClient, stream, group, consumer string, minIdle time.Duration) *RedisStreamConsumer {
	return newRedisStreamConsumer(client, stream, group, consumer, "0", minIdle)
}

func newRedisStreamConsumer(client redis.UniversalClient, stream, group, consumer, groupStart string, minIdle time.Duration) *RedisStreamConsumer {
	if group == "" {
		group = defaultRedisStreamGroup
	}
	if consumer == "" {
		consumer = "consumer"
	}
	if minIdle < 0 {
		minIdle = defaultRedisStreamIdle
	}
	if strings.TrimSpace(groupStart) == "" {
		groupStart = "0"
	}
	return &RedisStreamConsumer{client: client, stream: stream, group: group, consumer: consumer, groupStart: groupStart, minIdle: minIdle}
}

func (c *RedisStreamConsumer) EnsureGroup(ctx context.Context) error {
	err := c.client.XGroupCreateMkStream(ctx, c.stream, c.group, c.groupStart).Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		return fmt.Errorf("create Redis Stream consumer group: %w", err)
	}
	return nil
}

func (c *RedisStreamConsumer) Receive(ctx context.Context) (*events.StreamMessage, error) {
	if len(c.buffer) > 0 {
		msg := c.buffer[0]
		c.buffer = c.buffer[1:]
		return msg, nil
	}

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
		if messages := streamMessagesFromRedis(claimed); len(messages) > 0 {
			c.buffer = append(c.buffer, messages[1:]...)
			return messages[0], nil
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
		if messages := streamMessagesFromRedis(stream.Messages); len(messages) > 0 {
			c.buffer = append(c.buffer, messages[1:]...)
			return messages[0], nil
		}
	}
	return nil, nil
}

func streamMessagesFromRedis(messages []redis.XMessage) []*events.StreamMessage {
	if len(messages) == 0 {
		return nil
	}
	res := make([]*events.StreamMessage, 0, len(messages))
	for _, message := range messages {
		data, ok := message.Values["data"]
		if !ok {
			res = append(res, &events.StreamMessage{ID: message.ID})
			continue
		}
		payload, ok := data.(string)
		if !ok {
			if bytes, ok := data.([]byte); ok {
				res = append(res, &events.StreamMessage{ID: message.ID, Data: bytes})
				continue
			}
			res = append(res, &events.StreamMessage{ID: message.ID})
			continue
		}
		res = append(res, &events.StreamMessage{ID: message.ID, Data: []byte(payload)})
	}
	return res
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
