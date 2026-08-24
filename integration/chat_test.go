//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/ent/userblock"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
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
		rr := makeRequest("GET", "/api/chats", nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.ChatListResponse](t, rr)
		assert.Len(t, dataList, 3)

		assert.Equal(t, chat3.ID, dataList[0].ID)
		assert.Equal(t, "My Group", dataList[0].Name)
		assert.Equal(t, 5, dataList[0].UnreadCount)
		assert.NotNil(t, dataList[0].InviteCode)
		assert.Equal(t, "mygroup", *dataList[0].InviteCode)
		assert.NotNil(t, dataList[0].InviteExpiresAt)
		assert.Nil(t, dataList[0].OtherUserID)
		assert.NotNil(t, dataList[0].LastMessage)
		assert.Equal(t, "regular", dataList[0].LastMessage.Type)

		assert.Equal(t, chat2.ID, dataList[1].ID)
		assert.Contains(t, dataList[1].Name, "user3")
		assert.Equal(t, 0, dataList[1].UnreadCount)
		assert.Nil(t, dataList[1].InviteCode)
		assert.NotNil(t, dataList[1].OtherUserID)
		assert.Equal(t, u3.ID, *dataList[1].OtherUserID)

		assert.Equal(t, chat1.ID, dataList[2].ID)
		assert.Contains(t, dataList[2].Name, "user2")
		assert.Equal(t, 3, dataList[2].UnreadCount)
		assert.NotNil(t, dataList[2].OtherUserID)
		assert.Equal(t, u2.ID, *dataList[2].OtherUserID)
	})

	t.Run("Success - Pagination", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats?limit=2", nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := parseResponse[[]model.ChatListResponse](t, rr)
		assert.Len(t, dataList, 2)
		assert.True(t, resp.Meta.HasNext)
		assert.NotEmpty(t, resp.Meta.NextCursor)

		cursor := resp.Meta.NextCursor
		rr2 := makeRequest("GET", fmt.Sprintf("/api/chats?limit=2&cursor=%s", cursor), nil, token1)
		assert.Equal(t, http.StatusOK, rr2.Code)

		dataList2 := parseResponse[[]model.ChatListResponse](t, rr2)
		assert.Len(t, dataList2, 1)
		assert.Equal(t, chat1.ID, dataList2[0].ID)
	})

	t.Run("Success - Search by Username", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats?query=user3", nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.ChatListResponse](t, rr)
		assert.Len(t, dataList, 1)
		assert.Contains(t, dataList[0].Name, "user3")
	})

	t.Run("Success - Search No Results", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats?query=NamaNgawur123", nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.ChatListResponse](t, rr)
		assert.Empty(t, dataList)
	})

	t.Run("Fail - Invalid Cursor Format", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats?cursor=bukan-base64-valid", nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("Success - Last Message Placeholder for Deleted Message", func(t *testing.T) {
		u4 := createTestUser(t, "user4")

		delChat := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(context.Background())
		testClient.PrivateChat.Create().SetChat(delChat).SetUser1(u1).SetUser2(u4).SaveX(context.Background())

		msg := testClient.Message.Create().SetChat(delChat).SetSender(u4).SetType(message.TypeRegular).SetContent("This will be deleted").SaveX(context.Background())
		testClient.Message.UpdateOne(msg).SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		delChat.Update().SetUpdatedAt(time.Now().UTC()).SetLastMessage(msg).SetLastMessageAt(msg.CreatedAt).ExecX(context.Background())

		rr := makeRequest("GET", "/api/chats", nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.ChatListResponse](t, rr)
		assert.NotEmpty(t, dataList)
		assert.Equal(t, delChat.ID, dataList[0].ID)
		assert.NotNil(t, dataList[0].LastMessage)
		assert.Empty(t, dataList[0].LastMessage.Content)
		assert.NotNil(t, dataList[0].LastMessage.DeletedAt)
	})

	t.Run("Success - Exclude Hidden Private Chat", func(t *testing.T) {
		pc, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chat1.ID)).Only(context.Background())
		testClient.PrivateChat.UpdateOne(pc).SetUser1HiddenAt(time.Now().UTC()).ExecX(context.Background())

		rr := makeRequest("GET", "/api/chats", nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.ChatListResponse](t, rr)
		for _, c := range dataList {
			assert.NotEqual(t, chat1.ID, c.ID)
		}
	})

	t.Run("Success - Reappear Hidden Chat on New Message", func(t *testing.T) {
		pc, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chat1.ID)).Only(context.Background())
		testClient.PrivateChat.UpdateOne(pc).SetUser1HiddenAt(time.Now().UTC()).ExecX(context.Background())

		rr1 := makeRequest("GET", "/api/chats", nil, token1)
		dataList1 := parseResponse[[]model.ChatListResponse](t, rr1)
		for _, c := range dataList1 {
			assert.NotEqual(t, chat1.ID, c.ID)
		}

		newMsg := testClient.Message.Create().SetChat(chat1).SetSender(u2).SetType(message.TypeRegular).SetContent("New message").SetCreatedAt(time.Now().UTC().Add(time.Second)).SaveX(context.Background())
		testClient.Chat.UpdateOneID(chat1.ID).SetUpdatedAt(time.Now().UTC().Add(time.Second)).SetLastMessage(newMsg).SetLastMessageAt(newMsg.CreatedAt).ExecX(context.Background())

		rr2 := makeRequest("GET", "/api/chats", nil, token1)
		dataList2 := parseResponse[[]model.ChatListResponse](t, rr2)
		found := false
		for _, c := range dataList2 {
			if c.ID == chat1.ID {
				found = true
				break
			}
		}
		assert.True(t, found)
	})

	t.Run("Success - List Chat with Blocked User", func(t *testing.T) {
		media, _ := testClient.Media.Create().
			SetFileName("u2_avatar.jpg").SetOriginalName("u2.jpg").
			SetFileSize(1024).SetMimeType("image/jpeg").
			SetUploader(u2).
			Save(context.Background())
		testClient.User.UpdateOne(u2).SetAvatar(media).ExecX(context.Background())

		testClient.UserBlock.Create().SetBlockerID(u1.ID).SetBlockedID(u2.ID).SaveX(context.Background())

		rr := makeRequest("GET", "/api/chats", nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.ChatListResponse](t, rr)
		var blockedChat *model.ChatListResponse
		for _, c := range dataList {
			if c.ID == chat1.ID {
				blockedChat = &c
				break
			}
		}

		assert.NotNil(t, blockedChat)
		assert.True(t, blockedChat.IsBlockedByMe)
		assert.NotEqual(t, "", blockedChat.Avatar)
		assert.NotEqual(t, "", blockedChat.Name)
		assert.False(t, blockedChat.IsOnline)
		assert.NotNil(t, blockedChat.OtherUserID)
		assert.Equal(t, u2.ID, *blockedChat.OtherUserID)

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

		rr := makeRequest("GET", "/api/chats", nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.ChatListResponse](t, rr)
		assert.NotEmpty(t, dataList)
		assert.Equal(t, chat3.ID, dataList[0].ID)
		assert.NotNil(t, dataList[0].LastMessage)
		assert.Contains(t, dataList[0].LastMessage.ActionData["target_name"], "user2")
	})

	t.Run("Success - Exclude Deleted Group", func(t *testing.T) {
		chat3.Update().SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		rr := makeRequest("GET", "/api/chats", nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.ChatListResponse](t, rr)
		for _, c := range dataList {
			assert.NotEqual(t, chat3.ID, c.ID)
		}
	})

	t.Run("Success - Chat with Deleted User", func(t *testing.T) {
		testClient.User.UpdateOne(u2).SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		rr := makeRequest("GET", "/api/chats", nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.ChatListResponse](t, rr)
		var deletedUserChat *model.ChatListResponse
		for _, c := range dataList {
			if c.ID == chat1.ID {
				deletedUserChat = &c
				break
			}
		}

		assert.NotNil(t, deletedUserChat)
		assert.True(t, deletedUserChat.OtherUserIsDeleted)
		assert.False(t, deletedUserChat.IsOnline)
	})

	t.Run("Unauthorized", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats", nil, "")
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
		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s", chat1.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.ChatListResponse](t, rr)
		assert.Equal(t, chat1.ID, data.ID)
		assert.Contains(t, data.Name, "user2")
		assert.Equal(t, 3, data.UnreadCount)
		assert.Nil(t, data.InviteCode)
	})

	t.Run("Success - Get Chat with Blocked User", func(t *testing.T) {
		testClient.UserBlock.Create().SetBlockerID(u1.ID).SetBlockedID(u2.ID).SaveX(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s", chat1.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.ChatListResponse](t, rr)
		assert.True(t, data.IsBlockedByMe)
		assert.NotEqual(t, "", data.Name)
		assert.False(t, data.IsOnline)

		testClient.UserBlock.Delete().Where(userblock.BlockerID(u1.ID), userblock.BlockedID(u2.ID)).ExecX(context.Background())
	})

	t.Run("Success - Get Group Chat as Owner", func(t *testing.T) {
		chatGroup := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		gc := testClient.GroupChat.Create().SetChat(chatGroup).SetCreator(u1).SetName("Count Test").SetInviteCode("counttest").SetInviteExpiresAt(time.Now().Add(time.Hour)).SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u2).SaveX(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s", chatGroup.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.ChatListResponse](t, rr)
		assert.Equal(t, 2, data.MemberCount)
		assert.NotNil(t, data.InviteCode)
		assert.Equal(t, "counttest", *data.InviteCode)
		assert.NotNil(t, data.InviteExpiresAt)
	})

	t.Run("Success - Get Group Chat as Member", func(t *testing.T) {
		chatGroup := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		gc := testClient.GroupChat.Create().SetChat(chatGroup).SetCreator(u1).SetName("Member Test").SetInviteCode("membertest").SetInviteExpiresAt(time.Now().Add(time.Hour)).SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).SetRole(groupmember.RoleMember).SaveX(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s", chatGroup.ID), nil, token3)
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.ChatListResponse](t, rr)
		assert.Nil(t, data.InviteCode)
		assert.Nil(t, data.InviteExpiresAt)
	})

	t.Run("Success - Get Public Group", func(t *testing.T) {
		chatPublic := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		testClient.GroupChat.Create().SetChat(chatPublic).SetCreator(u2).SetName("Public Group").SetIsPublic(true).SetInviteCode("publicgroup").SaveX(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s", chatPublic.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.ChatListResponse](t, rr)
		assert.Equal(t, "Public Group", data.Name)
		assert.NotNil(t, data.InviteCode)
		assert.Equal(t, "publicgroup", *data.InviteCode)
		assert.Nil(t, data.MyRole)
	})

	t.Run("Fail - Get Private Group", func(t *testing.T) {
		chatPrivate := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		testClient.GroupChat.Create().SetChat(chatPrivate).SetCreator(u2).SetName("Private Group").SetIsPublic(false).SetInviteCode("privategroup").SaveX(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s", chatPrivate.ID), nil, token1)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Invalid ID", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats/abc", nil, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Not Found or Forbidden", func(t *testing.T) {
		chat2 := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(context.Background())
		testClient.PrivateChat.Create().SetChat(chat2).SetUser1(u2).SetUser2(u3).SaveX(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s", chat2.ID), nil, token1)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Get Deleted Chat", func(t *testing.T) {
		chat1.Update().SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s", chat1.ID), nil, token1)
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
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/%s/read", chat1.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		pc, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chat1.ID)).Only(context.Background())
		assert.Equal(t, 0, pc.User1UnreadCount)
		assert.NotNil(t, pc.User1LastReadAt)
	})

	t.Run("Success - Mark Read While Blocked", func(t *testing.T) {
		testClient.UserBlock.Create().SetBlockerID(u2.ID).SetBlockedID(u1.ID).SaveX(context.Background())

		pc, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chat1.ID)).Only(context.Background())
		testClient.PrivateChat.UpdateOne(pc).SetUser1UnreadCount(5).SetUser1LastReadAt(time.Time{}).ExecX(context.Background())

		rr := makeRequest("POST", fmt.Sprintf("/api/chats/%s/read", chat1.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		pcAfter, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chat1.ID)).Only(context.Background())
		assert.Equal(t, 0, pcAfter.User1UnreadCount)
		assert.True(t, pcAfter.User1LastReadAt.IsZero())

		testClient.UserBlock.Delete().Where(userblock.BlockerID(u2.ID), userblock.BlockedID(u1.ID)).ExecX(context.Background())
	})

	t.Run("Success - Mark Group Chat Read", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/%s/read", chat2.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		gm, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u1.ID)).Only(context.Background())
		assert.Equal(t, 0, gm.UnreadCount)
		assert.NotNil(t, gm.LastReadAt)
	})

	t.Run("Fail - Not a Member", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/%s/read", chat1.ID), nil, token3)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Not Found", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/%s/read", uuid.New()), nil, token1)
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
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/%s/hide", chat1.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		pc, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chat1.ID)).Only(context.Background())
		assert.NotNil(t, pc.User1HiddenAt)
		assert.Nil(t, pc.User2HiddenAt)
		assert.Equal(t, 0, pc.User1UnreadCount)
	})

	t.Run("Fail - Chat Not Found", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/%s/hide", uuid.New()), nil, token1)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Not Private Chat", func(t *testing.T) {
		chatGroup := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		testClient.GroupChat.Create().SetChat(chatGroup).SetCreator(u1).SetName("Group").SetInviteCode("group").SaveX(context.Background())

		rr := makeRequest("POST", fmt.Sprintf("/api/chats/%s/hide", chatGroup.ID), nil, token1)
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
	rr := makeRequest("POST", "/api/messages", reqBody, token1)
	assert.Equal(t, http.StatusOK, rr.Code)

	gm1, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u1.ID)).Only(context.Background())
	gm2, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u2.ID)).Only(context.Background())
	gm3, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u3.ID)).Only(context.Background())

	assert.Equal(t, 0, gm1.UnreadCount)
	assert.Equal(t, 1, gm2.UnreadCount)
	assert.Equal(t, 1, gm3.UnreadCount)

	rrRead := makeRequest("POST", fmt.Sprintf("/api/chats/%s/read", chatGroup.ID), nil, token2)
	assert.Equal(t, http.StatusOK, rrRead.Code)

	gm2After, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u2.ID)).Only(context.Background())
	gm3After, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u3.ID)).Only(context.Background())

	assert.Equal(t, 0, gm2After.UnreadCount)
	assert.Equal(t, 1, gm3After.UnreadCount)
}
