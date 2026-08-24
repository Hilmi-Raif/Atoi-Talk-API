package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/ent/report"
	"AtoiTalkAPI/ent/user"
	adaptermocks "AtoiTalkAPI/internal/adapter/mocks"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	repositorymocks "AtoiTalkAPI/internal/repository/mocks"
	"AtoiTalkAPI/internal/websocket"
	websocketmocks "AtoiTalkAPI/internal/websocket/mocks"
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newAdminServiceTest(t *testing.T) (*AdminService, *ent.Client, *repositorymocks.MockSessionStore, *websocketmocks.MockPublisher) {
	t.Helper()
	client := enttest.Open(t, dialect.SQLite, "file:admin-service-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	sessionStore := repositorymocks.NewMockSessionStore(t)
	publisher := websocketmocks.NewMockPublisher(t)
	service := NewAdminService(
		client,
		&config.AppConfig{},
		config.NewValidator(),
		publisher,
		sessionStore,
		repositorymocks.NewMockGroupMemberReader(t),
		adaptermocks.NewMockURLGenerator(t),
	)
	return service, client, sessionStore, publisher
}

func TestAdminServiceBanUserUpdatesUserRevokesSessionsAndBroadcasts(t *testing.T) {
	service, client, sessionStore, publisher := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	adminID := uuid.New()
	target := client.User.Create().SetUsername("target").SetRole(user.RoleUser).SaveX(ctx)

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, target.ID).Return(helper.SessionRevokeSnapshot{}, nil)
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, target.ID, mock.AnythingOfType("int64")).Return("marker", nil)
	publisher.EXPECT().BroadcastToUser(target.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventUserBanned
	})).Return()
	publisher.EXPECT().BroadcastToContacts(target.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventUserBanned
	})).Return()
	publisher.EXPECT().DisconnectUser(target.ID).Return()

	err := service.BanUser(ctx, adminID, model.BanUserRequest{
		TargetUserID:  target.ID,
		Reason:        " abuse ",
		DurationHours: 24,
	})

	require.NoError(t, err)
	updated := client.User.GetX(ctx, target.ID)
	require.True(t, updated.IsBanned)
	require.Equal(t, "abuse", *updated.BanReason)
	require.NotNil(t, updated.BannedUntil)

	err = service.BanUser(ctx, adminID, model.BanUserRequest{
		TargetUserID:  target.ID,
		Reason:        "abuse",
		DurationHours: 0,
	})
	require.NoError(t, err)
}

func TestAdminServiceBanUserRejectsAdminAndMissingUser(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	adminUser := client.User.Create().SetUsername("adminuser").SetRole(user.RoleAdmin).SaveX(ctx)

	err := service.BanUser(ctx, uuid.New(), model.BanUserRequest{TargetUserID: adminUser.ID, Reason: "abuse"})
	require.Error(t, err)

	err = service.BanUser(ctx, uuid.New(), model.BanUserRequest{TargetUserID: uuid.New(), Reason: "abuse"})
	require.Error(t, err)
}

func TestAdminServiceBanUserReturnsSessionUnavailable(t *testing.T) {
	service, client, sessionStore, publisher := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	target := client.User.Create().SetUsername("target").SetRole(user.RoleUser).SaveX(ctx)
	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, target.ID).Return(helper.SessionRevokeSnapshot{}, errors.New("redis unavailable"))

	err := service.BanUser(ctx, uuid.New(), model.BanUserRequest{TargetUserID: target.ID, Reason: "abuse"})

	require.Error(t, err)
	publisher.AssertNotCalled(t, "BroadcastToUser", mock.Anything, mock.Anything)
	updated := client.User.GetX(ctx, target.ID)
	require.False(t, updated.IsBanned)
}

func TestAdminServiceUnbanUserUpdatesBannedUserAndBroadcasts(t *testing.T) {
	service, client, _, publisher := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	adminID := uuid.New()
	banReason := "abuse"
	bannedUntil := time.Now().UTC().Add(time.Hour)
	target := client.User.Create().SetUsername("target").SetRole(user.RoleUser).SetIsBanned(true).SetBanReason(banReason).SetBannedUntil(bannedUntil).SaveX(ctx)

	publisher.EXPECT().BroadcastToUser(target.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventUserUnbanned
	})).Return()
	publisher.EXPECT().BroadcastToContacts(target.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventUserUnbanned
	})).Return()

	err := service.UnbanUser(ctx, adminID, target.ID)

	require.NoError(t, err)
	updated := client.User.GetX(ctx, target.ID)
	require.False(t, updated.IsBanned)
	require.Nil(t, updated.BanReason)
	require.Nil(t, updated.BannedUntil)
}

func TestAdminServiceUnbanUserIsNoOpForActiveUser(t *testing.T) {
	service, client, _, publisher := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	target := client.User.Create().SetUsername("target").SetRole(user.RoleUser).SaveX(ctx)

	require.NoError(t, service.UnbanUser(ctx, uuid.New(), target.ID))
	publisher.AssertNotCalled(t, "BroadcastToUser", mock.Anything, mock.Anything)
}

func TestAdminServiceUnbanUserRejectsNotFoundUser(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()

	require.Error(t, service.UnbanUser(context.Background(), uuid.New(), uuid.New()))
}

func TestAdminServiceGetReportsMapsReporterAndPagination(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	reporter := client.User.Create().SetUsername("reporter").SetFullName("Reporter Name").SaveX(ctx)
	target := client.User.Create().SetUsername("target").SaveX(ctx)
	client.Report.Create().
		SetTargetType(report.TargetTypeUser).
		SetReason("spam").
		SetReporterID(reporter.ID).
		SetTargetUserID(target.ID).
		SaveX(ctx)

	results, cursor, hasNext, err := service.GetReports(ctx, model.GetReportsRequest{Limit: 10})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Empty(t, cursor)
	require.False(t, hasNext)
	require.Equal(t, "Reporter Name", results[0].ReporterName)
	require.Equal(t, "spam", results[0].Reason)
}

func TestAdminServiceGetReportsUsesDeletedReporterFallback(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	reporter := client.User.Create().SetUsername("deleted").SetFullName("Deleted Name").SaveX(ctx)
	target := client.User.Create().SetUsername("target").SaveX(ctx)
	client.User.UpdateOneID(reporter.ID).SetDeletedAt(time.Now().UTC()).SaveX(ctx)
	client.Report.Create().
		SetTargetType(report.TargetTypeUser).
		SetReason("abuse").
		SetReporterID(reporter.ID).
		SetTargetUserID(target.ID).
		SaveX(ctx)

	results, _, _, err := service.GetReports(ctx, model.GetReportsRequest{Limit: 10})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "Deleted Name", results[0].ReporterName)
}

func TestAdminServiceGetReportsFiltersStatusAndPaginates(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	first := client.Report.Create().
		SetTargetType(report.TargetTypeUser).
		SetReason("spam first").
		SetStatus(report.StatusPending).
		SaveX(ctx)
	second := client.Report.Create().
		SetTargetType(report.TargetTypeUser).
		SetReason("spam second").
		SetStatus(report.StatusPending).
		SaveX(ctx)
	client.Report.Create().
		SetTargetType(report.TargetTypeUser).
		SetReason("resolved").
		SetStatus(report.StatusResolved).
		SaveX(ctx)

	results, cursor, hasNext, err := service.GetReports(ctx, model.GetReportsRequest{
		Status: report.StatusPending.String(),
		Query:  "spam",
		Limit:  1,
	})

	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, second.ID, results[0].ID)
	require.NotEmpty(t, cursor)
	require.True(t, hasNext)

	next, nextCursor, nextHasNext, err := service.GetReports(ctx, model.GetReportsRequest{
		Status: report.StatusPending.String(),
		Cursor: cursor,
		Limit:  1,
	})

	require.NoError(t, err)
	require.Len(t, next, 1)
	require.Equal(t, first.ID, next[0].ID)
	require.Empty(t, nextCursor)
	require.False(t, nextHasNext)
}

