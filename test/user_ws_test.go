package test

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/websocket"
	"bytes"
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

	req := newGroupJSONRequest("POST", "/api/chats/group", token, model.CreateGroupChatRequest{
		Name:      name,
		MemberIDs: memberIDs,
		IsPublic:  isPublic,
	})
	rr := executeRequest(req)
	if !assert.Equal(t, http.StatusOK, rr.Code) {
		printBody(t, rr)
		return uuid.Nil
	}

	var chatResp helper.ResponseSuccess
	json.Unmarshal(rr.Body.Bytes(), &chatResp)
	chatData := chatResp.Data.(map[string]interface{})
	chatID, _ := uuid.Parse(chatData["id"].(string))
	return chatID
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
	assert.Equal(t, int64(1), exists, "Redis key for online status should exist")

	ttl, err := redisAdapter.Client().TTL(context.Background(), key).Result()
	assert.NoError(t, err)
	assert.True(t, ttl > 0, "Redis key should have a TTL")

	assert.True(t, ttl <= 70*time.Second, "TTL should be <= 70s")
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
	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/api/messages", bytes.NewBuffer(jsonBody))
	req.Header.Set("Authorization", "Bearer "+token1)
	req.Header.Set("Content-Type", "application/json")

	rr := executeRequest(req)
	assert.Equal(t, http.StatusOK, rr.Code)

	event := waitForEvent(t, conn2, websocket.EventMessageNew, 2*time.Second)
	assert.NotNil(t, event, "Should have received message.new event")

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
	assert.NotNil(t, event, "User 2 should receive typing event")
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

	time.Sleep(200 * time.Millisecond)

	header2 := http.Header{}
	conn2, _, err := ws.DefaultDialer.Dial(wsURL2, header2)
	assert.NoError(t, err)

	eventOnline := waitForEvent(t, conn1, websocket.EventUserOnline, 2*time.Second)
	assert.NotNil(t, eventOnline, "User 1 should receive user.online event for User 2")
	if eventOnline != nil {
		payload, _ := eventOnline.Payload.(map[string]interface{})
		assert.Equal(t, user2.ID.String(), payload["user_id"])
	}

	conn2.Close()
	time.Sleep(200 * time.Millisecond)

	eventOffline := waitForEvent(t, conn1, websocket.EventUserOffline, 2*time.Second)
	assert.NotNil(t, eventOffline, "User 1 should receive user.offline event for User 2")
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
	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/api/messages", bytes.NewBuffer(jsonBody))
	req.Header.Set("Authorization", "Bearer "+token1)
	req.Header.Set("Content-Type", "application/json")
	executeRequest(req)

	eventA := waitForEvent(t, conn2A, websocket.EventMessageNew, 2*time.Second)
	assert.NotNil(t, eventA, "Device A should receive message")

	eventB := waitForEvent(t, conn2B, websocket.EventMessageNew, 2*time.Second)
	assert.NotNil(t, eventB, "Device B should receive message")
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
	jsonMsg, _ := json.Marshal(reqMsg)
	reqSend, _ := http.NewRequest("POST", "/api/messages", bytes.NewBuffer(jsonMsg))
	reqSend.Header.Set("Authorization", "Bearer "+token1)
	reqSend.Header.Set("Content-Type", "application/json")
	executeRequest(reqSend)

	time.Sleep(200 * time.Millisecond)

	req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/%s/read", chatID), nil)
	req.Header.Set("Authorization", "Bearer "+token2)
	executeRequest(req)

	event := waitForEvent(t, conn2B, websocket.EventChatRead, 2*time.Second)
	assert.NotNil(t, event, "Device B should receive chat.read event")
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
	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/api/messages", bytes.NewBuffer(jsonBody))
	req.Header.Set("Authorization", "Bearer "+token1)
	req.Header.Set("Content-Type", "application/json")
	executeRequest(req)

	event := waitForEvent(t, conn3, websocket.EventMessageNew, 1*time.Second)
	assert.Nil(t, event, "User 3 (Outsider) should NOT receive message.new event")
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

	req, _ := http.NewRequest("POST", fmt.Sprintf("/api/users/%s/block", user2.ID), nil)
	req.Header.Set("Authorization", "Bearer "+token1)
	executeRequest(req)

	assert.NotNil(t, waitForEvent(t, conn1A, websocket.EventUserBlock, 2*time.Second))
	assert.NotNil(t, waitForEvent(t, conn1B, websocket.EventUserBlock, 2*time.Second))
	assert.NotNil(t, waitForEvent(t, conn2, websocket.EventUserBlock, 2*time.Second))

	reqUnblock, _ := http.NewRequest("POST", fmt.Sprintf("/api/users/%s/unblock", user2.ID), nil)
	reqUnblock.Header.Set("Authorization", "Bearer "+token1)
	executeRequest(reqUnblock)

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

	req := newProfileJSONRequest(token1, model.UpdateProfileRequest{FullName: "Updated Name"})
	executeRequest(req)

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
	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/api/messages", bytes.NewBuffer(jsonBody))
	req.Header.Set("Authorization", "Bearer "+token1)
	req.Header.Set("Content-Type", "application/json")
	rr := executeRequest(req)

	var msgResp helper.ResponseSuccess
	json.Unmarshal(rr.Body.Bytes(), &msgResp)
	msgData := msgResp.Data.(map[string]interface{})
	msgIDStr := msgData["id"].(string)
	msgID, _ := uuid.Parse(msgIDStr)

	reqDel, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/messages/%s", msgID), nil)
	reqDel.Header.Set("Authorization", "Bearer "+token1)
	executeRequest(reqDel)

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
	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/api/messages", bytes.NewBuffer(jsonBody))
	req.Header.Set("Authorization", "Bearer "+token1)
	req.Header.Set("Content-Type", "application/json")
	rr := executeRequest(req)

	var msgResp helper.ResponseSuccess
	json.Unmarshal(rr.Body.Bytes(), &msgResp)
	msgData := msgResp.Data.(map[string]interface{})
	msgIDStr := msgData["id"].(string)
	msgID, _ := uuid.Parse(msgIDStr)

	waitForEvent(t, conn2, websocket.EventMessageNew, 2*time.Second)

	editBody := model.EditMessageRequest{
		Content: "Edited Content",
	}
	jsonEdit, _ := json.Marshal(editBody)
	reqEdit, _ := http.NewRequest("PUT", fmt.Sprintf("/api/messages/%s", msgID), bytes.NewBuffer(jsonEdit))
	reqEdit.Header.Set("Authorization", "Bearer "+token1)
	reqEdit.Header.Set("Content-Type", "application/json")
	executeRequest(reqEdit)

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

	req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/%s/hide", chatID), nil)
	req.Header.Set("Authorization", "Bearer "+token1)
	executeRequest(req)

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
	addBody, _ := json.Marshal(addReqBody)
	addReq, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members", chatID), bytes.NewBuffer(addBody))
	addReq.Header.Set("Content-Type", "application/json")
	addReq.Header.Set("Authorization", "Bearer "+token1)
	executeRequest(addReq)

	events3 := waitForEvents(t, conn3, []websocket.EventType{websocket.EventChatNew, websocket.EventMessageNew}, 2*time.Second)
	assert.NotNil(t, events3[websocket.EventChatNew], "u3 should receive chat.new")
	assert.NotNil(t, events3[websocket.EventMessageNew], "u3 should receive message.new")

	if msgEvent, ok := events3[websocket.EventMessageNew]; ok {
		payloadMap, ok := msgEvent.Payload.(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, float64(3), payloadMap["member_count"], "member_count should be 3")
	}

	assert.NotNil(t, waitForEvent(t, conn1, websocket.EventMessageNew, 2*time.Second), "u1 should receive message.new")
	assert.NotNil(t, waitForEvent(t, conn2, websocket.EventMessageNew, 2*time.Second), "u2 should receive message.new")
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
	updateReq := newGroupJSONRequest("PUT", fmt.Sprintf("/api/chats/group/%s", chatID), token1, model.UpdateGroupChatRequest{Name: &newName})
	executeRequest(updateReq)

	events := waitForEvents(t, conn2, []websocket.EventType{websocket.EventChatUpdate, websocket.EventMessageNew}, 2*time.Second)
	assert.NotNil(t, events[websocket.EventChatUpdate], "User 2 should receive chat.update")
	assert.NotNil(t, events[websocket.EventMessageNew], "User 2 should receive message.new")
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
	jsonRole, _ := json.Marshal(roleBody)
	roleReq, _ := http.NewRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", chatID, u3.ID), bytes.NewBuffer(jsonRole))
	roleReq.Header.Set("Content-Type", "application/json")
	roleReq.Header.Set("Authorization", "Bearer "+token1)
	executeRequest(roleReq)

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
	updateReq := newGroupJSONRequest("PUT", fmt.Sprintf("/api/chats/group/%s", chatID), token1, model.UpdateGroupChatRequest{IsPublic: &isPublic})
	executeRequest(updateReq)

	event2 := waitForEvent(t, conn2, websocket.EventChatUpdate, 2*time.Second)
	assert.NotNil(t, event2, "Member (u2) should receive chat.update")
	if event2 != nil {
		payload := event2.Payload.(map[string]interface{})
		assert.True(t, payload["is_public"].(bool))

		assert.NotNil(t, payload["invite_code"], "Member SHOULD see invite_code for public group")
	}

	event3 := waitForEvent(t, conn3, websocket.EventChatUpdate, 2*time.Second)
	assert.NotNil(t, event3, "Admin (u3) should receive chat.update")
	if event3 != nil {
		payload := event3.Payload.(map[string]interface{})
		assert.True(t, payload["is_public"].(bool))

		assert.NotNil(t, payload["invite_code"], "Admin SHOULD see invite_code for public group")
	}

	isPublic = false
	updateReq2 := newGroupJSONRequest("PUT", fmt.Sprintf("/api/chats/group/%s", chatID), token1, model.UpdateGroupChatRequest{IsPublic: &isPublic})
	executeRequest(updateReq2)

	event2Private := waitForEvent(t, conn2, websocket.EventChatUpdate, 2*time.Second)
	assert.NotNil(t, event2Private, "Member (u2) should receive chat.update")
	if event2Private != nil {
		payload := event2Private.Payload.(map[string]interface{})
		assert.False(t, payload["is_public"].(bool))
		assert.Nil(t, payload["invite_code"], "Member should NOT see invite_code")
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
	assert.True(t, foundCode, "Admin (u3) should receive at least one chat.update event WITH invite_code")
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
	jsonRole, _ := json.Marshal(roleBody)
	roleReq, _ := http.NewRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", chatID, u2.ID), bytes.NewBuffer(jsonRole))
	roleReq.Header.Set("Content-Type", "application/json")
	roleReq.Header.Set("Authorization", "Bearer "+token1)
	executeRequest(roleReq)

	server := httptest.NewServer(testRouter)
	defer server.Close()
	wsURL2 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2
	wsURL3 := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token3

	conn2, _, _ := ws.DefaultDialer.Dial(wsURL2, nil)
	defer conn2.Close()
	conn3, _, _ := ws.DefaultDialer.Dial(wsURL3, nil)
	defer conn3.Close()

	time.Sleep(200 * time.Millisecond)

	resetReq, _ := http.NewRequest("PUT", fmt.Sprintf("/api/chats/group/%s/invite", chatID), nil)
	resetReq.Header.Set("Authorization", "Bearer "+token1)
	executeRequest(resetReq)

	event2 := waitForEvent(t, conn2, websocket.EventChatUpdate, 2*time.Second)
	assert.NotNil(t, event2, "Admin (u2) should receive chat.update event")
	if event2 != nil {
		payload := event2.Payload.(map[string]interface{})
		assert.NotNil(t, payload["invite_code"])
		assert.NotNil(t, payload["invite_expires_at"])
	}

	event3 := waitForEvent(t, conn3, websocket.EventChatUpdate, 1*time.Second)
	assert.Nil(t, event3, "Member (u3) should NOT receive chat.update event for invite code reset")
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

	kickReq, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", chatID, u2.ID), nil)
	kickReq.Header.Set("Authorization", "Bearer "+token1)
	executeRequest(kickReq)

	assert.NotNil(t, waitForEvent(t, conn1, websocket.EventMessageNew, 2*time.Second))

	event := waitForEvent(t, conn2, websocket.EventChatDelete, 2*time.Second)
	assert.NotNil(t, event, "User 2 should receive chat.delete")
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
	jsonRole, _ := json.Marshal(roleBody)
	roleReq, _ := http.NewRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", chatID, u2.ID), bytes.NewBuffer(jsonRole))
	roleReq.Header.Set("Content-Type", "application/json")
	roleReq.Header.Set("Authorization", "Bearer "+token1)
	executeRequest(roleReq)

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
	jsonTransfer, _ := json.Marshal(transferBody)
	transferReq, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/transfer", chatID), bytes.NewBuffer(jsonTransfer))
	transferReq.Header.Set("Content-Type", "application/json")
	transferReq.Header.Set("Authorization", "Bearer "+token1)
	executeRequest(transferReq)

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
	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("DELETE", "/api/account", bytes.NewBuffer(jsonBody))
	req.Header.Set("Authorization", "Bearer "+token1)
	req.Header.Set("Content-Type", "application/json")
	executeRequest(req)

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

	reqUnban, _ := http.NewRequest("POST", fmt.Sprintf("/api/admin/users/%s/unban", user1.ID), nil)
	reqUnban.Header.Set("Authorization", "Bearer "+adminToken)
	executeRequest(reqUnban)

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

	joinReq, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/join", chatID), nil)
	joinReq.Header.Set("Authorization", "Bearer "+token2)
	executeRequest(joinReq)

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
	jsonBody, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/api/chats/private", bytes.NewBuffer(jsonBody))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := executeRequest(req)
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
	assert.True(t, foundEvent, "Should have received event '"+string(eventType)+"'")
}
