//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/websocket"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gorilla "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

func TestGroupModeration_DeleteMessage(t *testing.T) {
	clearDatabase(context.Background())

	password := "Password123!"
	hashedPassword, _ := helper.HashPassword(password)

	admin := createTestUser(t, "admin_mod")
	testClient.User.UpdateOne(admin).SetRole(user.RoleUser).SetPasswordHash(hashedPassword).ExecX(context.Background())
	adminToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, admin.ID)

	member := createTestUser(t, "member_mod")
	testClient.User.UpdateOne(member).SetRole(user.RoleUser).SetPasswordHash(hashedPassword).ExecX(context.Background())
	memberToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, member.ID)

	chatEntity := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc := testClient.GroupChat.Create().
		SetChat(chatEntity).
		SetCreator(admin).
		SetName("Moderation Group").
		SetInviteCode("MOD123").
		SaveX(context.Background())

	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(admin).SetRole(groupmember.RoleOwner).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(member).SetRole(groupmember.RoleMember).SaveX(context.Background())

	msgMember := testClient.Message.Create().
		SetChat(chatEntity).
		SetSender(member).
		SetContent("Member Message").
		SaveX(context.Background())

	msgAdmin := testClient.Message.Create().
		SetChat(chatEntity).
		SetSender(admin).
		SetContent("Admin Message").
		SaveX(context.Background())

	server := httptest.NewServer(testRouter)
	defer server.Close()

	t.Run("Admin Can Delete Member Message", func(t *testing.T) {
		wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + memberToken
		wsConn, _, err := gorilla.DefaultDialer.Dial(wsURL, nil)
		assert.NoError(t, err)
		defer wsConn.Close()

		rr := makeRequest("DELETE", fmt.Sprintf("/api/messages/%s", msgMember.ID), nil, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		updatedMsg, _ := testClient.Message.Query().Where(message.ID(msgMember.ID)).Only(context.Background())
		assert.NotNil(t, updatedMsg.DeletedAt)

		wsConn.SetReadDeadline(time.Now().Add(3 * time.Second))
		var receivedEvent websocket.Event
		found := false
		for {
			err := wsConn.ReadJSON(&receivedEvent)
			if err != nil {
				break
			}
			if receivedEvent.Type == websocket.EventMessageDelete {
				payload := receivedEvent.Payload.(map[string]interface{})
				if payload["message_id"] == msgMember.ID.String() {
					found = true
					break
				}
			}
		}
		assert.True(t, found)
	})

	t.Run("Member Cannot Delete Admin Message", func(t *testing.T) {
		rr := makeRequest("DELETE", fmt.Sprintf("/api/messages/%s", msgAdmin.ID), nil, memberToken)
		assert.Equal(t, http.StatusForbidden, rr.Code)

		updatedMsg, _ := testClient.Message.Query().Where(message.ID(msgAdmin.ID)).Only(context.Background())
		assert.Nil(t, updatedMsg.DeletedAt)
	})
}
