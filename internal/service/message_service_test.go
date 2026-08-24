package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/ent/message"
	adaptermocks "AtoiTalkAPI/internal/adapter/mocks"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/repository"
	repositorymocks "AtoiTalkAPI/internal/repository/mocks"
	"AtoiTalkAPI/internal/websocket"
	websocketmocks "AtoiTalkAPI/internal/websocket/mocks"
	"context"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newMessageServiceTest(t *testing.T) (*MessageService, *ent.Client, *repositorymocks.MockMessageReader) {
	t.Helper()
	client := enttest.Open(t, dialect.SQLite, "file:message-service-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	messageRepo := repositorymocks.NewMockMessageReader(t)
	service := NewMessageService(
		client,
		messageRepo,
		&config.AppConfig{},
		config.NewValidator(),
		adaptermocks.NewMockURLGenerator(t),
		nil,
	)
	return service, client, messageRepo
}

func createPrivateMessageServiceFixture(t *testing.T, client *ent.Client) (context.Context, *ent.User, *ent.User, *ent.Chat, *ent.Message) {
	t.Helper()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SetFullName("Current").SaveX(ctx)
	other := client.User.Create().SetUsername("other").SetFullName("Other").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(current.ID).SetUser2ID(other.ID).SaveX(ctx)
	msg := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetSenderID(current.ID).
		SetType(message.TypeRegular).
		SetContent("hello").
		SaveX(ctx)
	return ctx, current, other, chatEntity, msg
}

func loadMessageServiceFixture(ctx context.Context, client *ent.Client, messageID uuid.UUID) *ent.Message {
	return client.Message.Query().
		Where(message.ID(messageID)).
		WithSender().
		OnlyX(ctx)
}

func TestMessageServiceGetMessagesMapsPrivateMessages(t *testing.T) {
	service, client, messageRepo := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, msg := createPrivateMessageServiceFixture(t, client)
	loadedChat := client.Chat.Query().
		Where(chat.ID(chatEntity.ID)).
		WithPrivateChat(func(query *ent.PrivateChatQuery) {
			query.WithUser1().WithUser2()
		}).
		OnlyX(ctx)
	loadedMessage := loadMessageServiceFixture(ctx, client, msg.ID)
	messageRepo.EXPECT().GetMessages(ctx, chatEntity.ID, (*time.Time)(nil), uuid.Nil, 10, "older").Return([]*ent.Message{loadedMessage}, nil)

	result, nextCursor, hasNext, prevCursor, hasPrev, err := service.GetMessages(ctx, current.ID, model.GetMessagesRequest{
		ChatID:    chatEntity.ID,
		Limit:     10,
		Direction: "older",
	})

	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, msg.ID, result[0].ID)
	require.Equal(t, "hello", result[0].Content)
	require.NotEmpty(t, nextCursor)
	require.NotEmpty(t, prevCursor)
	require.False(t, hasNext)
	require.False(t, hasPrev)
	_ = loadedChat
}

func TestMessageServiceGetMessagesEnrichesGroupSystemMessage(t *testing.T) {
	service, client, messageRepo := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SetFullName("Current").SaveX(ctx)
	actor := client.User.Create().SetUsername("actor").SetFullName("Actor").SaveX(ctx)
	target := client.User.Create().SetUsername("target").SetFullName("Target").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("group").SetInviteCode("group-invite").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(current.ID).SetRole(groupmember.RoleMember).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(actor.ID).SetRole(groupmember.RoleAdmin).SaveX(ctx)
	systemMessage := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetSenderID(actor.ID).
		SetType(message.TypeSystemAdd).
		SetActionData(map[string]interface{}{
			"target_id": target.ID.String(),
			"actor_id":  actor.ID.String(),
		}).
		SetCreatedAt(time.Now().UTC()).
		SaveX(ctx)
	loadedMessage := client.Message.Query().Where(message.ID(systemMessage.ID)).WithSender().OnlyX(ctx)
	messageRepo.EXPECT().GetMessages(ctx, chatEntity.ID, (*time.Time)(nil), uuid.Nil, 10, "older").Return([]*ent.Message{loadedMessage}, nil)

	result, _, _, _, _, err := service.GetMessages(ctx, current.ID, model.GetMessagesRequest{
		ChatID:    chatEntity.ID,
		Limit:     10,
		Direction: "older",
	})

	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, "admin", result[0].SenderRole)
	require.Equal(t, target.ID.String(), result[0].ActionData["target_id"])
	require.Equal(t, "Target", result[0].ActionData["target_name"])
	require.Equal(t, "Actor", result[0].ActionData["actor_name"])
}

