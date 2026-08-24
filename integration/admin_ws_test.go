//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/websocket"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

func TestAdminWS_ResetGroupInfo(t *testing.T) {
	server := httptest.NewServer(testRouter)
	defer server.Close()

	admin := createTestUser(t, "admin_ws")
	testClient.User.UpdateOne(admin).SetRole(user.RoleAdmin).Exec(context.Background())
	adminToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, admin.ID)

	member := createTestUser(t, "member_ws")
	memberToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, member.ID)

	chat, err := testClient.Chat.Create().SetType("group").Save(context.Background())
	if err != nil {
		t.Fatalf("Failed to create chat: %v", err)
	}

	group, err := testClient.GroupChat.Create().
		SetName("Test Group WS").
		SetCreatedBy(member.ID).
		SetChat(chat).
		SetInviteCode("INVITE123").
		Save(context.Background())
	if err != nil {
		t.Fatalf("Failed to create group: %v", err)
	}

	testClient.GroupMember.Create().
		SetGroupChat(group).
		SetUser(member).
		SetRole(groupmember.RoleOwner).
		Save(context.Background())

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + memberToken
	wsConn, _, err := gorilla.DefaultDialer.Dial(wsURL, nil)
	assert.NoError(t, err)
	defer wsConn.Close()

	resetPayload := model.ResetGroupInfoRequest{
		ResetName:        true,
		ResetDescription: true,
	}
	resp := makeRequest("POST", "/api/admin/groups/"+chat.ID.String()+"/reset", resetPayload, adminToken)
	assert.Equal(t, http.StatusOK, resp.Code)

	wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))

	var receivedEvent websocket.Event
	found := false
	for {
		err := wsConn.ReadJSON(&receivedEvent)
		if err != nil {
			break
		}
		if receivedEvent.Type == "chat.update" {
			payloadMap, ok := receivedEvent.Payload.(map[string]any)
			if ok {
				if id, ok := payloadMap["id"].(string); ok && id == chat.ID.String() {
					found = true
					assert.Contains(t, payloadMap["name"], "Group "+group.ID.String()[:8])
					break
				}
			}
		}
	}
	assert.True(t, found)
}

func TestAdminWS_ResolveReport_DeleteMessage(t *testing.T) {
	server := httptest.NewServer(testRouter)
	defer server.Close()

	admin := createTestUser(t, "admin_rr")
	testClient.User.UpdateOne(admin).SetRole(user.RoleAdmin).Exec(context.Background())
	adminToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, admin.ID)

	user1 := createTestUser(t, "user_rr")
	user1Token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

	user2 := createTestUser(t, "user_rr_2")

	chat, _ := testClient.Chat.Create().SetType("private").Save(context.Background())

	_, err := testClient.PrivateChat.Create().
		SetChat(chat).
		SetUser1(user1).
		SetUser2(user2).
		Save(context.Background())
	if err != nil {
		t.Fatalf("Failed to create private chat: %v", err)
	}

	msg, err := testClient.Message.Create().
		SetChat(chat).
		SetSender(user1).
		SetContent("Bad message").
		Save(context.Background())
	if err != nil {
		t.Fatalf("Failed to create message: %v", err)
	}

	report, err := testClient.Report.Create().
		SetReporter(user1).
		SetTargetType("message").
		SetMessage(msg).
		SetReason("Spam").
		Save(context.Background())
	if err != nil {
		t.Fatalf("Failed to create report: %v", err)
	}

	wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + user1Token
	wsConn, _, err := gorilla.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("WebSocket connection failed: %v", err)
	}
	defer wsConn.Close()

	resolvePayload := model.ResolveReportRequest{
		Status: "resolved",
		Notes:  "Deleted content",
	}
	resp := makeRequest("PUT", "/api/admin/reports/"+report.ID.String()+"/resolve", resolvePayload, adminToken)
	assert.Equal(t, http.StatusOK, resp.Code)

	wsConn.SetReadDeadline(time.Now().Add(5 * time.Second))
	found := false
	var receivedEvent websocket.Event
	for {
		err := wsConn.ReadJSON(&receivedEvent)
		if err != nil {
			break
		}
		if receivedEvent.Type == "message.delete" {
			payloadMap, ok := receivedEvent.Payload.(map[string]any)
			if ok {
				if id, ok := payloadMap["message_id"].(string); ok && id == msg.ID.String() {
					found = true
					break
				}
			}
		}
	}
	assert.True(t, found)

	deletedMsg, _ := testClient.Message.Query().Where(message.ID(msg.ID)).Only(context.Background())
	assert.NotNil(t, deletedMsg.DeletedAt)
}
