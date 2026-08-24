//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestAdminGetUsers(t *testing.T) {
	clearDatabase(context.Background())

	password := "Password123!"
	hashedPassword, _ := helper.HashPassword(password)

	createUser := func(prefix string, role user.Role) *struct {
		ID    string
		Email string
		Name  string
	} {
		email := fmt.Sprintf("%s_%d@test.com", prefix, time.Now().UnixNano())
		username := fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())

		u, err := testClient.User.Create().
			SetEmail(email).
			SetUsername(username).
			SetFullName(prefix + " User").
			SetPasswordHash(hashedPassword).
			SetRole(role).
			Save(context.Background())
		if err != nil {
			t.Fatalf("Failed to create user %s: %v", prefix, err)
		}
		return &struct {
			ID    string
			Email string
			Name  string
		}{ID: u.ID.String(), Email: email, Name: prefix + " User"}
	}

	admin := createUser("admin", user.RoleAdmin)
	createUser("user1", user.RoleUser)
	createUser("user2", user.RoleUser)
	createUser("user3", user.RoleUser)

	regularUser := createUser("regular", user.RoleUser)

	loginPayload := model.LoginRequest{
		Email:        admin.Email,
		Password:     password,
		CaptchaToken: cfTurnstileAlwaysPasses,
	}
	rr := makeRequest("POST", "/api/auth/login", loginPayload, "")
	loginResp := parseResponse[model.AuthResponse](t, rr)
	adminToken := loginResp.Token

	regularToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, testClient.User.Query().Where(user.EmailEQ(regularUser.Email)).OnlyX(context.Background()).ID)

	t.Run("Success - List All Users", func(t *testing.T) {
		rr := makeRequest("GET", "/api/admin/users", nil, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		resp := parseResponse[[]model.AdminUserListResponse](t, rr)
		assert.GreaterOrEqual(t, len(resp), 5)
	})

	t.Run("Success - Filter by Role", func(t *testing.T) {
		rr := makeRequest("GET", "/api/admin/users?role=admin", nil, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		resp := parseResponse[[]model.AdminUserListResponse](t, rr)
		assert.GreaterOrEqual(t, len(resp), 1)
		for _, u := range resp {
			assert.Equal(t, "admin", u.Role)
		}
	})

	t.Run("Success - Search by Query", func(t *testing.T) {
		rr := makeRequest("GET", "/api/admin/users?query=user1", nil, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		resp := parseResponse[[]model.AdminUserListResponse](t, rr)
		assert.GreaterOrEqual(t, len(resp), 1)
	})

	t.Run("Success - Pagination", func(t *testing.T) {
		rr := makeRequest("GET", "/api/admin/users?limit=2", nil, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		resp := parseResponse[[]model.AdminUserListResponse](t, rr)
		assert.Len(t, resp, 2)

		var raw helper.ResponseWithPagination
		_ = json.Unmarshal(rr.Body.Bytes(), &raw)
		assert.True(t, raw.Meta.HasNext)
		assert.NotEmpty(t, raw.Meta.NextCursor)

		rr2 := makeRequest("GET", fmt.Sprintf("/api/admin/users?limit=2&cursor=%s", raw.Meta.NextCursor), nil, adminToken)
		assert.Equal(t, http.StatusOK, rr2.Code)

		resp2 := parseResponse[[]model.AdminUserListResponse](t, rr2)
		assert.NotNil(t, resp2)
	})

	t.Run("Fail - Forbidden for Regular User", func(t *testing.T) {
		rr := makeRequest("GET", "/api/admin/users", nil, regularToken)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Unauthorized", func(t *testing.T) {
		rr := makeRequest("GET", "/api/admin/users", nil, "")
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})
}

func TestAdminGetUserDetail(t *testing.T) {
	clearDatabase(context.Background())

	password := "Password123!"
	hashedPassword, _ := helper.HashPassword(password)

	admin, _ := testClient.User.Create().
		SetEmail("admin_detail@test.com").
		SetUsername("admin_detail").
		SetFullName("Admin Detail").
		SetPasswordHash(hashedPassword).
		SetRole(user.RoleAdmin).
		Save(context.Background())

	targetUser, _ := testClient.User.Create().
		SetEmail("target_detail@test.com").
		SetUsername("target_detail").
		SetFullName("Target User").
		SetBio("Test bio").
		SetPasswordHash(hashedPassword).
		SetRole(user.RoleUser).
		Save(context.Background())

	regularUser, _ := testClient.User.Create().
		SetEmail("regular_detail@test.com").
		SetUsername("regular_detail").
		SetFullName("Regular User").
		SetPasswordHash(hashedPassword).
		SetRole(user.RoleUser).
		Save(context.Background())

	adminToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, admin.ID)
	regularToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, regularUser.ID)

	t.Run("Success - Get User Detail", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/admin/users/%s", targetUser.ID), nil, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.AdminUserDetailResponse](t, rr)
		assert.Equal(t, targetUser.ID, data.ID)
		assert.Equal(t, "target_detail", data.Username)
		assert.Equal(t, "Target User", *data.FullName)
		assert.Equal(t, "Test bio", *data.Bio)
		assert.Equal(t, "user", data.Role)
	})

	t.Run("Fail - User Not Found", func(t *testing.T) {
		rr := makeRequest("GET", "/api/admin/users/00000000-0000-0000-0000-000000000000", nil, adminToken)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Invalid UUID", func(t *testing.T) {
		rr := makeRequest("GET", "/api/admin/users/invalid-uuid", nil, adminToken)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Forbidden for Regular User", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/admin/users/%s", targetUser.ID), nil, regularToken)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})
}