func TestAdminServiceGetReportsRejectsInvalidRequest(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer client.Close()

	results, cursor, hasNext, err := service.GetReports(context.Background(), model.GetReportsRequest{Limit: 51})

	require.Nil(t, results)
	require.Empty(t, cursor)
	require.False(t, hasNext)
	require.Error(t, err)
}

func TestAdminServiceGetReportDetailRefreshesMessageEvidence(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	reporter := client.User.Create().SetUsername("reporter").SetFullName("Reporter").SaveX(ctx)
	sender := client.User.Create().SetUsername("sender").SetFullName("Sender").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChat(chatEntity).SetUser1ID(reporter.ID).SetUser2ID(sender.ID).SaveX(ctx)
	msg := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetContent("reported").SaveX(ctx)
	r := client.Report.Create().
		SetTargetType(report.TargetTypeMessage).
		SetReason("abuse").
		SetReporterID(reporter.ID).
		SetMessageID(msg.ID).
		SetEvidenceSnapshot(map[string]interface{}{
			"attachments": []interface{}{
				map[string]interface{}{"file_name": "evidence/photo.png"},
				"legacy.txt",
			},
		}).
		SaveX(ctx)
	storage.EXPECT().GetPresignedURL("evidence/photo.png", 15*time.Minute).Return("https://signed/photo", nil)
	storage.EXPECT().GetPresignedURL("legacy.txt", 15*time.Minute).Return("https://signed/legacy", nil)

	result, err := service.GetReportDetail(ctx, r.ID)

	require.NoError(t, err)
	require.Equal(t, r.ID, result.ID)
	require.Equal(t, msg.ID, *result.TargetID)
	require.False(t, result.TargetIsDeleted)
	require.Equal(t, "Reporter", result.ReporterName)
	attachments, ok := result.EvidenceSnapshot["attachments"].([]interface{})
	require.True(t, ok)
	require.Equal(t, map[string]interface{}{"file_name": "evidence/photo.png", "url": "https://signed/photo"}, attachments[0])
	require.Equal(t, "https://signed/legacy", attachments[1])
}

func TestAdminServiceGetReportDetailFallsBackWhenAttachmentSigningFails(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	reporter := client.User.Create().SetUsername("reporter").SaveX(ctx)
	target := client.User.Create().SetUsername("target").SaveX(ctx)
	r := client.Report.Create().
		SetTargetType(report.TargetTypeUser).
		SetReason("abuse").
		SetReporterID(reporter.ID).
		SetTargetUserID(target.ID).
		SaveX(ctx)

	result, err := service.GetReportDetail(ctx, r.ID)

	require.NoError(t, err)
	require.Equal(t, target.ID, *result.TargetID)
	require.False(t, result.TargetIsDeleted)
	storage.AssertNotCalled(t, "GetPresignedURL", mock.Anything, mock.Anything)
}

func TestAdminServiceGetReportDetailReturnsNotFound(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()

	result, err := service.GetReportDetail(context.Background(), uuid.New())

	require.Nil(t, result)
	require.Error(t, err)
}

func TestAdminServiceGetReportDetailMapsBannedReporterAndGroupTarget(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	bannedUntil := time.Now().UTC().Add(time.Hour)
	reporter := client.User.Create().SetUsername("reporter").SetFullName("Reporter").SetIsBanned(true).SetBannedUntil(bannedUntil).SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Reported Group").SetInviteCode("reported-group").SaveX(ctx)
	r := client.Report.Create().
		SetTargetType(report.TargetTypeGroup).
		SetReason("abuse").
		SetReporterID(reporter.ID).
		SetGroupID(group.ID).
		SaveX(ctx)

	result, err := service.GetReportDetail(ctx, r.ID)

	require.NoError(t, err)
	require.Equal(t, group.ID, *result.TargetID)
	require.False(t, result.TargetIsDeleted)
	require.True(t, result.ReporterIsBanned)
	require.False(t, result.ReporterIsDeleted)
}

func TestAdminServiceResolveReportUpdatesResolution(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	reporter := client.User.Create().SetUsername("reporter").SaveX(ctx)
	target := client.User.Create().SetUsername("target").SaveX(ctx)
	admin := client.User.Create().SetUsername("admin").SetRole(user.RoleAdmin).SaveX(ctx)
	r := client.Report.Create().
		SetTargetType(report.TargetTypeUser).
		SetReason("abuse").
		SetReporterID(reporter.ID).
		SetTargetUserID(target.ID).
		SaveX(ctx)
	adminID := admin.ID

	err := service.ResolveReport(ctx, adminID, r.ID, model.ResolveReportRequest{
		Status: report.StatusRejected.String(),
		Notes:  "  reviewed and rejected  ",
	})

	require.NoError(t, err)
	updated := client.Report.GetX(ctx, r.ID)
	require.Equal(t, report.StatusRejected, updated.Status)
	require.Equal(t, "reviewed and rejected", *updated.ResolutionNotes)
	require.Equal(t, adminID, *updated.ResolvedByID)
	require.NotNil(t, updated.ResolvedAt)
}

func TestAdminServiceResolveReportRejectsInvalidOrMissingReport(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	err := service.ResolveReport(ctx, uuid.New(), uuid.New(), model.ResolveReportRequest{Status: "pending"})
	require.Error(t, err)

	err = service.ResolveReport(ctx, uuid.New(), uuid.New(), model.ResolveReportRequest{Status: report.StatusResolved.String()})
	require.Error(t, err)
}

func TestAdminServiceResolveReportDeletesActiveMessageWhenResolved(t *testing.T) {
	service, client, _, publisher := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	reporter := client.User.Create().SetUsername("reporter").SaveX(ctx)
	sender := client.User.Create().SetUsername("sender").SaveX(ctx)
	admin := client.User.Create().SetUsername("admin").SetRole(user.RoleAdmin).SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChat(chatEntity).SetUser1ID(reporter.ID).SetUser2ID(sender.ID).SaveX(ctx)
	msg := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetContent("reported").SaveX(ctx)
	r := client.Report.Create().
		SetTargetType(report.TargetTypeMessage).
		SetReason("abuse").
		SetReporterID(reporter.ID).
		SetMessageID(msg.ID).
		SaveX(ctx)
	deleted := make(chan struct{})
	publisher.EXPECT().BroadcastToChat(chatEntity.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventMessageDelete
	})).Run(func(uuid.UUID, websocket.Event) { close(deleted) }).Return()

	err := service.ResolveReport(ctx, admin.ID, r.ID, model.ResolveReportRequest{Status: report.StatusResolved.String()})
	require.NoError(t, err)
	select {
	case <-deleted:
	case <-time.After(time.Second):
		t.Fatal("message deletion event was not published")
	}

	updatedMessage := client.Message.GetX(ctx, msg.ID)
	require.Nil(t, updatedMessage.Content)
	require.NotNil(t, updatedMessage.DeletedAt)
}

