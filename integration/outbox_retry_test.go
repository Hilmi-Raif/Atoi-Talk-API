//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/internal/domain/helper"
	redisinfra "AtoiTalkAPI/internal/infrastructure/redis"
	"AtoiTalkAPI/internal/messaging/events"
	messageworker "AtoiTalkAPI/internal/message_worker"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

type claimedOutboxStore struct {
	*messageworker.EntOutboxStore
	record messageworker.OutboxRecord
}

func (s claimedOutboxStore) Claim(context.Context, int, time.Duration) ([]messageworker.OutboxRecord, error) {
	return []messageworker.OutboxRecord{s.record}, nil
}

type failSecondPublish struct {
	messageworker.StreamPublisher
	calls int
}

func (p *failSecondPublish) Publish(ctx context.Context, event events.MessageEvent) (string, error) {
	p.calls++
	if p.calls == 2 {
		return "", errors.New("simulated publisher outage")
	}
	return p.StreamPublisher.Publish(ctx, event)
}

func TestOutboxRetryAfterPartialFanoutKeepsUnreadAndEventsUnique(t *testing.T) {
	ctx := context.Background()
	clearDatabase(ctx)
	sender := createTestUser(t, "retry-sender")
	first := createTestUser(t, "retry-first")
	second := createTestUser(t, "retry-second")
	conversation := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := testClient.GroupChat.Create().SetChat(conversation).SetCreator(sender).SetName("retry group").SetInviteCode(uuid.NewString()).SaveX(ctx)
	for _, member := range []uuid.UUID{sender.ID, first.ID, second.ID} {
		testClient.GroupMember.Create().SetGroupChat(group).SetUserID(member).SaveX(ctx)
	}
	msg := testClient.Message.Create().SetChat(conversation).SetSender(sender).SetType(message.TypeRegular).SetContent("deliver exactly once").SaveX(ctx)
	row := testClient.MessageOutbox.Create().SetChatID(conversation.ID).SetMessageID(msg.ID).SetSenderID(sender.ID).
		SetEventType(string(events.EventMessageNew)).SetAvailableAt(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
	stream := "integration:retry:" + uuid.NewString()
	defer redisAdapter.Client().Del(ctx, stream, "stream:event:published:"+stream+":"+row.ID.String())
	publisher := redisinfra.NewRedisStreamPublisher(redisAdapter.Client(), stream, 100)
	store := messageworker.NewEntOutboxStoreWithURLGenerator(testClient, testStorageAdapter)
	config := messageworker.WorkerConfig{BatchSize: 1, MaxAttempts: 3, RetryBaseDelay: time.Hour}

	firstToken := uuid.New()
	testClient.MessageOutbox.UpdateOneID(row.ID).SetLockToken(firstToken).SetLockedAt(time.Now().UTC()).SetAttemptCount(1).ExecX(ctx)
	record := messageworker.OutboxRecord{ID: row.ID, LockToken: firstToken, EventType: string(events.EventMessageNew), ChatID: conversation.ID, MessageID: msg.ID, SenderID: sender.ID, AttemptCount: 1}
	failing := &failSecondPublish{StreamPublisher: publisher}
	processed, err := messageworker.NewWorker(claimedOutboxStore{store, record}, failing, config).ProcessBatch(ctx)
	require.ErrorContains(t, err, "simulated publisher outage")
	require.Zero(t, processed)
	require.EqualValues(t, 1, redisAdapter.Client().XLen(ctx, stream).Val())
	row = testClient.MessageOutbox.GetX(ctx, row.ID)
	require.NotNil(t, row.ProjectedAt)
	require.Nil(t, row.PublishedAt)
	require.Nil(t, row.LockToken)
	for _, userID := range []uuid.UUID{first.ID, second.ID} {
		member := testClient.GroupMember.Query().Where(groupmember.GroupChatID(group.ID), groupmember.UserID(userID)).OnlyX(ctx)
		require.Equal(t, 1, member.UnreadCount)
	}

	secondToken := uuid.New()
	testClient.MessageOutbox.UpdateOneID(row.ID).SetLockToken(secondToken).SetLockedAt(time.Now().UTC()).SetAttemptCount(2).ExecX(ctx)
	record.LockToken = secondToken
	record.AttemptCount = 2
	processed, err = messageworker.NewWorker(claimedOutboxStore{messageworker.NewEntOutboxStoreWithURLGenerator(testClient, testStorageAdapter), record}, publisher, config).ProcessBatch(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	require.NotNil(t, testClient.MessageOutbox.GetX(ctx, row.ID).PublishedAt)
	require.EqualValues(t, 2, redisAdapter.Client().XLen(ctx, stream).Val())
	entries := redisAdapter.Client().XRange(ctx, stream, "-", "+").Val()
	require.Len(t, entries, 2)
	targets := make([]uuid.UUID, 0, len(entries))
	for _, entry := range entries {
		payload, ok := entry.Values["data"].(string)
		require.True(t, ok)
		event, err := events.UnmarshalMessageEvent([]byte(payload))
		require.NoError(t, err)
		targets = append(targets, event.TargetUserID)
	}
	require.ElementsMatch(t, []uuid.UUID{first.ID, second.ID}, targets)
	for _, userID := range []uuid.UUID{first.ID, second.ID} {
		member := testClient.GroupMember.Query().Where(groupmember.GroupChatID(group.ID), groupmember.UserID(userID)).OnlyX(ctx)
		require.Equal(t, 1, member.UnreadCount)
	}
}

func TestDelayedOutboxDoesNotGiveNewGroupMemberOldUnread(t *testing.T) {
	ctx := context.Background()
	clearDatabase(ctx)
	sender := createTestUser(t, "join-race-sender")
	newMember := createTestUser(t, "join-race-member")
	conversation := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := testClient.GroupChat.Create().SetChat(conversation).SetCreator(sender).SetName("join race").SetInviteCode(uuid.NewString()).SaveX(ctx)
	testClient.GroupMember.Create().SetGroupChat(group).SetUser(sender).SaveX(ctx)
	createdAt := time.Now().UTC().Add(-time.Minute)
	msg := testClient.Message.Create().SetChat(conversation).SetSender(sender).SetType(message.TypeRegular).SetContent("before joining").SetCreatedAt(createdAt).SaveX(ctx)
	row := testClient.MessageOutbox.Create().SetChatID(conversation.ID).SetMessageID(msg.ID).SetSenderID(sender.ID).
		SetEventType(string(events.EventMessageNew)).SetCreatedAt(createdAt).SetAvailableAt(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
	member := testClient.GroupMember.Create().SetGroupChat(group).SetUser(newMember).SaveX(ctx)
	require.True(t, member.JoinedAt.After(createdAt))
	store := messageworker.NewEntOutboxStoreWithURLGenerator(testClient, testStorageAdapter)
	lockToken := uuid.New()
	testClient.MessageOutbox.UpdateOneID(row.ID).SetLockToken(lockToken).SetLockedAt(time.Now().UTC()).SetAttemptCount(1).ExecX(ctx)
	record := messageworker.OutboxRecord{ID: row.ID, LockToken: lockToken, EventType: string(events.EventMessageNew), ChatID: conversation.ID, MessageID: msg.ID, SenderID: sender.ID, AttemptCount: 1}
	require.NoError(t, store.Project(ctx, record))
	require.Zero(t, testClient.GroupMember.GetX(ctx, member.ID).UnreadCount)
	toSend, err := store.BuildEvents(ctx, record)
	require.NoError(t, err)
	require.Empty(t, toSend)
}

func TestDelayedOutboxDoesNotRedeliverOldMessageAfterGroupRejoin(t *testing.T) {
	ctx := context.Background()
	clearDatabase(ctx)
	sender := createTestUser(t, "rejoin-sender")
	rejoining := createTestUser(t, "rejoin-member")
	conversation := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := testClient.GroupChat.Create().SetChat(conversation).SetCreator(sender).SetName("rejoin test").SetInviteCode(uuid.NewString()).SaveX(ctx)
	testClient.GroupMember.Create().SetGroupChat(group).SetUser(sender).SaveX(ctx)
	member := testClient.GroupMember.Create().SetGroupChat(group).SetUser(rejoining).SaveX(ctx)
	createdAt := time.Now().UTC().Add(-time.Minute)
	msg := testClient.Message.Create().SetChat(conversation).SetSender(sender).SetType(message.TypeRegular).SetContent("before rejoining").SetCreatedAt(createdAt).SaveX(ctx)
	row := testClient.MessageOutbox.Create().SetChatID(conversation.ID).SetMessageID(msg.ID).SetSenderID(sender.ID).
		SetEventType(string(events.EventMessageNew)).SetCreatedAt(createdAt).SetAvailableAt(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
	require.NoError(t, testClient.GroupMember.DeleteOne(member).Exec(ctx))
	member = testClient.GroupMember.Create().SetGroupChat(group).SetUser(rejoining).SaveX(ctx)
	require.True(t, member.JoinedAt.After(msg.CreatedAt))
	store := messageworker.NewEntOutboxStoreWithURLGenerator(testClient, testStorageAdapter)
	lockToken := uuid.New()
	testClient.MessageOutbox.UpdateOneID(row.ID).SetLockToken(lockToken).SetLockedAt(time.Now().UTC()).ExecX(ctx)
	record := messageworker.OutboxRecord{ID: row.ID, LockToken: lockToken, EventType: string(events.EventMessageNew), ChatID: conversation.ID, MessageID: msg.ID, SenderID: sender.ID}
	require.NoError(t, store.Project(ctx, record))
	require.Zero(t, testClient.GroupMember.GetX(ctx, member.ID).UnreadCount)
	toSend, err := store.BuildEvents(ctx, record)
	require.NoError(t, err)
	require.Empty(t, toSend)
}

func TestDelayedOutboxDoesNotRestoreUnreadForHiddenPrivateChat(t *testing.T) {
	ctx := context.Background()
	clearDatabase(ctx)
	sender := createTestUser(t, "hidden-sender")
	recipient := createTestUser(t, "hidden-recipient")
	token, err := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, recipient.ID)
	require.NoError(t, err)
	conversation := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	private := testClient.PrivateChat.Create().SetChat(conversation).SetUser1(sender).SetUser2(recipient).SaveX(ctx)
	createdAt := time.Now().UTC().Add(-time.Minute)
	msg := testClient.Message.Create().SetChat(conversation).SetSender(sender).SetType(message.TypeRegular).SetContent("before hiding").SetCreatedAt(createdAt).SaveX(ctx)
	row := testClient.MessageOutbox.Create().SetChatID(conversation.ID).SetMessageID(msg.ID).SetSenderID(sender.ID).
		SetEventType(string(events.EventMessageNew)).SetAvailableAt(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
	require.Equal(t, 200, makeRequest("POST", "/api/chats/"+conversation.ID.String()+"/hide", nil, token).Code)
	private = testClient.PrivateChat.Query().Where(privatechat.ID(private.ID)).OnlyX(ctx)
	require.NotNil(t, private.User2HiddenAt)
	require.True(t, private.User2HiddenAt.After(createdAt))
	store := messageworker.NewEntOutboxStoreWithURLGenerator(testClient, testStorageAdapter)
	lockToken := uuid.New()
	testClient.MessageOutbox.UpdateOneID(row.ID).SetLockToken(lockToken).SetLockedAt(time.Now().UTC()).ExecX(ctx)
	record := messageworker.OutboxRecord{ID: row.ID, LockToken: lockToken, EventType: string(events.EventMessageNew), ChatID: conversation.ID, MessageID: msg.ID, SenderID: sender.ID}
	require.NoError(t, store.Project(ctx, record))
	private = testClient.PrivateChat.Query().Where(privatechat.ID(private.ID)).OnlyX(ctx)
	require.Zero(t, private.User2UnreadCount, "message hidden before projection must not become unread")
	toSend, err := store.BuildEvents(ctx, record)
	require.NoError(t, err)
	require.Empty(t, toSend, "hidden history must not be delivered after hide")
	newMessage := testClient.Message.Create().SetChat(conversation).SetSender(sender).SetType(message.TypeRegular).SetContent("after hiding").SaveX(ctx)
	newRow := testClient.MessageOutbox.Create().SetChatID(conversation.ID).SetMessageID(newMessage.ID).SetSenderID(sender.ID).
		SetEventType(string(events.EventMessageNew)).SetAvailableAt(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
	newToken := uuid.New()
	testClient.MessageOutbox.UpdateOneID(newRow.ID).SetLockToken(newToken).SetLockedAt(time.Now().UTC()).ExecX(ctx)
	newRecord := messageworker.OutboxRecord{ID: newRow.ID, LockToken: newToken, EventType: string(events.EventMessageNew), ChatID: conversation.ID, MessageID: newMessage.ID, SenderID: sender.ID}
	require.NoError(t, store.Project(ctx, newRecord))
	require.Equal(t, 1, testClient.PrivateChat.GetX(ctx, private.ID).User2UnreadCount)
	newEvents, err := store.BuildEvents(ctx, newRecord)
	require.NoError(t, err)
	require.Len(t, newEvents, 1)
	require.Equal(t, recipient.ID, newEvents[0].TargetUserID)
}

func TestHiddenPrivateHistoryDoesNotReceiveEditOrDeleteEvents(t *testing.T) {
	for _, eventType := range []events.EventType{events.EventMessageUpdate, events.EventMessageDelete} {
		t.Run(string(eventType), func(t *testing.T) {
			ctx := context.Background()
			clearDatabase(ctx)
			sender := createTestUser(t, "history-sender")
			recipient := createTestUser(t, "history-recipient")
			token, err := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, recipient.ID)
			require.NoError(t, err)
			conversation := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
			testClient.PrivateChat.Create().SetChat(conversation).SetUser1(sender).SetUser2(recipient).SaveX(ctx)
			msg := testClient.Message.Create().SetChat(conversation).SetSender(sender).SetType(message.TypeRegular).
				SetContent("hidden history").SetCreatedAt(time.Now().UTC().Add(-time.Minute)).SaveX(ctx)
			require.Equal(t, 200, makeRequest("POST", "/api/chats/"+conversation.ID.String()+"/hide", nil, token).Code)
			row := testClient.MessageOutbox.Create().SetChatID(conversation.ID).SetMessageID(msg.ID).SetSenderID(sender.ID).
				SetEventType(string(eventType)).SetAvailableAt(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
			store := messageworker.NewEntOutboxStoreWithURLGenerator(testClient, testStorageAdapter)
			eventsToSend, err := store.BuildEvents(ctx, messageworker.OutboxRecord{
				ID: row.ID, EventType: string(eventType), ChatID: conversation.ID, MessageID: msg.ID, SenderID: sender.ID,
			})
			require.NoError(t, err)
			require.Empty(t, eventsToSend, "hidden message must not reappear in realtime updates")
			newMessage := testClient.Message.Create().SetChat(conversation).SetSender(sender).SetType(message.TypeRegular).
				SetContent("visible after hiding").SaveX(ctx)
			newRow := testClient.MessageOutbox.Create().SetChatID(conversation.ID).SetMessageID(newMessage.ID).SetSenderID(sender.ID).
				SetEventType(string(eventType)).SetAvailableAt(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
			newEvents, err := store.BuildEvents(ctx, messageworker.OutboxRecord{
				ID: newRow.ID, EventType: string(eventType), ChatID: conversation.ID, MessageID: newMessage.ID, SenderID: sender.ID,
			})
			require.NoError(t, err)
			require.Len(t, newEvents, 1)
			require.Equal(t, recipient.ID, newEvents[0].TargetUserID)
		})
	}
}

func TestExpiredOutboxLeaseRejectsStaleWorkerAndRecovers(t *testing.T) {
	ctx := context.Background()
	clearDatabase(ctx)
	sender := createTestUser(t, "lease-sender")
	recipient := createTestUser(t, "lease-recipient")
	conversation := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	private := testClient.PrivateChat.Create().SetChat(conversation).SetUser1(sender).SetUser2(recipient).SaveX(ctx)
	msg := testClient.Message.Create().SetChat(conversation).SetSender(sender).SetType(message.TypeRegular).SetContent("reclaim me").SaveX(ctx)
	row := testClient.MessageOutbox.Create().SetChatID(conversation.ID).SetMessageID(msg.ID).SetSenderID(sender.ID).
		SetEventType(string(events.EventMessageNew)).SetAvailableAt(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
	store := messageworker.NewEntOutboxStoreWithURLGenerator(testClient, testStorageAdapter)
	firstToken := uuid.New()
	secondToken := uuid.New()
	testClient.MessageOutbox.UpdateOneID(row.ID).SetLockToken(firstToken).SetLockedAt(time.Now().UTC().Add(-2 * time.Minute)).SetAttemptCount(1).ExecX(ctx)
	first := messageworker.OutboxRecord{ID: row.ID, LockToken: firstToken, EventType: string(events.EventMessageNew), ChatID: conversation.ID, MessageID: msg.ID, SenderID: sender.ID, AttemptCount: 1}
	testClient.MessageOutbox.UpdateOneID(row.ID).SetLockToken(secondToken).SetLockedAt(time.Now().UTC()).SetAttemptCount(2).ExecX(ctx)
	second := first
	second.LockToken = secondToken
	second.AttemptCount = 2
	require.Error(t, store.Project(ctx, first))
	require.NoError(t, store.Project(ctx, second))
	require.ErrorIs(t, store.MarkPublished(ctx, first.ID, first.LockToken, time.Now().UTC()), messageworker.ErrOutboxLeaseLost)
	require.NoError(t, store.MarkPublished(ctx, second.ID, second.LockToken, time.Now().UTC()))
	require.Equal(t, 1, testClient.PrivateChat.GetX(ctx, private.ID).User2UnreadCount)
	require.NotNil(t, testClient.MessageOutbox.GetX(ctx, row.ID).PublishedAt)
}
