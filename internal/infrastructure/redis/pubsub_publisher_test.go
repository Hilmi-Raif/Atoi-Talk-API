package redis

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/internal/messaging/events"
	"context"
	"encoding/json"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestPubSubPublisherPublishesNonDurableEvents(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()

	publisher := NewPubSubPublisher(nil, client)
	targetID := uuid.New()
	pubsub := client.Subscribe(context.Background(), pubSubChannel)
	defer pubsub.Close()
	_, err := pubsub.Receive(context.Background())
	require.NoError(t, err)

	publisher.BroadcastToUser(targetID, events.Event{Type: events.EventChatUpdate})

	messageCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	message, err := pubsub.ReceiveMessage(messageCtx)
	require.NoError(t, err)

	var payload pubSubPayload
	require.NoError(t, json.Unmarshal([]byte(message.Payload), &payload))
	require.Equal(t, targetID.String(), payload.TargetUserID)
	var event events.Event
	require.NoError(t, json.Unmarshal(payload.EventData, &event))
	require.Equal(t, events.EventChatUpdate, event.Type)
}

func TestPubSubPublisherDoesNotPublishDurableMessageEvents(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	defer client.Close()

	publisher := NewPubSubPublisher(nil, client)
	pubsub := client.Subscribe(context.Background(), pubSubChannel)
	defer pubsub.Close()
	_, err := pubsub.Receive(context.Background())
	require.NoError(t, err)
	messages := pubsub.Channel()

	publisher.BroadcastToUser(uuid.New(), events.Event{Type: events.EventMessageNew})

	select {
	case message := <-messages:
		t.Fatalf("durable event was published to Pub/Sub: %s", message.Payload)
	case <-time.After(100 * time.Millisecond):
	}
}

func TestPubSubPublisherBroadcastsToPrivateAndGroupMembers(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, dialect.SQLite, "file:pubsub-members?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = db.Close() })
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	publisher := NewPubSubPublisher(db, client)

	first := db.User.Create().SetUsername("first").SaveX(ctx)
	second := db.User.Create().SetUsername("second").SaveX(ctx)
	third := db.User.Create().SetUsername("third").SaveX(ctx)
	privateEntity := db.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	db.PrivateChat.Create().SetChatID(privateEntity.ID).SetUser1ID(first.ID).SetUser2ID(second.ID).SaveX(ctx)
	groupEntity := db.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := db.GroupChat.Create().SetChatID(groupEntity.ID).SetName("group").SetInviteCode("group-code").SaveX(ctx)
	db.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(first.ID).SaveX(ctx)
	db.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(third.ID).SaveX(ctx)

	pubsub := client.Subscribe(ctx, pubSubChannel)
	t.Cleanup(func() { _ = pubsub.Close() })
	_, err := pubsub.Receive(ctx)
	require.NoError(t, err)

	publisher.BroadcastToChat(privateEntity.ID, events.Event{Type: events.EventChatUpdate})
	publisher.BroadcastToChat(groupEntity.ID, events.Event{Type: events.EventChatDelete})

	targets := receiveTargets(t, pubsub, 4)
	require.ElementsMatch(t, []string{first.ID.String(), second.ID.String(), first.ID.String(), third.ID.String()}, targets)
}

func TestPubSubPublisherBroadcastsPresenceOnlyToUnblockedContacts(t *testing.T) {
	ctx := context.Background()
	db := enttest.Open(t, dialect.SQLite, "file:pubsub-contacts?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = db.Close() })
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	publisher := NewPubSubPublisher(db, client)

	current := db.User.Create().SetUsername("current").SaveX(ctx)
	allowed := db.User.Create().SetUsername("allowed").SaveX(ctx)
	blocked := db.User.Create().SetUsername("blocked").SaveX(ctx)
	allowedChat := db.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	db.PrivateChat.Create().SetChatID(allowedChat.ID).SetUser1ID(current.ID).SetUser2ID(allowed.ID).SaveX(ctx)
	blockedChat := db.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	db.PrivateChat.Create().SetChatID(blockedChat.ID).SetUser1ID(current.ID).SetUser2ID(blocked.ID).SaveX(ctx)
	db.UserBlock.Create().SetBlockerID(blocked.ID).SetBlockedID(current.ID).SaveX(ctx)

	pubsub := client.Subscribe(ctx, pubSubChannel)
	t.Cleanup(func() { _ = pubsub.Close() })
	_, err := pubsub.Receive(ctx)
	require.NoError(t, err)

	publisher.BroadcastToContacts(current.ID, events.Event{Type: events.EventUserOnline})
	require.Equal(t, []string{allowed.ID.String()}, receiveTargets(t, pubsub, 1))
}

func TestPubSubPublisherDisconnectsUserAndHandlesNilInputs(t *testing.T) {
	ctx := context.Background()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	publisher := NewPubSubPublisher(nil, client)
	userID := uuid.New()

	pubsub := client.Subscribe(ctx, pubSubControlChannel)
	t.Cleanup(func() { _ = pubsub.Close() })
	_, err := pubsub.Receive(ctx)
	require.NoError(t, err)

	publisher.DisconnectUser(userID)
	require.Equal(t, []string{userID.String()}, receiveTargets(t, pubsub, 1))
	require.Contains(t, publisher.String(), "PubSubPublisher")

	var nilPublisher *PubSubPublisher
	nilPublisher.BroadcastToUser(userID, events.Event{Type: events.EventChatUpdate})
	nilPublisher.BroadcastToChat(uuid.Nil, events.Event{Type: events.EventChatUpdate})
	nilPublisher.BroadcastToContacts(uuid.Nil, events.Event{Type: events.EventUserOnline})
	nilPublisher.DisconnectUser(uuid.Nil)
}

func receiveTargets(t *testing.T, pubsub *redis.PubSub, count int) []string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	targets := make([]string, 0, count)
	for len(targets) < count {
		message, err := pubsub.ReceiveMessage(ctx)
		require.NoError(t, err)
		var payload pubSubPayload
		require.NoError(t, json.Unmarshal([]byte(message.Payload), &payload))
		targets = append(targets, payload.TargetUserID)
	}
	return targets
}