func TestAdminServiceResolveReportNoOpsWhenStatusUnchanged(t *testing.T) {
	service, client, _, publisher := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	r := client.Report.Create().
		SetTargetType(report.TargetTypeUser).
		SetReason("abuse").
		SetStatus(report.StatusRejected).
		SaveX(ctx)

	require.NoError(t, service.ResolveReport(ctx, uuid.New(), r.ID, model.ResolveReportRequest{Status: report.StatusRejected.String()}))
	publisher.AssertNotCalled(t, "BroadcastToChat", mock.Anything, mock.Anything)
	updated := client.Report.GetX(ctx, r.ID)
	require.Nil(t, updated.ResolvedAt)
}

func TestAdminServiceDeleteReportOnlyDeletesTerminalReports(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	pending := client.Report.Create().SetTargetType(report.TargetTypeUser).SetReason("pending").SaveX(ctx)
	resolved := client.Report.Create().SetTargetType(report.TargetTypeUser).SetReason("resolved").SetStatus(report.StatusResolved).SaveX(ctx)
	rejected := client.Report.Create().SetTargetType(report.TargetTypeUser).SetReason("rejected").SetStatus(report.StatusRejected).SaveX(ctx)

	require.Error(t, service.DeleteReport(ctx, pending.ID))
	require.NoError(t, service.DeleteReport(ctx, resolved.ID))
	require.NoError(t, service.DeleteReport(ctx, rejected.ID))
	_, err := client.Report.Get(ctx, resolved.ID)
	require.True(t, ent.IsNotFound(err))
	_, err = client.Report.Get(ctx, rejected.ID)
	require.True(t, ent.IsNotFound(err))
}

func TestAdminServiceDeleteReportReturnsNotFound(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()

	require.Error(t, service.DeleteReport(context.Background(), uuid.New()))
}

func TestAdminServiceGetDashboardStatsCountsEntities(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	client.User.Create().SetUsername("one").SaveX(ctx)
	client.User.Create().SetUsername("two").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("group").SetInviteCode("dashboard-group").SaveX(ctx)
	client.Message.Create().SetChatID(chatEntity.ID).SetContent("hello").SaveX(ctx)
	client.Report.Create().SetTargetType(report.TargetTypeUser).SetReason("pending").SaveX(ctx)
	client.Report.Create().SetTargetType(report.TargetTypeUser).SetReason("resolved").SetStatus(report.StatusResolved).SaveX(ctx)

	stats, err := service.GetDashboardStats(ctx)
	require.NoError(t, err)
	require.NotNil(t, stats)
	require.Equal(t, 2, stats.TotalUsers)
	require.Equal(t, 1, stats.TotalGroups)
	require.Equal(t, 1, stats.TotalMessages)
	require.Equal(t, 1, stats.ActiveReports)
}

func TestAdminServiceResetUserInfoWithLastSeenAndAvatar(t *testing.T) {
	service, client, _, publisher := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	now := time.Now().UTC()
	avatar := client.Media.Create().
		SetFileName("reset-user-avatar.png").
		SetOriginalName("orig.png").
		SetFileSize(100).
		SetMimeType("image/png").
		SetCategory(media.CategoryUserAvatar).
		SaveX(ctx)

	target := client.User.Create().
		SetUsername("reset-me-target").
		SetFullName("Old Target Name").
		SetBio("Old Bio").
		SetAvatar(avatar).
		SetLastSeenAt(now).
		SaveX(ctx)

	userBroadcastDone := make(chan struct{}, 1)
	publisher.EXPECT().BroadcastToUser(target.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventUserUpdate
	})).Run(func(uuid.UUID, websocket.Event) {
		userBroadcastDone <- struct{}{}
	}).Once()

	contactsBroadcastDone := make(chan struct{}, 1)
	publisher.EXPECT().BroadcastToContacts(target.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventUserUpdate
	})).Run(func(uuid.UUID, websocket.Event) {
		contactsBroadcastDone <- struct{}{}
	}).Once()

	err := service.ResetUserInfo(ctx, model.ResetUserInfoRequest{
		TargetUserID: target.ID,
		ResetName:    true,
		ResetBio:     true,
		ResetAvatar:  true,
	})
	require.NoError(t, err)

	select {
	case <-userBroadcastDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for user update broadcast")
	}

	select {
	case <-contactsBroadcastDone:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for contacts update broadcast")
	}
}

func TestAdminServiceGetUserDetailWithNilFields(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	u := client.User.Create().SaveX(ctx)

	detail, err := service.GetUserDetail(ctx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, detail)
	require.Equal(t, "", detail.Username)
	require.Equal(t, "", *detail.Email)
	require.Equal(t, "", *detail.FullName)
	require.Equal(t, "", *detail.Bio)
	require.Nil(t, detail.BannedUntil)
	require.Nil(t, detail.LastSeenAt)
}

func TestAdminServiceGetGroupDetailWithCreatorWithoutFullName(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	creator := client.User.Create().SetUsername("creator-nofn").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(chatEntity.ID).
		SetName("No FN Group").
		SetCreatedBy(creator.ID).
		SetInviteCode("nofn-grp-code").
		SaveX(ctx)

	detail, err := service.GetGroupDetail(ctx, chatEntity.ID)
	require.NoError(t, err)
	require.NotNil(t, detail)
	require.Equal(t, group.ID, detail.ID)
	require.Equal(t, &creator.ID, detail.CreatorID)
	require.Nil(t, detail.CreatorName)
	require.Equal(t, "", detail.Avatar)
}

func TestAdminServiceGetUsersWithNilFieldsAndPermanentBan(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	client.User.Create().
		SetIsBanned(true).
		SetBanReason("perm ban").
		SaveX(ctx)

	users, _, _, err := service.GetUsers(ctx, model.AdminGetUserListRequest{Limit: 10})
	require.NoError(t, err)
	require.NotEmpty(t, users)
	require.True(t, users[0].IsBanned)
	require.Equal(t, "", users[0].Username)
	require.Equal(t, "", *users[0].Email)
	require.Equal(t, "", *users[0].FullName)
}

func TestAdminServiceGetReportDetailWithPermanentlyBannedSenderAndUser(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	reporter := client.User.Create().SetUsername("rep-pban-test").SaveX(ctx)
	targetUser := client.User.Create().SetUsername("perm-banned-user").SetIsBanned(true).SaveX(ctx)

	rUser := client.Report.Create().
		SetReporterID(reporter.ID).
		SetTargetType(report.TargetTypeUser).
		SetTargetUserID(targetUser.ID).
		SetReason("spam").
		SetStatus(report.StatusPending).
		SaveX(ctx)

	detailUser, err := service.GetReportDetail(ctx, rUser.ID)
	require.NoError(t, err)
	require.NotNil(t, detailUser)
	require.True(t, detailUser.TargetIsBanned)
	require.False(t, detailUser.TargetIsDeleted)

	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	msg := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(targetUser.ID).SetContent("perm msg").SaveX(ctx)

	rMsg := client.Report.Create().
		SetReporterID(reporter.ID).
		SetTargetType(report.TargetTypeMessage).
		SetMessageID(msg.ID).
		SetReason("spam message").
		SetStatus(report.StatusPending).
		SaveX(ctx)

	detailMsg, err := service.GetReportDetail(ctx, rMsg.ID)
	require.NoError(t, err)
	require.NotNil(t, detailMsg)
	require.True(t, detailMsg.TargetIsBanned)
	require.False(t, detailMsg.TargetIsDeleted)
}

