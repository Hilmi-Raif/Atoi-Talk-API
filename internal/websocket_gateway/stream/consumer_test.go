package stream

import (
	"context"
	"errors"
	"testing"

	"AtoiTalkAPI/internal/messaging/events"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type loopingStreamConsumer struct {
	receives int
	cancel   context.CancelFunc
}

func (c *loopingStreamConsumer) Receive(ctx context.Context) (*StreamMessage, error) {
	c.receives++
	if c.receives == 1 {
		c.cancel()
		return nil, nil
	}
	return nil, ctx.Err()
}

func (c *loopingStreamConsumer) Ack(context.Context, string) error { return nil }

type fakeStreamConsumer struct {
	message *StreamMessage
	acked   []string
}

func (c *fakeStreamConsumer) Receive(context.Context) (*StreamMessage, error) {
	return c.message, nil
}

func (c *fakeStreamConsumer) Ack(_ context.Context, id string) error {
	c.acked = append(c.acked, id)
	return nil
}

type fakeLocalDelivery struct {
	userID uuid.UUID
	data   []byte
	err    error
}

func (d *fakeLocalDelivery) Deliver(_ context.Context, userID uuid.UUID, data []byte) error {
	d.userID = userID
	d.data = append([]byte(nil), data...)
	return d.err
}

func TestConsumerDeliversBeforeAcknowledgingStreamMessage(t *testing.T) {
	event := events.MessageEvent{
		ID:           uuid.New(),
		Type:         string(events.EventMessageNew),
		TargetUserID: uuid.New(),
		Payload:      []byte(`{"type":"message.new"}`),
	}
	data, err := event.Marshal()
	require.NoError(t, err)
	stream := &fakeStreamConsumer{message: &StreamMessage{ID: "stream-1", Data: data}}
	delivery := &fakeLocalDelivery{}
	consumer := NewConsumer(stream, delivery)

	require.NoError(t, consumer.Process(context.Background()))
	require.Equal(t, event.TargetUserID, delivery.userID)
	require.Equal(t, event.Payload, delivery.data)
	require.Equal(t, []string{"stream-1"}, stream.acked)
}

func TestConsumerDoesNotAcknowledgeWhenLocalDeliveryFails(t *testing.T) {
	event := events.MessageEvent{ID: uuid.New(), TargetUserID: uuid.New(), Payload: []byte("payload")}
	data, err := event.Marshal()
	require.NoError(t, err)
	stream := &fakeStreamConsumer{message: &StreamMessage{ID: "stream-2", Data: data}}
	delivery := &fakeLocalDelivery{err: errors.New("send buffer full")}
	consumer := NewConsumer(stream, delivery)

	require.Error(t, consumer.Process(context.Background()))
	require.Empty(t, stream.acked)
}

func TestConsumerDoesNotDeliverDuplicateEventIDTwice(t *testing.T) {
	event := events.MessageEvent{
		ID:           uuid.New(),
		OutboxID:     uuid.New(),
		Type:         string(events.EventMessageNew),
		TargetUserID: uuid.New(),
		Payload:      []byte(`{"type":"message.new"}`),
	}
	data, err := event.Marshal()
	require.NoError(t, err)
	stream := &repeatingStreamConsumer{
		messages: []*StreamMessage{
			{ID: "stream-duplicate-1", Data: data},
			{ID: "stream-duplicate-2", Data: data},
		},
	}
	delivery := &countingLocalDelivery{}
	consumer := NewConsumer(stream, delivery)

	require.NoError(t, consumer.Process(context.Background()))
	require.NoError(t, consumer.Process(context.Background()))
	require.Equal(t, 1, delivery.calls)
	require.Equal(t, []string{"stream-duplicate-1", "stream-duplicate-2"}, stream.acked)
}

type repeatingStreamConsumer struct {
	messages []*StreamMessage
	acked    []string
}

func (c *repeatingStreamConsumer) Receive(context.Context) (*StreamMessage, error) {
	if len(c.messages) == 0 {
		return nil, nil
	}
	message := c.messages[0]
	c.messages = c.messages[1:]
	return message, nil
}

func (c *repeatingStreamConsumer) Ack(_ context.Context, id string) error {
	c.acked = append(c.acked, id)
	return nil
}

type countingLocalDelivery struct {
	calls int
}

func (d *countingLocalDelivery) Deliver(context.Context, uuid.UUID, []byte) error {
	d.calls++
	return nil
}

func TestConsumerRunStopsWhenStreamContextIsCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := &loopingStreamConsumer{cancel: cancel}
	consumer := NewConsumer(stream, &fakeLocalDelivery{})

	require.NoError(t, consumer.Run(ctx))
	require.Equal(t, 1, stream.receives)
}
