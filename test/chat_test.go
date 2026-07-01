package test

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/ent/userblock"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestGetChats(t *testing.T) {
	clearDatabase(context.Background())

	u1 := createTestUser(t, "user1")
	u2 := createTestUser(t, "user2")
	u3 := createTestUser(t, "user3")

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)

	chat1 := testClient.Chat.Create().SetType(chat.TypePrivate).SetUpdatedAt(time.Now().UTC().Add(-2 * time.Hour)).SaveX(context.Background())

	testClient.PrivateChat.Create().SetChat(chat1).SetUser1(u1).SetUser2(u2).SetUser1UnreadCount(3).SaveX(context.Background())
	msg1 := testClient.Message.Create().SetChat(chat1).SetSender(u2).SetType(message.TypeRegular).SetContent("Old message").SetCreatedAt(time.Now().UTC().Add(-2 * time.Hour)).SaveX(context.Background())
	chat1.Update().SetLastMessage(msg1).SetLastMessageAt(msg1.CreatedAt).ExecX(context.Background())

	chat2 := testClient.Chat.Create().SetType(chat.TypePrivate).SetUpdatedAt(time.Now().UTC().Add(-1 * time.Hour)).SaveX(context.Background())
	testClient.PrivateChat.Create().SetChat(chat2).SetUser1(u1).SetUser2(u3).SetUser1UnreadCount(0).SaveX(context.Background())
	msg2 := testClient.Message.Create().SetChat(chat2).SetSender(u3).SetType(message.TypeRegular).SetContent("New message").SetCreatedAt(time.Now().UTC().Add(-1 * time.Hour)).SaveX(context.Background())
	chat2.Update().SetLastMessage(msg2).SetLastMessageAt(msg2.CreatedAt).ExecX(context.Background())

	chat3 := testClient.Chat.Create().SetType(chat.TypeGroup).SetUpdatedAt(time.Now().UTC()).SaveX(context.Background())
	gc := testClient.GroupChat.Create().SetChat(chat3).SetCreator(u1).SetName("My Group").SetInviteCode("mygroup").SetInviteExpiresAt(time.Now().Add(time.Hour)).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetRole(groupmember.RoleOwner).SetUnreadCount(5).SaveX(context.Background())
	msg3 := testClient.Message.Create().SetChat(chat3).SetSender(u1).SetType(message.TypeRegular).SetContent("Group message").SetCreatedAt(time.Now().UTC()).SaveX(context.Background())
	chat3.Update().SetLastMessage(msg3).SetLastMessageAt(msg3.CreatedAt).ExecX(context.Background())

	t.Run("Success - List All Chats", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/chats", nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		assert.Len(t, dataList, 3)

		c1 := dataList[0].(map[string]interface{})
		c2 := dataList[1].(map[string]interface{})
		c3 := dataList[2].(map[string]interface{})

		assert.Equal(t, chat3.ID.String(), c1["id"])
		assert.Equal(t, "My Group", c1["name"])
		assert.Equal(t, float64(5), c1["unread_count"])
		assert.Equal(t, "mygroup", c1["invite_code"], "Group chat should have invite_code")
		assert.NotNil(t, c1["invite_expires_at"], "Owner should see invite_expires_at")
		assert.Nil(t, c1["other_user_id"], "Group chat should not have other_user_id")
		lastMsg1, _ := c1["last_message"].(map[string]interface{})
		assert.Equal(t, "regular", lastMsg1["type"])

		assert.Equal(t, chat2.ID.String(), c2["id"])

		assert.Contains(t, c2["name"], "user3")
		assert.Equal(t, float64(0), c2["unread_count"], "unread_count should be present even when it is 0")
		assert.Nil(t, c2["invite_code"], "Private chat should NOT have invite_code")
		assert.Equal(t, u3.ID.String(), c2["other_user_id"], "Private chat should have other_user_id")

		assert.Equal(t, chat1.ID.String(), c3["id"])
		assert.Contains(t, c3["name"], "user2")
		assert.Equal(t, float64(3), c3["unread_count"])
		assert.Equal(t, u2.ID.String(), c3["other_user_id"], "Private chat should have other_user_id")
	})

	t.Run("Success - Pagination", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/chats?limit=2", nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 2)
		assert.True(t, resp.Meta.HasNext)
		assert.NotEmpty(t, resp.Meta.NextCursor)

		cursor := resp.Meta.NextCursor
		req2, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats?limit=2&cursor=%s", cursor), nil)
		req2.Header.Set("Authorization", "Bearer "+token1)
		rr2 := executeRequest(req2)

		if !assert.Equal(t, http.StatusOK, rr2.Code) {
			printBody(t, rr2)
		}
		var resp2 helper.ResponseWithPagination
		json.Unmarshal(rr2.Body.Bytes(), &resp2)
		dataList2 := resp2.Data.([]interface{})
		assert.Len(t, dataList2, 1)
		assert.Equal(t, chat1.ID.String(), dataList2[0].(map[string]interface{})["id"])
	})

	t.Run("Success - Search by Username", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/chats?query=user3", nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 1)
		assert.Contains(t, dataList[0].(map[string]interface{})["name"], "user3")
	})

	t.Run("Success - Search No Results", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/chats?query=NamaNgawur123", nil)
		req.Header.Set("Authorization", "Bearer "+token1)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)
		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.Empty(t, resp.Data)
	})

	t.Run("Fail - Invalid Cursor Format", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/chats?cursor=bukan-base64-valid", nil)
		req.Header.Set("Authorization", "Bearer "+token1)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("Success - Last Message Placeholder for Deleted Message", func(t *testing.T) {

		u4 := createTestUser(t, "user4")

		delChat := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(context.Background())
		testClient.PrivateChat.Create().SetChat(delChat).SetUser1(u1).SetUser2(u4).SaveX(context.Background())

		msg := testClient.Message.Create().SetChat(delChat).SetSender(u4).SetType(message.TypeRegular).SetContent("This will be deleted").SaveX(context.Background())
		testClient.Message.UpdateOne(msg).SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		delChat.Update().SetUpdatedAt(time.Now().UTC()).SetLastMessage(msg).SetLastMessageAt(msg.CreatedAt).ExecX(context.Background())

		req, _ := http.NewRequest("GET", "/api/chats", nil)
		req.Header.Set("Authorization", "Bearer "+token1)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		topChat := dataList[0].(map[string]interface{})
		assert.Equal(t, delChat.ID.String(), topChat["id"])

		lastMsg, ok := topChat["last_message"].(map[string]interface{})
		assert.True(t, ok)
		assert.Nil(t, lastMsg["content"])
		assert.NotNil(t, lastMsg["deleted_at"])
	})

	t.Run("Success - Exclude Hidden Private Chat", func(t *testing.T) {

		pc, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chat1.ID)).Only(context.Background())
		testClient.PrivateChat.UpdateOne(pc).SetUser1HiddenAt(time.Now().UTC()).ExecX(context.Background())

		req, _ := http.NewRequest("GET", "/api/chats", nil)
		req.Header.Set("Authorization", "Bearer "+token1)
		rr := executeRequest(req)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		for _, item := range dataList {
			c := item.(map[string]interface{})
			assert.NotEqual(t, chat1.ID.String(), c["id"], "Hidden chat should not appear")
		}
	})

	t.Run("Success - Reappear Hidden Chat on New Message", func(t *testing.T) {

		pc, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chat1.ID)).Only(context.Background())
		testClient.PrivateChat.UpdateOne(pc).SetUser1HiddenAt(time.Now().UTC()).ExecX(context.Background())

		req1, _ := http.NewRequest("GET", "/api/chats", nil)
		req1.Header.Set("Authorization", "Bearer "+token1)
		rr1 := executeRequest(req1)
		var resp1 helper.ResponseWithPagination
		json.Unmarshal(rr1.Body.Bytes(), &resp1)
		dataList1 := resp1.Data.([]interface{})
		for _, item := range dataList1 {
			c := item.(map[string]interface{})
			assert.NotEqual(t, chat1.ID.String(), c["id"], "Chat should be hidden")
		}

		newMsg := testClient.Message.Create().SetChat(chat1).SetSender(u2).SetType(message.TypeRegular).SetContent("New message").SetCreatedAt(time.Now().UTC().Add(time.Second)).SaveX(context.Background())
		testClient.Chat.UpdateOneID(chat1.ID).SetUpdatedAt(time.Now().UTC().Add(time.Second)).SetLastMessage(newMsg).SetLastMessageAt(newMsg.CreatedAt).ExecX(context.Background())

		req2, _ := http.NewRequest("GET", "/api/chats", nil)
		req2.Header.Set("Authorization", "Bearer "+token1)
		rr2 := executeRequest(req2)
		var resp2 helper.ResponseWithPagination
		json.Unmarshal(rr2.Body.Bytes(), &resp2)
		dataList2 := resp2.Data.([]interface{})

		found := false
		for _, item := range dataList2 {
			c := item.(map[string]interface{})
			if c["id"].(string) == chat1.ID.String() {
				found = true
				break
			}
		}
		assert.True(t, found, "Chat should reappear after new activity")
	})

	t.Run("Success - List Chat with Blocked User", func(t *testing.T) {

		media, _ := testClient.Media.Create().
			SetFileName("u2_avatar.jpg").SetOriginalName("u2.jpg").
			SetFileSize(1024).SetMimeType("image/jpeg").
			SetUploader(u2).
			Save(context.Background())
		testClient.User.UpdateOne(u2).SetAvatar(media).ExecX(context.Background())

		testClient.UserBlock.Create().SetBlockerID(u1.ID).SetBlockedID(u2.ID).SaveX(context.Background())

		req, _ := http.NewRequest("GET", "/api/chats", nil)
		req.Header.Set("Authorization", "Bearer "+token1)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		var blockedChat map[string]interface{}
		for _, item := range dataList {
			c := item.(map[string]interface{})
			if c["id"].(string) == chat1.ID.String() {
				blockedChat = c
				break
			}
		}

		assert.NotNil(t, blockedChat)
		assert.True(t, blockedChat["is_blocked_by_me"].(bool))

		assert.NotEqual(t, "", blockedChat["avatar"])
		assert.NotEqual(t, "", blockedChat["name"])

		assert.False(t, blockedChat["is_online"].(bool))
		assert.Equal(t, u2.ID.String(), blockedChat["other_user_id"])

		testClient.UserBlock.Delete().Where(userblock.BlockerID(u1.ID), userblock.BlockedID(u2.ID)).ExecX(context.Background())
	})

	t.Run("Success - Dynamic Target Name Resolution", func(t *testing.T) {

		sysMsg := testClient.Message.Create().
			SetChat(chat3).
			SetSender(u1).
			SetType(message.TypeSystemAdd).
			SetActionData(map[string]interface{}{
				"target_id": u2.ID.String(),
				"actor_id":  u1.ID.String(),
			}).
			SetCreatedAt(time.Now().UTC().Add(time.Hour)).
			SaveX(context.Background())

		chat3.Update().SetLastMessage(sysMsg).SetLastMessageAt(sysMsg.CreatedAt).ExecX(context.Background())

		req, _ := http.NewRequest("GET", "/api/chats", nil)
		req.Header.Set("Authorization", "Bearer "+token1)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		c := dataList[0].(map[string]interface{})
		assert.Equal(t, chat3.ID.String(), c["id"])

		lastMsg := c["last_message"].(map[string]interface{})
		actionData := lastMsg["action_data"].(map[string]interface{})

		assert.Contains(t, actionData["target_name"], "user2")
	})

	t.Run("Success - Exclude Deleted Group", func(t *testing.T) {

		chat3.Update().SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		req, _ := http.NewRequest("GET", "/api/chats", nil)
		req.Header.Set("Authorization", "Bearer "+token1)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		for _, item := range dataList {
			c := item.(map[string]interface{})
			assert.NotEqual(t, chat3.ID.String(), c["id"], "Deleted group should not appear")
		}
	})

	t.Run("Success - Chat with Deleted User", func(t *testing.T) {

		testClient.User.UpdateOne(u2).SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		req, _ := http.NewRequest("GET", "/api/chats", nil)
		req.Header.Set("Authorization", "Bearer "+token1)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		var deletedUserChat map[string]interface{}
		for _, item := range dataList {
			c := item.(map[string]interface{})
			if c["id"].(string) == chat1.ID.String() {
				deletedUserChat = c
				break
			}
		}

		assert.NotNil(t, deletedUserChat)
		assert.True(t, deletedUserChat["other_user_is_deleted"].(bool))
		assert.False(t, deletedUserChat["is_online"].(bool))

	})

	t.Run("Unauthorized", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/chats", nil)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})
}