func TestAdminServiceGetDashboardStatsReturnsErrorOnCancelledContext(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := service.GetDashboardStats(ctx)
	require.Error(t, err)
}

func TestAdminServiceGetUsersFiltersMapsAndPaginates(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	now := time.Now().UTC()
	first := client.User.Create().SetUsername("alpha").SetEmail("alpha@example.com").SetFullName("Alpha").SetRole(user.RoleUser).SetCreatedAt(now).SaveX(ctx)
	client.User.Create().SetUsername("admin").SetEmail("admin@example.com").SetRole(user.RoleAdmin).SetCreatedAt(now.Add(time.Second)).SaveX(ctx)
	client.User.Create().SetUsername("expired").SetEmail("expired@example.com").SetIsBanned(true).SetBannedUntil(now.Add(-time.Hour)).SetCreatedAt(now.Add(2 * time.Second)).SaveX(ctx)
	client.User.Create().SetUsername("other").SetEmail("other@example.com").SetCreatedAt(now.Add(3 * time.Second)).SaveX(ctx)

	users, cursor, hasNext, err := service.GetUsers(ctx, model.AdminGetUserListRequest{Query: "example.com", Role: "user", Limit: 2})

	require.NoError(t, err)
	require.Len(t, users, 2)
	require.True(t, hasNext)
	require.NotEmpty(t, cursor)
	require.Equal(t, "user", users[0].Role)
	require.False(t, users[0].IsBanned)
	require.NotNil(t, first)
}

func TestAdminServiceGetUsersRejectsInvalidRequest(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()

	_, _, _, err := service.GetUsers(context.Background(), model.AdminGetUserListRequest{Limit: 51})
	require.Error(t, err)
}

func TestAdminServiceGetUsersIgnoresInvalidCursorAndDefaultsLimit(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	client.User.Create().SetUsername("user").SetEmail("user@example.com").SaveX(ctx)

	users, cursor, hasNext, err := service.GetUsers(ctx, model.AdminGetUserListRequest{Cursor: "not-a-valid-cursor"})

	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Empty(t, cursor)
	require.False(t, hasNext)
}

func TestAdminServiceGetUserDetailMapsCountsAvatarAndExpiredBan(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	target := client.User.Create().SetUsername("detail").SetEmail("detail@example.com").SetFullName("Detail").SetBio("bio").SetIsBanned(true).SetBannedUntil(time.Now().UTC().Add(-time.Hour)).SaveX(ctx)
	avatar := client.Media.Create().SetFileName("avatars/detail.png").SetOriginalName("detail.png").SetFileSize(10).SetMimeType("image/png").SetCategory(media.CategoryUserAvatar).SaveX(ctx)
	client.User.UpdateOneID(target.ID).SetAvatarID(avatar.ID).SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("group").SetInviteCode("detail-group").SaveX(ctx)
	member := client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(target.ID).SetRole(groupmember.RoleMember).SaveX(ctx)
	_ = member
	client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(target.ID).SetContent("hello").SaveX(ctx)
	storage.EXPECT().GetPublicURL(avatar.FileName).Return("https://cdn/detail.png")

	detail, err := service.GetUserDetail(ctx, target.ID)

	require.NoError(t, err)
	require.Equal(t, target.ID, detail.ID)
	require.Equal(t, "https://cdn/detail.png", detail.Avatar)
	require.Equal(t, 1, detail.TotalMessages)
	require.Equal(t, 1, detail.TotalGroups)
	require.False(t, detail.IsBanned)
}

func TestAdminServiceGetUserDetailReturnsNotFound(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()

	detail, err := service.GetUserDetail(context.Background(), uuid.New())
	require.Nil(t, detail)
	require.Error(t, err)
}

func TestAdminServiceGetUserDetailKeepsActiveBan(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	target := client.User.Create().SetUsername("banned").SetIsBanned(true).SetBannedUntil(time.Now().UTC().Add(time.Hour)).SaveX(ctx)

	detail, err := service.GetUserDetail(ctx, target.ID)

	require.NoError(t, err)
	require.True(t, detail.IsBanned)
}

func TestAdminServiceResetUserInfoClearsSelectedFieldsAndBroadcasts(t *testing.T) {
	service, client, _, publisher := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	target := client.User.Create().SetUsername("reset").SetFullName("Old Name").SetBio("old bio").SaveX(ctx)
	publisher.EXPECT().BroadcastToUser(target.ID, mock.MatchedBy(func(event websocket.Event) bool { return event.Type == websocket.EventUserUpdate })).Return()
	publisher.EXPECT().BroadcastToContacts(target.ID, mock.MatchedBy(func(event websocket.Event) bool { return event.Type == websocket.EventUserUpdate })).Return()

	err := service.ResetUserInfo(ctx, model.ResetUserInfoRequest{TargetUserID: target.ID, ResetBio: true, ResetName: true})

	require.NoError(t, err)
	updated := client.User.GetX(ctx, target.ID)
	require.Nil(t, updated.Bio)
	require.Equal(t, "User "+target.ID.String()[:8], *updated.FullName)
}

func TestAdminServiceResetUserInfoRejectsAdminAndNoOpsWithoutChanges(t *testing.T) {
	service, client, _, publisher := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	admin := client.User.Create().SetUsername("admin").SetRole(user.RoleAdmin).SaveX(ctx)
	require.Error(t, service.ResetUserInfo(ctx, model.ResetUserInfoRequest{TargetUserID: admin.ID, ResetName: true}))
	regular := client.User.Create().SetUsername("regular").SaveX(ctx)
	client.User.UpdateOneID(regular.ID).SetFullName("User " + regular.ID.String()[:8]).SaveX(ctx)
	require.NoError(t, service.ResetUserInfo(ctx, model.ResetUserInfoRequest{TargetUserID: regular.ID, ResetName: true}))
	publisher.AssertNotCalled(t, "BroadcastToUser", mock.Anything, mock.Anything)
}

func TestAdminServiceGetGroupsUsesAggregateCounts(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("group").SetInviteCode("groups-list").SaveX(ctx)
	memberReader := service.groupMemberRepo.(*repositorymocks.MockGroupMemberReader)
	memberReader.EXPECT().CountActiveMembersByGroupIDs(mock.Anything, group.ID).Return(map[uuid.UUID]int{group.ID: 7}, nil)

	groups, _, hasNext, err := service.GetGroups(ctx, model.AdminGetGroupListRequest{Limit: 10})

	require.NoError(t, err)
	require.False(t, hasNext)
	require.Len(t, groups, 1)
	require.Equal(t, 7, groups[0].MemberCount)
	require.Equal(t, chatEntity.ID, groups[0].ChatID)
}

func TestAdminServiceGetGroupsReturnsCountError(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("group").SetInviteCode("groups-error").SaveX(ctx)
	memberReader := service.groupMemberRepo.(*repositorymocks.MockGroupMemberReader)
	memberReader.EXPECT().CountActiveMembersByGroupIDs(mock.Anything, group.ID).Return(nil, errors.New("count failed"))

	groups, _, _, err := service.GetGroups(ctx, model.AdminGetGroupListRequest{Limit: 10})

	require.Nil(t, groups)
	require.Error(t, err)
}

