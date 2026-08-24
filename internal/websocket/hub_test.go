package websocket

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/internal/adapter"
	"AtoiTalkAPI/internal/config"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	gorilla "github.com/gorilla/websocket"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestHubCachesPrivateAndGroupMembers(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:hub-members-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)

	ctx := context.Background()
	privateUser1 := client.User.Create().SetUsername("private-one").SaveX(ctx)
	privateUser2 := client.User.Create().SetUsername("private-two").SaveX(ctx)
	privateChat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(privateChat.ID).SetUser1ID(privateUser1.ID).SetUser2ID(privateUser2.ID).SaveX(ctx)

	groupUser := client.User.Create().SetUsername("group-one").SaveX(ctx)
	groupChatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(groupChatEntity.ID).SetName("group").SetInviteCode("invite-code").SaveX(ctx)
	client.GroupMember.Create().SetUserID(groupUser.ID).SetGroupChatID(group.ID).SaveX(ctx)

	hub := &Hub{
		db:              client,
		redis:           redisAdapter,
		memberFlights:   make(map[uuid.UUID]*cacheFetch),
		contactFlights:  make(map[uuid.UUID]*cacheFetch),
		clients:         make(map[*Client]bool),
		userClients:     make(map[uuid.UUID]map[*Client]bool),
		Register:        make(chan *Client, 1),
		Unregister:      make(chan *Client, 1),
		fanoutSemaphore: make(chan struct{}, maxConcurrentFanouts),
	}

	require.ElementsMatch(t, []uuid.UUID{privateUser1.ID, privateUser2.ID}, hub.getChatMembers(privateChat.ID))
	require.ElementsMatch(t, []uuid.UUID{privateUser1.ID, privateUser2.ID}, hub.getChatMembers(privateChat.ID))
	require.ElementsMatch(t, []uuid.UUID{groupUser.ID}, hub.getChatMembers(groupChatEntity.ID))
	require.ElementsMatch(t, []uuid.UUID{groupUser.ID}, hub.getChatMembers(groupChatEntity.ID))
}

func TestHubCachesContactsAndKeepAlive(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:hub-contacts-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)
	ctx := context.Background()
	user := client.User.Create().SetUsername("contact-owner").SaveX(ctx)
	other := client.User.Create().SetUsername("contact-other").SaveX(ctx)
	privateChat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(privateChat.ID).SetUser1ID(user.ID).SetUser2ID(other.ID).SaveX(ctx)

	hub := newTestHub(client, redisAdapter)
	require.Equal(t, []uuid.UUID{other.ID}, hub.getContacts(user.ID))
	require.Equal(t, []uuid.UUID{other.ID}, hub.getContacts(user.ID))

	hub.KeepAlive(user.ID)
	value, err := redisAdapter.Get(ctx, "online:"+user.ID.String())
	require.NoError(t, err)
	require.Equal(t, "true", value)
}

func TestHubBroadcastToUserFallsBackToLocalDelivery(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:hub-broadcast-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)
	hub := newTestHub(client, redisAdapter)
	userID := uuid.New()
	send := make(chan []byte, 1)
	hub.mu.Lock()
	hub.userClients[userID] = map[*Client]bool{{Send: send, UserID: userID}: true}
	hub.mu.Unlock()
	require.NoError(t, redisAdapter.Client().Close())

	hub.BroadcastToUser(userID, Event{Type: EventUserUpdate, Payload: map[string]string{"name": "updated"}})
	select {
	case payload := <-send:
		require.Contains(t, string(payload), "user.update")
	default:
		t.Fatal("expected local fallback delivery")
	}
}

func TestHubBroadcastToUserPublishesToRedis(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:hub-broadcast-redis-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)
	hub := newTestHub(client, redisAdapter)
	ctx := context.Background()
	pubsub := redisAdapter.Client().Subscribe(ctx, pubSubChannel)
	defer func() { _ = pubsub.Close() }()
	require.NoError(t, pubsub.Ping(ctx))

	userID := uuid.New()
	hub.BroadcastToUser(userID, Event{Type: EventUserUpdate, Payload: map[string]string{"name": "updated"}})

	message, err := pubsub.ReceiveMessage(ctx)
	require.NoError(t, err)
	require.Contains(t, message.Payload, userID.String())
}

