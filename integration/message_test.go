//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestSendMessage(t *testing.T) {
	clearDatabase(context.Background())

	password := "Password123!"
	hashedPassword, _ := helper.HashPassword(password)

	u1, _ := testClient.User.Create().SetEmail("u1@test.com").SetUsername("u1").SetFullName("User 1").SetPasswordHash(hashedPassword).Save(context.Background())
	u2, _ := testClient.User.Create().SetEmail("u2@test.com").SetUsername("u2").SetFullName("User 2").SetPasswordHash(hashedPassword).Save(context.Background())
	u3, _ := testClient.User.Create().SetEmail("u3@test.com").SetUsername("u3").SetFullName("User 3").SetPasswordHash(hashedPassword).Save(context.Background())

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token3, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u3.ID)

	chatEntity, _ := testClient.Chat.Create().SetType(chat.TypePrivate).Save(context.Background())
	testClient.PrivateChat.Create().SetChat(chatEntity).SetUser1(u1).SetUser2(u2).Save(context.Background())

	t.Run("Success - Send Text Message", func(t *testing.T) {
		reqBody := model.SendMessageRequest{
			ChatID:  chatEntity.ID,
			Content: "Hello User 2!",
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.MessageResponse](t, rr)
		assert.Equal(t, chatEntity.ID, dataMap.ChatID)
		assert.NotNil(t, dataMap.SenderID)
		assert.Equal(t, u1.ID, *dataMap.SenderID)
		assert.Equal(t, "Hello User 2!", dataMap.Content)
		assert.Equal(t, "regular", dataMap.Type)
		assert.Empty(t, dataMap.Attachments)

		pc, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chatEntity.ID)).Only(context.Background())
		assert.Equal(t, 0, pc.User1UnreadCount)
		assert.Equal(t, 1, pc.User2UnreadCount)
	})

	t.Run("Success - Send Text Message with Whitespace", func(t *testing.T) {
		reqBody := model.SendMessageRequest{
			ChatID:  chatEntity.ID,
			Content: "  Hello World  ",
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.MessageResponse](t, rr)
		assert.Equal(t, "Hello World", dataMap.Content)

		msg, _ := testClient.Message.Get(context.Background(), dataMap.ID)
		assert.Equal(t, "Hello World", *msg.Content)
	})

	t.Run("Fail - Sender is Blocked", func(t *testing.T) {
		testClient.UserBlock.Create().SetBlockerID(u2.ID).SetBlockedID(u1.ID).Exec(context.Background())

		reqBody := model.SendMessageRequest{
			ChatID:  chatEntity.ID,
			Content: "This message should be blocked",
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusForbidden, rr.Code)

		testClient.UserBlock.Delete().Exec(context.Background())
	})

	t.Run("Fail - Send Message to Deleted User", func(t *testing.T) {
		testClient.User.UpdateOne(u2).SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		reqBody := model.SendMessageRequest{
			ChatID:  chatEntity.ID,
			Content: "Hello Ghost?",
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusForbidden, rr.Code)

		testClient.User.UpdateOne(u2).ClearDeletedAt().ExecX(context.Background())
	})

	t.Run("Success - Send Message with Reply", func(t *testing.T) {
		msg1, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u2.ID).
			SetType(message.TypeRegular).
			SetContent("Original Message").
			Save(context.Background())

		replyToID := msg1.ID
		reqBody := model.SendMessageRequest{
			ChatID:    chatEntity.ID,
			Content:   "This is a reply",
			ReplyToID: &replyToID,
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.MessageResponse](t, rr)
		assert.NotNil(t, dataMap.ReplyTo)
		assert.Equal(t, msg1.ID, dataMap.ReplyTo.ID)
		assert.Equal(t, "User 2", dataMap.ReplyTo.SenderName)
		assert.Equal(t, "Original Message", dataMap.ReplyTo.Content)
		assert.Equal(t, "regular", dataMap.ReplyTo.Type)

		msg2, _ := testClient.Message.Get(context.Background(), dataMap.ID)
		assert.Equal(t, msg1.ID, *msg2.ReplyToID)
	})

	t.Run("Fail - Reply to System Message", func(t *testing.T) {
		sysMsg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u2.ID).
			SetType(message.TypeSystemCreate).
			SetActionData(map[string]interface{}{"foo": "bar"}).
			Save(context.Background())

		replyToID := sysMsg.ID
		reqBody := model.SendMessageRequest{
			ChatID:    chatEntity.ID,
			Content:   "Replying to system",
			ReplyToID: &replyToID,
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Reply to Non-Existent Message", func(t *testing.T) {
		replyToID := uuid.New()
		reqBody := model.SendMessageRequest{
			ChatID:    chatEntity.ID,
			Content:   "Reply to ghost",
			ReplyToID: &replyToID,
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Reply to Message in Different Chat", func(t *testing.T) {
		chat2, _ := testClient.Chat.Create().SetType(chat.TypePrivate).Save(context.Background())
		testClient.PrivateChat.Create().SetChat(chat2).SetUser1(u1).SetUser2(u3).Save(context.Background())
		msgOther, _ := testClient.Message.Create().SetChatID(chat2.ID).SetSenderID(u3.ID).SetType(message.TypeRegular).SetContent("Other Chat Msg").Save(context.Background())

		replyToID := msgOther.ID
		reqBody := model.SendMessageRequest{
			ChatID:    chatEntity.ID,
			Content:   "Cross-chat reply",
			ReplyToID: &replyToID,
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Success - Send Message with Attachment", func(t *testing.T) {
		m, _ := testClient.Media.Create().
			SetFileName("test_file.jpg").
			SetOriginalName("test.jpg").
			SetFileSize(1024).
			SetMimeType("image/jpeg").
			SetUploaderID(u1.ID).
			Save(context.Background())

		s3Client.PutObject(context.Background(), &s3.PutObjectInput{
			Bucket: aws.String(testConfig.S3BucketPrivate),
			Key:    aws.String(m.FileName),
			Body:   bytes.NewReader([]byte("test content")),
		})

		reqBody := model.SendMessageRequest{
			ChatID:        chatEntity.ID,
			AttachmentIDs: []uuid.UUID{m.ID},
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.MessageResponse](t, rr)
		assert.Len(t, dataMap.Attachments, 1)
		assert.Equal(t, m.ID, dataMap.Attachments[0].ID)

		msg, _ := testClient.Message.Get(context.Background(), dataMap.ID)
		attachedMedia, _ := msg.QueryAttachments().Only(context.Background())
		assert.Equal(t, m.ID, attachedMedia.ID)

		pc, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chatEntity.ID)).Only(context.Background())
		assert.Equal(t, 0, pc.User1UnreadCount)
		assert.Equal(t, 4, pc.User2UnreadCount)
	})

	t.Run("Fail - Attachment Belongs to Another User", func(t *testing.T) {
		m, _ := testClient.Media.Create().
			SetFileName("user2_file.jpg").
			SetOriginalName("user2.jpg").
			SetFileSize(1024).
			SetMimeType("image/jpeg").
			SetUploaderID(u2.ID).
			Save(context.Background())

		reqBody := model.SendMessageRequest{
			ChatID:        chatEntity.ID,
			AttachmentIDs: []uuid.UUID{m.ID},
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Attachment Already Used", func(t *testing.T) {
		otherMsg, _ := testClient.Message.Create().SetChatID(chatEntity.ID).SetSenderID(u1.ID).SetType(message.TypeRegular).Save(context.Background())
		m, _ := testClient.Media.Create().
			SetFileName("used.jpg").
			SetOriginalName("used.jpg").
			SetFileSize(1024).
			SetMimeType("image/jpeg").
			SetMessage(otherMsg).
			SetUploaderID(u1.ID).
			Save(context.Background())

		reqBody := model.SendMessageRequest{
			ChatID:        chatEntity.ID,
			AttachmentIDs: []uuid.UUID{m.ID},
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Success - SendMessage does not unhide", func(t *testing.T) {
		pc, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chatEntity.ID)).Only(context.Background())
		hiddenTime := time.Now().UTC().Add(-time.Hour)
		testClient.PrivateChat.UpdateOne(pc).SetUser1HiddenAt(hiddenTime).Exec(context.Background())

		reqBody := model.SendMessageRequest{
			ChatID:  chatEntity.ID,
			Content: "This should not unhide",
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		pcUpdated, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chatEntity.ID)).Only(context.Background())
		assert.NotNil(t, pcUpdated.User1HiddenAt)
		assert.WithinDuration(t, hiddenTime, *pcUpdated.User1HiddenAt, time.Second)
	})

	t.Run("Success - Group Chat Multiple Recipients", func(t *testing.T) {
		groupChat, _ := testClient.Chat.Create().SetType(chat.TypeGroup).Save(context.Background())
		gc, _ := testClient.GroupChat.Create().SetChat(groupChat).SetCreator(u1).SetName("Test Group").SetInviteCode("msgtest").Save(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).Save(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u2).Save(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).Save(context.Background())

		reqBody := model.SendMessageRequest{
			ChatID:  groupChat.ID,
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
	})

	t.Run("Fail - Group Non-Member", func(t *testing.T) {
		groupChat, _ := testClient.Chat.Create().SetType(chat.TypeGroup).Save(context.Background())
		gc, _ := testClient.GroupChat.Create().SetChat(groupChat).SetCreator(u1).SetName("Secret Group").SetInviteCode("secret").Save(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).Save(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u2).Save(context.Background())

		reqBody := model.SendMessageRequest{
			ChatID:  groupChat.ID,
			Content: "Let me in!",
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token3)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Attachment Not Found", func(t *testing.T) {
		reqBody := model.SendMessageRequest{
			ChatID:        chatEntity.ID,
			AttachmentIDs: []uuid.UUID{uuid.New()},
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Chat Not Found", func(t *testing.T) {
		reqBody := model.SendMessageRequest{
			ChatID:  uuid.New(),
			Content: "Hello?",
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Validation Error", func(t *testing.T) {
		reqBody := model.SendMessageRequest{
			ChatID: chatEntity.ID,
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Reply to Deleted Message", func(t *testing.T) {
		deletedMsg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u2.ID).
			SetType(message.TypeRegular).
			SetContent("I am deleted").
			SetDeletedAt(time.Now().UTC()).
			Save(context.Background())

		replyToID := deletedMsg.ID
		reqBody := model.SendMessageRequest{
			ChatID:    chatEntity.ID,
			Content:   "Reply to void",
			ReplyToID: &replyToID,
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Send Message to Deleted Group", func(t *testing.T) {
		groupChat, _ := testClient.Chat.Create().SetType(chat.TypeGroup).Save(context.Background())
		gc, _ := testClient.GroupChat.Create().SetChat(groupChat).SetCreator(u1).SetName("Deleted Group").SetInviteCode("deleted").Save(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).Save(context.Background())

		groupChat.Update().SetDeletedAt(time.Now().UTC()).Exec(context.Background())

		reqBody := model.SendMessageRequest{
			ChatID:  groupChat.ID,
			Content: "Hello Ghost!",
		}
		rr := makeRequest("POST", "/api/messages", reqBody, token1)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestGetMessages(t *testing.T) {
	clearDatabase(context.Background())

	password := "Password123!"
	hashedPassword, _ := helper.HashPassword(password)
	u1, _ := testClient.User.Create().SetEmail("u1@test.com").SetUsername("u1").SetFullName("User 1").SetPasswordHash(hashedPassword).Save(context.Background())
	u2, _ := testClient.User.Create().SetEmail("u2@test.com").SetUsername("u2").SetFullName("User 2").SetPasswordHash(hashedPassword).Save(context.Background())
	u3, _ := testClient.User.Create().SetEmail("u3@test.com").SetUsername("u3").SetFullName("User 3").SetPasswordHash(hashedPassword).Save(context.Background())

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token3, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u3.ID)

	chatEntity, _ := testClient.Chat.Create().SetType(chat.TypePrivate).Save(context.Background())
	testClient.PrivateChat.Create().SetChat(chatEntity).SetUser1(u1).SetUser2(u2).Save(context.Background())

	var msgIDs []uuid.UUID
	for i := 1; i <= 20; i++ {
		msg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u1.ID).
			SetType(message.TypeRegular).
			SetContent(fmt.Sprintf("Message %d", i)).
			Save(context.Background())
		msgIDs = append(msgIDs, msg.ID)
		time.Sleep(10 * time.Millisecond)
	}

	t.Run("Success - Get Messages First Page", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages?limit=2", chatEntity.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := parseResponse[[]model.MessageResponse](t, rr)

		assert.Len(t, dataList, 2)
		assert.Equal(t, msgIDs[18], dataList[0].ID)
		assert.Equal(t, msgIDs[19], dataList[1].ID)
		assert.True(t, resp.Meta.HasNext)
		assert.NotEmpty(t, resp.Meta.NextCursor)
	})

	t.Run("Success - Get Messages Next Page", func(t *testing.T) {
		cursor := base64.URLEncoding.EncodeToString([]byte(msgIDs[18].String()))
		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages?limit=2&cursor=%s", chatEntity.ID, cursor), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.MessageResponse](t, rr)
		assert.Len(t, dataList, 2)
		assert.Equal(t, msgIDs[16], dataList[0].ID)
		assert.Equal(t, msgIDs[17], dataList[1].ID)
	})

	t.Run("Success - Get Messages Newer", func(t *testing.T) {
		cursor := base64.URLEncoding.EncodeToString([]byte(msgIDs[1].String()))
		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages?limit=2&cursor=%s&direction=newer", chatEntity.ID, cursor), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := parseResponse[[]model.MessageResponse](t, rr)

		assert.Len(t, dataList, 2)
		assert.Equal(t, msgIDs[2], dataList[0].ID)
		assert.Equal(t, msgIDs[3], dataList[1].ID)
		assert.True(t, resp.Meta.HasNext)
		assert.True(t, resp.Meta.HasPrev)
	})

	t.Run("Success - Jump to Message", func(t *testing.T) {
		jumpU1, _ := testClient.User.Create().SetEmail("jump1@test.com").SetUsername("jump1").SetFullName("Jump 1").Save(context.Background())
		jumpU2, _ := testClient.User.Create().SetEmail("jump2@test.com").SetUsername("jump2").SetFullName("Jump 2").Save(context.Background())
		jumpToken1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, jumpU1.ID)

		jumpChat, _ := testClient.Chat.Create().SetType(chat.TypePrivate).Save(context.Background())
		testClient.PrivateChat.Create().SetChat(jumpChat).SetUser1(jumpU1).SetUser2(jumpU2).Save(context.Background())

		var jumpMsgIDs []uuid.UUID
		for i := 1; i <= 20; i++ {
			msg, _ := testClient.Message.Create().
				SetChatID(jumpChat.ID).
				SetSenderID(jumpU1.ID).
				SetType(message.TypeRegular).
				SetContent(fmt.Sprintf("Jump Message %d", i)).
				Save(context.Background())
			jumpMsgIDs = append(jumpMsgIDs, msg.ID)
			time.Sleep(10 * time.Millisecond)
		}

		targetID := jumpMsgIDs[9]
		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages?limit=5&around_message_id=%s", jumpChat.ID, targetID), nil, jumpToken1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.MessageResponse](t, rr)
		assert.Len(t, dataList, 5)
		assert.Equal(t, jumpMsgIDs[7], dataList[0].ID)
		assert.Equal(t, jumpMsgIDs[8], dataList[1].ID)
		assert.Equal(t, jumpMsgIDs[9], dataList[2].ID)
		assert.Equal(t, jumpMsgIDs[10], dataList[3].ID)
		assert.Equal(t, jumpMsgIDs[11], dataList[4].ID)
	})

	t.Run("Success - Verify Pagination Cursors", func(t *testing.T) {
		pU1, _ := testClient.User.Create().SetEmail("page1@test.com").SetUsername("page1").SetFullName("Page 1").Save(context.Background())
		pU2, _ := testClient.User.Create().SetEmail("page2@test.com").SetUsername("page2").SetFullName("Page 2").Save(context.Background())
		pToken1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, pU1.ID)

		pChat, _ := testClient.Chat.Create().SetType(chat.TypePrivate).Save(context.Background())
		testClient.PrivateChat.Create().SetChat(pChat).SetUser1(pU1).SetUser2(pU2).Save(context.Background())

		var pMsgIDs []uuid.UUID
		for i := 1; i <= 10; i++ {
			msg, _ := testClient.Message.Create().
				SetChatID(pChat.ID).
				SetSenderID(pU1.ID).
				SetType(message.TypeRegular).
				SetContent(fmt.Sprintf("Page Message %d", i)).
				Save(context.Background())
			pMsgIDs = append(pMsgIDs, msg.ID)
			time.Sleep(10 * time.Millisecond)
		}

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages?limit=2", pChat.ID), nil, pToken1)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := parseResponse[[]model.MessageResponse](t, rr)
		assert.Len(t, dataList, 2)
		assert.Equal(t, pMsgIDs[8], dataList[0].ID)
		assert.Equal(t, pMsgIDs[9], dataList[1].ID)

		assert.True(t, resp.Meta.HasNext)
		assert.False(t, resp.Meta.HasPrev)
		assert.Equal(t, base64.URLEncoding.EncodeToString([]byte(pMsgIDs[8].String())), resp.Meta.NextCursor)
		assert.Equal(t, base64.URLEncoding.EncodeToString([]byte(pMsgIDs[9].String())), resp.Meta.PrevCursor)

		cursor := resp.Meta.NextCursor
		rr = makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages?limit=2&cursor=%s", pChat.ID, cursor), nil, pToken1)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp2 helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp2)
		dataList = parseResponse[[]model.MessageResponse](t, rr)
		assert.Len(t, dataList, 2)
		assert.Equal(t, pMsgIDs[6], dataList[0].ID)
		assert.Equal(t, pMsgIDs[7], dataList[1].ID)
		assert.True(t, resp2.Meta.HasNext)
		assert.True(t, resp2.Meta.HasPrev)

		cursor = base64.URLEncoding.EncodeToString([]byte(pMsgIDs[2].String()))
		rr = makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages?limit=2&cursor=%s", pChat.ID, cursor), nil, pToken1)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp3 helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp3)
		dataList = parseResponse[[]model.MessageResponse](t, rr)
		assert.Len(t, dataList, 2)
		assert.Equal(t, pMsgIDs[0], dataList[0].ID)
		assert.Equal(t, pMsgIDs[1], dataList[1].ID)

		assert.False(t, resp3.Meta.HasNext)
		assert.True(t, resp3.Meta.HasPrev)
	})

	t.Run("Success - Empty Chat", func(t *testing.T) {
		emptyChat, _ := testClient.Chat.Create().SetType(chat.TypePrivate).Save(context.Background())
		testClient.PrivateChat.Create().SetChat(emptyChat).SetUser1(u1).SetUser2(u3).Save(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages", emptyChat.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.MessageResponse](t, rr)
		assert.Empty(t, dataList)
	})

	t.Run("Fail - Not Member", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages", chatEntity.ID), nil, token3)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Chat Not Found", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats/99999/messages", nil, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Success - Soft Deleted Message Shows Placeholder", func(t *testing.T) {
		delMsg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u1.ID).
			SetType(message.TypeRegular).
			SetContent("Sensitive Info").
			SetDeletedAt(time.Now().UTC()).
			Save(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages", chatEntity.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.MessageResponse](t, rr)
		found := false
		for _, m := range dataList {
			if m.ID == delMsg.ID {
				assert.Empty(t, m.Content)
				assert.NotNil(t, m.DeletedAt)
				found = true
			}
		}
		assert.True(t, found)
	})

	t.Run("Success - Reply Preview for Deleted Message", func(t *testing.T) {
		originalMsg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u2.ID).
			SetType(message.TypeRegular).
			SetContent("I will be deleted").
			Save(context.Background())

		replyMsg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u1.ID).
			SetType(message.TypeRegular).
			SetContent("Replying to a soon-to-be-deleted message").
			SetReplyToID(originalMsg.ID).
			Save(context.Background())

		testClient.Message.UpdateOne(originalMsg).SetDeletedAt(time.Now().UTC()).Exec(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages", chatEntity.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.MessageResponse](t, rr)
		var targetReply *model.MessageResponse
		for _, m := range dataList {
			if m.ID == replyMsg.ID {
				targetReply = &m
				break
			}
		}

		assert.NotNil(t, targetReply)
		assert.NotNil(t, targetReply.ReplyTo)
		assert.Empty(t, targetReply.ReplyTo.Content)
		assert.NotNil(t, targetReply.ReplyTo.DeletedAt)
		assert.Equal(t, "User 2", targetReply.ReplyTo.SenderName)
	})

	t.Run("Success - Media Placeholder Preview", func(t *testing.T) {
		media, _ := testClient.Media.Create().
			SetFileName("test_image.jpg").
			SetOriginalName("image.jpg").
			SetFileSize(12345).
			SetMimeType("image/jpeg").
			SetUploaderID(u1.ID).
			Save(context.Background())

		s3Client.PutObject(context.Background(), &s3.PutObjectInput{
			Bucket: aws.String(testConfig.S3BucketPrivate),
			Key:    aws.String(media.FileName),
			Body:   bytes.NewReader([]byte("test content")),
		})

		msgWithMedia, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u1.ID).
			SetType(message.TypeRegular).
			AddAttachmentIDs(media.ID).
			Save(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages", chatEntity.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.MessageResponse](t, rr)
		var targetMsg *model.MessageResponse
		for _, m := range dataList {
			if m.ID == msgWithMedia.ID {
				targetMsg = &m
				break
			}
		}

		assert.NotNil(t, targetMsg)
		assert.Empty(t, targetMsg.Content)
		assert.Len(t, targetMsg.Attachments, 1)
	})

	t.Run("Success - GetMessages respects hidden_at", func(t *testing.T) {
		clearDatabase(context.Background())
		user1, _ := testClient.User.Create().SetEmail("u1@test.com").SetUsername("u1").SetFullName("User 1").Save(context.Background())
		user2, _ := testClient.User.Create().SetEmail("u2@test.com").SetUsername("u2").SetFullName("User 2").Save(context.Background())
		tok1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

		chatEnt, _ := testClient.Chat.Create().SetType(chat.TypePrivate).Save(context.Background())
		pc, _ := testClient.PrivateChat.Create().SetChat(chatEnt).SetUser1(user1).SetUser2(user2).Save(context.Background())

		testClient.Message.Create().SetChatID(chatEnt.ID).SetSenderID(user2.ID).SetType(message.TypeRegular).SetContent("Old Message 1").SetCreatedAt(time.Now().UTC().Add(-2 * time.Hour)).SaveX(context.Background())
		testClient.Message.Create().SetChatID(chatEnt.ID).SetSenderID(user1.ID).SetType(message.TypeRegular).SetContent("Old Message 2").SetCreatedAt(time.Now().UTC().Add(-1 * time.Hour)).SaveX(context.Background())

		hideTime := time.Now().UTC()
		testClient.PrivateChat.UpdateOne(pc).SetUser1HiddenAt(hideTime).ExecX(context.Background())
		time.Sleep(10 * time.Millisecond)

		msg3, _ := testClient.Message.Create().SetChatID(chatEnt.ID).SetSenderID(user2.ID).SetType(message.TypeRegular).SetContent("New Message 3").Save(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages", chatEnt.ID), nil, tok1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.MessageResponse](t, rr)
		assert.Len(t, dataList, 1)
		assert.Equal(t, msg3.ID, dataList[0].ID)
		assert.Equal(t, "New Message 3", dataList[0].Content)
	})

	t.Run("Success - Dynamic Target Name Resolution", func(t *testing.T) {
		clearDatabase(context.Background())
		user1, _ := testClient.User.Create().SetEmail("u1@test.com").SetUsername("u1").SetFullName("User 1").Save(context.Background())
		user2, _ := testClient.User.Create().SetEmail("u2@test.com").SetUsername("u2").SetFullName("User 2").Save(context.Background())
		tok1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

		newChat, _ := testClient.Chat.Create().SetType(chat.TypePrivate).Save(context.Background())
		testClient.PrivateChat.Create().SetChat(newChat).SetUser1(user1).SetUser2(user2).Save(context.Background())

		sysMsg := testClient.Message.Create().
			SetChatID(newChat.ID).
			SetSenderID(user1.ID).
			SetType(message.TypeSystemAdd).
			SetActionData(map[string]interface{}{
				"target_id": user2.ID.String(),
				"actor_id":  user1.ID.String(),
			}).
			SaveX(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages", newChat.ID), nil, tok1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.MessageResponse](t, rr)
		var targetMsg *model.MessageResponse
		for _, m := range dataList {
			if m.ID == sysMsg.ID {
				targetMsg = &m
				break
			}
		}

		assert.NotNil(t, targetMsg)
		assert.Equal(t, "User 2", targetMsg.ActionData["target_name"])
	})

	t.Run("Fail - Get Messages from Deleted Group", func(t *testing.T) {
		clearDatabase(context.Background())
		user1, _ := testClient.User.Create().SetEmail("u1@test.com").SetUsername("u1").SetFullName("User 1").Save(context.Background())
		tok1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

		groupChat, _ := testClient.Chat.Create().SetType(chat.TypeGroup).Save(context.Background())
		gc, _ := testClient.GroupChat.Create().SetChat(groupChat).SetCreator(user1).SetName("Deleted Group").SetInviteCode("deleted").Save(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(user1).Save(context.Background())

		groupChat.Update().SetDeletedAt(time.Now().UTC()).Exec(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages", groupChat.ID), nil, tok1)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Success - Messages Persist After Account Deletion", func(t *testing.T) {
		clearDatabase(context.Background())
		user1, _ := testClient.User.Create().SetEmail("u1@test.com").SetUsername("u1").SetFullName("User 1").Save(context.Background())
		user2, _ := testClient.User.Create().SetEmail("u2@test.com").SetUsername("u2").SetFullName("User 2").Save(context.Background())
		tok1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

		chatEnt, _ := testClient.Chat.Create().SetType(chat.TypePrivate).Save(context.Background())
		testClient.PrivateChat.Create().SetChat(chatEnt).SetUser1(user1).SetUser2(user2).Save(context.Background())

		msg, _ := testClient.Message.Create().
			SetChatID(chatEnt.ID).
			SetSenderID(user2.ID).
			SetType(message.TypeRegular).
			SetContent("I will be deleted").
			Save(context.Background())

		testClient.User.UpdateOne(user2).
			SetDeletedAt(time.Now().UTC()).
			SetFullName("Deleted Account").
			SetEmail("deleted@deleted").
			SetUsername("deleted").
			ExecX(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/%s/messages", chatEnt.ID), nil, tok1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.MessageResponse](t, rr)
		var targetMsg *model.MessageResponse
		for _, m := range dataList {
			if m.ID == msg.ID {
				targetMsg = &m
				break
			}
		}

		assert.NotNil(t, targetMsg)
		assert.Equal(t, "I will be deleted", targetMsg.Content)
		assert.Nil(t, targetMsg.SenderID)
		assert.Equal(t, "Deleted User", targetMsg.SenderName)
	})
}

func TestEditMessage(t *testing.T) {
	clearDatabase(context.Background())

	password := "Password123!"
	hashedPassword, _ := helper.HashPassword(password)
	u1, _ := testClient.User.Create().SetEmail("u1@test.com").SetUsername("u1").SetFullName("User 1").SetPasswordHash(hashedPassword).Save(context.Background())
	u2, _ := testClient.User.Create().SetEmail("u2@test.com").SetUsername("u2").SetFullName("User 2").SetPasswordHash(hashedPassword).Save(context.Background())

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	chatEntity, _ := testClient.Chat.Create().SetType(chat.TypePrivate).Save(context.Background())
	testClient.PrivateChat.Create().SetChat(chatEntity).SetUser1(u1).SetUser2(u2).Save(context.Background())

	t.Run("Success - Edit Text Message", func(t *testing.T) {
		msg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u1.ID).
			SetType(message.TypeRegular).
			SetContent("Original Content").
			Save(context.Background())

		reqBody := model.EditMessageRequest{
			Content: "Edited Content",
		}
		rr := makeRequest("PUT", fmt.Sprintf("/api/messages/%s", msg.ID), reqBody, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.MessageResponse](t, rr)
		assert.Equal(t, "Edited Content", dataMap.Content)
		assert.NotNil(t, dataMap.EditedAt)

		updatedMsg, _ := testClient.Message.Get(context.Background(), msg.ID)
		assert.Equal(t, "Edited Content", *updatedMsg.Content)
		assert.NotNil(t, updatedMsg.EditedAt)
	})

	t.Run("Success - Edit Message with Attachments", func(t *testing.T) {
		att1, _ := testClient.Media.Create().
			SetFileName("att1.jpg").SetOriginalName("att1.jpg").SetFileSize(100).SetMimeType("image/jpeg").
			SetUploaderID(u1.ID).Save(context.Background())

		msg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u1.ID).
			SetType(message.TypeRegular).
			SetContent("With Attachment").
			AddAttachmentIDs(att1.ID).
			Save(context.Background())

		att2, _ := testClient.Media.Create().
			SetFileName("att2.jpg").SetOriginalName("att2.jpg").SetFileSize(100).SetMimeType("image/jpeg").
			SetUploaderID(u1.ID).Save(context.Background())

		s3Client.PutObject(context.Background(), &s3.PutObjectInput{
			Bucket: aws.String(testConfig.S3BucketPrivate),
			Key:    aws.String(att2.FileName),
			Body:   bytes.NewReader([]byte("test content")),
		})

		reqBody := model.EditMessageRequest{
			Content:       "Updated Attachments",
			AttachmentIDs: []uuid.UUID{att2.ID},
		}
		rr := makeRequest("PUT", fmt.Sprintf("/api/messages/%s", msg.ID), reqBody, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.MessageResponse](t, rr)
		assert.Len(t, dataMap.Attachments, 1)
		assert.Equal(t, att2.ID, dataMap.Attachments[0].ID)

		updatedMsg, _ := testClient.Message.Query().Where(message.ID(msg.ID)).WithAttachments().Only(context.Background())
		atts, _ := updatedMsg.QueryAttachments().All(context.Background())
		assert.Len(t, atts, 1)
		assert.Equal(t, att2.ID, atts[0].ID)

		att1Reload, _ := testClient.Media.Get(context.Background(), att1.ID)
		assert.Nil(t, att1Reload.MessageID)
	})

	t.Run("Success - Edit Text Only Keeps Existing Attachments", func(t *testing.T) {
		att, _ := testClient.Media.Create().
			SetFileName("keep.jpg").SetOriginalName("keep.jpg").SetFileSize(100).SetMimeType("image/jpeg").
			SetUploaderID(u1.ID).Save(context.Background())

		msg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u1.ID).
			SetType(message.TypeRegular).
			SetContent("Keep Attachment").
			AddAttachmentIDs(att.ID).
			Save(context.Background())

		s3Client.PutObject(context.Background(), &s3.PutObjectInput{
			Bucket: aws.String(testConfig.S3BucketPrivate),
			Key:    aws.String(att.FileName),
			Body:   bytes.NewReader([]byte("test content")),
		})

		rr := makeRequest("PUT", fmt.Sprintf("/api/messages/%s", msg.ID), map[string]interface{}{"content": "Edited Only"}, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.MessageResponse](t, rr)
		assert.Len(t, dataMap.Attachments, 1)
		assert.Equal(t, att.ID, dataMap.Attachments[0].ID)

		updatedMsg, _ := testClient.Message.Query().Where(message.ID(msg.ID)).WithAttachments().Only(context.Background())
		assert.Len(t, updatedMsg.Edges.Attachments, 1)
		assert.Equal(t, att.ID, updatedMsg.Edges.Attachments[0].ID)
	})

	t.Run("Success - Edit Explicit Empty Attachments Removes All Attachments", func(t *testing.T) {
		att, _ := testClient.Media.Create().
			SetFileName("remove.jpg").SetOriginalName("remove.jpg").SetFileSize(100).SetMimeType("image/jpeg").
			SetUploaderID(u1.ID).Save(context.Background())

		msg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u1.ID).
			SetType(message.TypeRegular).
			SetContent("Remove Attachment").
			AddAttachmentIDs(att.ID).
			Save(context.Background())

		reqBody := map[string]interface{}{
			"content":        "Removed Attachments",
			"attachment_ids": []string{},
		}
		rr := makeRequest("PUT", fmt.Sprintf("/api/messages/%s", msg.ID), reqBody, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.MessageResponse](t, rr)
		assert.Empty(t, dataMap.Attachments)

		updatedMsg, _ := testClient.Message.Query().Where(message.ID(msg.ID)).WithAttachments().Only(context.Background())
		assert.Empty(t, updatedMsg.Edges.Attachments)
	})

	t.Run("Fail - Edit Other User Message", func(t *testing.T) {
		msg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u1.ID).
			SetType(message.TypeRegular).
			SetContent("User 1 Msg").
			Save(context.Background())

		rr := makeRequest("PUT", fmt.Sprintf("/api/messages/%s", msg.ID), model.EditMessageRequest{Content: "Hacked"}, token2)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Edit Deleted Message", func(t *testing.T) {
		msg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u1.ID).
			SetType(message.TypeRegular).
			SetContent("Deleted Msg").
			SetDeletedAt(time.Now().UTC()).
			Save(context.Background())

		rr := makeRequest("PUT", fmt.Sprintf("/api/messages/%s", msg.ID), model.EditMessageRequest{Content: "Resurrect"}, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Edit System Message", func(t *testing.T) {
		sysMsg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u1.ID).
			SetType(message.TypeSystemCreate).
			SetActionData(map[string]interface{}{"foo": "bar"}).
			Save(context.Background())

		rr := makeRequest("PUT", fmt.Sprintf("/api/messages/%s", sysMsg.ID), model.EditMessageRequest{Content: "Hacked System"}, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Invalid Attachments", func(t *testing.T) {
		msg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u1.ID).
			SetType(message.TypeRegular).
			SetContent("Msg").
			Save(context.Background())

		attOther, _ := testClient.Media.Create().
			SetFileName("other.jpg").SetOriginalName("other.jpg").SetFileSize(100).SetMimeType("image/jpeg").
			SetUploaderID(u2.ID).Save(context.Background())

		reqBody := model.EditMessageRequest{
			Content:       "Steal Attachment",
			AttachmentIDs: []uuid.UUID{attOther.ID},
		}
		rr := makeRequest("PUT", fmt.Sprintf("/api/messages/%s", msg.ID), reqBody, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Edit Message Too Old", func(t *testing.T) {
		msg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u1.ID).
			SetType(message.TypeRegular).
			SetContent("Old Msg").
			SetCreatedAt(time.Now().UTC().Add(-20 * time.Minute)).
			Save(context.Background())

		rr := makeRequest("PUT", fmt.Sprintf("/api/messages/%s", msg.ID), model.EditMessageRequest{Content: "Too Late"}, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}
