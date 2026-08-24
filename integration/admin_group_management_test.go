//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAdminGetGroups(t *testing.T) {
	clearDatabase(context.Background())

	password := "Password123!"
	hashedPassword, _ := helper.HashPassword(password)

	admin, _ := testClient.User.Create().
		SetEmail("admin_groups@test.com").
		SetUsername("admin_groups").
		SetFullName("Admin Groups").
		SetPasswordHash(hashedPassword).
		SetRole(user.RoleAdmin).
		Save(context.Background())

	regularUser, _ := testClient.User.Create().
		SetEmail("regular_groups@test.com").
		SetUsername("regular_groups").
		SetFullName("Regular Groups").
		SetPasswordHash(hashedPassword).
		Save(context.Background())

	for i := 1; i <= 5; i++ {
		chatEntity := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		gc := testClient.GroupChat.Create().
			SetChat(chatEntity).
			SetCreator(admin).
			SetName(fmt.Sprintf("Test Group %d", i)).
			SetInviteCode(fmt.Sprintf("CODE%d", i)).
			SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(admin).SetRole(groupmember.RoleOwner).SaveX(context.Background())
	}

	adminToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, admin.ID)
	regularToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, regularUser.ID)

	t.Run("Success List All Groups", func(t *testing.T) {
		rr := makeRequest("GET", "/api/admin/groups", nil, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		resp := parseResponse[[]model.AdminGroupListResponse](t, rr)
		assert.GreaterOrEqual(t, len(resp), 5)
	})

	t.Run("Success Search by Name", func(t *testing.T) {
		rr := makeRequest("GET", "/api/admin/groups?query=Group%201", nil, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		resp := parseResponse[[]model.AdminGroupListResponse](t, rr)
		assert.GreaterOrEqual(t, len(resp), 1)
	})

	t.Run("Success Pagination", func(t *testing.T) {
		rr := makeRequest("GET", "/api/admin/groups?limit=2", nil, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		resp := parseResponse[[]model.AdminGroupListResponse](t, rr)
		assert.Len(t, resp, 2)
	})

	t.Run("Fail Forbidden for Regular User", func(t *testing.T) {
		rr := makeRequest("GET", "/api/admin/groups", nil, regularToken)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})
}

func TestAdminGetGroupDetail(t *testing.T) {
	clearDatabase(context.Background())

	password := "Password123!"
	hashedPassword, _ := helper.HashPassword(password)

	admin, _ := testClient.User.Create().
		SetEmail("admin_group_detail@test.com").
		SetUsername("admin_group_detail").
		SetFullName("Admin Group Detail").
		SetPasswordHash(hashedPassword).
		SetRole(user.RoleAdmin).
		Save(context.Background())

	regularUser, _ := testClient.User.Create().
		SetEmail("regular_group_detail@test.com").
		SetUsername("regular_group_detail").
		SetFullName("Regular Group Detail").
		SetPasswordHash(hashedPassword).
		Save(context.Background())

	chatEntity := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
	gc := testClient.GroupChat.Create().
		SetChat(chatEntity).
		SetCreator(admin).
		SetName("Detail Test Group").
		SetDescription("Test Description").
		SetInviteCode("DETAILCODE").
		SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(admin).SetRole(groupmember.RoleOwner).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(regularUser).SetRole(groupmember.RoleMember).SaveX(context.Background())

	testClient.Message.Create().SetChat(chatEntity).SetSender(admin).SetContent("Msg 1").SaveX(context.Background())
	testClient.Message.Create().SetChat(chatEntity).SetSender(admin).SetContent("Msg 2").SaveX(context.Background())

	adminToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, admin.ID)
	regularToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, regularUser.ID)

	t.Run("Success Get Group Detail", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/admin/groups/%s", gc.ChatID), nil, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		detail := parseResponse[model.AdminGroupDetailResponse](t, rr)
		assert.Equal(t, gc.ID, detail.ID)
		assert.Equal(t, "Detail Test Group", detail.Name)
		assert.Equal(t, "Test Description", *detail.Description)
		assert.Equal(t, 2, detail.MemberCount)
		assert.Equal(t, 2, detail.TotalMessages)
	})

	t.Run("Fail Group Not Found", func(t *testing.T) {
		rr := makeRequest("GET", "/api/admin/groups/01900000-0000-7000-8000-000000000001", nil, adminToken)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail Forbidden for Regular User", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/admin/groups/%s", gc.ChatID), nil, regularToken)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})
}

