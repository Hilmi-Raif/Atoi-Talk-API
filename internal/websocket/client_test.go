package websocket

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/internal/adapter"
	"AtoiTalkAPI/internal/config"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	gorilla "github.com/gorilla/websocket"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestClientWritePumpWritesMessagesAndClose(t *testing.T) {
	serverConn := make(chan *gorilla.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		upgrader := gorilla.Upgrader{}
		conn, err := upgrader.Upgrade(w, r, nil)
		require.NoError(t, err)
		serverConn <- conn
	}))
	defer server.Close()
	conn, _, err := gorilla.DefaultDialer.Dial("ws"+server.URL[len("http"):], nil)
	require.NoError(t, err)
	defer conn.Close()
	peer := <-serverConn
	defer peer.Close()

	client := &Client{Conn: conn, Send: make(chan []byte, 1)}
	done := make(chan struct{})
	go func() {
		client.WritePump()
		close(done)
	}()
	client.Send <- []byte(`{"type":"user.update"}`)
	messageType, message, err := peer.ReadMessage()
	require.NoError(t, err)
	require.Equal(t, gorilla.TextMessage, messageType)
	require.JSONEq(t, `{"type":"user.update"}`, string(message))
	close(client.Send)
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("write pump did not stop after send channel closed")
	}
}

func TestClientReadPumpUnregistersOnConnectionClose(t *testing.T) {
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

	hub := &Hub{Unregister: make(chan *Client, 1)}
	client := &Client{Hub: hub, Conn: conn, Send: make(chan []byte, 1), UserID: uuid.New()}
	done := make(chan struct{})
	go func() {
		client.ReadPump()
		close(done)
	}()
	_ = peer.WriteMessage(gorilla.TextMessage, []byte("not-json"))
	_ = peer.WriteMessage(gorilla.CloseMessage, gorilla.FormatCloseMessage(gorilla.CloseNormalClosure, "done"))
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("read pump did not stop")
	}
	select {
	case got := <-hub.Unregister:
		require.Same(t, client, got)
	case <-time.After(time.Second):
		t.Fatal("expected client unregister")
	}
}

func TestClientReadPumpHandlesTypingEvent(t *testing.T) {
	clientDB := enttest.Open(t, dialect.SQLite, "file:client-typing-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer clientDB.Close()
	redisServer := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{RedisHost: redisServer.Host(), RedisPort: redisServer.Port()})
	require.NoError(t, err)
	ctx := context.Background()
	user := clientDB.User.Create().SetUsername("typing-user").SaveX(ctx)
	chatEntity := clientDB.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	other := clientDB.User.Create().SetUsername("typing-other").SaveX(ctx)
	clientDB.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(user.ID).SetUser2ID(other.ID).SaveX(ctx)

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

	hub := newTestHub(clientDB, redisAdapter)
	hub.Unregister = make(chan *Client, 1)
	client := &Client{Hub: hub, Conn: conn, Send: make(chan []byte, 1), UserID: user.ID}
	done := make(chan struct{})
	go func() {
		client.ReadPump()
		close(done)
	}()
	_ = peer.WriteJSON(Event{Type: EventTyping, Meta: &EventMeta{ChatID: chatEntity.ID}})
	_ = peer.WriteMessage(gorilla.PingMessage, []byte("ping"))
	_ = peer.WriteMessage(gorilla.CloseMessage, gorilla.FormatCloseMessage(gorilla.CloseNormalClosure, "done"))
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("read pump did not stop")
	}
}

func TestClientWritePumpNextWriterError(t *testing.T) {
	serverConn := make(chan *gorilla.Conn, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := (&gorilla.Upgrader{}).Upgrade(w, r, nil)
		require.NoError(t, err)
		serverConn <- conn
	}))
	defer server.Close()
	conn, _, err := gorilla.DefaultDialer.Dial("ws"+server.URL[len("http"):], nil)
	require.NoError(t, err)
	peer := <-serverConn
	_ = peer.Close()
	_ = conn.Close()

	client := &Client{Conn: conn, Send: make(chan []byte, 1)}
	client.Send <- []byte("message")
	done := make(chan struct{})
	go func() {
		client.WritePump()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("write pump did not stop on closed conn")
	}
}
