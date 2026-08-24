//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"context"
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestGetDashboardStats(t *testing.T) {
	if testClient == nil {
		t.Fatal("testClient is nil")
	}
	clearDatabase(context.Background())

	adminUser := createTestUser(t, "admin_stats")
	_, err := testClient.User.UpdateOne(adminUser).SetRole(user.RoleAdmin).Save(context.Background())
	if err != nil {
		t.Fatalf("Failed to promote user to admin: %v", err)
	}

	createTestUser(t, "user1")
	createTestUser(t, "user2")
	createTestUser(t, "user3")

	owner := adminUser
	_, err = testClient.GroupChat.Create().
		SetChat(testClient.Chat.Create().SetType("group").SaveX(context.Background())).
		SetName("Group 1").
		SetInviteCode("CODE1").
		SetCreatedBy(owner.ID).
		Save(context.Background())
	if err != nil {
		t.Fatalf("Failed to create group: %v", err)
	}
	_, err = testClient.GroupChat.Create().
		SetChat(testClient.Chat.Create().SetType("group").SaveX(context.Background())).
		SetName("Group 2").
		SetInviteCode("CODE2").
		SetCreatedBy(owner.ID).
		Save(context.Background())
	if err != nil {
		t.Fatalf("Failed to create group: %v", err)
	}

	group1 := testClient.GroupChat.Query().Where(groupchat.Name("Group 1")).WithChat().OnlyX(context.Background())
	chat1 := group1.Edges.Chat

	testClient.Message.Create().SetChat(chat1).SetSender(adminUser).SetContent("Msg 1").SaveX(context.Background())
	testClient.Message.Create().SetChat(chat1).SetSender(adminUser).SetContent("Msg 2").SaveX(context.Background())
	testClient.Message.Create().SetChat(chat1).SetSender(adminUser).SetContent("Msg 3").SaveX(context.Background())

	users := testClient.User.Query().AllX(context.Background())
	target := users[1]

	testClient.Report.Create().
		SetReporter(adminUser).
		SetTargetUserID(target.ID).
		SetTargetType("user").
		SetReason("Spam").
		SetEvidenceSnapshot(map[string]interface{}{"foo": "bar"}).
		SaveX(context.Background())

	testClient.Report.Create().
		SetReporter(adminUser).
		SetTargetUserID(target.ID).
		SetTargetType("user").
		SetReason("Abuse").
		SetEvidenceSnapshot(map[string]interface{}{"foo": "bar"}).
		SaveX(context.Background())

	token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, adminUser.ID)

	rr := makeRequest("GET", "/api/admin/dashboard", nil, token)
	assert.Equal(t, http.StatusOK, rr.Code)

	stats := parseResponse[model.DashboardStatsResponse](t, rr)
	assert.Equal(t, 4, stats.TotalUsers)
	assert.Equal(t, 2, stats.TotalGroups)
	assert.Equal(t, 3, stats.TotalMessages)
	assert.Equal(t, 2, stats.ActiveReports)
}