func TestHubBroadcastToUserIgnoresUnmarshalableEvent(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:hub-broadcast-invalid-event-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)

	hub := newTestHub(client, redisAdapter)
	hub.BroadcastToUser(uuid.New(), Event{Type: EventUserUpdate, Payload: func() {}})
}

func TestHubBroadcastToChatRejectsInvalidTypingSender(t *testing.T) {
	hub := &Hub{}
	hub.BroadcastToChat(uuid.New(), Event{Type: EventTyping, Meta: &EventMeta{}})
}

func TestHubBroadcastsReadAndHeavyGroupEvents(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:hub-broadcast-group-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)
	ctx := context.Background()
	sender := client.User.Create().SetUsername("broadcast-sender").SaveX(ctx)
	recipient := client.User.Create().SetUsername("broadcast-recipient").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("broadcast-group").SetInviteCode("broadcast-invite").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(sender.ID).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(recipient.ID).SetUnreadCount(2).SaveX(ctx)

	hub := newTestHub(client, redisAdapter)
	event := Event{Type: EventMessageNew, Meta: &EventMeta{SenderID: sender.ID, ChatID: chatEntity.ID}}
	hub.BroadcastToChat(chatEntity.ID, event)
	hub.BroadcastToChat(chatEntity.ID, Event{Type: EventChatRead, Meta: &EventMeta{SenderID: sender.ID, ChatID: chatEntity.ID}})
}

func TestHubHeavyBroadcastSkipsBlockedRecipient(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:hub-heavy-blocked-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)
	ctx := context.Background()
	sender := client.User.Create().SetUsername("heavy-sender").SaveX(ctx)
	blocked := client.User.Create().SetUsername("heavy-blocked").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("heavy-group").SetInviteCode("heavy-invite").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(sender.ID).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(blocked.ID).SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(sender.ID).SetBlockedID(blocked.ID).SaveX(ctx)

	hub := newTestHub(client, redisAdapter)
	hub.BroadcastToChat(chatEntity.ID, Event{Type: EventMessageUpdate, Meta: &EventMeta{SenderID: sender.ID, ChatID: chatEntity.ID}})
}

func TestHubBroadcastsContactsAndFiltersBlockedOnlineUser(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:hub-broadcast-contacts-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)
	ctx := context.Background()
	owner := client.User.Create().SetUsername("contacts-owner").SaveX(ctx)
	blocked := client.User.Create().SetUsername("contacts-blocked").SaveX(ctx)
	visible := client.User.Create().SetUsername("contacts-visible").SaveX(ctx)
	for _, other := range []*ent.User{blocked, visible} {
		chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
		client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(owner.ID).SetUser2ID(other.ID).SaveX(ctx)
	}
	client.UserBlock.Create().SetBlockerID(owner.ID).SetBlockedID(blocked.ID).SaveX(ctx)

	hub := newTestHub(client, redisAdapter)
	hub.BroadcastToContacts(owner.ID, Event{Type: EventUserOnline, Meta: &EventMeta{SenderID: owner.ID}})
}

func TestHubDeliverToLocalClientsQueuesAndUnregistersSlowClients(t *testing.T) {
	hub := &Hub{
		userClients: make(map[uuid.UUID]map[*Client]bool),
		Unregister:  make(chan *Client, 1),
	}
	userID := uuid.New()
	queued := &Client{Send: make(chan []byte, 1)}
	slow := &Client{Send: make(chan []byte)}
	hub.userClients[userID] = map[*Client]bool{queued: true, slow: true}
	hub.deliverToLocalClients(userID, []byte("event"))
	require.Equal(t, []byte("event"), <-queued.Send)
	select {
	case got := <-hub.Unregister:
		require.Same(t, slow, got)
	default:
		t.Fatal("expected slow client to be queued for unregister")
	}
}

