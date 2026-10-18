//go:build integration

package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"AtoiTalkAPI/internal/domain/helper"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestNotificationPreferencesAccountAndChatIsolation(t *testing.T) {
	ctx := context.Background()
	clearDatabase(ctx)
	a := createTestUser(t, "notification-a")
	b := createTestUser(t, "notification-b")
	outsider := createTestUser(t, "notification-outsider")
	token, err := helper.GenerateJWT(testConfig.JWTSecret, 3600, a.ID)
	require.NoError(t, err)
	otherToken, err := helper.GenerateJWT(testConfig.JWTSecret, 3600, outsider.ID)
	require.NoError(t, err)
	c := testClient.Chat.Create().SetType("private").SaveX(ctx)
	testClient.PrivateChat.Create().SetChat(c).SetUser1(a).SetUser2(b).SetUser1UnreadCount(3).SaveX(ctx)
	msg := testClient.Message.Create().SetChat(c).SetSender(b).SetContent("hello").SaveX(ctx)
	testClient.Chat.UpdateOne(c).SetLastMessage(msg).SetLastMessageAt(msg.CreatedAt).SaveX(ctx)
	path := "/api/chats/" + c.ID.String() + "/notifications"
	require.Equal(t, http.StatusUnauthorized, makeRequest(http.MethodGet, "/api/notifications", nil, "").Code)
	require.Equal(t, http.StatusNotFound, makeRequest(http.MethodPut, "/api/chats/"+uuid.NewString()+"/notifications", map[string]any{}, token).Code)
	require.Equal(t, http.StatusForbidden, makeRequest(http.MethodPut, path, map[string]any{"muted_forever": true}, otherToken).Code)
	require.Equal(t, http.StatusBadRequest, makeRequest(http.MethodPut, path, map[string]any{"muted_until": time.Now().UTC().Add(-time.Hour)}, token).Code)
	require.Equal(t, http.StatusOK, makeRequest(http.MethodPut, path, map[string]any{"muted_until": time.Now().UTC().Add(time.Hour)}, token).Code)
	require.Equal(t, http.StatusOK, makeRequest(http.MethodPut, path, map[string]any{"muted_forever": true}, token).Code)
	row := testClient.ChatNotificationPreference.Query().OnlyX(ctx)
	require.True(t, row.MutedForever)
	require.Nil(t, row.MutedUntil)
	require.Equal(t, http.StatusOK, makeRequest(http.MethodPut, "/api/notifications", map[string]any{"message_notifications_enabled": false}, token).Code)
	require.False(t, testClient.User.GetX(ctx, a.ID).MessageNotificationsEnabled)
	require.True(t, testClient.User.GetX(ctx, b.ID).MessageNotificationsEnabled)
	require.Equal(t, 1, testClient.ChatNotificationPreference.Query().CountX(ctx))
	require.Equal(t, 3, testClient.PrivateChat.Query().OnlyX(ctx).User1UnreadCount)
	settings := makeRequest(http.MethodGet, "/api/notifications", nil, token)
	require.Equal(t, http.StatusOK, settings.Code, settings.Body.String())
	require.NotContains(t, settings.Body.String(), c.ID.String())
	muted := makeRequest(http.MethodGet, "/api/notifications/muted-chats?limit=1", nil, token)
	require.Equal(t, http.StatusOK, muted.Code, muted.Body.String())
	require.Contains(t, muted.Body.String(), c.ID.String())
	detail := makeRequest(http.MethodGet, "/api/chats/"+c.ID.String(), nil, token)
	require.Equal(t, http.StatusOK, detail.Code, detail.Body.String())
	var payload struct {
		Data struct {
			MutedForever bool `json:"muted_forever"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(detail.Body.Bytes(), &payload))
	require.True(t, payload.Data.MutedForever)
	list := makeRequest(http.MethodGet, "/api/chats", nil, token)
	require.Equal(t, http.StatusOK, list.Code, list.Body.String())
	require.Contains(t, list.Body.String(), `"muted_forever":true`)
	require.Equal(t, http.StatusOK, makeRequest(http.MethodPut, path, map[string]any{}, token).Code)
	require.Zero(t, testClient.ChatNotificationPreference.Query().CountX(ctx))
}

func TestMutedChatsPaginationExcludesExpiredAndInaccessibleChats(t *testing.T) {
	ctx := context.Background()
	clearDatabase(ctx)
	owner, outsider := createTestUser(t, "mute-page-owner"), createTestUser(t, "mute-page-outsider")
	token, err := helper.GenerateJWT(testConfig.JWTSecret, 3600, owner.ID)
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		peer := createTestUser(t, fmt.Sprintf("mute-page-peer-%d", i))
		c := testClient.Chat.Create().SetType("private").SaveX(ctx)
		participant := owner
		if i == 4 {
			participant = outsider
		}
		testClient.PrivateChat.Create().SetChat(c).SetUser1(participant).SetUser2(peer).SaveX(ctx)
		q := testClient.ChatNotificationPreference.Create().SetUserID(owner.ID).SetChatID(c.ID).SetMutedForever(true)
		if i == 3 {
			q.SetMutedForever(false).SetMutedUntil(time.Now().UTC().Add(-time.Hour))
		}
		q.SaveX(ctx)
	}
	var first struct {
		Data []struct {
			ChatID string `json:"chat_id"`
		} `json:"data"`
		Meta struct {
			Next string `json:"next_cursor"`
			More bool   `json:"has_next"`
		} `json:"meta"`
	}
	r := makeRequest(http.MethodGet, "/api/notifications/muted-chats?limit=2", nil, token)
	require.Equal(t, 200, r.Code)
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &first))
	require.Len(t, first.Data, 2)
	require.True(t, first.Meta.More)
	var second struct {
		Data []struct {
			ChatID string `json:"chat_id"`
		} `json:"data"`
		Meta struct {
			More bool `json:"has_next"`
		} `json:"meta"`
	}
	r = makeRequest(http.MethodGet, "/api/notifications/muted-chats?limit=2&cursor="+first.Meta.Next, nil, token)
	require.Equal(t, 200, r.Code)
	require.NoError(t, json.Unmarshal(r.Body.Bytes(), &second))
	require.Len(t, second.Data, 1)
	require.False(t, second.Meta.More)
	for _, row := range first.Data {
		require.NotEqual(t, row.ChatID, second.Data[0].ChatID)
	}
	for _, query := range []string{"?limit=51", "?cursor=invalid"} {
		require.Equal(t, 400, makeRequest(http.MethodGet, "/api/notifications/muted-chats"+query, nil, token).Code)
	}
}