func TestAdminServiceGetGroupsFiltersAndPaginates(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	firstChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	first := client.GroupChat.Create().SetChatID(firstChat.ID).SetName("alpha").SetInviteCode("groups-alpha").SaveX(ctx)
	secondChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	second := client.GroupChat.Create().SetChatID(secondChat.ID).SetName("alpha second").SetInviteCode("groups-second").SaveX(ctx)
	memberReader := service.groupMemberRepo.(*repositorymocks.MockGroupMemberReader)
	memberReader.On("CountActiveMembersByGroupIDs", mock.Anything, mock.Anything).Return(map[uuid.UUID]int{}, nil).Twice()

	groups, cursor, hasNext, err := service.GetGroups(ctx, model.AdminGetGroupListRequest{Query: "alpha", Limit: 1})

	require.NoError(t, err)
	require.Len(t, groups, 1)
	require.NotEmpty(t, cursor)
	require.True(t, hasNext)

	next, nextCursor, nextHasNext, err := service.GetGroups(ctx, model.AdminGetGroupListRequest{Query: "alpha", Cursor: cursor, Limit: 1})

	require.NoError(t, err)
	require.Len(t, next, 1)
	require.ElementsMatch(t, []uuid.UUID{first.ID, second.ID}, []uuid.UUID{groups[0].ID, next[0].ID})
	require.Empty(t, nextCursor)
	require.False(t, nextHasNext)
}

func TestAdminServiceGetGroupDetailMapsCreatorAvatarAndCounts(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	creator := client.User.Create().SetUsername("creator").SetFullName("Creator").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	avatar := client.Media.Create().SetFileName("groups/detail.png").SetOriginalName("detail.png").SetFileSize(10).SetMimeType("image/png").SetCategory(media.CategoryGroupAvatar).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetCreatedBy(creator.ID).SetName("group").SetDescription("description").SetInviteCode("group-detail").SetAvatarID(avatar.ID).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(creator.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(creator.ID).SetContent("hello").SaveX(ctx)
	storage.EXPECT().GetPublicURL(avatar.FileName).Return("https://cdn/group.png")

	detail, err := service.GetGroupDetail(ctx, chatEntity.ID)

	require.NoError(t, err)
	require.Equal(t, group.ID, detail.ID)
	require.Equal(t, chatEntity.ID, detail.ChatID)
	require.Equal(t, "https://cdn/group.png", detail.Avatar)
	require.Equal(t, "Creator", *detail.CreatorName)
	require.Equal(t, 1, detail.MemberCount)
	require.Equal(t, 1, detail.TotalMessages)
}

func TestAdminServiceGetGroupDetailReturnsNotFound(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()

	detail, err := service.GetGroupDetail(context.Background(), uuid.New())
	require.Nil(t, detail)
	require.Error(t, err)
}

func TestAdminServiceGetUserDetailMapsAvatarAndCounts(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)

	avatar := client.Media.Create().SetFileName("users/avatar.png").SetOriginalName("avatar.png").SetFileSize(10).SetMimeType("image/png").SetCategory(media.CategoryUserAvatar).SaveX(ctx)
	target := client.User.Create().
		SetUsername("detailed-user").
		SetEmail("detailed@example.com").
		SetFullName("Detailed User").
		SetBio("User bio").
		SetAvatarID(avatar.ID).
		SaveX(ctx)

	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("User Group").SetInviteCode("user-group").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(target.ID).SetRole(groupmember.RoleMember).SaveX(ctx)
	client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(target.ID).SetContent("hello world").SaveX(ctx)

	storage.EXPECT().GetPublicURL(avatar.FileName).Return("https://cdn/avatar.png")

	resp, err := service.GetUserDetail(ctx, target.ID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, target.ID, resp.ID)
	require.Equal(t, "detailed-user", resp.Username)
	require.Equal(t, "detailed@example.com", *resp.Email)
	require.Equal(t, "Detailed User", *resp.FullName)
	require.Equal(t, "User bio", *resp.Bio)
	require.Equal(t, "https://cdn/avatar.png", resp.Avatar)
	require.Equal(t, 1, resp.TotalMessages)
	require.Equal(t, 1, resp.TotalGroups)
}

func TestAdminServiceGetDashboardStatsCountsAllEntities(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("u1").SaveX(ctx)
	u2 := client.User.Create().SetUsername("u2").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Group 1").SetInviteCode("dash-group").SaveX(ctx)
	client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(u1.ID).SetContent("message 1").SaveX(ctx)
	client.Report.Create().SetTargetType(report.TargetTypeUser).SetReason("spam").SetReporterID(u1.ID).SetTargetUserID(u2.ID).SetStatus(report.StatusPending).SaveX(ctx)

	stats, err := service.GetDashboardStats(ctx)
	require.NoError(t, err)
	require.NotNil(t, stats)
	require.Equal(t, 2, stats.TotalUsers)
	require.Equal(t, 1, stats.TotalGroups)
	require.Equal(t, 1, stats.TotalMessages)
	require.Equal(t, 1, stats.ActiveReports)
}

func TestAdminServiceBanUserRejectsBanningAnotherAdmin(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	admin1 := client.User.Create().SetUsername("admin1").SetRole(user.RoleAdmin).SaveX(ctx)
	admin2 := client.User.Create().SetUsername("admin2").SetRole(user.RoleAdmin).SaveX(ctx)

	err := service.BanUser(ctx, admin1.ID, model.BanUserRequest{
		TargetUserID: admin2.ID,
		Reason:       "violation",
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "Cannot ban another admin")
}

func TestAdminServiceBanUserSetsTemporaryBan(t *testing.T) {
	service, client, sessionStore, publisher := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	admin := client.User.Create().SetUsername("admin-temp").SetRole(user.RoleAdmin).SaveX(ctx)
	target := client.User.Create().SetUsername("target-temp").SetRole(user.RoleUser).SaveX(ctx)

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, target.ID).Return(helper.SessionRevokeSnapshot{Exists: true, Value: "marker", TTL: time.Hour}, nil)
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, target.ID, mock.Anything).Return("marker", nil)
	publisher.EXPECT().BroadcastToUser(target.ID, mock.MatchedBy(func(e websocket.Event) bool {
		return e.Type == websocket.EventUserBanned
	})).Return()
	publisher.EXPECT().BroadcastToContacts(target.ID, mock.MatchedBy(func(e websocket.Event) bool {
		return e.Type == websocket.EventUserBanned
	})).Return()
	publisher.EXPECT().DisconnectUser(target.ID).Return()

	err := service.BanUser(ctx, admin.ID, model.BanUserRequest{
		TargetUserID:  target.ID,
		Reason:        "temporary issue",
		DurationHours: 24,
	})
	require.NoError(t, err)

	updated := client.User.GetX(ctx, target.ID)
	require.True(t, updated.IsBanned)
	require.NotNil(t, updated.BannedUntil)
	require.Equal(t, "temporary issue", *updated.BanReason)

	err = service.BanUser(ctx, admin.ID, model.BanUserRequest{
		TargetUserID:  target.ID,
		Reason:        "temporary issue",
		DurationHours: 24,
	})
	require.NoError(t, err)
}

func TestAdminServiceUnbanUserReturnsEarlyWhenNotBanned(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	admin := client.User.Create().SetUsername("admin-unban-early").SetRole(user.RoleAdmin).SaveX(ctx)
	target := client.User.Create().SetUsername("target-unban-early").SetRole(user.RoleUser).SetIsBanned(false).SaveX(ctx)

	err := service.UnbanUser(ctx, admin.ID, target.ID)
	require.NoError(t, err)
}

