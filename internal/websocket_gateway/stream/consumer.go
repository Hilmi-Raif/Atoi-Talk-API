package stream

import (
	"AtoiTalkAPI/internal/infrastructure/observability"
	"AtoiTalkAPI/internal/messaging/events"
	"context"
	"log/slog"
	"sync"

	"github.com/google/uuid"
)

type StreamMessage = events.StreamMessage

type StreamConsumer interface {
	Receive(context.Context) (*StreamMessage, error)
	Ack(context.Context, string) error
}

type LocalDelivery interface {
	Deliver(context.Context, uuid.UUID, []byte) error
}

type Consumer struct {
	stream      StreamConsumer
	delivery    LocalDelivery
	dedupeMu    sync.Mutex
	deliveredID map[string]struct{}
	dedupeOrder []string
}

func NewConsumer(stream StreamConsumer, delivery LocalDelivery) *Consumer {
	return &Consumer{
		stream:      stream,
		delivery:    delivery,
		deliveredID: make(map[string]struct{}),
	}
}

func (c *Consumer) Process(ctx context.Context) error {
	message, err := c.stream.Receive(ctx)
	if err != nil {
		return err
	}
	if message == nil {
		return nil
	}

	event, err := events.UnmarshalMessageEvent(message.Data)
	if err != nil {
		slog.WarnContext(ctx, "Ignoring malformed realtime message event", "stream_id", message.ID, "stage", "decode", "outcome", "discarded")
		return c.stream.Ack(ctx, message.ID)
	}
	ctx, span := observability.StartServiceSpan(observability.WithTraceParent(ctx, event.TraceParent), "gateway.deliver")
	defer span.End()
	logger := slog.Default().With("event_id", event.ID, "outbox_id", event.OutboxID, "message_id", event.MessageID, "stream_id", message.ID)
	if event.ID != uuid.Nil && c.wasDelivered(event.ID.String()) {
		return c.stream.Ack(ctx, message.ID)
	}
	if err := c.delivery.Deliver(ctx, event.TargetUserID, event.Payload); err != nil {
		logger.WarnContext(ctx, "Realtime delivery failed", "stage", "deliver", "outcome", "pending")
		observability.RecordError(span, err)
		return err
	}
	if event.ID != uuid.Nil {
		c.rememberDelivered(event.ID.String())
	}
	err = c.stream.Ack(ctx, message.ID)
	if err != nil {
		logger.ErrorContext(ctx, "Realtime acknowledgement failed", "stage", "ack", "outcome", "pending")
	}
	return err
}

const maxRememberedRealtimeEvents = 10000

func (c *Consumer) wasDelivered(id string) bool {
	c.dedupeMu.Lock()
	defer c.dedupeMu.Unlock()
	_, ok := c.deliveredID[id]
	return ok
}

func (c *Consumer) rememberDelivered(id string) {
	c.dedupeMu.Lock()
	defer c.dedupeMu.Unlock()
	if _, ok := c.deliveredID[id]; ok {
		return
	}
	c.deliveredID[id] = struct{}{}
	c.dedupeOrder = append(c.dedupeOrder, id)
	if len(c.dedupeOrder) > maxRememberedRealtimeEvents {
		oldest := c.dedupeOrder[0]
		delete(c.deliveredID, oldest)
		c.dedupeOrder = c.dedupeOrder[1:]
	}
}

func (c *Consumer) Run(ctx context.Context) error {
	for {
		err := c.Process(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
	}
}
