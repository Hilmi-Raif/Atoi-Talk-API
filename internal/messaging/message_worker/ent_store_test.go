package messageworker

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/messageoutbox"
	"AtoiTalkAPI/internal/domain/model"
	websocket "AtoiTalkAPI/internal/messaging/events"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

type messageWorkerURLGenerator struct{}

func (messageWorkerURLGenerator) GetPublicURL(path string) string {
	return "https://cdn.example/" + path
}

func (messageWorkerURLGenerator) GetPresignedURL(path string, _ time.Duration) (string, error) {
	return "https://signed.example/" + path, nil
}

func newMessageWorkerEntClient(t *testing.T) *ent.Client {
	t.Helper()
	return enttest.Open(t, dialect.SQLite, "file:message-worker-store-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
}

func createOutboxRecord(t *testing.T, client *ent.Client, availableAt time.Time) *ent.MessageOutbox {
	t.Helper()
	return client.MessageOutbox.Create().
		SetEventType("message.new").
		SetMessageID(uuid.New()).
		SetChatID(uuid.New()).
		SetAvailableAt(availableAt).
		SaveX(context.Background())
}

func TestEntOutboxStoreClaimsOnlyEligibleRowsWithLeaseTokens(t *testing.T) {
	client := newMessageWorkerEntClient(t)
	defer client.Close()

	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	ready := createOutboxRecord(t, client, now.Add(-time.Second))
	locked := createOutboxRecord(t, client, now.Add(-time.Second))
	lockedAt := now.Add(-2 * time.Minute)
	locked.Update().SetLockedAt(lockedAt).SetLockToken(uuid.New()).SaveX(context.Background())
	createOutboxRecord(t, client, now.Add(time.Minute))

	store := NewEntOutboxStoreWithDialect(client, dialect.SQLite)
	store.now = func() time.Time { return now }

	claimed, err := store.Claim(context.Background(), 10, time.Minute)
	require.NoError(t, err)
	require.Len(t, claimed, 2)
	require.NotEqual(t, uuid.Nil, claimed[0].LockToken)
	require.NotEqual(t, uuid.Nil, claimed[1].LockToken)
	require.Equal(t, claimed[0].LockToken, claimed[1].LockToken)

	byID := map[uuid.UUID]OutboxRecord{}
	for _, record := range claimed {
		byID[record.ID] = record
		require.Equal(t, 1, record.AttemptCount)
	}
	require.Contains(t, byID, ready.ID)
	require.Contains(t, byID, locked.ID)

	for _, record := range claimed {
		stored := client.MessageOutbox.Query().Where(messageoutbox.ID(record.ID)).OnlyX(context.Background())
		require.NotNil(t, stored.LockedAt)
		require.NotNil(t, stored.LockToken)
		require.Equal(t, record.LockToken, *stored.LockToken)
	}
}

func TestEntOutboxStoreClaimUpdatesBatchWithoutPerRowReads(t *testing.T) {
	var updateStatements atomic.Int32
	client := enttest.Open(t, dialect.SQLite, "file:message-worker-claim-batch?mode=memory&cache=shared&_fk=1", enttest.WithOptions(
		ent.Debug(),
		ent.Log(func(values ...any) {
			if strings.Contains(strings.ToUpper(fmt.Sprint(values...)), "UPDATE `MESSAGE_OUTBOXES`") {
				updateStatements.Add(1)
			}
		}),
	))
	defer client.Close()

	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	for range 5 {
		createOutboxRecord(t, client, now.Add(-time.Second))
	}
	updateStatements.Store(0)
	store := NewEntOutboxStoreWithDialect(client, dialect.SQLite)
	store.now = func() time.Time { return now }

	claimed, err := store.Claim(context.Background(), 5, time.Minute)
	require.NoError(t, err)
	require.Len(t, claimed, 5)
	require.LessOrEqual(t, updateStatements.Load(), int32(1), "claim should lease a batch with one update statement")
}

func TestEntOutboxStoreReportsBacklogByProjectionAndLeaseState(t *testing.T) {
	client := newMessageWorkerEntClient(t)
	defer client.Close()
	ctx := context.Background()

	createOutboxRecord(t, client, time.Now().Add(-time.Minute))
	locked := createOutboxRecord(t, client, time.Now().Add(-time.Minute))
	locked.Update().SetLockedAt(time.Now()).SetLockToken(uuid.New()).SaveX(ctx)
	unprojected := createOutboxRecord(t, client, time.Now().Add(-time.Minute))
	unprojected.Update().SetProjectedAt(time.Now()).SaveX(ctx)
	published := createOutboxRecord(t, client, time.Now().Add(-time.Minute))
	published.Update().SetPublishedAt(time.Now()).SaveX(ctx)

	backlog, err := NewEntOutboxStoreWithDialect(client, dialect.SQLite).Backlog(ctx)

	require.NoError(t, err)
	require.Equal(t, 3, backlog.Pending)
	require.Equal(t, 1, backlog.Locked)
	require.Equal(t, 2, backlog.Unprojected)
	require.Equal(t, 3, backlog.Unpublished)
}

func TestEntOutboxStoreRejectsStaleOwnerAndTruncatesRetryError(t *testing.T) {
	client := newMessageWorkerEntClient(t)
	defer client.Close()

	now := time.Date(2026, time.January, 1, 12, 0, 0, 0, time.UTC)
	record := createOutboxRecord(t, client, now)
	store := NewEntOutboxStoreWithDialect(client, dialect.SQLite)
	store.now = func() time.Time { return now }

	claimed, err := store.Claim(context.Background(), 1, time.Minute)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	staleErr := errors.New(strings.Repeat("x", maxStoredOutboxErrorLength+100))
	err = store.MarkFailed(context.Background(), record.ID, uuid.New(), staleErr, now.Add(time.Minute))
	require.Error(t, err)
	require.Contains(t, err.Error(), "ownership")

	err = store.MarkFailed(context.Background(), record.ID, claimed[0].LockToken, staleErr, now.Add(time.Minute))
	require.NoError(t, err)

	stored := client.MessageOutbox.Query().Where(messageoutbox.ID(record.ID)).OnlyX(context.Background())
	require.NotNil(t, stored.LastError)
	require.Len(t, *stored.LastError, maxStoredOutboxErrorLength)
	require.Nil(t, stored.LockToken)
	require.Nil(t, stored.LockedAt)
}

func TestEntOutboxStoreProjectsPrivateMessageExactlyOnce(t *testing.T) {
	client := newMessageWorkerEntClient(t)
	defer client.Close()
	ctx := context.Background()

	sender := client.User.Create().SetUsername("projection-sender").SaveX(ctx)
	recipient := client.User.Create().SetUsername("projection-recipient").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	private := client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(sender.ID).SetUser2ID(recipient.ID).SaveX(ctx)
	msg := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetType(message.TypeRegular).SetContent("project me").SaveX(ctx)
	outbox := client.MessageOutbox.Create().SetEventType("message.new").SetMessageID(msg.ID).SetChatID(chatEntity.ID).SetSenderID(sender.ID).SaveX(ctx)

	store := NewEntOutboxStoreWithDialect(client, dialect.SQLite)
	claimed, err := store.Claim(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	require.NoError(t, store.Project(ctx, claimed[0]))
	require.NoError(t, store.Project(ctx, claimed[0]))

	updatedChat := client.Chat.GetX(ctx, chatEntity.ID)
	require.Equal(t, msg.ID, *updatedChat.LastMessageID)
	updatedPrivate := client.PrivateChat.GetX(ctx, private.ID)
	require.Equal(t, 1, updatedPrivate.User2UnreadCount)
	require.Equal(t, 0, updatedPrivate.User1UnreadCount)
	require.NotNil(t, updatedPrivate.User1LastReadAt)
	updatedOutbox := client.MessageOutbox.GetX(ctx, outbox.ID)
	require.NotNil(t, updatedOutbox.ProjectedAt)
}

func TestEntOutboxStoreProjectsGroupMessageExactlyOnce(t *testing.T) {
	client := newMessageWorkerEntClient(t)
	defer client.Close()
	ctx := context.Background()

	sender := client.User.Create().SetUsername("group-projection-sender").SaveX(ctx)
	recipient := client.User.Create().SetUsername("group-projection-recipient").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("projection-group").SetInviteCode("projection-group-invite").SaveX(ctx)
	senderMember := client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(sender.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	recipientMember := client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(recipient.ID).SetRole(groupmember.RoleMember).SaveX(ctx)
	msg := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetType(message.TypeRegular).SetContent("project group").SaveX(ctx)
	client.MessageOutbox.Create().SetEventType("message.new").SetMessageID(msg.ID).SetChatID(chatEntity.ID).SetSenderID(sender.ID).SaveX(ctx)

	store := NewEntOutboxStoreWithDialect(client, dialect.SQLite)
	claimed, err := store.Claim(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.NoError(t, store.Project(ctx, claimed[0]))
	require.NoError(t, store.Project(ctx, claimed[0]))

	updatedSender := client.GroupMember.GetX(ctx, senderMember.ID)
	updatedRecipient := client.GroupMember.GetX(ctx, recipientMember.ID)
	require.Zero(t, updatedSender.UnreadCount)
	require.Equal(t, 1, updatedRecipient.UnreadCount)
	require.NotNil(t, updatedSender.LastReadAt)
}

func TestEntOutboxStoreBuildsPersonalUnreadCountForGroupRecipients(t *testing.T) {
	client := newMessageWorkerEntClient(t)
	defer client.Close()
	ctx := context.Background()

	sender := client.User.Create().SetUsername("unread-event-sender").SaveX(ctx)
	recipient := client.User.Create().SetUsername("unread-event-recipient").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("unread-event-group").SetInviteCode("unread-event-invite").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(sender.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(recipient.ID).SetRole(groupmember.RoleMember).SaveX(ctx)
	messageEntity := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetType(message.TypeRegular).SetContent("unread event").SaveX(ctx)
	outbox := client.MessageOutbox.Create().
		SetEventType(string(websocket.EventMessageNew)).
		SetMessageID(messageEntity.ID).
		SetChatID(chatEntity.ID).
		SetSenderID(sender.ID).
		SaveX(ctx)

	store := NewEntOutboxStoreWithDialect(client, dialect.SQLite)
	claimed, err := store.Claim(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Len(t, claimed, 1)
	require.NoError(t, store.Project(ctx, claimed[0]))

	built, err := store.BuildEvents(ctx, claimed[0])
	require.NoError(t, err)
	require.Len(t, built, 1)

	var event websocket.Event
	require.NoError(t, json.Unmarshal(built[0].Payload, &event))
	require.NotNil(t, event.Meta)
	require.Equal(t, 1, event.Meta.UnreadCount)
	_ = outbox
}

func TestEntOutboxStoreDoesNotProjectWithStaleLease(t *testing.T) {
	client := newMessageWorkerEntClient(t)
	defer client.Close()
	ctx := context.Background()

	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	sender := client.User.Create().SetUsername("stale-sender").SaveX(ctx)
	recipient := client.User.Create().SetUsername("stale-recipient").SaveX(ctx)
	private := client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(sender.ID).SetUser2ID(recipient.ID).SaveX(ctx)
	msg := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetType(message.TypeRegular).SetContent("stale").SaveX(ctx)
	outbox := client.MessageOutbox.Create().SetEventType("message.new").SetMessageID(msg.ID).SetChatID(chatEntity.ID).SetSenderID(sender.ID).SaveX(ctx)

	store := NewEntOutboxStoreWithDialect(client, dialect.SQLite)
	claimed, err := store.Claim(ctx, 1, time.Minute)
	require.NoError(t, err)
	require.Len(t, claimed, 1)

	err = store.Project(ctx, OutboxRecord{ID: outbox.ID, LockToken: uuid.New(), MessageID: msg.ID, ChatID: chatEntity.ID, SenderID: sender.ID, EventType: "message.new"})
	require.Error(t, err)
	require.Zero(t, client.PrivateChat.GetX(ctx, private.ID).User2UnreadCount)
	require.Nil(t, client.MessageOutbox.GetX(ctx, outbox.ID).ProjectedAt)
}

func TestEntOutboxStoreBuildsTargetedEventsFromSourceData(t *testing.T) {
	client := newMessageWorkerEntClient(t)
	defer client.Close()
	ctx := context.Background()

	sender := client.User.Create().SetUsername("event-sender").SetFullName("Event Sender").SaveX(ctx)
	recipient := client.User.Create().SetUsername("event-recipient").SetFullName("Event Recipient").SaveX(ctx)
	blocked := client.User.Create().SetUsername("event-blocked").SetFullName("Blocked Recipient").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	private := client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(sender.ID).SetUser2ID(recipient.ID).SaveX(ctx)
	messageEntity := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetType(message.TypeRegular).SetContent("durable payload").SaveX(ctx)
	outbox := client.MessageOutbox.Create().
		SetEventType(string(websocket.EventMessageNew)).
		SetMessageID(messageEntity.ID).
		SetChatID(chatEntity.ID).
		SetSenderID(sender.ID).
		SaveX(ctx)

	client.UserBlock.Create().SetBlockerID(sender.ID).SetBlockedID(blocked.ID).SaveX(ctx)
	store := NewEntOutboxStoreWithDialect(client, dialect.SQLite)

	built, err := store.BuildEvents(ctx, OutboxRecord{
		ID:        outbox.ID,
		EventType: string(websocket.EventMessageNew),
		MessageID: messageEntity.ID,
		ChatID:    chatEntity.ID,
		SenderID:  sender.ID,
		LockToken: uuid.New(),
	})
	require.NoError(t, err)
	require.Len(t, built, 1)
	require.Equal(t, recipient.ID, built[0].TargetUserID)
	require.Equal(t, outbox.ID, built[0].OutboxID)
	require.Equal(t, uuid.NewSHA1(uuid.Nil, []byte(outbox.ID.String()+":"+recipient.ID.String())), built[0].ID)
	require.Equal(t, messageEntity.ID, built[0].MessageID)
	require.Equal(t, chatEntity.ID, built[0].ChatID)
	require.Equal(t, sender.ID, built[0].SenderID)

	var event websocket.Event
	require.NoError(t, json.Unmarshal(built[0].Payload, &event))
	require.Equal(t, websocket.EventMessageNew, event.Type)
	messagePayload, err := json.Marshal(event.Payload)
	require.NoError(t, err)
	var response model.MessageResponse
	require.NoError(t, json.Unmarshal(messagePayload, &response))
	require.Equal(t, messageEntity.ID, response.ID)
	require.Equal(t, "durable payload", response.Content)
	require.NotContains(t, string(built[0].Payload), recipient.ID.String())
	_ = private
}

func TestEntOutboxStoreBuildsMessageEventsWithAttachments(t *testing.T) {
	client := newMessageWorkerEntClient(t)
	defer client.Close()
	ctx := context.Background()

	sender := client.User.Create().SetUsername("attachment-sender").SetFullName("Attachment Sender").SaveX(ctx)
	recipient := client.User.Create().SetUsername("attachment-recipient").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(sender.ID).SetUser2ID(recipient.ID).SaveX(ctx)
	messageEntity := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetSenderID(sender.ID).
		SetType(message.TypeRegular).
		SetContent("with attachment").
		SaveX(ctx)
	attachment := client.Media.Create().
		SetFileName("messages/attachment.png").
		SetOriginalName("attachment.png").
		SetFileSize(12).
		SetMimeType("image/png").
		SetMessageID(messageEntity.ID).
		SaveX(ctx)
	outbox := client.MessageOutbox.Create().
		SetEventType(string(websocket.EventMessageNew)).
		SetMessageID(messageEntity.ID).
		SetChatID(chatEntity.ID).
		SetSenderID(sender.ID).
		SaveX(ctx)

	store := NewEntOutboxStoreWithURLGenerator(client, messageWorkerURLGenerator{})
	built, err := store.BuildEvents(ctx, OutboxRecord{
		ID:        outbox.ID,
		EventType: string(websocket.EventMessageNew),
		MessageID: messageEntity.ID,
		ChatID:    chatEntity.ID,
		SenderID:  sender.ID,
		LockToken: uuid.New(),
	})
	require.NoError(t, err)
	require.Len(t, built, 1)

	var event websocket.Event
	require.NoError(t, json.Unmarshal(built[0].Payload, &event))
	payload, err := json.Marshal(event.Payload)
	require.NoError(t, err)
	var response model.MessageResponse
	require.NoError(t, json.Unmarshal(payload, &response))
	require.Len(t, response.Attachments, 1)
	require.Equal(t, attachment.ID, response.Attachments[0].ID)
	require.Equal(t, "https://signed.example/messages/attachment.png", response.Attachments[0].URL)
}

func TestEntOutboxStoreDoesNotConfuseUnrelatedBlocksWithRecipientBlock(t *testing.T) {
	client := newMessageWorkerEntClient(t)
	defer client.Close()
	ctx := context.Background()

	sender := client.User.Create().SetUsername("block-sender").SaveX(ctx)
	recipient := client.User.Create().SetUsername("block-recipient").SaveX(ctx)
	otherSenderTarget := client.User.Create().SetUsername("other-sender-target").SaveX(ctx)
	otherRecipientTarget := client.User.Create().SetUsername("other-recipient-target").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(sender.ID).SetUser2ID(recipient.ID).SaveX(ctx)
	messageEntity := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetType(message.TypeRegular).SetContent("not blocked").SaveX(ctx)
	outbox := client.MessageOutbox.Create().
		SetEventType(string(websocket.EventMessageNew)).
		SetMessageID(messageEntity.ID).
		SetChatID(chatEntity.ID).
		SetSenderID(sender.ID).
		SaveX(ctx)

	client.UserBlock.Create().SetBlockerID(sender.ID).SetBlockedID(otherSenderTarget.ID).SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(recipient.ID).SetBlockedID(otherRecipientTarget.ID).SaveX(ctx)

	store := NewEntOutboxStoreWithDialect(client, dialect.SQLite)
	built, err := store.BuildEvents(ctx, OutboxRecord{
		ID:        outbox.ID,
		EventType: string(websocket.EventMessageNew),
		MessageID: messageEntity.ID,
		ChatID:    chatEntity.ID,
		SenderID:  sender.ID,
		LockToken: uuid.New(),
	})
	require.NoError(t, err)
	require.Len(t, built, 1)
	require.Equal(t, recipient.ID, built[0].TargetUserID)
}

func TestEntOutboxStoreBuildsUpdatedMessageEvent(t *testing.T) {
	client := newMessageWorkerEntClient(t)
	defer client.Close()
	ctx := context.Background()

	sender := client.User.Create().SetUsername("update-event-sender").SetFullName("Update Sender").SaveX(ctx)
	recipient := client.User.Create().SetUsername("update-event-recipient").SetFullName("Update Recipient").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(sender.ID).SetUser2ID(recipient.ID).SaveX(ctx)
	messageEntity := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetType(message.TypeRegular).SetContent("updated content").SaveX(ctx)
	outbox := client.MessageOutbox.Create().
		SetEventType(string(websocket.EventMessageUpdate)).
		SetMessageID(messageEntity.ID).
		SetChatID(chatEntity.ID).
		SetSenderID(sender.ID).
		SaveX(ctx)

	built, err := NewEntOutboxStoreWithDialect(client, dialect.SQLite).BuildEvents(ctx, OutboxRecord{
		ID: outbox.ID, EventType: string(websocket.EventMessageUpdate), MessageID: messageEntity.ID,
		ChatID: chatEntity.ID, SenderID: sender.ID, LockToken: uuid.New(),
	})
	require.NoError(t, err)
	require.Len(t, built, 1)

	var event websocket.Event
	require.NoError(t, json.Unmarshal(built[0].Payload, &event))
	require.Equal(t, websocket.EventMessageUpdate, event.Type)
	payload, err := json.Marshal(event.Payload)
	require.NoError(t, err)
	var response model.MessageResponse
	require.NoError(t, json.Unmarshal(payload, &response))
	require.Equal(t, "updated content", response.Content)
}

func TestEntOutboxStoreBuildsDeletedMessageEvent(t *testing.T) {
	client := newMessageWorkerEntClient(t)
	defer client.Close()
	ctx := context.Background()

	sender := client.User.Create().SetUsername("delete-event-sender").SaveX(ctx)
	recipient := client.User.Create().SetUsername("delete-event-recipient").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(sender.ID).SetUser2ID(recipient.ID).SaveX(ctx)
	messageEntity := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetType(message.TypeRegular).SetContent("deleted").SaveX(ctx)
	client.Message.UpdateOneID(messageEntity.ID).SetDeletedAt(time.Now().UTC()).ClearContent().SaveX(ctx)
	outbox := client.MessageOutbox.Create().
		SetEventType(string(websocket.EventMessageDelete)).
		SetMessageID(messageEntity.ID).
		SetChatID(chatEntity.ID).
		SetSenderID(sender.ID).
		SaveX(ctx)

	built, err := NewEntOutboxStoreWithDialect(client, dialect.SQLite).BuildEvents(ctx, OutboxRecord{
		ID: outbox.ID, EventType: string(websocket.EventMessageDelete), MessageID: messageEntity.ID,
		ChatID: chatEntity.ID, SenderID: sender.ID, LockToken: uuid.New(),
	})
	require.NoError(t, err)
	require.Len(t, built, 1)

	var event websocket.Event
	require.NoError(t, json.Unmarshal(built[0].Payload, &event))
	require.Equal(t, websocket.EventMessageDelete, event.Type)
	payload, err := json.Marshal(event.Payload)
	require.NoError(t, err)
	var deleted map[string]uuid.UUID
	require.NoError(t, json.Unmarshal(payload, &deleted))
	require.Equal(t, messageEntity.ID, deleted["message_id"])
}

func TestEntOutboxStoreBuildsEventsWithSingleBatchUnreadAndBlock(t *testing.T) {
	client := newMessageWorkerEntClient(t)
	defer client.Close()
	ctx := context.Background()

	sender := client.User.Create().SetUsername("sender-batch").SetFullName("Sender Batch").SaveX(ctx)
	member1 := client.User.Create().SetUsername("m1-batch").SetFullName("Member 1 Batch").SaveX(ctx)
	member2 := client.User.Create().SetUsername("m2-batch").SetFullName("Member 2 Batch").SaveX(ctx)

	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Batch Group").SetInviteCode("BATCH123").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(sender.ID).SetRole(groupmember.RoleAdmin).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(member1.ID).SetRole(groupmember.RoleMember).SetUnreadCount(3).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(member2.ID).SetRole(groupmember.RoleMember).SetUnreadCount(5).SaveX(ctx)

	msg := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetSenderID(sender.ID).
		SetContent("Batch unread test").
		SetType(message.TypeRegular).
		SaveX(ctx)

	outbox := client.MessageOutbox.Create().
		SetEventType(string(websocket.EventMessageNew)).
		SetMessageID(msg.ID).
		SetChatID(chatEntity.ID).
		SetSenderID(sender.ID).
		SaveX(ctx)

	store := NewEntOutboxStoreWithDialect(client, dialect.SQLite)
	built, err := store.BuildEvents(ctx, OutboxRecord{
		ID:        outbox.ID,
		EventType: string(websocket.EventMessageNew),
		MessageID: msg.ID,
		ChatID:    chatEntity.ID,
		SenderID:  sender.ID,
		LockToken: uuid.New(),
	})
	require.NoError(t, err)
	require.Len(t, built, 2)

	unreadMap := make(map[uuid.UUID]int)
	for _, ev := range built {
		var clientEvent websocket.Event
		require.NoError(t, json.Unmarshal(ev.Payload, &clientEvent))
		require.NotNil(t, clientEvent.Meta)
		unreadMap[ev.TargetUserID] = clientEvent.Meta.UnreadCount
	}

	require.Equal(t, 3, unreadMap[member1.ID])
	require.Equal(t, 5, unreadMap[member2.ID])
}
