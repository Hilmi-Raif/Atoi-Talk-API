package pubsub

import (
	"context"
	"encoding/json"
	"log/slog"

	"github.com/redis/go-redis/v9"
)

type Payload struct {
	TargetUserID string `json:"target_user_id"`
	EventData    []byte `json:"event_data"`
}

type PubSubListener struct {
	client  redis.UniversalClient
	channel string
	deliver func(context.Context, string, []byte) error
	ready   chan struct{}
}

type Delivery interface {
	DeliverString(context.Context, string, []byte) error
}

func NewPubSubListener(client redis.UniversalClient, channel string, delivery Delivery) *PubSubListener {
	return &PubSubListener{
		client:  client,
		channel: channel,
		deliver: delivery.DeliverString,
		ready:   make(chan struct{}),
	}
}

func (l *PubSubListener) Ready() <-chan struct{} {
	if l == nil {
		return nil
	}
	return l.ready
}

func (l *PubSubListener) Run(ctx context.Context) error {
	if l == nil || l.client == nil || l.channel == "" || l.deliver == nil {
		return nil
	}

	pubsub := l.client.Subscribe(ctx, l.channel)
	defer func() { _ = pubsub.Close() }()

	if _, err := pubsub.Receive(ctx); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		return err
	}
	close(l.ready)

	for {
		select {
		case <-ctx.Done():
			return nil
		case msg, ok := <-pubsub.Channel():
			if !ok {
				return nil
			}

			var payload Payload
			if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
				slog.ErrorContext(ctx, "failed to decode realtime Pub/Sub event", "error", err)
				continue
			}
			if err := l.deliver(ctx, payload.TargetUserID, payload.EventData); err != nil && ctx.Err() == nil {
				slog.ErrorContext(ctx, "failed to deliver realtime Pub/Sub event", "error", err)
			}
		}
	}
}