func TestAdminDissolveGroup(t *testing.T) {
	clearDatabase(context.Background())

	password := "Password123!"
	hashedPassword, _ := helper.HashPassword(password)

	admin, _ := testClient.User.Create().
		SetEmail("admin_dissolve@test.com").
		SetUsername("admin_dissolve").
		SetFullName("Admin Dissolve").
		SetPasswordHash(hashedPassword).
		SetRole(user.RoleAdmin).
		Save(context.Background())

	regularUser, _ := testClient.User.Create().
		SetEmail("regular_dissolve@test.com").
		SetUsername("regular_dissolve").
		SetFullName("Regular Dissolve").
		SetPasswordHash(hashedPassword).
		Save(context.Background())

	adminToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, admin.ID)
	regularToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, regularUser.ID)

	t.Run("Success Dissolve Group", func(t *testing.T) {
		chatEntity := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		gc := testClient.GroupChat.Create().
			SetChat(chatEntity).
			SetCreator(admin).
			SetName("Group To Dissolve").
			SetInviteCode(fmt.Sprintf("DISSOLVE%d", time.Now().UnixNano())).
			SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(admin).SetRole(groupmember.RoleOwner).SaveX(context.Background())

		rr := makeRequest("DELETE", fmt.Sprintf("/api/admin/groups/%s", gc.ChatID), nil, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		updatedChat, _ := testClient.Chat.Get(context.Background(), chatEntity.ID)
		assert.NotNil(t, updatedChat.DeletedAt)
	})

	t.Run("Fail Group Not Found", func(t *testing.T) {
		rr := makeRequest("DELETE", "/api/admin/groups/01900000-0000-7000-8000-000000000001", nil, adminToken)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail Forbidden for Regular User", func(t *testing.T) {
		chatEntity := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		gc := testClient.GroupChat.Create().
			SetChat(chatEntity).
			SetCreator(admin).
			SetName("Group Cannot Dissolve").
			SetInviteCode(fmt.Sprintf("NODISSOLVE%d", time.Now().UnixNano())).
			SaveX(context.Background())

		rr := makeRequest("DELETE", fmt.Sprintf("/api/admin/groups/%s", gc.ChatID), nil, regularToken)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})
}

func TestAdminResetGroupInfo(t *testing.T) {
	clearDatabase(context.Background())

	password := "Password123!"
	hashedPassword, _ := helper.HashPassword(password)

	admin, _ := testClient.User.Create().
		SetEmail("admin_reset_group@test.com").
		SetUsername("admin_reset_group").
		SetFullName("Admin Reset Group").
		SetPasswordHash(hashedPassword).
		SetRole(user.RoleAdmin).
		Save(context.Background())

	adminToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, admin.ID)

	t.Run("Success Reset Description", func(t *testing.T) {
		chatEntity := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		gc := testClient.GroupChat.Create().
			SetChat(chatEntity).
			SetCreator(admin).
			SetName("Reset Desc Group").
			SetDescription("This is a bad description").
			SetInviteCode(fmt.Sprintf("RESETDESC%d", time.Now().UnixNano())).
			SaveX(context.Background())

		reqBody := model.ResetGroupInfoRequest{
			ResetDescription: true,
		}
		rr := makeRequest("POST", fmt.Sprintf("/api/admin/groups/%s/reset", gc.ChatID), reqBody, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		updatedGC, _ := testClient.GroupChat.Get(context.Background(), gc.ID)
		if updatedGC.Description != nil {
			assert.Equal(t, "", *updatedGC.Description)
		}
	})

	t.Run("Success Reset Name", func(t *testing.T) {
		chatEntity := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		gc := testClient.GroupChat.Create().
			SetChat(chatEntity).
			SetCreator(admin).
			SetName("Bad Group Name").
			SetInviteCode(fmt.Sprintf("RESETNAME%d", time.Now().UnixNano())).
			SaveX(context.Background())

		reqBody := model.ResetGroupInfoRequest{
			ResetName: true,
		}
		rr := makeRequest("POST", fmt.Sprintf("/api/admin/groups/%s/reset", gc.ChatID), reqBody, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		updatedGC, _ := testClient.GroupChat.Get(context.Background(), gc.ID)
		assert.Contains(t, updatedGC.Name, "Group ")
	})

	t.Run("Success Reset Avatar", func(t *testing.T) {
		chatEntity := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		gc := testClient.GroupChat.Create().
			SetChat(chatEntity).
			SetCreator(admin).
			SetName("Reset Avatar Group").
			SetInviteCode(fmt.Sprintf("RESETAVATAR%d", time.Now().UnixNano())).
			SaveX(context.Background())

		media, _ := testClient.Media.Create().
			SetFileName("bad_group_avatar.jpg").
			SetOriginalName("bad.jpg").
			SetFileSize(1024).
			SetMimeType("image/jpeg").
			SetUploader(admin).
			Save(context.Background())

		testClient.GroupChat.UpdateOne(gc).SetAvatar(media).ExecX(context.Background())

		reqBody := model.ResetGroupInfoRequest{
			ResetAvatar: true,
		}
		rr := makeRequest("POST", fmt.Sprintf("/api/admin/groups/%s/reset", gc.ChatID), reqBody, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		updatedGC, _ := testClient.GroupChat.Query().Where().WithAvatar().First(context.Background())
		assert.Nil(t, updatedGC.Edges.Avatar)
	})

	t.Run("Fail Group Not Found", func(t *testing.T) {
		reqBody := model.ResetGroupInfoRequest{
			ResetDescription: true,
		}
		rr := makeRequest("POST", "/api/admin/groups/01900000-0000-7000-8000-000000000001/reset", reqBody, adminToken)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}