func TestMessageServiceGetMessagesRejectsNonMember(t *testing.T) {
	service, client, messageRepo := newMessageServiceTest(t)
	defer client.Close()
	ctx, _, _, chatEntity, _ := createPrivateMessageServiceFixture(t, client)

	result, nextCursor, hasNext, prevCursor, hasPrev, err := service.GetMessages(ctx, uuid.New(), model.GetMessagesRequest{
		ChatID: chatEntity.ID,
		Limit:  10,
	})

	require.Nil(t, result)
	require.Empty(t, nextCursor)
	require.False(t, hasNext)
	require.Empty(t, prevCursor)
	require.False(t, hasPrev)
	require.Error(t, err)
	messageRepo.AssertNotCalled(t, "GetMessages", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestMessageServiceGetMessagesMapsRepositoryError(t *testing.T) {
	service, client, messageRepo := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, _ := createPrivateMessageServiceFixture(t, client)
	messageRepo.EXPECT().GetMessages(mock.Anything, chatEntity.ID, (*time.Time)(nil), uuid.Nil, 10, "older").Return(nil, errors.New("repository unavailable"))

	result, nextCursor, hasNext, prevCursor, hasPrev, err := service.GetMessages(ctx, current.ID, model.GetMessagesRequest{
		ChatID:    chatEntity.ID,
		Limit:     10,
		Direction: "older",
	})

	require.Nil(t, result)
	require.Empty(t, nextCursor)
	require.False(t, hasNext)
	require.Empty(t, prevCursor)
	require.False(t, hasPrev)
	require.Error(t, err)
}

func TestMessageServiceGetMessagesUsesAroundRepositoryPath(t *testing.T) {
	service, client, messageRepo := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, msg := createPrivateMessageServiceFixture(t, client)
	loadedMessage := loadMessageServiceFixture(ctx, client, msg.ID)
	messageRepo.EXPECT().GetMessagesAround(ctx, chatEntity.ID, (*time.Time)(nil), msg.ID, 10).
		Return([]*ent.Message{loadedMessage}, nil)
	aroundID := msg.ID

	result, nextCursor, hasNext, prevCursor, hasPrev, err := service.GetMessages(ctx, current.ID, model.GetMessagesRequest{
		ChatID:          chatEntity.ID,
		AroundMessageID: &aroundID,
		Limit:           10,
	})

	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, msg.ID, result[0].ID)
	require.NotEmpty(t, nextCursor)
	require.NotEmpty(t, prevCursor)
	require.False(t, hasNext)
	require.False(t, hasPrev)
}

func TestMessageServiceGetMessagesRejectsInvalidCursorBeforeRepository(t *testing.T) {
	service, client, messageRepo := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, _ := createPrivateMessageServiceFixture(t, client)

	result, nextCursor, hasNext, prevCursor, hasPrev, err := service.GetMessages(ctx, current.ID, model.GetMessagesRequest{
		ChatID:    chatEntity.ID,
		Cursor:    "not-base64",
		Direction: "older",
		Limit:     10,
	})

	require.Nil(t, result)
	require.Empty(t, nextCursor)
	require.False(t, hasNext)
	require.Empty(t, prevCursor)
	require.False(t, hasPrev)
	require.Error(t, err)
	messageRepo.AssertNotCalled(t, "GetMessages", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestMessageServiceGetMessagesMapsNotFoundRepositoryError(t *testing.T) {
	service, client, messageRepo := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, _ := createPrivateMessageServiceFixture(t, client)
	messageRepo.EXPECT().GetMessages(ctx, chatEntity.ID, (*time.Time)(nil), uuid.Nil, 10, "older").
		Return(nil, repository.ErrMessageNotFound)

	result, _, _, _, _, err := service.GetMessages(ctx, current.ID, model.GetMessagesRequest{
		ChatID:    chatEntity.ID,
		Direction: "older",
		Limit:     10,
	})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestMessageServiceDeleteMessageAllowsSender(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, _, msg := createPrivateMessageServiceFixture(t, client)

	require.NoError(t, service.DeleteMessage(ctx, current.ID, msg.ID))

	deleted := client.Message.GetX(ctx, msg.ID)
	require.NotNil(t, deleted.DeletedAt)
	require.Nil(t, deleted.Content)
}

func TestMessageServiceDeleteMessageRejectsUnauthorizedUser(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, _, _, _, msg := createPrivateMessageServiceFixture(t, client)

	err := service.DeleteMessage(ctx, uuid.New(), msg.ID)

	require.Error(t, err)
	require.Nil(t, client.Message.GetX(ctx, msg.ID).DeletedAt)
}

func TestMessageServiceDeleteMessageRejectsAlreadyDeletedMessage(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, _, msg := createPrivateMessageServiceFixture(t, client)
	client.Message.UpdateOneID(msg.ID).SetDeletedAt(time.Now()).SaveX(ctx)

	err := service.DeleteMessage(ctx, current.ID, msg.ID)

	require.Error(t, err)
}

func TestMessageServiceDeleteMessageAllowsGroupAdminAndBroadcasts(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	admin := client.User.Create().SetUsername("group-admin").SaveX(ctx)
	sender := client.User.Create().SetUsername("group-sender").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Group").SetInviteCode("group-invite").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(admin.ID).SetRole(groupmember.RoleAdmin).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(sender.ID).SetRole(groupmember.RoleMember).SaveX(ctx)
	msg := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetType(message.TypeRegular).SetContent("remove me").SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	deleted := make(chan websocket.Event, 1)
	publisher.EXPECT().BroadcastToChat(chatEntity.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventMessageDelete
	})).Run(func(_ uuid.UUID, event websocket.Event) { deleted <- event }).Once()
	service.wsHub = publisher

	require.NoError(t, service.DeleteMessage(ctx, admin.ID, msg.ID))
	updated := client.Message.GetX(ctx, msg.ID)
	require.NotNil(t, updated.DeletedAt)
	require.Nil(t, updated.Content)
	select {
	case event := <-deleted:
		require.Equal(t, websocket.EventMessageDelete, event.Type)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for delete event")
	}
}

func TestMessageServiceDeleteMessageRejectsNonRegularMessage(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, _ := createPrivateMessageServiceFixture(t, client)
	systemMessage := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetSenderID(current.ID).
		SetType(message.TypeSystemRename).
		SetContent("system").
		SaveX(ctx)

	err := service.DeleteMessage(ctx, current.ID, systemMessage.ID)

	require.Error(t, err)
	require.Nil(t, client.Message.GetX(ctx, systemMessage.ID).DeletedAt)
}

func TestMessageServiceSendMessageCreatesPrivateMessage(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	current := client.User.Create().SetUsername("sender").SetFullName("Sender").SaveX(ctx)
	other := client.User.Create().SetUsername("recipient").SetFullName("Recipient").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(current.ID).SetUser2ID(other.ID).SaveX(ctx)

	result, err := service.SendMessage(ctx, current.ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: " hello ",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "hello", result.Content)
	require.Equal(t, current.ID, *result.SenderID)
	require.Equal(t, "Sender", result.SenderName)
	require.Equal(t, chatEntity.ID, result.ChatID)
	require.NotNil(t, result.CreatedAt)

	privateChat := client.PrivateChat.Query().Where().OnlyX(ctx)
	require.Equal(t, 1, privateChat.User2UnreadCount)
	require.Equal(t, 0, privateChat.User1UnreadCount)
}

func TestMessageServiceSendMessageCreatesGroupMessageAndUpdatesUnreadCounts(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	sender := client.User.Create().SetUsername("group-sender").SetFullName("Sender").SaveX(ctx)
	recipient := client.User.Create().SetUsername("group-recipient").SetFullName("Recipient").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("group").SetInviteCode("group-invite").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(sender.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(recipient.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	result, err := service.SendMessage(ctx, sender.ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "group message",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "owner", result.SenderRole)
	recipientMember := client.GroupMember.Query().Where(groupmember.GroupChatID(group.ID), groupmember.UserID(recipient.ID)).OnlyX(ctx)
	senderMember := client.GroupMember.Query().Where(groupmember.GroupChatID(group.ID), groupmember.UserID(sender.ID)).OnlyX(ctx)
	require.Equal(t, 1, recipientMember.UnreadCount)
	require.Equal(t, 0, senderMember.UnreadCount)
}

func TestMessageServiceSendMessageLinksCompletedAttachment(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, _ := createPrivateMessageServiceFixture(t, client)
	attachment := client.Media.Create().
		SetFileName("attachments/file.bin").
		SetOriginalName("file.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetCategory(media.CategoryMessageAttachment).
		SetUploadStatus(media.UploadStatusCompleted).
		SetUploaderID(current.ID).
		SaveX(ctx)
	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	storage.EXPECT().GetPresignedURL("attachments/file.bin", 15*time.Minute).Return("https://download.test/file", nil)

	result, err := service.SendMessage(ctx, current.ID, model.SendMessageRequest{
		ChatID:        chatEntity.ID,
		Content:       "with file",
		AttachmentIDs: []uuid.UUID{attachment.ID},
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Len(t, result.Attachments, 1)
	require.Equal(t, "https://download.test/file", result.Attachments[0].URL)
}

func TestMessageServiceSendMessageRejectsDuplicateAttachmentsBeforeTransaction(t *testing.T) {
	service, _, _ := newMessageServiceTest(t)
	attachmentID := uuid.New()

	result, err := service.SendMessage(context.Background(), uuid.New(), model.SendMessageRequest{
		ChatID:        uuid.New(),
		AttachmentIDs: []uuid.UUID{attachmentID, attachmentID},
	})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestMessageServiceSendMessageRejectsTooManyAttachmentsBeforeTransaction(t *testing.T) {
	service, _, _ := newMessageServiceTest(t)
	attachmentIDs := make([]uuid.UUID, 21)
	for i := range attachmentIDs {
		attachmentIDs[i] = uuid.New()
	}

	result, err := service.SendMessage(context.Background(), uuid.New(), model.SendMessageRequest{
		ChatID:        uuid.New(),
		AttachmentIDs: attachmentIDs,
	})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestMessageServiceSendMessageRejectsInvalidReply(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, _ := createPrivateMessageServiceFixture(t, client)

	result, err := service.SendMessage(ctx, current.ID, model.SendMessageRequest{
		ChatID:    chatEntity.ID,
		Content:   "reply",
		ReplyToID: func() *uuid.UUID { id := uuid.New(); return &id }(),
	})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestMessageServiceSendMessageRejectsBlockedRecipient(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, other, chatEntity, _ := createPrivateMessageServiceFixture(t, client)
	client.UserBlock.Create().SetBlockerID(current.ID).SetBlockedID(other.ID).SaveX(ctx)

	result, err := service.SendMessage(ctx, current.ID, model.SendMessageRequest{ChatID: chatEntity.ID, Content: "blocked"})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestMessageServiceSendMessageRejectsDeletedChat(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, _ := createPrivateMessageServiceFixture(t, client)
	client.Chat.UpdateOneID(chatEntity.ID).SetDeletedAt(time.Now().UTC()).SaveX(ctx)

	result, err := service.SendMessage(ctx, current.ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "deleted chat",
	})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestMessageServiceSendMessageRejectsDeletedOrBannedRecipient(t *testing.T) {
	tests := []struct {
		name   string
		update func(*ent.UserUpdateOne)
	}{
		{name: "deleted", update: func(update *ent.UserUpdateOne) { update.SetDeletedAt(time.Now().UTC()) }},
		{name: "permanently banned", update: func(update *ent.UserUpdateOne) { update.SetIsBanned(true) }},
		{name: "temporarily banned", update: func(update *ent.UserUpdateOne) {
			update.SetIsBanned(true).SetBannedUntil(time.Now().UTC().Add(time.Hour))
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			service, client, _ := newMessageServiceTest(t)
			defer client.Close()
			ctx, current, other, chatEntity, _ := createPrivateMessageServiceFixture(t, client)
			update := client.User.UpdateOneID(other.ID)
			tt.update(update)
			update.SaveX(ctx)

			result, err := service.SendMessage(ctx, current.ID, model.SendMessageRequest{
				ChatID:  chatEntity.ID,
				Content: "blocked recipient",
			})

			require.Nil(t, result)
			require.Error(t, err)
		})
	}
}

func TestMessageServiceSendMessageRejectsInvalidAttachment(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, _ := createPrivateMessageServiceFixture(t, client)

	result, err := service.SendMessage(ctx, current.ID, model.SendMessageRequest{
		ChatID:        chatEntity.ID,
		Content:       "invalid attachment",
		AttachmentIDs: []uuid.UUID{uuid.New()},
	})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestMessageServiceEditMessageUpdatesContent(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx, current, _, _, msg := createPrivateMessageServiceFixture(t, client)

	result, err := service.EditMessage(ctx, current.ID, msg.ID, model.EditMessageRequest{Content: " updated "})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "updated", result.Content)
	updated := client.Message.GetX(ctx, msg.ID)
	require.Equal(t, "updated", *updated.Content)
	require.NotNil(t, updated.EditedAt)
}

func TestMessageServiceEditMessageReturnsExistingMessageWhenNothingChanges(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx, current, _, _, msg := createPrivateMessageServiceFixture(t, client)

	result, err := service.EditMessage(ctx, current.ID, msg.ID, model.EditMessageRequest{Content: "hello"})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "hello", result.Content)
	require.Nil(t, client.Message.GetX(ctx, msg.ID).EditedAt)
}

func TestMessageServiceEditMessageRejectsUnauthorizedAndOldMessage(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx, current, other, _, msg := createPrivateMessageServiceFixture(t, client)

	result, err := service.EditMessage(ctx, other.ID, msg.ID, model.EditMessageRequest{Content: "updated"})
	require.Nil(t, result)
	require.Error(t, err)

	old := time.Now().UTC().Add(-16 * time.Minute)
	require.NoError(t, client.Message.DeleteOneID(msg.ID).Exec(ctx))
	oldMessage := client.Message.Create().
		SetChatID(msg.ChatID).
		SetSenderID(current.ID).
		SetType(message.TypeRegular).
		SetContent("hello").
		SetCreatedAt(old).
		SaveX(ctx)
	result, err = service.EditMessage(ctx, current.ID, oldMessage.ID, model.EditMessageRequest{Content: "updated"})
	require.Nil(t, result)
	require.Error(t, err)
}

func TestMessageServiceEditMessageRejectsDuplicateAttachmentsBeforeTransaction(t *testing.T) {
	service, _, _ := newMessageServiceTest(t)
	attachmentID := uuid.New()

	result, err := service.EditMessage(context.Background(), uuid.New(), uuid.New(), model.EditMessageRequest{
		Content:          "updated",
		AttachmentIDs:    []uuid.UUID{attachmentID, attachmentID},
		HasAttachmentIDs: true,
	})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestMessageServiceEditMessageLinksAndUnlinksAttachments(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, _, msg := createPrivateMessageServiceFixture(t, client)
	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	storage.EXPECT().GetPresignedURL(mock.Anything, mock.Anything).Return("https://cdn.test/file", nil).Maybe()
	oldAttachment := client.Media.Create().
		SetFileName("old.bin").
		SetOriginalName("old.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusCompleted).
		SetUploaderID(current.ID).
		SetMessageID(msg.ID).
		SaveX(ctx)
	newAttachment := client.Media.Create().
		SetFileName("new.bin").
		SetOriginalName("new.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusCompleted).
		SetUploaderID(current.ID).
		SaveX(ctx)
	storage.EXPECT().GetPublicURL(mock.Anything).Return("https://cdn.test/file").Maybe()

	result, err := service.EditMessage(ctx, current.ID, msg.ID, model.EditMessageRequest{
		Content:          "updated",
		AttachmentIDs:    []uuid.UUID{newAttachment.ID},
		HasAttachmentIDs: true,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "updated", result.Content)
	attachments := client.Message.GetX(ctx, msg.ID).QueryAttachments().AllX(ctx)
	require.Len(t, attachments, 1)
	require.Equal(t, newAttachment.ID, attachments[0].ID)
	_ = oldAttachment
}

func TestMessageServiceEditMessageRejectsDeletedChatAndNonRegularMessage(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, msg := createPrivateMessageServiceFixture(t, client)

	client.Chat.UpdateOneID(chatEntity.ID).SetDeletedAt(time.Now().UTC()).SaveX(ctx)
	result, err := service.EditMessage(ctx, current.ID, msg.ID, model.EditMessageRequest{Content: "updated"})
	require.Nil(t, result)
	require.Error(t, err)

	client.Chat.UpdateOneID(chatEntity.ID).ClearDeletedAt().SaveX(ctx)
	client.Message.UpdateOneID(msg.ID).SetType(message.TypeSystemRename).SaveX(ctx)
	result, err = service.EditMessage(ctx, current.ID, msg.ID, model.EditMessageRequest{Content: "updated"})
	require.Nil(t, result)
	require.Error(t, err)
}

func TestMessageServiceGetMessagesRejectsMissingChat(t *testing.T) {
	service, client, messageRepo := newMessageServiceTest(t)
	defer client.Close()

	result, nextCursor, hasNext, prevCursor, hasPrev, err := service.GetMessages(context.Background(), uuid.New(), model.GetMessagesRequest{
		ChatID:    uuid.New(),
		Limit:     10,
		Direction: "older",
	})

	require.Nil(t, result)
	require.Empty(t, nextCursor)
	require.False(t, hasNext)
	require.Empty(t, prevCursor)
	require.False(t, hasPrev)
	require.Error(t, err)
	messageRepo.AssertNotCalled(t, "GetMessages", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestMessageServiceDeleteMessageRejectsMissingOrDeletedChat(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, msg := createPrivateMessageServiceFixture(t, client)

	err := service.DeleteMessage(ctx, current.ID, uuid.New())
	require.Error(t, err)

	client.Chat.UpdateOneID(chatEntity.ID).SetDeletedAt(time.Now().UTC()).SaveX(ctx)
	err = service.DeleteMessage(ctx, current.ID, msg.ID)
	require.Error(t, err)
	require.Nil(t, client.Message.GetX(ctx, msg.ID).DeletedAt)
}

func TestMessageServiceSendMessageAndEditMessageBroadcasts(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, msg := createPrivateMessageServiceFixture(t, client)

	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher

	doneSend := make(chan struct{}, 1)
	publisher.EXPECT().BroadcastToChat(chatEntity.ID, mock.MatchedBy(func(evt websocket.Event) bool {
		return evt.Type == websocket.EventMessageNew
	})).Run(func(uuid.UUID, websocket.Event) {
		doneSend <- struct{}{}
	}).Once()

	res, err := service.SendMessage(ctx, current.ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "broadcast send test",
	})
	require.NoError(t, err)
	require.NotNil(t, res)

	select {
	case <-doneSend:
	case <-time.After(time.Second):
		t.Fatal("expected send broadcast")
	}

	doneEdit := make(chan struct{}, 1)
	publisher.EXPECT().BroadcastToChat(chatEntity.ID, mock.MatchedBy(func(evt websocket.Event) bool {
		return evt.Type == websocket.EventMessageUpdate
	})).Run(func(uuid.UUID, websocket.Event) {
		doneEdit <- struct{}{}
	}).Once()

	editRes, err := service.EditMessage(ctx, current.ID, msg.ID, model.EditMessageRequest{
		Content: "broadcast edit test",
	})
	require.NoError(t, err)
	require.NotNil(t, editRes)

	select {
	case <-doneEdit:
	case <-time.After(time.Second):
		t.Fatal("expected edit broadcast")
	}
}

func TestMessageServiceSendMessageRejectsNonParticipantAndNilUserInPrivateChat(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, _, _, chatEntity, _ := createPrivateMessageServiceFixture(t, client)
	stranger := client.User.Create().SetUsername("stranger").SetFullName("Stranger").SaveX(ctx)

	result, err := service.SendMessage(ctx, stranger.ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "hello from stranger",
	})
	require.Nil(t, result)
	require.Error(t, err)

	lonelyChat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(lonelyChat.ID).SetUser1ID(stranger.ID).SaveX(ctx)

	result, err = service.SendMessage(ctx, stranger.ID, model.SendMessageRequest{
		ChatID:  lonelyChat.ID,
		Content: "hello into the void",
	})
	require.Nil(t, result)
	require.Error(t, err)
}

func TestMessageServiceSendMessageRejectsNonMemberInGroupChat(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetUsername("owner-msg").SaveX(ctx)
	stranger := client.User.Create().SetUsername("stranger-msg").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("msg group").SetInviteCode("msg-inv").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	result, err := service.SendMessage(ctx, stranger.ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "hello group",
	})
	require.Nil(t, result)
	require.Error(t, err)
}

func TestMessageServiceSendMessageWithValidReplyTo(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, chatEntity, originalMsg := createPrivateMessageServiceFixture(t, client)

	result, err := service.SendMessage(ctx, current.ID, model.SendMessageRequest{
		ChatID:    chatEntity.ID,
		Content:   "reply message",
		ReplyToID: &originalMsg.ID,
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "reply message", result.Content)
	require.NotNil(t, result.ReplyTo)
	require.Equal(t, originalMsg.ID, result.ReplyTo.ID)
}

func TestMessageServiceSendMessageFromUser2UpdatesUser1UnreadAndBroadcasts(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	user1 := client.User.Create().SetUsername("u1-msg").SetFullName("User 1").SaveX(ctx)
	user2 := client.User.Create().SetUsername("u2-msg").SetFullName("User 2").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(user1.ID).SetUser2ID(user2.ID).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	msgChan := make(chan websocket.Event, 1)
	publisher.EXPECT().BroadcastToChat(chatEntity.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventMessageNew
	})).Run(func(_ uuid.UUID, event websocket.Event) { msgChan <- event }).Once()
	service.wsHub = publisher

	result, err := service.SendMessage(ctx, user2.ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "from user2",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "from user2", result.Content)

	privateChat := client.PrivateChat.Query().Where().OnlyX(ctx)
	require.Equal(t, 1, privateChat.User1UnreadCount)
	require.Equal(t, 0, privateChat.User2UnreadCount)

	select {
	case event := <-msgChan:
		require.Equal(t, websocket.EventMessageNew, event.Type)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message new event")
	}
}

func TestMessageServiceEditMessageInGroupChatAndBroadcasts(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	admin := client.User.Create().SetUsername("admin-edit").SetFullName("Admin Edit").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Edit Group").SetInviteCode("edit-inv").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(admin.ID).SetRole(groupmember.RoleAdmin).SaveX(ctx)
	msg := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(admin.ID).SetType(message.TypeRegular).SetContent("initial content").SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	updateChan := make(chan websocket.Event, 1)
	publisher.EXPECT().BroadcastToChat(chatEntity.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventMessageUpdate
	})).Run(func(_ uuid.UUID, event websocket.Event) { updateChan <- event }).Once()
	service.wsHub = publisher

	result, err := service.EditMessage(ctx, admin.ID, msg.ID, model.EditMessageRequest{
		Content: "updated content",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "updated content", result.Content)
	require.Equal(t, "admin", result.SenderRole)

	select {
	case event := <-updateChan:
		require.Equal(t, websocket.EventMessageUpdate, event.Type)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message update event")
	}
}

func TestMessageServiceEditMessageNoOpReturnsSameMessage(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, _, msg := createPrivateMessageServiceFixture(t, client)

	result, err := service.EditMessage(ctx, current.ID, msg.ID, model.EditMessageRequest{
		Content: *msg.Content,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, *msg.Content, result.Content)
}

func TestMessageServiceEditMessageRejectsInvalidAttachments(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, current, _, _, msg := createPrivateMessageServiceFixture(t, client)

	fakeID := uuid.New()
	result, err := service.EditMessage(ctx, current.ID, msg.ID, model.EditMessageRequest{
		Content:          "new content",
		HasAttachmentIDs: true,
		AttachmentIDs:    []uuid.UUID{fakeID},
	})
	require.Nil(t, result)
	require.Error(t, err)
}

func TestMessageServiceSendMessageInGroupChatWithReplyAndBroadcasts(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("u1-group-msg").SetFullName("User One").SaveX(ctx)
	u2 := client.User.Create().SetUsername("u2-group-msg").SetFullName("User Two").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Msg Group").SetInviteCode("msg-grp-inv").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(u1.ID).SetRole(groupmember.RoleOwner).SetUnreadCount(0).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(u2.ID).SetRole(groupmember.RoleMember).SetUnreadCount(0).SaveX(ctx)

	replyMsg := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(u2.ID).SetType(message.TypeRegular).SetContent("reply target").SaveX(ctx)
	client.Media.Create().
		SetUploadedByID(u2.ID).
		SetFileName("reply/att.png").
		SetOriginalName("att.png").
		SetMimeType("image/png").
		SetCategory(media.CategoryMessageAttachment).
		SetUploadStatus(media.UploadStatusCompleted).
		SetFileSize(500).
		SetMessageID(replyMsg.ID).
		SaveX(ctx)

	newAttachment := client.Media.Create().
		SetUploadedByID(u1.ID).
		SetFileName("new/att.png").
		SetOriginalName("att.png").
		SetMimeType("image/png").
		SetCategory(media.CategoryMessageAttachment).
		SetUploadStatus(media.UploadStatusCompleted).
		SetFileSize(600).
		SaveX(ctx)

	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	storage.EXPECT().GetPublicURL(mock.Anything).Return("https://cdn/file").Maybe()
	storage.EXPECT().GetPresignedURL(mock.Anything, mock.Anything).Return("https://cdn/presigned", nil).Maybe()

	publisher := websocketmocks.NewMockPublisher(t)
	msgChan := make(chan websocket.Event, 1)
	publisher.EXPECT().BroadcastToChat(chatEntity.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventMessageNew
	})).Run(func(_ uuid.UUID, event websocket.Event) { msgChan <- event }).Once()
	service.wsHub = publisher

	res, err := service.SendMessage(ctx, u1.ID, model.SendMessageRequest{
		ChatID:        chatEntity.ID,
		Content:       "replying with attachment",
		ReplyToID:     &replyMsg.ID,
		AttachmentIDs: []uuid.UUID{newAttachment.ID},
	})

	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "replying with attachment", res.Content)
	require.Equal(t, "owner", res.SenderRole)
	require.NotNil(t, res.ReplyTo)
	require.Equal(t, "reply target", res.ReplyTo.Content)
	require.Equal(t, "User Two", res.ReplyTo.SenderName)

	u2Member := client.GroupMember.Query().Where(groupmember.GroupChatID(group.ID), groupmember.UserID(u2.ID)).OnlyX(ctx)
	require.Equal(t, 1, u2Member.UnreadCount)

	u1Member := client.GroupMember.Query().Where(groupmember.GroupChatID(group.ID), groupmember.UserID(u1.ID)).OnlyX(ctx)
	require.Equal(t, 0, u1Member.UnreadCount)

	select {
	case event := <-msgChan:
		require.Equal(t, websocket.EventMessageNew, event.Type)
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for group message new event")
	}
}

func TestMessageServiceSendMessageFromUser2RejectsDeletedOrBannedUser1(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("u1-target-del").SetFullName("User One").SaveX(ctx)
	u2 := client.User.Create().SetUsername("u2-sender").SetFullName("User Two").SaveX(ctx)

	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(u1.ID).SetUser2ID(u2.ID).SaveX(ctx)

	client.User.UpdateOne(u1).SetDeletedAt(time.Now()).SaveX(ctx)

	res, err := service.SendMessage(ctx, u2.ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "hello deleted",
	})
	require.Nil(t, res)
	require.Error(t, err)

	client.User.UpdateOne(u1).ClearDeletedAt().SetIsBanned(true).SaveX(ctx)

	resBanned, errBanned := service.SendMessage(ctx, u2.ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "hello banned",
	})
	require.Nil(t, resBanned)
	require.Error(t, errBanned)
}

func TestMessageServiceEditMessageUnlinksAttachmentAndRejectsInvalidNewAttachment(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("editor-u1").SetFullName("User One").SaveX(ctx)
	u2 := client.User.Create().SetUsername("editor-u2").SetFullName("User Two").SaveX(ctx)

	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(u1.ID).SetUser2ID(u2.ID).SaveX(ctx)

	oldAtt := client.Media.Create().
		SetUploadedByID(u1.ID).
		SetFileName("old/unlink.png").
		SetOriginalName("unlink.png").
		SetMimeType("image/png").
		SetCategory(media.CategoryMessageAttachment).
		SetUploadStatus(media.UploadStatusCompleted).
		SetFileSize(300).
		SaveX(ctx)

	origMsg := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetSenderID(u1.ID).
		SetType(message.TypeRegular).
		SetContent("original with att").
		AddAttachmentIDs(oldAtt.ID).
		SaveX(ctx)

	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	storage.EXPECT().GetPublicURL(mock.Anything).Return("https://cdn/file").Maybe()

	resUnlink, err := service.EditMessage(ctx, u1.ID, origMsg.ID, model.EditMessageRequest{
		Content:          "unlinked all atts",
		AttachmentIDs:    []uuid.UUID{},
		HasAttachmentIDs: true,
	})
	require.NoError(t, err)
	require.NotNil(t, resUnlink)
	require.Equal(t, "unlinked all atts", resUnlink.Content)
	require.Empty(t, resUnlink.Attachments)

	randomMediaID := uuid.New()
	resInvalid, errInvalid := service.EditMessage(ctx, u1.ID, origMsg.ID, model.EditMessageRequest{
		Content:          "trying invalid att",
		AttachmentIDs:    []uuid.UUID{randomMediaID},
		HasAttachmentIDs: true,
	})
	require.Nil(t, resInvalid)
	require.Error(t, errInvalid)
}

func TestMessageServiceSendMessageAndEditMessageGroupAttachmentsWithBroadcast(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("u1-attach-bc").SetFullName("Sender BC").SaveX(ctx)
	u2 := client.User.Create().SetUsername("u2-attach-bc").SetFullName("Receiver BC").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Attach BC Group").SetInviteCode("att-bc-inv").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(u1.ID).SetRole(groupmember.RoleOwner).SetUnreadCount(0).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(u2.ID).SetRole(groupmember.RoleMember).SetUnreadCount(0).SaveX(ctx)

	att1 := client.Media.Create().
		SetUploadedByID(u1.ID).
		SetFileName("att1.png").
		SetOriginalName("att1.png").
		SetMimeType("image/png").
		SetCategory(media.CategoryMessageAttachment).
		SetUploadStatus(media.UploadStatusCompleted).
		SetFileSize(100).
		SaveX(ctx)

	att2 := client.Media.Create().
		SetUploadedByID(u1.ID).
		SetFileName("att2.png").
		SetOriginalName("att2.png").
		SetMimeType("image/png").
		SetCategory(media.CategoryMessageAttachment).
		SetUploadStatus(media.UploadStatusCompleted).
		SetFileSize(200).
		SaveX(ctx)

	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	storage.EXPECT().GetPublicURL(mock.Anything).Return("https://cdn/file").Maybe()
	storage.EXPECT().GetPresignedURL(mock.Anything, mock.Anything).Return("https://cdn/presigned", nil).Maybe()

	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher

	newMsgChan := make(chan struct{}, 1)
	publisher.EXPECT().BroadcastToChat(chatEntity.ID, mock.MatchedBy(func(evt websocket.Event) bool {
		return evt.Type == websocket.EventMessageNew
	})).Run(func(uuid.UUID, websocket.Event) {
		newMsgChan <- struct{}{}
	}).Once()

	sentMsg, err := service.SendMessage(ctx, u1.ID, model.SendMessageRequest{
		ChatID:        chatEntity.ID,
		Content:       "Message with atts",
		AttachmentIDs: []uuid.UUID{att1.ID},
	})
	require.NoError(t, err)
	require.NotNil(t, sentMsg)
	require.Len(t, sentMsg.Attachments, 1)

	select {
	case <-newMsgChan:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for new message broadcast")
	}

	editMsgChan := make(chan struct{}, 1)
	publisher.EXPECT().BroadcastToChat(chatEntity.ID, mock.MatchedBy(func(evt websocket.Event) bool {
		return evt.Type == websocket.EventMessageUpdate
	})).Run(func(uuid.UUID, websocket.Event) {
		editMsgChan <- struct{}{}
	}).Once()

	editedMsg, err := service.EditMessage(ctx, u1.ID, sentMsg.ID, model.EditMessageRequest{
		Content:          "Edited message with two atts",
		AttachmentIDs:    []uuid.UUID{att1.ID, att2.ID},
		HasAttachmentIDs: true,
	})
	require.NoError(t, err)
	require.NotNil(t, editedMsg)
	require.Len(t, editedMsg.Attachments, 2)
	require.Equal(t, "Edited message with two atts", editedMsg.Content)

	select {
	case <-editMsgChan:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for edit message broadcast")
	}
}

func TestMessageServiceValidationErrors(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	u := client.User.Create().SetUsername("val-user").SaveX(ctx)

	_, err := service.SendMessage(ctx, u.ID, model.SendMessageRequest{
		ChatID:  uuid.Nil,
		Content: "",
	})
	require.Error(t, err)

	_, err = service.EditMessage(ctx, u.ID, uuid.Nil, model.EditMessageRequest{
		Content: "",
	})
	require.Error(t, err)

	_, _, _, _, _, err = service.GetMessages(ctx, u.ID, model.GetMessagesRequest{
		ChatID: uuid.Nil,
		Limit:  51,
	})
	require.Error(t, err)
}

func TestMessageServiceSendMessageCancelledContext(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	u := client.User.Create().SetUsername("cancel-user").SaveX(context.Background())
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(context.Background())

	_, err := service.SendMessage(ctx, u.ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "test message",
	})
	require.Error(t, err)
}

func TestMessageServiceEditMessageOnlyUnlinkAndOnlyContent(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	u := client.User.Create().SetUsername("edit-unlink-u").SaveX(ctx)
	other := client.User.Create().SetUsername("edit-unlink-o").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(u.ID).SetUser2ID(other.ID).SaveX(ctx)

	mediaObj := client.Media.Create().
		SetUploaderID(u.ID).
		SetCategory(media.CategoryMessageAttachment).
		SetFileName("att.jpg").
		SetOriginalName("att.jpg").
		SetMimeType("image/jpeg").
		SetFileSize(100).
		SetUploadStatus(media.UploadStatusCompleted).
		SaveX(ctx)

	msg := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetSenderID(u.ID).
		SetType(message.TypeRegular).
		SetContent("original content").
		AddAttachmentIDs(mediaObj.ID).
		SaveX(ctx)

	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	storage.EXPECT().GetPublicURL(mock.Anything).Return("https://cdn.test/att.jpg").Maybe()
	storage.EXPECT().GetPresignedURL(mock.Anything, mock.Anything).Return("https://cdn.test/att.jpg", nil).Maybe()

	edited, err := service.EditMessage(ctx, u.ID, msg.ID, model.EditMessageRequest{
		Content:          "original content",
		HasAttachmentIDs: true,
		AttachmentIDs:    []uuid.UUID{},
	})
	require.NoError(t, err)
	require.NotNil(t, edited)
	require.Empty(t, edited.Attachments)

	editedContentOnly, err := service.EditMessage(ctx, u.ID, msg.ID, model.EditMessageRequest{
		Content:          "new content without touch attachments",
		HasAttachmentIDs: false,
	})
	require.NoError(t, err)
	require.NotNil(t, editedContentOnly)
	require.Equal(t, "new content without touch attachments", editedContentOnly.Content)
}

func TestMessageServiceEditMessageCancelledContext(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	u := client.User.Create().SetUsername("cancel-edit-u").SaveX(context.Background())
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(context.Background())
	msg := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetSenderID(u.ID).
		SetType(message.TypeRegular).
		SetContent("original").
		SaveX(context.Background())

	_, err := service.EditMessage(ctx, u.ID, msg.ID, model.EditMessageRequest{
		Content: "new content",
	})
	require.Error(t, err)
}

func TestMessageServiceEditAndSendMessageDeletedSenderOrMessageNotFound(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	sender := client.User.Create().SetUsername("active-sender").SaveX(ctx)
	deletedOther := client.User.Create().SetUsername("deleted-other").SetDeletedAt(time.Now().UTC()).SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(sender.ID).SetUser2ID(deletedOther.ID).SaveX(ctx)

	_, err := service.SendMessage(ctx, sender.ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "hello to deleted",
	})
	require.Error(t, err)

	nonExistentMsgID := uuid.New()
	_, err = service.EditMessage(ctx, sender.ID, nonExistentMsgID, model.EditMessageRequest{
		Content: "new content",
	})
	require.Error(t, err)

	err = service.DeleteMessage(ctx, sender.ID, nonExistentMsgID)
	require.Error(t, err)

	cancCtx, cancel := context.WithCancel(context.Background())
	cancel()
	err = service.DeleteMessage(cancCtx, sender.ID, nonExistentMsgID)
	require.Error(t, err)

	_, _, _, _, _, err = service.GetMessages(cancCtx, sender.ID, model.GetMessagesRequest{ChatID: chatEntity.ID})
	require.Error(t, err)
}

func TestMessageServiceSendMessageRejectsWhenOtherUserNilInPrivateChat(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	sender := client.User.Create().SetUsername("u1-active").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(sender.ID).SaveX(ctx)

	_, err := service.SendMessage(ctx, sender.ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "test message",
	})
	require.Error(t, err)
}

func TestMessageServiceSendMessageAndEditMessageWithAvatarsAndBroadcasts(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	sender := client.User.Create().SetUsername("sender-av").SetFullName("Sender User").SaveX(ctx)
	replyUser := client.User.Create().SetUsername("reply-av").SetFullName("Reply User").SaveX(ctx)

	avatarMedia := client.Media.Create().
		SetUploadedByID(sender.ID).
		SetFileName("avatars/sender.png").
		SetOriginalName("sender.png").
		SetMimeType("image/png").
		SetCategory(media.CategoryUserAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetFileSize(200).
		SaveX(ctx)
	client.User.UpdateOne(sender).SetAvatar(avatarMedia).SaveX(ctx)

	replyAvatarMedia := client.Media.Create().
		SetUploadedByID(replyUser.ID).
		SetFileName("avatars/reply.png").
		SetOriginalName("reply.png").
		SetMimeType("image/png").
		SetCategory(media.CategoryUserAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetFileSize(200).
		SaveX(ctx)
	client.User.UpdateOne(replyUser).SetAvatar(replyAvatarMedia).SaveX(ctx)

	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(sender.ID).SetUser2ID(replyUser.ID).SaveX(ctx)

	replyAtt := client.Media.Create().
		SetUploadedByID(replyUser.ID).
		SetFileName("attachments/reply.png").
		SetOriginalName("reply.png").
		SetMimeType("image/png").
		SetCategory(media.CategoryMessageAttachment).
		SetUploadStatus(media.UploadStatusCompleted).
		SetFileSize(300).
		SaveX(ctx)

	replyMsg := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetSenderID(replyUser.ID).
		SetType(message.TypeRegular).
		SetContent("original reply").
		AddAttachmentIDs(replyAtt.ID).
		SaveX(ctx)

	hub := websocketmocks.NewMockPublisher(t)
	service.wsHub = hub
	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	storage.EXPECT().GetPublicURL(mock.Anything).Return("https://cdn/avatar.png").Maybe()
	storage.EXPECT().GetPresignedURL(mock.Anything, mock.Anything).Return("https://cdn/presigned", nil).Maybe()

	msgNewDone := make(chan struct{}, 1)
	hub.EXPECT().BroadcastToChat(chatEntity.ID, mock.MatchedBy(func(e websocket.Event) bool {
		if e.Type == websocket.EventMessageNew {
			select {
			case msgNewDone <- struct{}{}:
			default:
			}
			return true
		}
		return false
	})).Once()

	sent, err := service.SendMessage(ctx, sender.ID, model.SendMessageRequest{
		ChatID:    chatEntity.ID,
		Content:   "replying with avatar",
		ReplyToID: &replyMsg.ID,
	})
	require.NoError(t, err)
	require.NotNil(t, sent)
	require.Equal(t, "replying with avatar", sent.Content)
	require.NotNil(t, sent.ReplyTo)

	select {
	case <-msgNewDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message new")
	}

	msgUpdateDone := make(chan struct{}, 1)
	hub.EXPECT().BroadcastToChat(chatEntity.ID, mock.MatchedBy(func(e websocket.Event) bool {
		if e.Type == websocket.EventMessageUpdate {
			select {
			case msgUpdateDone <- struct{}{}:
			default:
			}
			return true
		}
		return false
	})).Once()

	edited, err := service.EditMessage(ctx, sender.ID, sent.ID, model.EditMessageRequest{
		Content: "edited replying with avatar",
	})
	require.NoError(t, err)
	require.NotNil(t, edited)
	require.Equal(t, "edited replying with avatar", edited.Content)

	select {
	case <-msgUpdateDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for message update")
	}
}

func TestMessageServiceSendMessageWithReplySenderFallback(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("u1-reply-fb").SaveX(ctx)
	u2 := client.User.Create().SetUsername("u2-reply-fb").SaveX(ctx)
	deletedTime := time.Now().UTC()
	u3 := client.User.Create().SetUsername("u3-reply-fb").SetDeletedAt(deletedTime).SaveX(ctx)

	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("reply fallback grp").SetInviteCode("rfb-code").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(u1.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(u2.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	replyMsg := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(u3.ID).SetType(message.TypeRegular).SetContent("reply source from deleted user").SaveX(ctx)

	sent, err := service.SendMessage(ctx, u1.ID, model.SendMessageRequest{
		ChatID:    chatEntity.ID,
		Content:   "reply to deleted user in group",
		ReplyToID: &replyMsg.ID,
	})
	require.NoError(t, err)
	require.NotNil(t, sent)
	require.NotNil(t, sent.ReplyTo)
	require.Equal(t, "Deleted User", sent.ReplyTo.SenderName)
	require.Nil(t, sent.ReplyTo.SenderID)
}

func TestMessageServiceSendMessageWithReplyToHavingAttachment(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("u1-reply-att").SaveX(ctx)
	u2 := client.User.Create().SetUsername("u2-reply-att").SaveX(ctx)

	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("reply att grp").SetInviteCode("ratt-code").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(u1.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(u2.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	att := client.Media.Create().
		SetFileName("reply-att.png").
		SetOriginalName("orig-reply.png").
		SetFileSize(200).
		SetMimeType("image/png").
		SetCategory(media.CategoryMessageAttachment).
		SetUploadStatus(media.UploadStatusCompleted).
		SetUploaderID(u2.ID).
		SaveX(ctx)

	replyMsg := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetSenderID(u2.ID).
		SetType(message.TypeRegular).
		SetContent("original with att").
		AddAttachments(att).
		SaveX(ctx)

	storage.EXPECT().GetPresignedURL(mock.Anything, mock.Anything).Return("https://cdn.example.com/reply-att.png", nil).Maybe()
	storage.EXPECT().GetPublicURL(mock.Anything).Return("https://cdn.example.com/reply-att.png").Maybe()

	sent, err := service.SendMessage(ctx, u1.ID, model.SendMessageRequest{
		ChatID:    chatEntity.ID,
		Content:   "replying to message with attachment",
		ReplyToID: &replyMsg.ID,
	})
	require.NoError(t, err)
	require.NotNil(t, sent)
	require.NotNil(t, sent.ReplyTo)
	require.Equal(t, replyMsg.ID, sent.ReplyTo.ID)
}

func TestMessageServiceEditMessageRejectsInvalidNewAttachmentIDs(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("u1-edit-inv-att").SaveX(ctx)
	u2 := client.User.Create().SetUsername("u2-edit-inv-att").SaveX(ctx)

	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("edit inv att grp").SetInviteCode("einv-code").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(u1.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(u2.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	msg := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetSenderID(u1.ID).
		SetType(message.TypeRegular).
		SetContent("original content").
		SaveX(ctx)

	fakeMediaID := uuid.New()
	edited, err := service.EditMessage(ctx, u1.ID, msg.ID, model.EditMessageRequest{
		Content:          "new content",
		AttachmentIDs:    []uuid.UUID{fakeMediaID},
		HasAttachmentIDs: true,
	})
	require.Error(t, err)
	require.Nil(t, edited)
}

func TestMessageServiceSendMessageUpdatesPrivateChatCountersForSender(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("u1-unhide").SaveX(ctx)
	u2 := client.User.Create().SetUsername("u2-unhide").SaveX(ctx)

	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	pc := client.PrivateChat.Create().
		SetChat(chatEntity).
		SetUser1ID(u1.ID).
		SetUser2ID(u2.ID).
		SetUser1UnreadCount(5).
		SetUser2UnreadCount(0).
		SaveX(ctx)

	sent, err := service.SendMessage(ctx, *pc.User1ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "msg from user1",
	})
	require.NoError(t, err)
	require.NotNil(t, sent)

	updatedPC, err := client.PrivateChat.Get(ctx, pc.ID)
	require.NoError(t, err)
	require.Equal(t, 0, updatedPC.User1UnreadCount)
	require.Equal(t, 1, updatedPC.User2UnreadCount)
	require.NotNil(t, updatedPC.User1LastReadAt)

	sent2, err := service.SendMessage(ctx, *pc.User2ID, model.SendMessageRequest{
		ChatID:  chatEntity.ID,
		Content: "msg from user2",
	})
	require.NoError(t, err)
	require.NotNil(t, sent2)

	updatedPC2, err := client.PrivateChat.Get(ctx, pc.ID)
	require.NoError(t, err)
	require.Equal(t, 0, updatedPC2.User2UnreadCount)
	require.Equal(t, 1, updatedPC2.User1UnreadCount)
	require.NotNil(t, updatedPC2.User2LastReadAt)
}

func TestMessageServiceGetMessagesDirectionNewerAndPagination(t *testing.T) {
	service, client, messageRepo := newMessageServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("u1-newer").SaveX(ctx)
	u2 := client.User.Create().SetUsername("u2-newer").SaveX(ctx)
	pChat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChat(pChat).SetUser1ID(u1.ID).SetUser2ID(u2.ID).SaveX(ctx)

	m1 := client.Message.Create().SetChatID(pChat.ID).SetSenderID(u1.ID).SetContent("m1").SaveX(ctx)
	m2 := client.Message.Create().SetChatID(pChat.ID).SetSenderID(u2.ID).SetContent("m2").SaveX(ctx)
	m3 := client.Message.Create().SetChatID(pChat.ID).SetSenderID(u1.ID).SetContent("m3").SaveX(ctx)

	messageRepo.EXPECT().GetMessages(mock.Anything, pChat.ID, mock.Anything, mock.Anything, 2, "newer").
		Return([]*ent.Message{m1, m2, m3}, nil)

	res, nextCursor, _, prevCursor, _, err := service.GetMessages(ctx, u1.ID, model.GetMessagesRequest{
		ChatID:    pChat.ID,
		Direction: "newer",
		Limit:     2,
	})
	require.NoError(t, err)
	require.Len(t, res, 2)
	require.NotEmpty(t, nextCursor)
	require.NotEmpty(t, prevCursor)
}

func TestMessageServiceSendMessageAllowsExpiredBannedRecipient(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	past := time.Now().UTC().Add(-2 * time.Hour)
	u1 := client.User.Create().SetUsername("u1-exp-snd").SaveX(ctx)
	u2 := client.User.Create().
		SetUsername("u2-exp-rcp").
		SetIsBanned(true).
		SetBannedUntil(past).
		SetBanReason("old temp ban").
		SaveX(ctx)

	pChat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChat(pChat).SetUser1ID(u1.ID).SetUser2ID(u2.ID).SaveX(ctx)

	res, err := service.SendMessage(ctx, u1.ID, model.SendMessageRequest{
		ChatID:  pChat.ID,
		Content: "message to previously banned user",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "message to previously banned user", res.Content)
}

func TestMessageServiceOperationsWithWsHubNil(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	service.wsHub = nil

	u1 := client.User.Create().SetUsername("u1-wsnil").SaveX(ctx)
	u2 := client.User.Create().SetUsername("u2-wsnil").SaveX(ctx)
	pChat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChat(pChat).SetUser1ID(u1.ID).SetUser2ID(u2.ID).SaveX(ctx)

	msg, err := service.SendMessage(ctx, u1.ID, model.SendMessageRequest{
		ChatID:  pChat.ID,
		Content: "hello wsnil",
	})
	require.NoError(t, err)
	require.NotNil(t, msg)

	edited, err := service.EditMessage(ctx, u1.ID, msg.ID, model.EditMessageRequest{
		Content: "hello wsnil edited",
	})
	require.NoError(t, err)
	require.NotNil(t, edited)

	err = service.DeleteMessage(ctx, u1.ID, msg.ID)
	require.NoError(t, err)
}

func TestMessageServiceSendMessageRejectsReplyToSystemOrDeletedMessage(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("reply-sys-u1").SaveX(ctx)
	u2 := client.User.Create().SetUsername("reply-sys-u2").SaveX(ctx)
	pChat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChat(pChat).SetUser1ID(u1.ID).SetUser2ID(u2.ID).SaveX(ctx)

	sysMsg := client.Message.Create().
		SetChat(pChat).
		SetSender(u1).
		SetType(message.TypeSystemLeave).
		SaveX(ctx)

	res, err := service.SendMessage(ctx, u1.ID, model.SendMessageRequest{
		ChatID:    pChat.ID,
		Content:   "reply to system",
		ReplyToID: &sysMsg.ID,
	})
	require.Nil(t, res)
	require.Error(t, err)

	delMsg := client.Message.Create().
		SetChat(pChat).
		SetSender(u1).
		SetType(message.TypeRegular).
		SetContent("regular deleted").
		SetDeletedAt(time.Now().UTC()).
		SaveX(ctx)

	res, err = service.SendMessage(ctx, u1.ID, model.SendMessageRequest{
		ChatID:    pChat.ID,
		Content:   "reply to deleted",
		ReplyToID: &delMsg.ID,
	})
	require.Nil(t, res)
	require.Error(t, err)
}

func TestMessageServiceEditMessageRejectsDeletedMessage(t *testing.T) {
	service, client, _ := newMessageServiceTest(t)
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("edit-del-u1").SaveX(ctx)
	u2 := client.User.Create().SetUsername("edit-del-u2").SaveX(ctx)
	pChat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChat(pChat).SetUser1ID(u1.ID).SetUser2ID(u2.ID).SaveX(ctx)

	delMsg := client.Message.Create().
		SetChat(pChat).
		SetSender(u1).
		SetType(message.TypeRegular).
		SetContent("regular deleted").
		SetDeletedAt(time.Now().UTC()).
		SaveX(ctx)

	res, err := service.EditMessage(ctx, u1.ID, delMsg.ID, model.EditMessageRequest{
		Content: "editing deleted message",
	})
	require.Nil(t, res)
	require.Error(t, err)
}
