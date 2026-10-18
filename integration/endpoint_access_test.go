//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/internal/domain/helper"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProtectedEndpointAccessBoundaries(t *testing.T) {
	clearDatabase(context.Background())
	u := testClient.User.Create().SetEmail("endpoint-access@test.com").SetUsername("endpointaccess").SetFullName("Regular user").SaveX(context.Background())
	token, err := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u.ID)
	require.NoError(t, err)
	id := u.ID.String()
	endpoints := []string{
		"POST /auth/logout",
		"PUT /push/devices/" + id, "DELETE /push/devices/" + id,
		"POST /media/upload", "POST /media/" + id + "/complete", "GET /media/" + id + "/url",
		"GET /user/current", "PUT /user/profile", "GET /users", "GET /users/blocked", "GET /users/" + id,
		"POST /users/" + id + "/block", "POST /users/" + id + "/unblock",
		"PUT /account/password", "PUT /account/email", "DELETE /account",
		"GET /chats", "GET /chats/" + id, "POST /chats/" + id + "/read", "POST /chats/" + id + "/hide",
		"GET /chats/" + id + "/messages", "POST /chats/private",
		"POST /chats/group", "PUT /chats/group/" + id, "DELETE /chats/group/" + id,
		"GET /chats/group/public", "GET /chats/group/" + id + "/members", "POST /chats/group/" + id + "/members",
		"POST /chats/group/" + id + "/leave", "POST /chats/group/" + id + "/join", "POST /chats/group/join/invite",
		"PUT /chats/group/" + id + "/invite", "POST /chats/group/" + id + "/members/" + id + "/kick",
		"PUT /chats/group/" + id + "/members/" + id + "/role", "POST /chats/group/" + id + "/transfer",
		"POST /messages", "PUT /messages/" + id, "DELETE /messages/" + id, "POST /reports",
		"GET /admin/dashboard", "GET /admin/users", "GET /admin/users/" + id,
		"POST /admin/users/" + id + "/reset", "POST /admin/users/ban", "POST /admin/users/" + id + "/unban",
		"GET /admin/reports", "GET /admin/reports/" + id, "PUT /admin/reports/" + id + "/resolve", "DELETE /admin/reports/" + id,
		"GET /admin/groups", "GET /admin/groups/" + id, "GET /admin/groups/" + id + "/members",
		"DELETE /admin/groups/" + id, "POST /admin/groups/" + id + "/reset",
	}
	for _, endpoint := range endpoints {
		t.Run(endpoint, func(t *testing.T) {
			method, path, _ := strings.Cut(endpoint, " ")
			rr := makeRequest(method, "/api"+path, nil, "")
			require.Equal(t, http.StatusUnauthorized, rr.Code, rr.Body.String())
			if strings.HasPrefix(path, "/admin/") {
				rr = makeRequest(method, "/api"+path, nil, token)
				require.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())
			}
		})
	}
}

func TestResourceEndpointsRejectOutsidersAndForgedRoles(t *testing.T) {
	ctx := context.Background()
	clearDatabase(ctx)
	owner := createTestUser(t, "boundary-owner")
	member := createTestUser(t, "boundary-member")
	outsider := createTestUser(t, "boundary-outsider")
	outsiderToken, err := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, outsider.ID)
	require.NoError(t, err)
	memberToken, err := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, member.ID)
	require.NoError(t, err)
	conversation := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := testClient.GroupChat.Create().SetChat(conversation).SetCreator(owner).SetName("Boundary group").SetInviteCode("boundary-invite").SaveX(ctx)
	testClient.GroupMember.Create().SetGroupChat(group).SetUser(owner).SetRole(groupmember.RoleOwner).SaveX(ctx)
	testClient.GroupMember.Create().SetGroupChat(group).SetUser(member).SaveX(ctx)
	msg := testClient.Message.Create().SetChat(conversation).SetSender(owner).SetType(message.TypeRegular).SetContent("Original message").SaveX(ctx)
	base := "/api/chats/group/" + conversation.ID.String()
	cases := []struct {
		name, method, path string
		body               map[string]any
	}{
		{"chat", "GET", "/api/chats/" + conversation.ID.String(), nil},
		{"history", "GET", "/api/chats/" + conversation.ID.String() + "/messages", nil},
		{"read", "POST", "/api/chats/" + conversation.ID.String() + "/read", nil},
		{"hide", "POST", "/api/chats/" + conversation.ID.String() + "/hide", nil},
		{"members", "GET", base + "/members", nil},
		{"update", "PUT", base, map[string]any{"name": "Stolen group", "user_id": owner.ID, "role": "owner"}},
		{"delete", "DELETE", base, nil},
		{"add", "POST", base + "/members", map[string]any{"user_ids": []any{outsider.ID}, "requestor_id": owner.ID}},
		{"leave", "POST", base + "/leave", nil},
		{"invite", "PUT", base + "/invite", nil},
		{"kick", "POST", base + "/members/" + member.ID.String() + "/kick", nil},
		{"role", "PUT", base + "/members/" + member.ID.String() + "/role", map[string]any{"role": "admin", "requestor_id": owner.ID}},
		{"transfer", "POST", base + "/transfer", map[string]any{"new_owner_id": member.ID, "user_id": owner.ID}},
		{"send", "POST", "/api/messages", map[string]any{"chat_id": conversation.ID, "content": "Forged message", "sender_id": owner.ID}},
		{"edit", "PUT", "/api/messages/" + msg.ID.String(), map[string]any{"content": "Forged edit", "user_id": owner.ID}},
		{"remove", "DELETE", "/api/messages/" + msg.ID.String(), nil},
	}
	for _, tc := range cases {
		t.Run("outsider/"+tc.name, func(t *testing.T) {
			rr := makeRequest(tc.method, tc.path, tc.body, outsiderToken)
			if tc.name == "hide" || tc.name == "leave" {
				require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
				return
			}
			require.Contains(t, []int{http.StatusForbidden, http.StatusNotFound}, rr.Code, rr.Body.String())
		})
	}
	for _, i := range []int{5, 6, 7, 9, 10, 11, 12} {
		tc := cases[i]
		t.Run("member/"+tc.name, func(t *testing.T) {
			body := tc.body
			if tc.name == "transfer" {
				body = map[string]any{"new_owner_id": outsider.ID, "user_id": owner.ID}
			}
			rr := makeRequest(tc.method, tc.path, body, memberToken)
			require.Equal(t, http.StatusForbidden, rr.Code, rr.Body.String())
		})
	}
	require.Equal(t, "Boundary group", testClient.GroupChat.GetX(ctx, group.ID).Name)
	require.Nil(t, testClient.Chat.GetX(ctx, conversation.ID).DeletedAt)
	require.Equal(t, 2, testClient.GroupMember.Query().Where(groupmember.GroupChatID(group.ID)).CountX(ctx))
	require.Equal(t, 1, testClient.GroupMember.Query().Where(groupmember.GroupChatID(group.ID), groupmember.UserID(owner.ID), groupmember.RoleEQ(groupmember.RoleOwner)).CountX(ctx))
	require.Equal(t, 1, testClient.Message.Query().CountX(ctx))
	stored := testClient.Message.GetX(ctx, msg.ID)
	require.Equal(t, "Original message", *stored.Content)
	require.Nil(t, stored.DeletedAt)
}