func TestGetChatByID(t *testing.T) {
	clearDatabase(context.Background())

	u1 := createTestUser(t, "user1")
	u2 := createTestUser(t, "user2")
	u3 := createTestUser(t, "user3")

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token3, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u3.ID)

	chat1 := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(context.Background())
	testClient.PrivateChat.Create().SetChat(chat1).SetUser1(u1).SetUser2(u2).SetUser1UnreadCount(3).SaveX(context.Background())

	t.Run("Success - Get Private Chat", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/%s", chat1.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		data := resp.Data.(map[string]interface{})

		assert.Equal(t, chat1.ID.String(), data["id"])
		assert.Contains(t, data["name"], "user2")
		assert.Equal(t, float64(3), data["unread_count"])
		assert.Nil(t, data["invite_code"], "Private chat should NOT have invite_code")
	})

	t.Run("Success - Get Chat with Blocked User", func(t *testing.T) {
		testClient.UserBlock.Create().SetBlockerID(u1.ID).SetBlockedID(u2.ID).SaveX(context.Background())

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/%s", chat1.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		data := resp.Data.(map[string]interface{})

		assert.True(t, data["is_blocked_by_me"].(bool))
		assert.NotEqual(t, "", data["name"])
		assert.False(t, data["is_online"].(bool))

		testClient.UserBlock.Delete().Where(userblock.BlockerID(u1.ID), userblock.BlockedID(u2.ID)).ExecX(context.Background())
	})

	t.Run("Success - Get Group Chat as Owner (Should see expiry)", func(t *testing.T) {
		chatGroup := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		gc := testClient.GroupChat.Create().SetChat(chatGroup).SetCreator(u1).SetName("Count Test").SetInviteCode("counttest").SetInviteExpiresAt(time.Now().Add(time.Hour)).SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u2).SaveX(context.Background())

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/%s", chatGroup.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		data := resp.Data.(map[string]interface{})

		assert.Equal(t, float64(2), data["member_count"])
		assert.Equal(t, "counttest", data["invite_code"], "Group chat should have invite_code")
		assert.NotNil(t, data["invite_expires_at"], "Owner should see invite_expires_at")
	})

	t.Run("Success - Get Group Chat as Member (Should NOT see expiry)", func(t *testing.T) {
		chatGroup := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		gc := testClient.GroupChat.Create().SetChat(chatGroup).SetCreator(u1).SetName("Member Test").SetInviteCode("membertest").SetInviteExpiresAt(time.Now().Add(time.Hour)).SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).SetRole(groupmember.RoleMember).SaveX(context.Background())

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/%s", chatGroup.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token3)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		data := resp.Data.(map[string]interface{})

		assert.Nil(t, data["invite_code"])
		assert.Nil(t, data["invite_expires_at"], "Regular member should NOT see invite_expires_at")
	})

	t.Run("Success - Get Public Group (Non-Member)", func(t *testing.T) {
		chatPublic := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		testClient.GroupChat.Create().SetChat(chatPublic).SetCreator(u2).SetName("Public Group").SetIsPublic(true).SetInviteCode("publicgroup").SaveX(context.Background())

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/%s", chatPublic.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		data := resp.Data.(map[string]interface{})

		assert.Equal(t, "Public Group", data["name"])
		assert.Equal(t, "publicgroup", data["invite_code"], "Public group should have invite_code")
		assert.Nil(t, data["my_role"], "Should not have a role")
	})

	t.Run("Fail - Get Private Group (Non-Member)", func(t *testing.T) {
		chatPrivate := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		testClient.GroupChat.Create().SetChat(chatPrivate).SetCreator(u2).SetName("Private Group").SetIsPublic(false).SetInviteCode("privategroup").SaveX(context.Background())

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/%s", chatPrivate.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Invalid ID", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/chats/abc", nil)
		req.Header.Set("Authorization", "Bearer "+token1)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Not Found or Forbidden", func(t *testing.T) {

		chat2 := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(context.Background())
		testClient.PrivateChat.Create().SetChat(chat2).SetUser1(u2).SetUser2(u3).SaveX(context.Background())

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/%s", chat2.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Get Deleted Chat", func(t *testing.T) {
		chat1.Update().SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/%s", chat1.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestMarkAsRead(t *testing.T) {
	clearDatabase(context.Background())

	u1 := createTestUser(t, "user1")
	u2 := createTestUser(t, "user2")
	u3 := createTestUser(t, "user3")

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token3, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u3.ID)

	chat1 := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(context.Background())
	testClient.PrivateChat.Create().SetChat(chat1).SetUser1(u1).SetUser2(u2).SetUser1UnreadCount(5).SaveX(context.Background())

	chat2 := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
	gc := testClient.GroupChat.Create().SetChat(chat2).SetCreator(u2).SetName("Test Group").SetInviteCode("testgroup").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetUnreadCount(10).SaveX(context.Background())

	t.Run("Success - Mark Private Chat Read", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/%s/read", chat1.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		pc, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chat1.ID)).Only(context.Background())
		assert.Equal(t, 0, pc.User1UnreadCount)
		assert.NotNil(t, pc.User1LastReadAt)
	})

	t.Run("Success - Mark Read While Blocked (Ninja Mode)", func(t *testing.T) {

		testClient.UserBlock.Create().SetBlockerID(u2.ID).SetBlockedID(u1.ID).SaveX(context.Background())

		pc, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chat1.ID)).Only(context.Background())
		testClient.PrivateChat.UpdateOne(pc).SetUser1UnreadCount(5).SetUser1LastReadAt(time.Time{}).ExecX(context.Background())

		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/%s/read", chat1.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		pcAfter, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chat1.ID)).Only(context.Background())
		assert.Equal(t, 0, pcAfter.User1UnreadCount, "Unread count should be reset")
		assert.True(t, pcAfter.User1LastReadAt.IsZero(), "LastReadAt should NOT be updated (Ninja Mode)")

		testClient.UserBlock.Delete().Where(userblock.BlockerID(u2.ID), userblock.BlockedID(u1.ID)).ExecX(context.Background())
	})

	t.Run("Success - Mark Group Chat Read", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/%s/read", chat2.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		gm, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u1.ID)).Only(context.Background())
		assert.Equal(t, 0, gm.UnreadCount)
		assert.NotNil(t, gm.LastReadAt)
	})

	t.Run("Fail - Not a Member", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/%s/read", chat1.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token3)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Not Found", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/%s/read", uuid.New()), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestHideChat(t *testing.T) {
	clearDatabase(context.Background())

	u1 := createTestUser(t, "user1")
	u2 := createTestUser(t, "user2")

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)

	chat1 := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(context.Background())
	testClient.PrivateChat.Create().SetChat(chat1).SetUser1(u1).SetUser2(u2).SetUser1UnreadCount(5).SaveX(context.Background())

	t.Run("Success - Hide Private Chat and Reset Unread Count", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/%s/hide", chat1.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		pc, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chat1.ID)).Only(context.Background())
		assert.NotNil(t, pc.User1HiddenAt)
		assert.Nil(t, pc.User2HiddenAt)
		assert.Equal(t, 0, pc.User1UnreadCount, "Unread count should be reset to 0 after hiding")
	})

	t.Run("Fail - Chat Not Found", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/%s/hide", uuid.New()), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Not Private Chat", func(t *testing.T) {
		chatGroup := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		testClient.GroupChat.Create().SetChat(chatGroup).SetCreator(u1).SetName("Group").SetInviteCode("group").SaveX(context.Background())

		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/%s/hide", chatGroup.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestGroupUnreadConsistency(t *testing.T) {
	clearDatabase(context.Background())

	u1 := createTestUser(t, "user1")
	u2 := createTestUser(t, "user2")
	u3 := createTestUser(t, "user3")

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	chatGroup := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
	gc := testClient.GroupChat.Create().SetChat(chatGroup).SetCreator(u1).SetName("Test Group").SetInviteCode("testgroup2").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u2).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).SaveX(context.Background())

	reqBody := model.SendMessageRequest{
		ChatID:  chatGroup.ID,
		Content: "Hello Group!",
	}
	body, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", "/api/messages", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token1)
	executeRequest(req)

	gm1, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u1.ID)).Only(context.Background())
	gm2, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u2.ID)).Only(context.Background())
	gm3, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u3.ID)).Only(context.Background())

	assert.Equal(t, 0, gm1.UnreadCount, "Sender should have 0 unread")
	assert.Equal(t, 1, gm2.UnreadCount, "Member 2 should have 1 unread")
	assert.Equal(t, 1, gm3.UnreadCount, "Member 3 should have 1 unread")

	reqRead, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/%s/read", chatGroup.ID), nil)
	reqRead.Header.Set("Authorization", "Bearer "+token2)
	executeRequest(reqRead)

	gm2After, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u2.ID)).Only(context.Background())
	gm3After, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u3.ID)).Only(context.Background())

	assert.Equal(t, 0, gm2After.UnreadCount, "Member 2 should have 0 unread after reading")
	assert.Equal(t, 1, gm3After.UnreadCount, "Member 3 should still have 1 unread")
}
