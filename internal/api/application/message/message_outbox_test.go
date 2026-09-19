package message

import (
	"AtoiTalkAPI/ent/messageoutbox"
	"AtoiTalkAPI/internal/domain/model"
	websocket "AtoiTalkAPI/internal/messaging/events"
	websocketmocks "AtoiTalkAPI/internal/messaging/events/mocks"
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMessageServiceSendMessagePersistsOutboxWithoutDirectBroadcast(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, _ := createPrivateMessageServiceFixture(t, client)

	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher

	result, err := service.SendMessage(ctx, current.ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "durable message",
	})

	require.NoError(t, err)
	require.NotNil(t, result)

	outbox, err := client.MessageOutbox.Query().
		Where(messageoutbox.ChatID(chatEntity.ID), messageoutbox.EventTypeEQ(string(websocket.EventMessageNew))).
		Only(ctx)
	require.NoError(t, err)
	require.Equal(t, result.ID, outbox.MessageID)
	require.Equal(t, current.ID, *outbox.SenderID)
	publisher.AssertNotCalled(t, "BroadcastToChat")
}

func TestMessageServiceSendMessageRollsBackOutboxWithMessage(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, _ := createPrivateMessageServiceFixture(t, client)
	messageCount := client.Message.Query().CountX(ctx)
	outboxCount := client.MessageOutbox.Query().CountX(ctx)

	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err := service.SendMessage(canceled, current.ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "rolled back",
	})
	require.Error(t, err)

	require.Equal(t, messageCount, client.Message.Query().CountX(ctx))
	require.Equal(t, outboxCount, client.MessageOutbox.Query().CountX(ctx))
}

func TestMessageOutboxSupportsProjectionMarker(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, _ := createPrivateMessageServiceFixture(t, client)

	_, err := service.SendMessage(ctx, current.ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "projection marker",
	})
	require.NoError(t, err)

	outbox, err := client.MessageOutbox.Query().
		Where(messageoutbox.ChatID(chatEntity.ID), messageoutbox.ProjectedAtIsNil()).
		Only(ctx)
	require.NoError(t, err)
	require.Nil(t, outbox.ProjectedAt)
}

func TestMessageServiceEditMessagePersistsOutboxWithoutDirectBroadcast(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, messageEntity := createPrivateMessageServiceFixture(t, client)

	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher

	result, err := service.EditMessage(ctx, current.ID, messageEntity.ID, model.EditMessageRequest{
		Content: "edited durable message",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	outbox, err := client.MessageOutbox.Query().Where(
		messageoutbox.ChatID(chatEntity.ID),
		messageoutbox.MessageID(messageEntity.ID),
		messageoutbox.EventTypeEQ(string(websocket.EventMessageUpdate)),
	).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, messageEntity.ID, outbox.MessageID)
	publisher.AssertNotCalled(t, "BroadcastToChat")
}

func TestMessageServiceDeleteMessagePersistsOutboxWithoutDirectBroadcast(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, messageEntity := createPrivateMessageServiceFixture(t, client)

	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher

	require.NoError(t, service.DeleteMessage(ctx, current.ID, messageEntity.ID))
	outbox, err := client.MessageOutbox.Query().Where(
		messageoutbox.ChatID(chatEntity.ID),
		messageoutbox.MessageID(messageEntity.ID),
		messageoutbox.EventTypeEQ(string(websocket.EventMessageDelete)),
	).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, messageEntity.ID, outbox.MessageID)
	publisher.AssertNotCalled(t, "BroadcastToChat")
}
