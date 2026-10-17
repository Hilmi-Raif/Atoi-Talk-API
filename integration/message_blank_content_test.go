//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/domain/model"
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMessageRejectsWhitespaceOnlyContentWithoutAttachments(t *testing.T) {
	ctx := context.Background()
	clearDatabase(ctx)
	sender := createTestUser(t, "blank-content-sender")
	recipient := createTestUser(t, "blank-content-recipient")
	conversation := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	testClient.PrivateChat.Create().SetChat(conversation).SetUser1(sender).SetUser2(recipient).SaveX(ctx)
	token, err := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, sender.ID)
	require.NoError(t, err)

	t.Run("send", func(t *testing.T) {
		response := makeRequest(http.MethodPost, "/api/messages", model.SendMessageRequest{ChatID: conversation.ID, Content: "  \t  "}, token)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		count, err := testClient.Message.Query().Where(message.ChatID(conversation.ID)).Count(ctx)
		require.NoError(t, err)
		require.Zero(t, count)
	})

	t.Run("edit", func(t *testing.T) {
		msg := testClient.Message.Create().SetChat(conversation).SetSender(sender).SetType(message.TypeRegular).SetContent("original").SaveX(ctx)
		response := makeRequest(http.MethodPut, "/api/messages/"+msg.ID.String(), model.EditMessageRequest{Content: "  \t  "}, token)
		require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
		stored := testClient.Message.GetX(ctx, msg.ID)
		require.Equal(t, "original", *stored.Content)
	})
}

func TestMessageEditMayClearTextWhenAttachmentsRemain(t *testing.T) {
	ctx := context.Background()
	clearDatabase(ctx)
	sender := createTestUser(t, "clear-text-sender")
	recipient := createTestUser(t, "clear-text-recipient")
	conversation := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	testClient.PrivateChat.Create().SetChat(conversation).SetUser1(sender).SetUser2(recipient).SaveX(ctx)
	token, err := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, sender.ID)
	require.NoError(t, err)
	attachment := testClient.Media.Create().SetFileName("clear-text-attachment.jpg").SetOriginalName("attachment.jpg").SetFileSize(100).SetMimeType("image/jpeg").SetUploader(sender).SaveX(ctx)
	msg := testClient.Message.Create().SetChat(conversation).SetSender(sender).SetType(message.TypeRegular).SetContent("original").AddAttachments(attachment).SaveX(ctx)
	require.Equal(t, msg.ID, *testClient.Media.GetX(ctx, attachment.ID).MessageID)

	response := makeRequest(http.MethodPut, "/api/messages/"+msg.ID.String(), map[string]string{"content": "  \t  "}, token)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
	stored := testClient.Message.Query().Where(message.ID(msg.ID)).WithAttachments().OnlyX(ctx)
	require.Equal(t, "", *stored.Content)
	require.Len(t, stored.Edges.Attachments, 1)

	response = makeRequest(http.MethodPut, "/api/messages/"+msg.ID.String(), map[string]interface{}{"content": "  \t  ", "attachment_ids": []string{}}, token)
	require.Equal(t, http.StatusBadRequest, response.Code, response.Body.String())
	stored = testClient.Message.Query().Where(message.ID(msg.ID)).WithAttachments().OnlyX(ctx)
	require.Len(t, stored.Edges.Attachments, 1)
}