func TestAdminServiceGetGroupDetailWithCreatorAndAvatar(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)

	creator := client.User.Create().SetUsername("group-creator").SetFullName("Group Creator").SaveX(ctx)
	avatar := client.Media.Create().
		SetUploadedByID(creator.ID).
		SetFileName("avatars/group.png").
		SetOriginalName("group.png").
		SetMimeType("image/png").
		SetCategory(media.CategoryGroupAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetFileSize(1024).
		SaveX(ctx)

	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(chatEntity.ID).
		SetName("Group with Avatar").
		SetInviteCode("avatar-group").
		SetCreatedBy(creator.ID).
		SetAvatarID(avatar.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(creator.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(creator.ID).SetContent("first msg").SaveX(ctx)

	storage.EXPECT().GetPublicURL(avatar.FileName).Return("https://cdn/group-avatar.png")

	resp, err := service.GetGroupDetail(ctx, group.ChatID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Equal(t, group.ID, resp.ID)
	require.Equal(t, "Group with Avatar", resp.Name)
	require.Equal(t, "Group Creator", *resp.CreatorName)
	require.Equal(t, creator.ID, *resp.CreatorID)
	require.Equal(t, "https://cdn/group-avatar.png", resp.Avatar)
	require.Equal(t, 1, resp.MemberCount)
	require.Equal(t, 1, resp.TotalMessages)
}

func TestAdminServiceGetUserDetailWithLastSeenAt(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	lastSeen := time.Now().Add(-10 * time.Minute)
	u := client.User.Create().
		SetUsername("lastseen-user").
		SetFullName("Last Seen User").
		SetLastSeenAt(lastSeen).
		SaveX(ctx)

	resp, err := service.GetUserDetail(ctx, u.ID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.NotNil(t, resp.LastSeenAt)
	require.Equal(t, "lastseen-user", resp.Username)
}

func TestAdminServiceGetGroupDetailWithMissingCreator(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(chatEntity.ID).
		SetName("No Creator Group").
		SetInviteCode("nocreatorcode").
		SaveX(ctx)

	resp, err := service.GetGroupDetail(ctx, group.ChatID)
	require.NoError(t, err)
	require.NotNil(t, resp)
	require.Nil(t, resp.CreatorID)
	require.Nil(t, resp.CreatorName)
}

func TestAdminServiceGetUsersWithSearchQueryAndRoleAdmin(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	client.User.Create().SetUsername("admin-search-1").SetRole(user.RoleAdmin).SetFullName("Admin One").SaveX(ctx)
	client.User.Create().SetUsername("admin-search-2").SetRole(user.RoleAdmin).SetFullName("Admin Two").SaveX(ctx)
	client.User.Create().SetUsername("user-search-1").SetRole(user.RoleUser).SetFullName("Normal User").SaveX(ctx)

	resp, nextCursor, hasMore, err := service.GetUsers(ctx, model.AdminGetUserListRequest{
		Role:  "admin",
		Query: "Admin",
		Limit: 10,
	})
	require.NoError(t, err)
	require.False(t, hasMore)
	require.Empty(t, nextCursor)
	require.Len(t, resp, 2)
}

func TestAdminServiceResolveReportOnAlreadyDeletedMessage(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	adminUser := client.User.Create().SetUsername("admin-resolve-del").SetRole(user.RoleAdmin).SaveX(ctx)
	reporter := client.User.Create().SetUsername("reporter-resolve-del").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	now := time.Now().UTC()
	deletedMsg := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetSenderID(adminUser.ID).
		SetContent("already deleted message").
		SetDeletedAt(now).
		SaveX(ctx)

	rep := client.Report.Create().
		SetReporter(reporter).
		SetTargetType(report.TargetTypeMessage).
		SetMessageID(deletedMsg.ID).
		SetReason("offensive content").
		SetStatus(report.StatusPending).
		SaveX(ctx)

	err := service.ResolveReport(ctx, adminUser.ID, rep.ID, model.ResolveReportRequest{
		Status: string(report.StatusResolved),
		Notes:  "resolved already deleted message",
	})
	require.NoError(t, err)

	updatedRep := client.Report.GetX(ctx, rep.ID)
	require.Equal(t, report.StatusResolved, updatedRep.Status)
	require.Equal(t, adminUser.ID, *updatedRep.ResolvedByID)
	require.Equal(t, "resolved already deleted message", *updatedRep.ResolutionNotes)
}

func TestAdminServiceDashboardStatsAndReportErrorBranches(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	adminUser := client.User.Create().SetUsername("dash-admin").SetRole(user.RoleAdmin).SaveX(ctx)
	client.User.Create().SetUsername("dash-user-1").SaveX(ctx)
	chat1 := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	client.GroupChat.Create().SetChatID(chat1.ID).SetName("Group 1").SetInviteCode("dash-g-1").SaveX(ctx)
	client.Message.Create().SetChatID(chat1.ID).SetSenderID(adminUser.ID).SetContent("dash msg").SaveX(ctx)
	client.Report.Create().SetReporter(adminUser).SetTargetType(report.TargetTypeUser).SetTargetUserID(adminUser.ID).SetReason("spam").SetStatus(report.StatusPending).SaveX(ctx)

	stats, err := service.GetDashboardStats(ctx)
	require.NoError(t, err)
	require.NotNil(t, stats)
	require.Equal(t, 2, stats.TotalUsers)
	require.Equal(t, 1, stats.TotalGroups)
	require.Equal(t, 1, stats.TotalMessages)
	require.Equal(t, 1, stats.ActiveReports)

	nonExistentID := uuid.New()
	err = service.ResolveReport(ctx, adminUser.ID, nonExistentID, model.ResolveReportRequest{
		Status: string(report.StatusResolved),
	})
	require.Error(t, err)

	err = service.DeleteReport(ctx, nonExistentID)
	require.Error(t, err)

	_, err = service.GetGroupDetail(ctx, nonExistentID)
	require.Error(t, err)
}

func TestAdminServiceResetUserInfoWithAvatarAndBroadcasts(t *testing.T) {
	service, client, _, publisher := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	targetUser := client.User.Create().SetUsername("reset-av-user").SetFullName("Custom Name").SetBio("Custom Bio").SaveX(ctx)
	avatar := client.Media.Create().
		SetUploadedByID(targetUser.ID).
		SetFileName("avatars/user.png").
		SetOriginalName("user.png").
		SetMimeType("image/png").
		SetCategory(media.CategoryUserAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetFileSize(500).
		SaveX(ctx)
	client.User.UpdateOneID(targetUser.ID).SetAvatar(avatar).SaveX(ctx)

	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	storage.EXPECT().GetPublicURL("avatars/user.png").Return("https://cdn.example.com/avatars/user.png").Maybe()

	events := make(chan struct{}, 2)
	publisher.EXPECT().BroadcastToUser(targetUser.ID, mock.Anything).Run(func(_ uuid.UUID, e websocket.Event) {
		if e.Type == websocket.EventUserUpdate {
			events <- struct{}{}
		}
	}).Once()
	publisher.EXPECT().BroadcastToContacts(targetUser.ID, mock.Anything).Run(func(_ uuid.UUID, e websocket.Event) {
		if e.Type == websocket.EventUserUpdate {
			events <- struct{}{}
		}
	}).Once()

	err := service.ResetUserInfo(ctx, model.ResetUserInfoRequest{
		TargetUserID: targetUser.ID,
		ResetAvatar:  true,
		ResetBio:     true,
		ResetName:    true,
	})
	require.NoError(t, err)

	for range 2 {
		select {
		case <-events:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for reset broadcasts")
		}
	}

	updated := client.User.GetX(ctx, targetUser.ID)
	require.Nil(t, updated.Bio)
	require.Equal(t, "User "+targetUser.ID.String()[:8], *updated.FullName)
}

func TestAdminServiceCancelledContextBranches(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	nonExistentID := uuid.New()
	_, err := service.GetDashboardStats(ctx)
	require.Error(t, err)

	_, _, _, err = service.GetUsers(ctx, model.AdminGetUserListRequest{Limit: 10})
	require.Error(t, err)

	_, err = service.GetUserDetail(ctx, nonExistentID)
	require.Error(t, err)

	_, _, _, err = service.GetGroups(ctx, model.AdminGetGroupListRequest{Limit: 10})
	require.Error(t, err)

	_, err = service.GetGroupDetail(ctx, nonExistentID)
	require.Error(t, err)

	err = service.DeleteReport(ctx, nonExistentID)
	require.Error(t, err)

	err = service.BanUser(ctx, nonExistentID, model.BanUserRequest{
		TargetUserID: nonExistentID,
		Reason:       "spam",
	})
	require.Error(t, err)

	err = service.UnbanUser(ctx, nonExistentID, nonExistentID)
	require.Error(t, err)

	err = service.ResetUserInfo(ctx, model.ResetUserInfoRequest{
		TargetUserID: nonExistentID,
	})
	require.Error(t, err)
}

func TestAdminServiceBanUserAlreadyPermanentlyBannedSameReason(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	adminID := uuid.New()
	target := client.User.Create().
		SetUsername("already-perm-banned").
		SetRole(user.RoleUser).
		SetIsBanned(true).
		SetBanReason("perm-reason").
		SaveX(ctx)

	err := service.BanUser(ctx, adminID, model.BanUserRequest{
		TargetUserID: target.ID,
		Reason:       "perm-reason",
	})
	require.NoError(t, err)
}

func TestAdminServiceGetReportDetailWithStringAttachmentsAndSenderBan(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	storage.EXPECT().GetPresignedURL("msg-att.png", 15*time.Minute).Return("https://cdn.example.com/msg-att.png", nil).Once()
	storage.EXPECT().GetPresignedURL("err-att.png", 15*time.Minute).Return("", errors.New("s3 err")).Once()

	bannedUntil := time.Now().Add(24 * time.Hour)
	sender := client.User.Create().SetUsername("banned-sender").SetIsBanned(true).SetBannedUntil(bannedUntil).SaveX(ctx)
	reporter := client.User.Create().SetUsername("reporter-user").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	msg := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetContent("bad msg").SaveX(ctx)

	r := client.Report.Create().
		SetReporterID(reporter.ID).
		SetTargetType(report.TargetTypeMessage).
		SetMessageID(msg.ID).
		SetReason("offensive").
		SetEvidenceSnapshot(map[string]interface{}{
			"attachments": []interface{}{"msg-att.png", "err-att.png"},
		}).
		SaveX(ctx)

	detail, err := service.GetReportDetail(ctx, r.ID)
	require.NoError(t, err)
	require.NotNil(t, detail)
	require.True(t, detail.TargetIsBanned)
	require.False(t, detail.TargetIsDeleted)
}

func TestAdminServiceGetGroupDetailWithAnonymousCreatorAndAvatar(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	storage := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	storage.EXPECT().GetPublicURL("group-avatar.png").Return("https://cdn.example.com/group-avatar.png").Once()

	avatar := client.Media.Create().
		SetFileName("group-avatar.png").
		SetOriginalName("avatar.png").
		SetFileSize(100).
		SetMimeType("image/png").
		SetCategory(media.CategoryGroupAvatar).
		SaveX(ctx)

	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(chatEntity.ID).
		SetName("Anonymous Group").
		SetAvatarID(avatar.ID).
		SetInviteCode("anon-grp-code").
		SaveX(ctx)

	detail, err := service.GetGroupDetail(ctx, chatEntity.ID)
	require.NoError(t, err)
	require.NotNil(t, detail)
	require.Equal(t, group.ID, detail.ID)
	require.Equal(t, "https://cdn.example.com/group-avatar.png", detail.Avatar)
	require.Nil(t, detail.CreatorID)
	require.Nil(t, detail.CreatorName)
}

func TestAdminServiceGetDashboardStatsSuccessWithPopulatedData(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("dash-user-1").SaveX(ctx)
	u2 := client.User.Create().SetUsername("dash-user-2").SaveX(ctx)

	c1 := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	client.GroupChat.Create().SetChatID(c1.ID).SetName("Dash Group").SetInviteCode("dash-code").SaveX(ctx)

	client.Message.Create().SetChatID(c1.ID).SetSenderID(u1.ID).SetContent("dash message").SaveX(ctx)

	client.Report.Create().
		SetReporterID(u1.ID).
		SetTargetType("user").
		SetTargetUserID(u2.ID).
		SetReason("spam").
		SetStatus(report.StatusPending).
		SaveX(ctx)

	stats, err := service.GetDashboardStats(ctx)
	require.NoError(t, err)
	require.NotNil(t, stats)
	require.Equal(t, 2, stats.TotalUsers)
	require.Equal(t, 1, stats.TotalGroups)
	require.Equal(t, 1, stats.TotalMessages)
	require.Equal(t, 1, stats.ActiveReports)
}

func TestAdminServiceResolveReportOnUserAndGroupReports(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	adminUser := client.User.Create().SetUsername("admin-res-usr").SetRole(user.RoleAdmin).SaveX(ctx)
	reporter := client.User.Create().SetUsername("rep-res-usr").SaveX(ctx)
	targetUser := client.User.Create().SetUsername("tgt-res-usr").SaveX(ctx)

	rUser := client.Report.Create().
		SetReporterID(reporter.ID).
		SetTargetType(report.TargetTypeUser).
		SetTargetUserID(targetUser.ID).
		SetReason("bad behavior").
		SetStatus(report.StatusPending).
		SaveX(ctx)

	err := service.ResolveReport(ctx, adminUser.ID, rUser.ID, model.ResolveReportRequest{
		Status: report.StatusResolved.String(),
		Notes:  "resolved user report",
	})
	require.NoError(t, err)

	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Res Group").SetInviteCode("res-grp-code").SaveX(ctx)

	rGroup := client.Report.Create().
		SetReporterID(reporter.ID).
		SetTargetType(report.TargetTypeGroup).
		SetGroupID(group.ID).
		SetReason("spam group").
		SetStatus(report.StatusPending).
		SaveX(ctx)

	err = service.ResolveReport(ctx, adminUser.ID, rGroup.ID, model.ResolveReportRequest{
		Status: report.StatusRejected.String(),
	})
	require.NoError(t, err)
}

func TestAdminServiceGetGroupsWithInvalidCursor(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	memberRepo := service.groupMemberRepo.(*repositorymocks.MockGroupMemberReader)
	ctx := context.Background()

	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Grp Inv Cur").SetInviteCode("grp-inv-cur").SaveX(ctx)

	memberRepo.EXPECT().CountActiveMembersByGroupIDs(mock.Anything, mock.Anything).Return(map[uuid.UUID]int{group.ID: 1}, nil).Maybe()

	groups, _, _, err := service.GetGroups(ctx, model.AdminGetGroupListRequest{
		Cursor: "invalid-base64!",
		Limit:  10,
	})
	require.NoError(t, err)
	require.NotEmpty(t, groups)

	badUUIDCursor := base64.URLEncoding.EncodeToString([]byte("not-a-uuid"))
	groups, _, _, err = service.GetGroups(ctx, model.AdminGetGroupListRequest{
		Cursor: badUUIDCursor,
		Limit:  10,
	})
	require.NoError(t, err)
	require.NotEmpty(t, groups)
}

func TestAdminServiceGetReportDetailWithDeletedMessage(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	reporter := client.User.Create().SetUsername("rep-del-msg").SaveX(ctx)
	sender := client.User.Create().SetUsername("snd-del-msg").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	now := time.Now().UTC()
	msg := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetSenderID(sender.ID).
		SetContent("deleted msg").
		SetDeletedAt(now).
		SaveX(ctx)

	r := client.Report.Create().
		SetReporterID(reporter.ID).
		SetTargetType(report.TargetTypeMessage).
		SetMessageID(msg.ID).
		SetReason("bad deleted msg").
		SetStatus(report.StatusPending).
		SaveX(ctx)

	detail, err := service.GetReportDetail(ctx, r.ID)
	require.NoError(t, err)
	require.NotNil(t, detail)
	require.True(t, detail.TargetIsDeleted)
}

func TestAdminServiceBanUserUpdatesExistingBanDurationAndReason(t *testing.T) {
	service, client, sessionStore, publisher := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	adminUser := client.User.Create().SetUsername("admin-ban-upd").SetRole(user.RoleAdmin).SaveX(ctx)
	initialUntil := time.Now().UTC().Add(24 * time.Hour)
	target := client.User.Create().
		SetUsername("tgt-ban-upd").
		SetIsBanned(true).
		SetBannedUntil(initialUntil).
		SetBanReason("old reason").
		SaveX(ctx)

	publisher.EXPECT().BroadcastToUser(target.ID, mock.Anything).Return().Maybe()
	publisher.EXPECT().BroadcastToContacts(target.ID, mock.Anything).Return().Maybe()
	publisher.EXPECT().DisconnectUser(target.ID).Return().Maybe()

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, target.ID).Return(helper.SessionRevokeSnapshot{}, nil).Once()
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, target.ID, mock.Anything).Return("marker", nil).Once()

	err := service.BanUser(ctx, adminUser.ID, model.BanUserRequest{
		TargetUserID:  target.ID,
		Reason:        "new perm reason",
		DurationHours: 0,
	})
	require.NoError(t, err)

	updated, err := client.User.Get(ctx, target.ID)
	require.NoError(t, err)
	require.True(t, updated.IsBanned)
	require.Nil(t, updated.BannedUntil)
	require.Equal(t, "new perm reason", *updated.BanReason)

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, target.ID).Return(helper.SessionRevokeSnapshot{}, nil).Once()
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, target.ID, mock.Anything).Return("marker", nil).Once()

	err = service.BanUser(ctx, adminUser.ID, model.BanUserRequest{
		TargetUserID:  target.ID,
		Reason:        "converted to temp",
		DurationHours: 48,
	})
	require.NoError(t, err)

	updatedTemp, err := client.User.Get(ctx, target.ID)
	require.NoError(t, err)
	require.True(t, updatedTemp.IsBanned)
	require.NotNil(t, updatedTemp.BannedUntil)
	require.Equal(t, "converted to temp", *updatedTemp.BanReason)
}

func TestAdminServiceGetDashboardStatsWithAllCounts(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("dash1").SetEmail("dash1@test.com").SaveX(ctx)
	u2 := client.User.Create().SetUsername("dash2").SetEmail("dash2@test.com").SaveX(ctx)

	pChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	client.GroupChat.Create().SetChatID(pChat.ID).SetName("Dash Group").SetInviteCode("dashcode").SaveX(ctx)

	client.Message.Create().SetChatID(pChat.ID).SetSenderID(u1.ID).SetContent("m1").SaveX(ctx)
	client.Message.Create().SetChatID(pChat.ID).SetSenderID(u2.ID).SetContent("m2").SaveX(ctx)

	client.Report.Create().SetReporterID(u1.ID).SetTargetType(report.TargetTypeUser).SetTargetUserID(u2.ID).SetReason("spam").SetStatus(report.StatusPending).SaveX(ctx)

	stats, err := service.GetDashboardStats(ctx)
	require.NoError(t, err)
	require.NotNil(t, stats)
	require.Equal(t, 2, stats.TotalUsers)
	require.Equal(t, 1, stats.TotalGroups)
	require.Equal(t, 2, stats.TotalMessages)
	require.Equal(t, 1, stats.ActiveReports)
}

func TestAdminServiceBanUserRevokeAllSessionsAtError(t *testing.T) {
	service, client, sessionStore, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	admin := client.User.Create().SetUsername("admin-ban-fail").SetRole(user.RoleAdmin).SaveX(ctx)
	target := client.User.Create().SetUsername("target-ban-fail").SaveX(ctx)

	sessionStore.EXPECT().SnapshotUserRevoke(mock.Anything, target.ID).Return(helper.SessionRevokeSnapshot{}, nil).Once()
	sessionStore.EXPECT().RevokeAllSessionsAt(mock.Anything, target.ID, mock.Anything).Return("", errors.New("redis error")).Once()

	err := service.BanUser(ctx, admin.ID, model.BanUserRequest{
		TargetUserID: target.ID,
		Reason:       "rule violation",
	})
	require.Error(t, err)
}

func TestAdminServiceGetGroupDetailWithFullDetails(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	creator := client.User.Create().SetUsername("grp-creator-full").SetFullName("Creator Full Name").SaveX(ctx)
	avatar := client.Media.Create().
		SetFileName("grp-avatar-full.png").
		SetOriginalName("grp-avatar-full.png").
		SetCategory(media.CategoryGroupAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetFileSize(100).
		SetMimeType("image/png").
		SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	desc := "A detailed group description"
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Full Detail Group").
		SetDescription(desc).
		SetIsPublic(true).
		SetInviteCode("full-code-123").
		SetCreatedBy(creator.ID).
		SetAvatar(avatar).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(creator.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.Message.Create().SetChatID(parentChat.ID).SetSenderID(creator.ID).SetContent("Hello group").SaveX(ctx)

	storageMock := service.storageAdapter.(*adaptermocks.MockURLGenerator)
	storageMock.EXPECT().GetPublicURL("grp-avatar-full.png").Return("https://cdn.example.com/grp-avatar-full.png").Once()

	detail, err := service.GetGroupDetail(ctx, parentChat.ID)
	require.NoError(t, err)
	require.NotNil(t, detail)
	require.Equal(t, "Full Detail Group", detail.Name)
	require.Equal(t, &desc, detail.Description)
	require.True(t, detail.IsPublic)
	require.Equal(t, 1, detail.MemberCount)
	require.Equal(t, 1, detail.TotalMessages)
	require.Equal(t, &creator.ID, detail.CreatorID)
	creatorName := "Creator Full Name"
	require.Equal(t, &creatorName, detail.CreatorName)
	avatarURL := "https://cdn.example.com/grp-avatar-full.png"
	require.Equal(t, avatarURL, detail.Avatar)
}

func TestAdminServiceGetGroupDetailWithoutAvatarAndCreatedByNil(t *testing.T) {
	service, client, _, _ := newAdminServiceTest(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("No Creator Group").
		SetInviteCode("no-creator-123").
		SetIsPublic(false).
		SaveX(ctx)

	detail, err := service.GetGroupDetail(ctx, parentChat.ID)
	require.NoError(t, err)
	require.NotNil(t, detail)
	require.Equal(t, group.ID, detail.ID)
	require.Nil(t, detail.CreatorID)
	require.Nil(t, detail.CreatorName)
	require.Empty(t, detail.Avatar)
}