func TestAdminResetUserInfo(t *testing.T) {
	clearDatabase(context.Background())

	password := "Password123!"
	hashedPassword, _ := helper.HashPassword(password)

	admin, _ := testClient.User.Create().
		SetEmail("admin_reset@test.com").
		SetUsername("admin_reset").
		SetFullName("Admin Reset").
		SetPasswordHash(hashedPassword).
		SetRole(user.RoleAdmin).
		Save(context.Background())

	adminToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, admin.ID)

	t.Run("Success - Reset Bio", func(t *testing.T) {
		targetUser, _ := testClient.User.Create().
			SetEmail("reset_bio@test.com").
			SetUsername("reset_bio").
			SetFullName("Reset Bio User").
			SetBio("This is my bio").
			SetPasswordHash(hashedPassword).
			Save(context.Background())

		reqBody := model.ResetUserInfoRequest{
			TargetUserID: targetUser.ID,
			ResetBio:     true,
		}
		rr := makeRequest("POST", fmt.Sprintf("/api/admin/users/%s/reset", targetUser.ID), reqBody, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		u, _ := testClient.User.Query().Where(user.ID(targetUser.ID)).Only(context.Background())
		assert.Nil(t, u.Bio)
	})

	t.Run("Success - Reset Name", func(t *testing.T) {
		targetUser, _ := testClient.User.Create().
			SetEmail("reset_name@test.com").
			SetUsername("reset_name").
			SetFullName("Bad Name").
			SetPasswordHash(hashedPassword).
			Save(context.Background())

		reqBody := model.ResetUserInfoRequest{
			TargetUserID: targetUser.ID,
			ResetName:    true,
		}
		rr := makeRequest("POST", fmt.Sprintf("/api/admin/users/%s/reset", targetUser.ID), reqBody, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		u, _ := testClient.User.Query().Where(user.ID(targetUser.ID)).Only(context.Background())
		assert.Contains(t, *u.FullName, "User ")
	})

	t.Run("Success - Reset Avatar", func(t *testing.T) {
		targetUser, _ := testClient.User.Create().
			SetEmail("reset_avatar@test.com").
			SetUsername("reset_avatar").
			SetFullName("Reset Avatar User").
			SetPasswordHash(hashedPassword).
			Save(context.Background())

		media, _ := testClient.Media.Create().
			SetFileName("bad_avatar.jpg").
			SetOriginalName("bad.jpg").
			SetFileSize(1024).
			SetMimeType("image/jpeg").
			SetUploader(targetUser).
			Save(context.Background())

		testClient.User.UpdateOne(targetUser).SetAvatar(media).ExecX(context.Background())

		reqBody := model.ResetUserInfoRequest{
			TargetUserID: targetUser.ID,
			ResetAvatar:  true,
		}
		rr := makeRequest("POST", fmt.Sprintf("/api/admin/users/%s/reset", targetUser.ID), reqBody, adminToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		u, _ := testClient.User.Query().Where(user.ID(targetUser.ID)).WithAvatar().Only(context.Background())
		assert.Nil(t, u.Edges.Avatar)

		mediaStillExists, _ := testClient.Media.Query().Where().Exist(context.Background())
		assert.True(t, mediaStillExists)
	})

	t.Run("Fail - User Not Found", func(t *testing.T) {
		nonExistentID := "01900000-0000-7000-8000-000000000001"
		reqBody := map[string]any{
			"reset_bio": true,
		}
		rr := makeRequest("POST", fmt.Sprintf("/api/admin/users/%s/reset", nonExistentID), reqBody, adminToken)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Forbidden for Regular User", func(t *testing.T) {
		regularUser, _ := testClient.User.Create().
			SetEmail("regular_forbidden@test.com").
			SetUsername("regular_forbidden").
			SetFullName("Regular Forbidden").
			SetPasswordHash(hashedPassword).
			Save(context.Background())

		regularToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, regularUser.ID)

		reqBody := model.ResetUserInfoRequest{
			TargetUserID: admin.ID,
			ResetBio:     true,
		}
		rr := makeRequest("POST", fmt.Sprintf("/api/admin/users/%s/reset", admin.ID), reqBody, regularToken)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})
}