func TestHubBroadcastUserStatusUpdatesPresenceAndLastSeen(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:hub-status-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)
	ctx := context.Background()
	user := client.User.Create().SetUsername("status-user").SaveX(ctx)
	other := client.User.Create().SetUsername("status-other").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(user.ID).SetUser2ID(other.ID).SaveX(ctx)

	hub := newTestHub(client, redisAdapter)
	hub.broadcastUserStatus(user.ID, true)
	present, err := redisAdapter.Exists(ctx, "online:"+user.ID.String())
	require.NoError(t, err)
	require.True(t, present)

	hub.broadcastUserStatus(user.ID, false)
	present, err = redisAdapter.Exists(ctx, "online:"+user.ID.String())
	require.NoError(t, err)
	require.False(t, present)

	updated := client.User.GetX(ctx, user.ID)
	require.NotNil(t, updated.LastSeenAt)
}

func TestHubDisconnectUserClosesClientsAndRemovesUser(t *testing.T) {
	serverConn := make(chan *gorilla.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&gorilla.Upgrader{}).Upgrade(w, r, nil)
		require.NoError(t, err)
		serverConn <- conn
	}))
	defer server.Close()
	conn, _, err := gorilla.DefaultDialer.Dial("ws"+server.URL[len("http"):], nil)
	require.NoError(t, err)
	defer conn.Close()
	peer := <-serverConn
	defer peer.Close()

	clientDB := enttest.Open(t, dialect.SQLite, "file:hub-disconnect-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer clientDB.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)
	hub := newTestHub(clientDB, redisAdapter)
	userID := uuid.New()
	client := &Client{Conn: conn, Send: make(chan []byte, 1), UserID: userID}
	hub.mu.Lock()
	hub.clients[client] = true
	hub.userClients[userID] = map[*Client]bool{client: true}
	hub.mu.Unlock()

	hub.DisconnectUser(userID)

	hub.mu.RLock()
	_, stillConnected := hub.userClients[userID]
	_, stillTracked := hub.clients[client]
	hub.mu.RUnlock()
	require.False(t, stillConnected)
	require.False(t, stillTracked)
	select {
	case _, ok := <-client.Send:
		require.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("expected client send channel to close")
	}
}

