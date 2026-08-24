//go:build integration

package integration

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/websocket"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	ws "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

func createWSGroupChat(t *testing.T, token, name string, memberIDs []uuid.UUID, isPublic bool) uuid.UUID {
	t.Helper()

	rr := makeRequest("POST", "/api/chats/group", model.CreateGroupChatRequest{
		Name:      name,
		MemberIDs: memberIDs,
		IsPublic:  isPublic,
	}, token)
	assert.Equal(t, http.StatusOK, rr.Code)

	chatData := parseResponse[model.ChatListResponse](t, rr)
	return chatData.ID
}

func waitForEvent(t *testing.T, conn *ws.Conn, eventType websocket.EventType, timeout time.Duration) *websocket.Event {
	deadline := time.Now().Add(timeout)
	conn.SetReadDeadline(deadline)

	for {
		if time.Now().After(deadline) {
			return nil
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			return nil
		}

		var event websocket.Event
		if err := json.Unmarshal(message, &event); err != nil {
			continue
		}

		if event.Type == eventType {
			return &event
		}
	}
}

func waitForEvents(t *testing.T, conn *ws.Conn, expectedTypes []websocket.EventType, timeout time.Duration) map[websocket.EventType]*websocket.Event {
	deadline := time.Now().Add(timeout)
	conn.SetReadDeadline(deadline)

	receivedEvents := make(map[websocket.EventType]*websocket.Event)
	remainingTypes := make(map[websocket.EventType]bool)
	for _, et := range expectedTypes {
		remainingTypes[et] = true
	}

	for len(remainingTypes) > 0 {
		if time.Now().After(deadline) {
			break
		}

		_, message, err := conn.ReadMessage()
		if err != nil {
			break
		}

		var event websocket.Event
		if err := json.Unmarshal(message, &event); err != nil {
			continue
		}

		if remainingTypes[event.Type] {
			receivedEvents[event.Type] = &event
			delete(remainingTypes, event.Type)
		}
	}

	return receivedEvents
}

