package test

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/report"
	"AtoiTalkAPI/ent/user"
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

func TestBannedUserRestrictions(t *testing.T) {
	clearDatabase(context.Background())

	password := "Password123!"
	hashedPassword, _ := helper.HashPassword(password)

	createUser := func(prefix string, isBanned bool, bannedUntil *time.Time) (*ent.User, string) {
		email := fmt.Sprintf("%s_%d@test.com", prefix, time.Now().UnixNano())
		username := fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())

		create := testClient.User.Create().
			SetEmail(email).
			SetUsername(username).
			SetFullName(prefix + " User").
			SetPasswordHash(hashedPassword).
			SetIsBanned(isBanned)

		if isBanned {
			create.SetBanReason("Spamming")
		}
		if bannedUntil != nil {
			create.SetBannedUntil(*bannedUntil)
		}

		u, err := create.Save(context.Background())
		if err != nil {
			t.Fatalf("Failed to create user %s: %v", prefix, err)
		}
		return u, email
	}

	normalUser, _ := createUser("normal", false, nil)
	adminUser, _ := createUser("admin", false, nil)

	testClient.User.UpdateOne(adminUser).SetRole(user.RoleAdmin).ExecX(context.Background())

	bannedUser, bannedEmail := createUser("banned", true, nil)

	until := time.Now().UTC().Add(1 * time.Hour)
	_, tempBannedEmail := createUser("temp", true, &until)

	expired := time.Now().UTC().Add(-1 * time.Hour)
	expiredBanUser, expiredBanEmail := createUser("expired", true, &expired)

	normalToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, normalUser.ID)
	adminToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, adminUser.ID)

	t.Run("Login - Banned User Cannot Login", func(t *testing.T) {
		reqBody := model.LoginRequest{
			Email:        bannedEmail,
			Password:     password,
			CaptchaToken: dummyTurnstileToken,
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/auth/login", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Login - Temp Banned User Cannot Login", func(t *testing.T) {
		reqBody := model.LoginRequest{
			Email:        tempBannedEmail,
			Password:     password,
			CaptchaToken: dummyTurnstileToken,
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/auth/login", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Login - Expired Ban User Can Login (Auto Unban)", func(t *testing.T) {
		reqBody := model.LoginRequest{
			Email:        expiredBanEmail,
			Password:     password,
			CaptchaToken: dummyTurnstileToken,
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/auth/login", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		u, _ := testClient.User.Query().Where(user.ID(expiredBanUser.ID)).Only(context.Background())
		assert.False(t, u.IsBanned)
		assert.Nil(t, u.BannedUntil)
	})

	t.Run("Search - Banned User Not Visible", func(t *testing.T) {

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/users?query=%s", *bannedUser.Username), nil)
		req.Header.Set("Authorization", "Bearer "+normalToken)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		assert.Len(t, dataList, 0)
	})

	t.Run("Search - Expired Ban User Visible", func(t *testing.T) {

		testClient.User.UpdateOne(expiredBanUser).
			SetIsBanned(true).
			SetBannedUntil(time.Now().UTC().Add(-1 * time.Hour)).
			ExecX(context.Background())

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/users?query=%s", *expiredBanUser.Username), nil)
		req.Header.Set("Authorization", "Bearer "+normalToken)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		assert.NotEmpty(t, dataList)

		found := false
		for _, item := range dataList {
			u := item.(map[string]interface{})
			if u["id"] == expiredBanUser.ID.String() {
				found = true
				break
			}
		}
		assert.True(t, found, "Expired ban user should be visible in search")
	})

	t.Run("Private Chat - Cannot Create with Banned User", func(t *testing.T) {
		reqBody := model.CreatePrivateChatRequest{TargetUserID: bannedUser.ID}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/chats/private", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+normalToken)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Group Chat - Cannot Add Banned User", func(t *testing.T) {

		chatEntity := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		gc := testClient.GroupChat.Create().SetChat(chatEntity).SetCreator(normalUser).SetName("Test Group").SetInviteCode("dummy_code_admin").SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(normalUser).SetRole(groupmember.RoleOwner).SaveX(context.Background())

		reqBody := model.AddGroupMemberRequest{UserIDs: []uuid.UUID{bannedUser.ID}}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+normalToken)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Group Chat - Cannot Create with Banned User", func(t *testing.T) {
		reqBody := model.CreateGroupChatRequest{
			Name:      "Fail Group",
			MemberIDs: []uuid.UUID{bannedUser.ID},
		}

		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/chats/group", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+normalToken)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Ban Revokes Existing Token", func(t *testing.T) {

		victim, _ := createUser("victim", false, nil)
		victimToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, victim.ID)

		req, _ := http.NewRequest("GET", "/api/user/current", nil)
		req.Header.Set("Authorization", "Bearer "+victimToken)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code, "Token should be valid initially")

		reqBody := model.BanUserRequest{
			TargetUserID: victim.ID,
			Reason:       "You are banned",
		}
		body, _ := json.Marshal(reqBody)
		reqBan, _ := http.NewRequest("POST", "/api/admin/users/ban", bytes.NewBuffer(body))
		reqBan.Header.Set("Content-Type", "application/json")
		reqBan.Header.Set("Authorization", "Bearer "+adminToken)
		rrBan := executeRequest(reqBan)
		assert.Equal(t, http.StatusOK, rrBan.Code, "Admin should be able to ban user")

		reqCheck, _ := http.NewRequest("GET", "/api/user/current", nil)
		reqCheck.Header.Set("Authorization", "Bearer "+victimToken)
		rrCheck := executeRequest(reqCheck)
		assert.Equal(t, http.StatusUnauthorized, rrCheck.Code, "Token should be revoked immediately after ban")
	})
}

func TestReportSystem(t *testing.T) {

	createUser := func(prefix string) *ent.User {
		email := fmt.Sprintf("%s_%d@test.com", prefix, time.Now().UnixNano())
		username := fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
		hashedPassword, _ := helper.HashPassword("Password123!")

		u, err := testClient.User.Create().
			SetEmail(email).
			SetUsername(username).
			SetFullName(prefix + " User").
			SetPasswordHash(hashedPassword).
			Save(context.Background())
		if err != nil {
			t.Fatalf("Failed to create user %s: %v", prefix, err)
		}
		return u
	}

	t.Run("Report Message - Success with Snapshot", func(t *testing.T) {
		clearDatabase(context.Background())

		reporter := createUser("reporter1")
		offender := createUser("offender1")
		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, reporter.ID)

		chatEntity := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(context.Background())
		testClient.PrivateChat.Create().SetChat(chatEntity).SetUser1(reporter).SetUser2(offender).SaveX(context.Background())

		m, _ := testClient.Media.Create().
			SetFileName("evidence.jpg").SetOriginalName("evidence.jpg").SetFileSize(100).SetMimeType("image/jpeg").
			SetUploader(offender).Save(context.Background())

		msg, _ := testClient.Message.Create().
			SetChat(chatEntity).
			SetSender(offender).
			SetType(message.TypeRegular).
			SetContent("Bad Message").
			AddAttachments(m).
			Save(context.Background())

		reqBody := model.CreateReportRequest{
			TargetType:  "message",
			MessageID:   &msg.ID,
			Reason:      "harassment",
			Description: "He is rude",
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/reports", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		rpt, err := testClient.Report.Query().
			Where(report.ReporterID(reporter.ID)).
			WithEvidenceMedia().
			Only(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, report.TargetTypeMessage, rpt.TargetType)
		assert.Equal(t, msg.ID, *rpt.MessageID)

		snapshot := rpt.EvidenceSnapshot
		assert.Equal(t, "Bad Message", snapshot["content"])
		assert.Equal(t, offender.ID.String(), snapshot["sender_id"])

		assert.Len(t, rpt.Edges.EvidenceMedia, 1)
		assert.Equal(t, m.ID, rpt.Edges.EvidenceMedia[0].ID)
	})

	t.Run("Report User - Success with Snapshot", func(t *testing.T) {
		clearDatabase(context.Background())

		reporter := createUser("reporter2")
		offender := createUser("offender2")
		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, reporter.ID)

		avatar, _ := testClient.Media.Create().
			SetFileName("bad_avatar.jpg").SetOriginalName("bad.jpg").SetFileSize(100).SetMimeType("image/jpeg").
			SetUploader(offender).Save(context.Background())

		testClient.User.UpdateOne(offender).SetAvatar(avatar).SetBio("Bad Bio").ExecX(context.Background())

		reqBody := model.CreateReportRequest{
			TargetType:   "user",
			TargetUserID: &offender.ID,
			Reason:       "impersonation",
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/reports", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		rpt, err := testClient.Report.Query().
			Where(report.TargetUserID(offender.ID)).
			WithEvidenceMedia().
			Only(context.Background())
		assert.NoError(t, err)

		snapshot := rpt.EvidenceSnapshot
		assert.Equal(t, "Bad Bio", snapshot["bio"])
		assert.Contains(t, snapshot["full_name"], "offender2")

		assert.Len(t, rpt.Edges.EvidenceMedia, 1)
		assert.Equal(t, avatar.ID, rpt.Edges.EvidenceMedia[0].ID)
	})

	t.Run("Report Group - Success", func(t *testing.T) {
		clearDatabase(context.Background())

		reporter := createUser("reporter3")
		offender := createUser("offender3")
		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, reporter.ID)

		chatEntity := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		gc := testClient.GroupChat.Create().SetChat(chatEntity).SetCreator(offender).SetName("Bad Group").SetInviteCode("badgroup").SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(offender).SetRole(groupmember.RoleOwner).SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(reporter).SetRole(groupmember.RoleMember).SaveX(context.Background())

		reqBody := model.CreateReportRequest{
			TargetType: "group",
			ChatID:     &chatEntity.ID,
			Reason:     "violence",
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/reports", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		rpt, err := testClient.Report.Query().
			Where(report.GroupID(gc.ID)).
			Only(context.Background())
		assert.NoError(t, err)

		snapshot := rpt.EvidenceSnapshot
		assert.Equal(t, "Bad Group", snapshot["name"])
	})

	t.Run("Report Group - Fail Not Member", func(t *testing.T) {
		clearDatabase(context.Background())

		reporter := createUser("reporter4")
		offender := createUser("offender4")
		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, reporter.ID)

		chatEntity := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		testClient.GroupChat.Create().SetChat(chatEntity).SetCreator(offender).SetName("Secret Bad Group").SetInviteCode("secretbad").SaveX(context.Background())

		reqBody := model.CreateReportRequest{
			TargetType: "group",
			ChatID:     &chatEntity.ID,
			Reason:     "violence",
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/reports", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Evidence Integrity - Delete Message After Report", func(t *testing.T) {
		clearDatabase(context.Background())

		reporter := createUser("reporter5")
		offender := createUser("offender5")
		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, reporter.ID)

		chatEntity := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(context.Background())
		testClient.PrivateChat.Create().SetChat(chatEntity).SetUser1(reporter).SetUser2(offender).SaveX(context.Background())

		msg, _ := testClient.Message.Create().
			SetChat(chatEntity).
			SetSender(offender).
			SetType(message.TypeRegular).
			SetContent("I will delete this").
			Save(context.Background())

		reqBody := model.CreateReportRequest{
			TargetType: "message",
			MessageID:  &msg.ID,
			Reason:     "spam",
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/reports", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)
		executeRequest(req)

		testClient.Message.UpdateOne(msg).SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		rpt, _ := testClient.Report.Query().Where(report.MessageID(msg.ID)).Only(context.Background())
		snapshot := rpt.EvidenceSnapshot
		assert.Equal(t, "I will delete this", snapshot["content"])

		deletedMsg, _ := testClient.Message.Query().Where(message.ID(msg.ID)).Only(context.Background())
		assert.NotNil(t, deletedMsg.DeletedAt)
	})
}

func TestAdminDashboard(t *testing.T) {
	clearDatabase(context.Background())

	createUser := func(prefix string, role user.Role) *ent.User {
		email := fmt.Sprintf("%s_%d@test.com", prefix, time.Now().UnixNano())
		username := fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
		hashedPassword, _ := helper.HashPassword("Password123!")

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
		return u
	}

	admin := createUser("admin", user.RoleAdmin)
	user1 := createUser("user1", user.RoleUser)
	user2 := createUser("user2", user.RoleUser)

	adminToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, admin.ID)
	userToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, user1.ID)

	testClient.Report.Create().
		SetReporter(user1).
		SetTargetType(report.TargetTypeUser).
		SetTargetUser(user2).
		SetReason("spam").
		SaveX(context.Background())

	testClient.Report.Create().
		SetReporter(user2).
		SetTargetType(report.TargetTypeUser).
		SetTargetUser(user1).
		SetReason("harassment").
		SaveX(context.Background())

	t.Run("Get Reports - Admin Success", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/admin/reports", nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 2)
	})

	t.Run("Get Reports - User Forbidden", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/admin/reports", nil)
		req.Header.Set("Authorization", "Bearer "+userToken)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Ban User via API - Admin Success", func(t *testing.T) {
		reqBody := model.BanUserRequest{
			TargetUserID: user2.ID,
			Reason:       "Violation of terms",
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/admin/users/ban", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+adminToken)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		u, _ := testClient.User.Query().Where(user.ID(user2.ID)).Only(context.Background())
		assert.True(t, u.IsBanned)
	})

	t.Run("Ban User via API - User Forbidden", func(t *testing.T) {
		reqBody := model.BanUserRequest{
			TargetUserID: user1.ID,
			Reason:       "I hate him",
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/admin/users/ban", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+userToken)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Resolve Report - Admin Success", func(t *testing.T) {
		rpt, _ := testClient.Report.Query().First(context.Background())

		reqBody := model.ResolveReportRequest{
			Status: "resolved",
			Notes:  "Done",
		}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("PUT", fmt.Sprintf("/api/admin/reports/%s/resolve", rpt.ID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+adminToken)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		updatedRpt, _ := testClient.Report.Query().Where(report.ID(rpt.ID)).Only(context.Background())
		assert.Equal(t, report.StatusResolved, updatedRpt.Status)
		assert.Equal(t, "Done", *updatedRpt.ResolutionNotes)
		assert.Equal(t, admin.ID, *updatedRpt.ResolvedByID)
	})
	t.Run("Delete Report - Admin Success", func(t *testing.T) {
		rpt, _ := testClient.Report.Create().
			SetReporter(user1).
			SetTargetType(report.TargetTypeUser).
			SetTargetUser(user2).
			SetReason("spam").
			SetStatus(report.StatusResolved).
			Save(context.Background())

		req, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/admin/reports/%s", rpt.ID), nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		exists, _ := testClient.Report.Query().Where(report.ID(rpt.ID)).Exist(context.Background())
		assert.False(t, exists)
	})

	t.Run("Delete Report - User Forbidden", func(t *testing.T) {
		rpt, _ := testClient.Report.Create().
			SetReporter(user1).
			SetTargetType(report.TargetTypeUser).
			SetTargetUser(user2).
			SetReason("spam").
			Save(context.Background())

		req, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/admin/reports/%s", rpt.ID), nil)
		req.Header.Set("Authorization", "Bearer "+userToken)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)

		exists, _ := testClient.Report.Query().Where(report.ID(rpt.ID)).Exist(context.Background())
		assert.True(t, exists)
	})

	t.Run("Delete Report - Not Found", func(t *testing.T) {
		req, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/admin/reports/%s", uuid.New()), nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Delete Report - Fail Pending Status", func(t *testing.T) {

		rpt, _ := testClient.Report.Create().
			SetReporter(user1).
			SetTargetType(report.TargetTypeUser).
			SetTargetUser(user2).
			SetReason("spam").
			SetStatus(report.StatusPending).
			Save(context.Background())

		req, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/admin/reports/%s", rpt.ID), nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)

		exists, _ := testClient.Report.Query().Where(report.ID(rpt.ID)).Exist(context.Background())
		assert.True(t, exists)
	})

	t.Run("Delete Report - Success Resolved Status", func(t *testing.T) {

		mediaItem, _ := testClient.Media.Create().
			SetFileName("evidence.jpg").
			SetOriginalName("evidence.jpg").
			SetFileSize(1024).
			SetMimeType("image/jpeg").
			SetUploader(user2).
			Save(context.Background())

		rpt, _ := testClient.Report.Create().
			SetReporter(user1).
			SetTargetType(report.TargetTypeUser).
			SetTargetUser(user2).
			SetReason("spam").
			SetStatus(report.StatusResolved).
			AddEvidenceMedia(mediaItem).
			Save(context.Background())

		hasMedia, _ := rpt.QueryEvidenceMedia().Where(media.ID(mediaItem.ID)).Exist(context.Background())
		assert.True(t, hasMedia)

		req, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/admin/reports/%s", rpt.ID), nil)
		req.Header.Set("Authorization", "Bearer "+adminToken)
		executeRequest(req)

		rptExists, _ := testClient.Report.Query().Where(report.ID(rpt.ID)).Exist(context.Background())
		assert.False(t, rptExists)

		mediaExists, _ := testClient.Media.Query().Where(media.ID(mediaItem.ID)).Exist(context.Background())
		assert.True(t, mediaExists)

		linkedReportsCount, _ := mediaItem.QueryReports().Count(context.Background())
		assert.Equal(t, 0, linkedReportsCount)
	})
}
