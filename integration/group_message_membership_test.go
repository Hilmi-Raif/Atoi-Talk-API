//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/domain/model"
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFormerGroupMemberCannotEditOrDeleteOwnMessage(t *testing.T) {
	ctx := context.Background()
	clearDatabase(ctx)
	owner := createTestUser(t, "former-group-owner")
	former := createTestUser(t, "former-group-member")
	token, err := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, former.ID)
	require.NoError(t, err)
	conversation := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := testClient.GroupChat.Create().SetChat(conversation).SetCreator(owner).SetName("former group").SetInviteCode("former-group").SaveX(ctx)
	testClient.GroupMember.Create().SetGroupChat(group).SetUser(owner).SetRole(groupmember.RoleOwner).SaveX(ctx)
	membership := testClient.GroupMember.Create().SetGroupChat(group).SetUser(former).SaveX(ctx)
	msg := testClient.Message.Create().SetChat(conversation).SetSender(former).SetType(message.TypeRegular).SetContent("before leaving").SaveX(ctx)
	require.NoError(t, testClient.GroupMember.DeleteOne(membership).Exec(ctx))

	edit := makeRequest(http.MethodPut, "/api/messages/"+msg.ID.String(), model.EditMessageRequest{Content: "after leaving"}, token)
	require.Equal(t, http.StatusForbidden, edit.Code, edit.Body.String())
	remove := makeRequest(http.MethodDelete, "/api/messages/"+msg.ID.String(), nil, token)
	require.Equal(t, http.StatusForbidden, remove.Code, remove.Body.String())
	stored := testClient.Message.GetX(ctx, msg.ID)
	require.Equal(t, "before leaving", *stored.Content)
	require.Nil(t, stored.DeletedAt)
}