func TestWebSocketConnection(t *testing.T) {
	clearDatabase(context.Background())

	user1 := createWSUser(t, "user1", "user1@example.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

	server := httptest.NewServer(testRouter)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token1
	header := http.Header{}

	conn, _, err := ws.DefaultDialer.Dial(wsURL, header)
	assert.NoError(t, err)
	defer conn.Close()
}

func TestWebSocketPresenceTTL(t *testing.T) {
	clearDatabase(context.Background())

	user1 := createWSUser(t, "user1", "user1@example.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

	server := httptest.NewServer(testRouter)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token1
	header := http.Header{}

	conn, _, err := ws.DefaultDialer.Dial(wsURL, header)
	assert.NoError(t, err)
	defer conn.Close()

	time.Sleep(200 * time.Millisecond)

	key := fmt.Sprintf("online:%s", user1.ID)
	exists, err := redisAdapter.Client().Exists(context.Background(), key).Result()
	assert.NoError(t, err)
	assert.Equal(t, int64(1), exists)

	ttl, err := redisAdapter.Client().TTL(context.Background(), key).Result()
	assert.NoError(t, err)
	assert.True(t, ttl > 0)
	assert.True(t, ttl <= 70*time.Second)
}

func TestWebSocketBroadcastMessage(t *testing.T) {
	clearDatabase(context.Background())

	user1 := createWSUser(t, "user1", "user1@example.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

	user2 := createWSUser(t, "user2", "user2@example.com")
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user2.ID)

	createWSPrivateChat(t, user2.ID, token1)

	server := httptest.NewServer(testRouter)
	defer server.Close()

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2
	header2 := http.Header{}
	conn2, _, err := ws.DefaultDialer.Dial(wsURL, header2)
	assert.NoError(t, err)
	defer conn2.Close()

	time.Sleep(200 * time.Millisecond)

	chats, _ := testClient.Chat.Query().All(context.Background())
	chatID := chats[0].ID

	reqBody := model.SendMessageRequest{
		ChatID:  chatID,
		Content: "Hello WebSocket",
	}
	rr := makeRequest("POST", "/api/messages", reqBody, token1)
	assert.Equal(t, http.StatusOK, rr.Code)

	event := waitForEvent(t, conn2, websocket.EventMessageNew, 2*time.Second)
	assert.NotNil(t, event)

	if event != nil {
		payloadMap, ok := event.Payload.(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "Hello WebSocket", payloadMap["content"])
		assert.Equal(t, "regular", payloadMap["type"])
		assert.NotNil(t, event.Meta)
		assert.Equal(t, 1, int(event.Meta.UnreadCount))
	}
}

func TestWebSocketTypingStatus(t *testing.T) {
	clearDatabase(context.Background())

	user1 := createWSUser(t, "user1", "user1@example.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

	user2 := createWSUser(t, "user2", "user2@example.com")
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user2.ID)

	createWSPrivateChat(t, user2.ID, token1)

	chats, _ := testClient.Chat.Query().All(context.Background())
	chatID := chats[0].ID

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL1 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token1
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2

	header1 := http.Header{}
	conn1, _, err := ws.DefaultDialer.Dial(wsURL1, header1)
	assert.NoError(t, err)
	defer conn1.Close()

	header2 := http.Header{}
	conn2, _, err := ws.DefaultDialer.Dial(wsURL2, header2)
	assert.NoError(t, err)
	defer conn2.Close()

	time.Sleep(200 * time.Millisecond)

	typingEvent := websocket.Event{
		Type: websocket.EventTyping,
		Meta: &websocket.EventMeta{
			ChatID:   chatID,
			SenderID: user1.ID,
		},
	}
	err = conn1.WriteJSON(typingEvent)
	assert.NoError(t, err)

	event := waitForEvent(t, conn2, websocket.EventTyping, 2*time.Second)
	assert.NotNil(t, event)
	if event != nil {
		assert.Equal(t, chatID, event.Meta.ChatID)
		assert.Equal(t, user1.ID, event.Meta.SenderID)
	}
}

func TestWebSocketUserPresence(t *testing.T) {
	clearDatabase(context.Background())

	user1 := createWSUser(t, "user1", "user1@example.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

	user2 := createWSUser(t, "user2", "user2@example.com")
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user2.ID)

	createWSPrivateChat(t, user2.ID, token1)

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL1 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token1
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2

	header1 := http.Header{}
	conn1, _, err := ws.DefaultDialer.Dial(wsURL1, header1)
	assert.NoError(t, err)
	defer conn1.Close()

	header2 := http.Header{}
	conn2, _, err := ws.DefaultDialer.Dial(wsURL2, header2)
	assert.NoError(t, err)

	eventOnline := waitForEvent(t, conn1, websocket.EventUserOnline, 2*time.Second)
	assert.NotNil(t, eventOnline)
	if eventOnline != nil {
		payload, _ := eventOnline.Payload.(map[string]interface{})
		assert.Equal(t, user2.ID.String(), payload["user_id"])
	}

	conn2.Close()
	time.Sleep(200 * time.Millisecond)

	eventOffline := waitForEvent(t, conn1, websocket.EventUserOffline, 2*time.Second)
	assert.NotNil(t, eventOffline)
	if eventOffline != nil {
		payload, _ := eventOffline.Payload.(map[string]interface{})
		assert.Equal(t, user2.ID.String(), payload["user_id"])
	}
}

func TestWebSocketMultiDevice(t *testing.T) {
	clearDatabase(context.Background())

	user1 := createWSUser(t, "user1", "user1@example.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

	user2 := createWSUser(t, "user2", "user2@example.com")
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user2.ID)

	createWSPrivateChat(t, user2.ID, token1)
	chats, _ := testClient.Chat.Query().All(context.Background())
	chatID := chats[0].ID

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2

	header2 := http.Header{}
	conn2A, _, err := ws.DefaultDialer.Dial(wsURL, header2)
	assert.NoError(t, err)
	defer conn2A.Close()

	conn2B, _, err := ws.DefaultDialer.Dial(wsURL, header2)
	assert.NoError(t, err)
	defer conn2B.Close()

	time.Sleep(200 * time.Millisecond)

	reqBody := model.SendMessageRequest{
		ChatID:  chatID,
		Content: "Sync Test",
	}
	rr := makeRequest("POST", "/api/messages", reqBody, token1)
	assert.Equal(t, http.StatusOK, rr.Code)

	eventA := waitForEvent(t, conn2A, websocket.EventMessageNew, 2*time.Second)
	assert.NotNil(t, eventA)

	eventB := waitForEvent(t, conn2B, websocket.EventMessageNew, 2*time.Second)
	assert.NotNil(t, eventB)
}

func TestWebSocketReadStatusSync(t *testing.T) {
	clearDatabase(context.Background())

	user1 := createWSUser(t, "user1", "user1@example.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

	user2 := createWSUser(t, "user2", "user2@example.com")
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user2.ID)

	createWSPrivateChat(t, user2.ID, token1)
	chats, _ := testClient.Chat.Query().All(context.Background())
	chatID := chats[0].ID

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2

	header2 := http.Header{}
	conn2A, _, _ := ws.DefaultDialer.Dial(wsURL, header2)
	defer conn2A.Close()
	conn2B, _, _ := ws.DefaultDialer.Dial(wsURL, header2)
	defer conn2B.Close()

	time.Sleep(200 * time.Millisecond)

	reqMsg := model.SendMessageRequest{
		ChatID:  chatID,
		Content: "Unread Message",
	}
	rrSend := makeRequest("POST", "/api/messages", reqMsg, token1)
	assert.Equal(t, http.StatusOK, rrSend.Code)

	time.Sleep(200 * time.Millisecond)

	rr := makeRequest("POST", fmt.Sprintf("/api/chats/%s/read", chatID), nil, token2)
	assert.Equal(t, http.StatusOK, rr.Code)

	event := waitForEvent(t, conn2B, websocket.EventChatRead, 2*time.Second)
	assert.NotNil(t, event)
	if event != nil {
		assert.Equal(t, chatID, event.Meta.ChatID)
	}
}

func TestWebSocketSecurityLeak(t *testing.T) {
	clearDatabase(context.Background())

	user1 := createWSUser(t, "user1", "user1@example.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

	user2 := createWSUser(t, "user2", "user2@example.com")

	user3 := createWSUser(t, "user3", "user3@example.com")
	token3, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user3.ID)

	createWSPrivateChat(t, user2.ID, token1)
	chats, _ := testClient.Chat.Query().All(context.Background())
	chatID := chats[0].ID

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token3

	header3 := http.Header{}
	conn3, _, err := ws.DefaultDialer.Dial(wsURL, header3)
	assert.NoError(t, err)
	defer conn3.Close()

	time.Sleep(200 * time.Millisecond)

	reqBody := model.SendMessageRequest{
		ChatID:  chatID,
		Content: "Secret Message",
	}
	rr := makeRequest("POST", "/api/messages", reqBody, token1)
	assert.Equal(t, http.StatusOK, rr.Code)

	event := waitForEvent(t, conn3, websocket.EventMessageNew, 1*time.Second)
	assert.Nil(t, event)
}

func TestWebSocketBlockUnblockSync(t *testing.T) {
	clearDatabase(context.Background())

	user1 := createWSUser(t, "user1", "user1@example.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

	user2 := createWSUser(t, "user2", "user2@example.com")
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user2.ID)

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL1 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token1
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2

	conn1A, _, err := ws.DefaultDialer.Dial(wsURL1, nil)
	assert.NoError(t, err)
	defer conn1A.Close()
	conn1B, _, err := ws.DefaultDialer.Dial(wsURL1, nil)
	assert.NoError(t, err)
	defer conn1B.Close()

	conn2, _, err := ws.DefaultDialer.Dial(wsURL2, nil)
	assert.NoError(t, err)
	defer conn2.Close()

	time.Sleep(200 * time.Millisecond)

	rrBlock := makeRequest("POST", fmt.Sprintf("/api/users/%s/block", user2.ID), nil, token1)
	assert.Equal(t, http.StatusOK, rrBlock.Code)

	assert.NotNil(t, waitForEvent(t, conn1A, websocket.EventUserBlock, 2*time.Second))
	assert.NotNil(t, waitForEvent(t, conn1B, websocket.EventUserBlock, 2*time.Second))
	assert.NotNil(t, waitForEvent(t, conn2, websocket.EventUserBlock, 2*time.Second))

	rrUnblock := makeRequest("POST", fmt.Sprintf("/api/users/%s/unblock", user2.ID), nil, token1)
	assert.Equal(t, http.StatusOK, rrUnblock.Code)

	assert.NotNil(t, waitForEvent(t, conn1A, websocket.EventUserUnblock, 2*time.Second))
	assert.NotNil(t, waitForEvent(t, conn1B, websocket.EventUserUnblock, 2*time.Second))
	assert.NotNil(t, waitForEvent(t, conn2, websocket.EventUserUnblock, 2*time.Second))
}

func TestWebSocketProfileUpdate(t *testing.T) {
	clearDatabase(context.Background())

	user1 := createWSUser(t, "user1", "user1@example.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

	user2 := createWSUser(t, "user2", "user2@example.com")
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user2.ID)

	createWSPrivateChat(t, user2.ID, token1)

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL1 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token1
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2

	conn1A, _, err := ws.DefaultDialer.Dial(wsURL1, nil)
	assert.NoError(t, err)
	defer conn1A.Close()
	conn1B, _, err := ws.DefaultDialer.Dial(wsURL1, nil)
	assert.NoError(t, err)
	defer conn1B.Close()

	conn2, _, err := ws.DefaultDialer.Dial(wsURL2, nil)
	assert.NoError(t, err)
	defer conn2.Close()

	time.Sleep(200 * time.Millisecond)

	rr := makeRequest("PUT", "/api/user/profile", model.UpdateProfileRequest{FullName: "Updated Name"}, token1)
	assert.Equal(t, http.StatusOK, rr.Code)

	assert.NotNil(t, waitForEvent(t, conn1B, websocket.EventUserUpdate, 2*time.Second))
	assert.NotNil(t, waitForEvent(t, conn2, websocket.EventUserUpdate, 2*time.Second))
}

func TestWebSocketMessageDelete(t *testing.T) {
	clearDatabase(context.Background())

	user1 := createWSUser(t, "user1", "user1@example.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

	user2 := createWSUser(t, "user2", "user2@example.com")
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user2.ID)

	createWSPrivateChat(t, user2.ID, token1)
	chats, _ := testClient.Chat.Query().All(context.Background())
	chatID := chats[0].ID

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2

	header2 := http.Header{}
	conn2, _, err := ws.DefaultDialer.Dial(wsURL, header2)
	assert.NoError(t, err)
	defer conn2.Close()

	time.Sleep(200 * time.Millisecond)

	reqBody := model.SendMessageRequest{
		ChatID:  chatID,
		Content: "To be deleted",
	}
	rr := makeRequest("POST", "/api/messages", reqBody, token1)
	assert.Equal(t, http.StatusOK, rr.Code)

	msgData := parseResponse[model.MessageResponse](t, rr)

	rrDel := makeRequest("DELETE", fmt.Sprintf("/api/messages/%s", msgData.ID), nil, token1)
	assert.Equal(t, http.StatusOK, rrDel.Code)

	assert.NotNil(t, waitForEvent(t, conn2, websocket.EventMessageDelete, 2*time.Second))
}

func TestWebSocketMessageUpdate(t *testing.T) {
	clearDatabase(context.Background())

	user1 := createWSUser(t, "user1", "user1@example.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

	user2 := createWSUser(t, "user2", "user2@example.com")
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user2.ID)

	createWSPrivateChat(t, user2.ID, token1)
	chats, _ := testClient.Chat.Query().All(context.Background())
	chatID := chats[0].ID

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2

	header2 := http.Header{}
	conn2, _, err := ws.DefaultDialer.Dial(wsURL, header2)
	assert.NoError(t, err)
	defer conn2.Close()

	time.Sleep(200 * time.Millisecond)

	reqBody := model.SendMessageRequest{
		ChatID:  chatID,
		Content: "Original Content",
	}
	rr := makeRequest("POST", "/api/messages", reqBody, token1)
	assert.Equal(t, http.StatusOK, rr.Code)

	msgData := parseResponse[model.MessageResponse](t, rr)

	waitForEvent(t, conn2, websocket.EventMessageNew, 2*time.Second)

	editBody := model.EditMessageRequest{
		Content: "Edited Content",
	}
	rrEdit := makeRequest("PUT", fmt.Sprintf("/api/messages/%s", msgData.ID), editBody, token1)
	assert.Equal(t, http.StatusOK, rrEdit.Code)

	assert.NotNil(t, waitForEvent(t, conn2, websocket.EventMessageUpdate, 2*time.Second))
}

func TestWebSocketChatHide(t *testing.T) {
	clearDatabase(context.Background())

	user1 := createWSUser(t, "user1", "user1@example.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

	user2 := createWSUser(t, "user2", "user2@example.com")

	createWSPrivateChat(t, user2.ID, token1)
	chats, _ := testClient.Chat.Query().All(context.Background())
	chatID := chats[0].ID

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token1

	header1 := http.Header{}
	conn1, _, err := ws.DefaultDialer.Dial(wsURL, header1)
	assert.NoError(t, err)
	defer conn1.Close()

	time.Sleep(200 * time.Millisecond)

	rr := makeRequest("POST", fmt.Sprintf("/api/chats/%s/hide", chatID), nil, token1)
	assert.Equal(t, http.StatusOK, rr.Code)

	assert.NotNil(t, waitForEvent(t, conn1, websocket.EventChatHide, 2*time.Second))
}

func TestWebSocketGroupChatCreation(t *testing.T) {
	clearDatabase(context.Background())
	u1 := createWSUser(t, "u1", "u1@test.com")
	u2 := createWSUser(t, "u2", "u2@test.com")
	u3 := createWSUser(t, "u3", "u3@test.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)
	token3, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u3.ID)

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL1 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token1
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2
	wsURL3 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token3

	conn1, _, _ := ws.DefaultDialer.Dial(wsURL1, nil)
	defer conn1.Close()
	conn2, _, _ := ws.DefaultDialer.Dial(wsURL2, nil)
	defer conn2.Close()
	conn3, _, _ := ws.DefaultDialer.Dial(wsURL3, nil)
	defer conn3.Close()

	time.Sleep(200 * time.Millisecond)

	createWSGroupChat(t, token1, "Test Group WS", []uuid.UUID{u2.ID, u3.ID}, false)

	assert.NotNil(t, waitForEvent(t, conn1, websocket.EventChatNew, 2*time.Second))
	assert.NotNil(t, waitForEvent(t, conn2, websocket.EventChatNew, 2*time.Second))
	assert.NotNil(t, waitForEvent(t, conn3, websocket.EventChatNew, 2*time.Second))
}

func TestWebSocketAddGroupMember(t *testing.T) {
	clearDatabase(context.Background())
	u1 := createWSUser(t, "u1", "u1@test.com")
	u2 := createWSUser(t, "u2", "u2@test.com")
	u3 := createWSUser(t, "u3", "u3@test.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)
	token3, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u3.ID)

	chatID := createWSGroupChat(t, token1, "Add Member WS Test", []uuid.UUID{u2.ID}, false)

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL1 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token1
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2
	wsURL3 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token3

	conn1, _, _ := ws.DefaultDialer.Dial(wsURL1, nil)
	defer conn1.Close()
	conn2, _, _ := ws.DefaultDialer.Dial(wsURL2, nil)
	defer conn2.Close()
	conn3, _, _ := ws.DefaultDialer.Dial(wsURL3, nil)
	defer conn3.Close()

	time.Sleep(200 * time.Millisecond)

	addReqBody := model.AddGroupMemberRequest{UserIDs: []uuid.UUID{u3.ID}}
	rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members", chatID), addReqBody, token1)
	assert.Equal(t, http.StatusOK, rr.Code)

	events3 := waitForEvents(t, conn3, []websocket.EventType{websocket.EventChatNew, websocket.EventMessageNew}, 2*time.Second)
	assert.NotNil(t, events3[websocket.EventChatNew])
	assert.NotNil(t, events3[websocket.EventMessageNew])

	if msgEvent, ok := events3[websocket.EventMessageNew]; ok {
		payloadMap, ok := msgEvent.Payload.(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, float64(3), payloadMap["member_count"])
	}

	assert.NotNil(t, waitForEvent(t, conn1, websocket.EventMessageNew, 2*time.Second))
	assert.NotNil(t, waitForEvent(t, conn2, websocket.EventMessageNew, 2*time.Second))
}

func TestWebSocketUpdateGroupChat(t *testing.T) {
	clearDatabase(context.Background())
	u1 := createWSUser(t, "u1", "u1@test.com")
	u2 := createWSUser(t, "u2", "u2@test.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	chatID := createWSGroupChat(t, token1, "Update WS Test", []uuid.UUID{u2.ID}, false)

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL1 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token1
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2

	conn1, _, _ := ws.DefaultDialer.Dial(wsURL1, nil)
	defer conn1.Close()
	conn2, _, _ := ws.DefaultDialer.Dial(wsURL2, nil)
	defer conn2.Close()

	time.Sleep(200 * time.Millisecond)

	newName := "New Group Name"
	rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s", chatID), model.UpdateGroupChatRequest{Name: &newName}, token1)
	assert.Equal(t, http.StatusOK, rr.Code)

	events := waitForEvents(t, conn2, []websocket.EventType{websocket.EventChatUpdate, websocket.EventMessageNew}, 2*time.Second)
	assert.NotNil(t, events[websocket.EventChatUpdate])
	assert.NotNil(t, events[websocket.EventMessageNew])
}

func TestWebSocketUpdateGroupVisibility(t *testing.T) {
	clearDatabase(context.Background())
	u1 := createWSUser(t, "u1", "u1@test.com")
	u2 := createWSUser(t, "u2", "u2@test.com")
	u3 := createWSUser(t, "u3", "u3@test.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)
	token3, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u3.ID)

	chatID := createWSGroupChat(t, token1, "Visibility Test", []uuid.UUID{u2.ID, u3.ID}, false)

	roleBody := model.UpdateGroupMemberRoleRequest{Role: "admin"}
	rrRole := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", chatID, u3.ID), roleBody, token1)
	assert.Equal(t, http.StatusOK, rrRole.Code)

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2
	wsURL3 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token3

	conn2, _, _ := ws.DefaultDialer.Dial(wsURL2, nil)
	defer conn2.Close()
	conn3, _, _ := ws.DefaultDialer.Dial(wsURL3, nil)
	defer conn3.Close()

	time.Sleep(200 * time.Millisecond)

	isPublic := true
	rrUpdate := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s", chatID), model.UpdateGroupChatRequest{IsPublic: &isPublic}, token1)
	assert.Equal(t, http.StatusOK, rrUpdate.Code)

	event2 := waitForEvent(t, conn2, websocket.EventChatUpdate, 2*time.Second)
	assert.NotNil(t, event2)
	if event2 != nil {
		payload := event2.Payload.(map[string]interface{})
		assert.True(t, payload["is_public"].(bool))
		assert.NotNil(t, payload["invite_code"])
	}

	event3 := waitForEvent(t, conn3, websocket.EventChatUpdate, 2*time.Second)
	assert.NotNil(t, event3)
	if event3 != nil {
		payload := event3.Payload.(map[string]interface{})
		assert.True(t, payload["is_public"].(bool))
		assert.NotNil(t, payload["invite_code"])
	}

	isPublic = false
	rrUpdate2 := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s", chatID), model.UpdateGroupChatRequest{IsPublic: &isPublic}, token1)
	assert.Equal(t, http.StatusOK, rrUpdate2.Code)

	event2Private := waitForEvent(t, conn2, websocket.EventChatUpdate, 2*time.Second)
	assert.NotNil(t, event2Private)
	if event2Private != nil {
		payload := event2Private.Payload.(map[string]interface{})
		assert.False(t, payload["is_public"].(bool))
		assert.Nil(t, payload["invite_code"])
	}

	foundCode := false
	deadline := time.Now().Add(3 * time.Second)
	conn3.SetReadDeadline(deadline)

	for {
		if time.Now().After(deadline) {
			break
		}
		_, msg, err := conn3.ReadMessage()
		if err != nil {
			break
		}
		var ev websocket.Event
		if err := json.Unmarshal(msg, &ev); err != nil {
			continue
		}

		if ev.Type == websocket.EventChatUpdate {
			payload := ev.Payload.(map[string]interface{})
			if payload["invite_code"] != nil {
				foundCode = true
				assert.NotNil(t, payload["invite_expires_at"])
				break
			}
		}
	}
	assert.True(t, foundCode)
}

func TestWebSocketResetInviteCodeBroadcast(t *testing.T) {
	clearDatabase(context.Background())
	u1 := createWSUser(t, "u1", "u1@test.com")
	u2 := createWSUser(t, "u2", "u2@test.com")
	u3 := createWSUser(t, "u3", "u3@test.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)
	token3, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u3.ID)

	chatID := createWSGroupChat(t, token1, "Reset Code WS Test", []uuid.UUID{u2.ID, u3.ID}, false)

	roleBody := model.UpdateGroupMemberRoleRequest{Role: "admin"}
	rrRole := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", chatID, u2.ID), roleBody, token1)
	assert.Equal(t, http.StatusOK, rrRole.Code)

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2
	wsURL3 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token3

	conn2, _, _ := ws.DefaultDialer.Dial(wsURL2, nil)
	defer conn2.Close()
	conn3, _, _ := ws.DefaultDialer.Dial(wsURL3, nil)
	defer conn3.Close()

	time.Sleep(200 * time.Millisecond)

	rrReset := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s/invite", chatID), nil, token1)
	assert.Equal(t, http.StatusOK, rrReset.Code)

	event2 := waitForEvent(t, conn2, websocket.EventChatUpdate, 2*time.Second)
	assert.NotNil(t, event2)
	if event2 != nil {
		payload := event2.Payload.(map[string]interface{})
		assert.NotNil(t, payload["invite_code"])
		assert.NotNil(t, payload["invite_expires_at"])
	}

	event3 := waitForEvent(t, conn3, websocket.EventChatUpdate, 1*time.Second)
	assert.Nil(t, event3)
}

func TestWebSocketKickMember(t *testing.T) {
	clearDatabase(context.Background())
	u1 := createWSUser(t, "u1", "u1@test.com")
	u2 := createWSUser(t, "u2", "u2@test.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	chatID := createWSGroupChat(t, token1, "Kick WS Test", []uuid.UUID{u2.ID}, false)

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL1 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token1
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2

	conn1, _, _ := ws.DefaultDialer.Dial(wsURL1, nil)
	defer conn1.Close()
	conn2, _, _ := ws.DefaultDialer.Dial(wsURL2, nil)
	defer conn2.Close()

	time.Sleep(200 * time.Millisecond)

	rrKick := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", chatID, u2.ID), nil, token1)
	assert.Equal(t, http.StatusOK, rrKick.Code)

	assert.NotNil(t, waitForEvent(t, conn1, websocket.EventMessageNew, 2*time.Second))

	event := waitForEvent(t, conn2, websocket.EventChatDelete, 2*time.Second)
	assert.NotNil(t, event)
}

func TestWebSocketUpdateRole(t *testing.T) {
	clearDatabase(context.Background())
	u1 := createWSUser(t, "u1", "u1@test.com")
	u2 := createWSUser(t, "u2", "u2@test.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	chatID := createWSGroupChat(t, token1, "Role WS Test", []uuid.UUID{u2.ID}, false)

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL1 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token1
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2

	conn1, _, _ := ws.DefaultDialer.Dial(wsURL1, nil)
	defer conn1.Close()
	conn2, _, _ := ws.DefaultDialer.Dial(wsURL2, nil)
	defer conn2.Close()

	time.Sleep(200 * time.Millisecond)

	roleBody := model.UpdateGroupMemberRoleRequest{Role: "admin"}
	rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", chatID, u2.ID), roleBody, token1)
	assert.Equal(t, http.StatusOK, rr.Code)

	verifyEvent(t, conn1, websocket.EventMessageNew, u1.ID, uuid.Nil)
	verifyEvent(t, conn2, websocket.EventMessageNew, u1.ID, uuid.Nil)
}

func TestWebSocketTransferOwnership(t *testing.T) {
	clearDatabase(context.Background())
	u1 := createWSUser(t, "u1", "u1@test.com")
	u2 := createWSUser(t, "u2", "u2@test.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	chatID := createWSGroupChat(t, token1, "Transfer WS Test", []uuid.UUID{u2.ID}, false)

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL1 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token1
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2

	conn1, _, _ := ws.DefaultDialer.Dial(wsURL1, nil)
	defer conn1.Close()
	conn2, _, _ := ws.DefaultDialer.Dial(wsURL2, nil)
	defer conn2.Close()

	time.Sleep(200 * time.Millisecond)

	transferBody := model.TransferGroupOwnershipRequest{NewOwnerID: u2.ID}
	rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/transfer", chatID), transferBody, token1)
	assert.Equal(t, http.StatusOK, rr.Code)

	verifyEvent(t, conn1, websocket.EventMessageNew, u1.ID, uuid.Nil)
	verifyEvent(t, conn2, websocket.EventMessageNew, u1.ID, uuid.Nil)
}

func TestWebSocketAccountDeletion(t *testing.T) {
	clearDatabase(context.Background())

	u1 := createWSUser(t, "u1", "u1@test.com")
	u2 := createWSUser(t, "u2", "u2@test.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	createWSPrivateChat(t, u2.ID, token1)

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2

	conn2, _, err := ws.DefaultDialer.Dial(wsURL2, nil)
	assert.NoError(t, err)
	defer conn2.Close()

	time.Sleep(200 * time.Millisecond)

	password := "password123"
	reqBody := model.DeleteAccountRequest{Password: &password}
	rr := makeRequest("DELETE", "/api/account", reqBody, token1)
	assert.Equal(t, http.StatusOK, rr.Code)

	verifyEvent(t, conn2, websocket.EventUserDeleted, u1.ID, uuid.Nil)
}

func TestWebSocketUnbanEvent(t *testing.T) {
	clearDatabase(context.Background())

	admin := createWSUser(t, "admin", "admin@test.com")
	testClient.User.UpdateOne(admin).SetRole(user.RoleAdmin).ExecX(context.Background())
	adminToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, admin.ID)

	user1 := createWSUser(t, "user1", "user1@test.com")
	user2 := createWSUser(t, "user2", "user2@test.com")
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user2.ID)

	createWSPrivateChat(t, user1.ID, token2)

	testClient.User.UpdateOne(user1).SetIsBanned(true).ExecX(context.Background())

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2

	conn2, _, err := ws.DefaultDialer.Dial(wsURL2, nil)
	assert.NoError(t, err)
	defer conn2.Close()

	time.Sleep(200 * time.Millisecond)

	rr := makeRequest("POST", fmt.Sprintf("/api/admin/users/%s/unban", user1.ID), nil, adminToken)
	assert.Equal(t, http.StatusOK, rr.Code)

	verifyEvent(t, conn2, websocket.EventUserUnbanned, admin.ID, uuid.Nil)
}

func TestWebSocketJoinGroupEvents(t *testing.T) {
	clearDatabase(context.Background())
	u1 := createWSUser(t, "u1", "u1@test.com")
	u2 := createWSUser(t, "u2", "u2@test.com")
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	uDummy := createWSUser(t, "dummy", "dummy@test.com")
	chatID := createWSGroupChat(t, token1, "Public Group WS", []uuid.UUID{uDummy.ID}, true)

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL1 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token1
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2

	conn1, _, _ := ws.DefaultDialer.Dial(wsURL1, nil)
	defer conn1.Close()
	conn2, _, _ := ws.DefaultDialer.Dial(wsURL2, nil)
	defer conn2.Close()

	time.Sleep(200 * time.Millisecond)

	rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/join", chatID), nil, token2)
	assert.Equal(t, http.StatusOK, rr.Code)

	verifyEvent(t, conn2, websocket.EventChatNew, u2.ID, uuid.Nil)
	verifyEvent(t, conn1, websocket.EventMessageNew, u2.ID, uuid.Nil)
	verifyEvent(t, conn2, websocket.EventMessageNew, u2.ID, uuid.Nil)
}

func createWSUser(t *testing.T, username, email string) *ent.User {
	hashedPassword, _ := helper.HashPassword("password123")

	if len(username) < 3 {
		username = username + "user"
	}
	u, err := testClient.User.Create().
		SetUsername(username).
		SetEmail(email).
		SetFullName(username).
		SetPasswordHash(hashedPassword).
		Save(context.Background())
	assert.NoError(t, err)
	return u
}

func createWSPrivateChat(t *testing.T, user2ID uuid.UUID, token string) {
	reqBody := model.CreatePrivateChatRequest{
		TargetUserID: user2ID,
	}
	rr := makeRequest("POST", "/api/chats/private", reqBody, token)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func verifyEvent(t *testing.T, conn *ws.Conn, eventType websocket.EventType, senderID, blockedID uuid.UUID) {
	conn.SetReadDeadline(time.Now().UTC().Add(2 * time.Second))
	foundEvent := false
	for i := 0; i < 5; i++ {
		_, message, err := conn.ReadMessage()
		if err != nil {
			break
		}
		var event websocket.Event
		json.Unmarshal(message, &event)

		if event.Type == eventType {
			foundEvent = true
			if event.Meta != nil {
				assert.Equal(t, senderID.String(), event.Meta.SenderID.String())
			}

			if eventType == websocket.EventUserBlock || eventType == websocket.EventUserUnblock {
				payload, ok := event.Payload.(map[string]interface{})
				assert.True(t, ok)
				assert.Equal(t, senderID.String(), payload["blocker_id"])
				assert.Equal(t, blockedID.String(), payload["blocked_id"])
			}
			break
		}
	}
	assert.True(t, foundEvent)
}