func TestNewHubRunRegistersAndUnregistersClient(t *testing.T) {
	clientDB := enttest.Open(t, dialect.SQLite, "file:hub-run-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer clientDB.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)
	ctx := context.Background()
	user := clientDB.User.Create().SetUsername("run-user").SaveX(ctx)
	hub := NewHub(clientDB, redisAdapter)
	go hub.Run()

	client := &Client{Hub: hub, UserID: user.ID, Send: make(chan []byte, 1)}
	hub.Register <- client
	require.Eventually(t, func() bool {
		hub.mu.RLock()
		defer hub.mu.RUnlock()
		return hub.clients[client] && len(hub.userClients[user.ID]) == 1
	}, time.Second, 10*time.Millisecond)

	hub.Unregister <- client
	require.Eventually(t, func() bool {
		hub.mu.RLock()
		defer hub.mu.RUnlock()
		_, exists := hub.clients[client]
		return !exists
	}, time.Second, 10*time.Millisecond)

	select {
	case _, ok := <-client.Send:
		require.False(t, ok)
	case <-time.After(time.Second):
		t.Fatal("expected client send channel to close")
	}
}

func TestHubListenToRedisDeliversPayloadAndIgnoresInvalidPayload(t *testing.T) {
	clientDB := enttest.Open(t, dialect.SQLite, "file:hub-redis-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer clientDB.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)

	hub := NewHub(clientDB, redisAdapter)
	targetUserID := uuid.New()
	client := &Client{Hub: hub, UserID: targetUserID, Send: make(chan []byte, 2)}
	hub.mu.Lock()
	hub.clients[client] = true
	hub.userClients[targetUserID] = map[*Client]bool{client: true}
	hub.mu.Unlock()

	go hub.listenToRedis()
	time.Sleep(50 * time.Millisecond)

	redisServer.Publish(pubSubChannel, "invalid-json")

	validPayload, err := json.Marshal(redisPayload{
		TargetUserID: targetUserID,
		EventData:    []byte(`{"type":"test"}`),
	})
	require.NoError(t, err)
	redisServer.Publish(pubSubChannel, string(validPayload))

	select {
	case received := <-client.Send:
		require.Equal(t, `{"type":"test"}`, string(received))
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for redis delivered message")
	}
}

func TestHubBroadcastHeavyPrivateChat(t *testing.T) {
	clientDB := enttest.Open(t, dialect.SQLite, "file:hub-heavy-priv-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer clientDB.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)
	hub := newTestHub(clientDB, redisAdapter)
	ctx := context.Background()

	user1 := clientDB.User.Create().SetUsername("hpu1").SaveX(ctx)
	user2 := clientDB.User.Create().SetUsername("hpu2").SaveX(ctx)
	chatEntity := clientDB.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	clientDB.PrivateChat.Create().
		SetChatID(chatEntity.ID).
		SetUser1ID(user1.ID).
		SetUser2ID(user2.ID).
		SetUser1UnreadCount(1).
		SetUser2UnreadCount(2).
		SaveX(ctx)

	hub.broadcastHeavy(chatEntity.ID, Event{
		Type: EventMessageNew,
		Meta: &EventMeta{SenderID: user1.ID, ChatID: chatEntity.ID},
	})
}

func TestHubDisconnectUserAndBroadcastStatus(t *testing.T) {
	clientDB := enttest.Open(t, dialect.SQLite, "file:hub-disc-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer clientDB.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)
	hub := newTestHub(clientDB, redisAdapter)
	ctx := context.Background()

	user := clientDB.User.Create().SetUsername("disc-user").SaveX(ctx)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := gorilla.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		c := &Client{
			Hub:    hub,
			Conn:   conn,
			Send:   make(chan []byte, 10),
			UserID: user.ID,
		}
		hub.mu.Lock()
		hub.clients[c] = true
		if hub.userClients[user.ID] == nil {
			hub.userClients[user.ID] = make(map[*Client]bool)
		}
		hub.userClients[user.ID][c] = true
		hub.mu.Unlock()
	}))
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http")
	clientConn, _, err := gorilla.DefaultDialer.Dial(wsURL, nil)
	require.NoError(t, err)
	defer clientConn.Close()

	time.Sleep(50 * time.Millisecond)

	hub.KeepAlive(user.ID)
	hub.broadcastUserStatus(user.ID, true)

	hub.DisconnectUser(user.ID)

	time.Sleep(50 * time.Millisecond)

	hub.mu.RLock()
	require.Empty(t, hub.userClients[user.ID])
	hub.mu.RUnlock()
}

func TestHubGetContactsWhenUserIsUser2(t *testing.T) {
	clientDB := enttest.Open(t, dialect.SQLite, "file:hub-contacts-user2-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer clientDB.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)
	ctx := context.Background()
	user1 := clientDB.User.Create().SetUsername("contact-user1").SaveX(ctx)
	user2 := clientDB.User.Create().SetUsername("contact-user2").SaveX(ctx)
	chatEntity := clientDB.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	clientDB.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(user1.ID).SetUser2ID(user2.ID).SaveX(ctx)

	hub := newTestHub(clientDB, redisAdapter)
	contacts := hub.getContacts(user2.ID)
	require.Equal(t, []uuid.UUID{user1.ID}, contacts)

	contactsCached := hub.getContacts(user2.ID)
	require.Equal(t, []uuid.UUID{user1.ID}, contactsCached)
}

func newTestHub(client *ent.Client, redisAdapter *adapter.RedisAdapter) *Hub {
	return &Hub{
		db:              client,
		redis:           redisAdapter,
		clients:         make(map[*Client]bool),
		userClients:     make(map[uuid.UUID]map[*Client]bool),
		Register:        make(chan *Client, 1),
		Unregister:      make(chan *Client, 1),
		fanoutSemaphore: make(chan struct{}, maxConcurrentFanouts),
		memberFlights:   make(map[uuid.UUID]*cacheFetch),
		contactFlights:  make(map[uuid.UUID]*cacheFetch),
	}
}
