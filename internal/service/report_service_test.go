package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/report"
	adaptermocks "AtoiTalkAPI/internal/adapter/mocks"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/model"
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newReportService(t *testing.T) (*ReportService, *ent.Client) {
	t.Helper()
	client := enttest.Open(t, dialect.SQLite, "file:report-service-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	storage := adaptermocks.NewMockPublicURLGenerator(t)
	storage.EXPECT().GetPublicURL(mock.Anything).Return("https://cdn.example.test/file").Maybe()
	return NewReportService(client, &config.AppConfig{}, config.NewValidator(), storage), client
}

func TestReportServiceCreateReportForMessageStoresEvidence(t *testing.T) {
	service, client := newReportService(t)
	defer client.Close()
	ctx := context.Background()

	reporter := client.User.Create().SetUsername("reporter").SaveX(ctx)
	sender := client.User.Create().SetUsername("sender").SetFullName("Sender").SaveX(ctx)
	chat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChat(chat).SetUser1ID(reporter.ID).SetUser2ID(sender.ID).SaveX(ctx)
	msg := client.Message.Create().SetChatID(chat.ID).SetSenderID(sender.ID).SetContent("abusive content").SaveX(ctx)

	err := service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{
		TargetType: "message",
		Reason:     "abuse",
		MessageID:  &msg.ID,
	})
	require.NoError(t, err)

	created := client.Report.Query().Where(report.ReporterID(reporter.ID)).OnlyX(ctx)
	require.Equal(t, report.TargetTypeMessage, created.TargetType)
	require.Equal(t, "abuse", created.Reason)
	require.Equal(t, report.StatusPending, created.Status)
	require.Equal(t, msg.ID, *created.MessageID)
	require.Equal(t, "abusive content", created.EvidenceSnapshot["content"])
	require.Equal(t, string("private"), created.EvidenceSnapshot["chat_type"])
}

func TestReportServiceCreateReportRejectsInvalidMessageRequests(t *testing.T) {
	service, client := newReportService(t)
	defer client.Close()
	ctx := context.Background()
	reporter := client.User.Create().SetUsername("reporter").SaveX(ctx)

	err := service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{TargetType: "message", Reason: "missing"})
	require.Error(t, err)

	missingID := uuid.New()
	err = service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{
		TargetType: "message",
		Reason:     "missing",
		MessageID:  &missingID,
	})
	require.Error(t, err)
}

func TestReportServiceCreateReportRejectsOwnMessage(t *testing.T) {
	service, client := newReportService(t)
	defer client.Close()
	ctx := context.Background()
	user := client.User.Create().SetUsername("reporter").SaveX(ctx)
	otherUser := client.User.Create().SetUsername("other").SaveX(ctx)
	chat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChat(chat).SetUser1ID(user.ID).SetUser2ID(otherUser.ID).SaveX(ctx)
	msg := client.Message.Create().SetChatID(chat.ID).SetSenderID(user.ID).SetContent("own message").SaveX(ctx)

	err := service.CreateReport(ctx, user.ID, model.CreateReportRequest{
		TargetType: "message",
		Reason:     "abuse",
		MessageID:  &msg.ID,
	})
	require.Error(t, err)
}

func TestReportServiceCreateReportForGroupRequiresMembershipAndStoresEvidence(t *testing.T) {
	service, client := newReportService(t)
	defer client.Close()
	ctx := context.Background()
	reporter := client.User.Create().SetUsername("reporter").SaveX(ctx)
	other := client.User.Create().SetUsername("other").SaveX(ctx)
	chat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chat.ID).SetName("Group").SetInviteCode("invite-group").SetCreatedBy(other.ID).SaveX(ctx)

	err := service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{TargetType: "group", Reason: "abuse", ChatID: &chat.ID})
	require.Error(t, err)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(reporter.ID).SetRole(groupmember.RoleMember).SaveX(ctx)
	err = service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{TargetType: "group", Reason: " abuse ", Description: " details ", ChatID: &chat.ID})
	require.NoError(t, err)

	created := client.Report.Query().Where(report.ReporterID(reporter.ID), report.TargetTypeEQ(report.TargetTypeGroup)).OnlyX(ctx)
	require.Equal(t, "abuse", created.Reason)
	require.Equal(t, "details", *created.Description)
	require.Equal(t, group.ID, *created.GroupID)
}

func TestReportServiceCreateReportForUserRejectsSelfAndStoresTarget(t *testing.T) {
	service, client := newReportService(t)
	defer client.Close()
	ctx := context.Background()
	reporter := client.User.Create().SetUsername("reporter").SaveX(ctx)
	target := client.User.Create().SetUsername("target").SetFullName("Target").SetBio("Bio").SaveX(ctx)

	err := service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{TargetType: "user", Reason: "abuse", TargetUserID: &reporter.ID})
	require.Error(t, err)

	err = service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{TargetType: "user", Reason: " abuse ", TargetUserID: &target.ID})
	require.NoError(t, err)
	created := client.Report.Query().Where(report.ReporterID(reporter.ID), report.TargetTypeEQ(report.TargetTypeUser)).OnlyX(ctx)
	require.Equal(t, target.ID, *created.TargetUserID)
	require.Equal(t, "abuse", created.Reason)
}

func TestReportServiceCreateReportForMessageStoresAttachmentsAndGroupEvidence(t *testing.T) {
	service, client := newReportService(t)
	defer client.Close()
	ctx := context.Background()
	reporter := client.User.Create().SetUsername("reporter").SaveX(ctx)
	sender := client.User.Create().SetUsername("sender").SetFullName("Sender").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Reported Group").SetInviteCode("invite").SetCreatedBy(sender.ID).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(reporter.ID).SetRole(groupmember.RoleMember).SaveX(ctx)
	attachment := client.Media.Create().
		SetFileName("evidence/file.jpg").
		SetOriginalName("file.jpg").
		SetFileSize(123).
		SetMimeType("image/jpeg").
		SetCategory(media.CategoryMessageAttachment).
		SetUploadStatus(media.UploadStatusCompleted).
		SaveX(ctx)
	msg := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetContent("reported").AddAttachmentIDs(attachment.ID).SaveX(ctx)

	err := service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{TargetType: "message", Reason: " abuse ", MessageID: &msg.ID})

	require.NoError(t, err)
	created := client.Report.Query().Where(report.ReporterID(reporter.ID), report.TargetTypeEQ(report.TargetTypeMessage)).OnlyX(ctx)
	require.Equal(t, "abuse", created.Reason)
	require.Equal(t, "group", created.EvidenceSnapshot["chat_type"])
	attachments, ok := created.EvidenceSnapshot["attachments"].([]interface{})
	require.True(t, ok)
	require.Len(t, attachments, 1)
}

func TestReportServiceCreateReportRejectsTooManyMessageAttachments(t *testing.T) {
	service, client := newReportService(t)
	defer client.Close()
	ctx := context.Background()
	reporter := client.User.Create().SetUsername("reporter").SaveX(ctx)
	sender := client.User.Create().SetUsername("sender").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChat(chatEntity).SetUser1ID(reporter.ID).SetUser2ID(sender.ID).SaveX(ctx)
	attachmentIDs := make([]uuid.UUID, 0, 21)
	for i := 0; i < 21; i++ {
		attachment := client.Media.Create().
			SetFileName("evidence-" + uuid.NewString() + ".bin").
			SetOriginalName("file.bin").
			SetFileSize(123).
			SetMimeType("application/octet-stream").
			SetCategory(media.CategoryMessageAttachment).
			SetUploadStatus(media.UploadStatusCompleted).
			SaveX(ctx)
		attachmentIDs = append(attachmentIDs, attachment.ID)
	}
	msg := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(sender.ID).SetContent("too many").AddAttachmentIDs(attachmentIDs...).SaveX(ctx)

	err := service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{TargetType: "message", Reason: "abuse", MessageID: &msg.ID})

	require.Error(t, err)
	require.Zero(t, client.Report.Query().CountX(ctx))
}

func TestReportServiceCreateReportRejectsMissingGroupAndUser(t *testing.T) {
	service, client := newReportService(t)
	defer client.Close()
	ctx := context.Background()
	reporter := client.User.Create().SetUsername("reporter").SaveX(ctx)
	missing := uuid.New()

	err := service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{TargetType: "group", Reason: "abuse", ChatID: &missing})
	require.Error(t, err)

	err = service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{TargetType: "user", Reason: "abuse", TargetUserID: &missing})
	require.Error(t, err)
}

func TestReportServiceCreateReportRejectsNonParticipant(t *testing.T) {
	service, client := newReportService(t)
	defer client.Close()
	ctx := context.Background()

	reporter := client.User.Create().SetUsername("reporter-outsider").SaveX(ctx)
	u1 := client.User.Create().SetUsername("u1-priv").SaveX(ctx)
	u2 := client.User.Create().SetUsername("u2-priv").SaveX(ctx)

	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChat(chatEntity).SetUser1ID(u1.ID).SetUser2ID(u2.ID).SaveX(ctx)
	msgPriv := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(u1.ID).SetContent("private secret").SaveX(ctx)

	err := service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{
		TargetType: "message",
		Reason:     "harassment",
		MessageID:  &msgPriv.ID,
	})
	require.Error(t, err)

	groupChatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(groupChatEntity.ID).SetName("Secret Group").SetInviteCode("sec-grp-inv").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(u1.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	msgGroup := client.Message.Create().SetChatID(groupChatEntity.ID).SetSenderID(u1.ID).SetContent("group secret").SaveX(ctx)

	err = service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{
		TargetType: "message",
		Reason:     "spam",
		MessageID:  &msgGroup.ID,
	})
	require.Error(t, err)
}

func TestReportServiceCreateReportGroupAndUserWithAvatar(t *testing.T) {
	service, client := newReportService(t)
	defer client.Close()
	ctx := context.Background()

	reporter := client.User.Create().SetUsername("reporter-av").SaveX(ctx)
	target := client.User.Create().SetUsername("target-av").SetFullName("Target Full").SaveX(ctx)

	userAv := client.Media.Create().
		SetFileName("avatars/user.png").
		SetOriginalName("user.png").
		SetFileSize(10).
		SetMimeType("image/png").
		SetCategory(media.CategoryUserAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetUploadedByID(target.ID).
		SaveX(ctx)
	client.User.UpdateOneID(target.ID).SetAvatar(userAv).SaveX(ctx)

	groupChatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupAv := client.Media.Create().
		SetFileName("avatars/grp.png").
		SetOriginalName("grp.png").
		SetFileSize(20).
		SetMimeType("image/png").
		SetCategory(media.CategoryGroupAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetUploadedByID(reporter.ID).
		SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(groupChatEntity.ID).
		SetName("Avatar Reported Group").
		SetInviteCode("av-rep-inv").
		SetAvatar(groupAv).
		SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(reporter.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	storage := service.storageAdapter.(*adaptermocks.MockPublicURLGenerator)
	storage.EXPECT().GetPublicURL("avatars/grp.png").Return("https://cdn/grp.png").Maybe()
	storage.EXPECT().GetPublicURL("avatars/user.png").Return("https://cdn/user.png").Maybe()

	err := service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{
		TargetType: "group",
		Reason:     "inappropriate avatar",
		ChatID:     &groupChatEntity.ID,
	})
	require.NoError(t, err)

	err = service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{
		TargetType:   "user",
		Reason:       "fake profile",
		TargetUserID: &target.ID,
	})
	require.NoError(t, err)
}

func TestReportServiceCreateReportEditedMessageWithSenderDetails(t *testing.T) {
	service, client := newReportService(t)
	defer client.Close()
	ctx := context.Background()

	reporter := client.User.Create().SetUsername("rep-ed").SaveX(ctx)
	sender := client.User.Create().SetUsername("sender-ed").SetFullName("Sender Full").SaveX(ctx)

	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(reporter.ID).SetUser2ID(sender.ID).SaveX(ctx)

	editedTime := time.Now().UTC()
	msg := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetSenderID(sender.ID).
		SetContent("edited offensive msg").
		SetEditedAt(editedTime).
		SaveX(ctx)

	err := service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{
		TargetType: "message",
		Reason:     "harassment",
		MessageID:  &msg.ID,
	})
	require.NoError(t, err)
}

func TestReportServiceCreateReportMessageWithNilSender(t *testing.T) {
	service, client := newReportService(t)
	defer client.Close()
	ctx := context.Background()

	reporter := client.User.Create().SetUsername("rep-nilsender").SaveX(ctx)
	other := client.User.Create().SetUsername("other-nilsender").SaveX(ctx)

	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(reporter.ID).SetUser2ID(other.ID).SaveX(ctx)

	msg := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetType(message.TypeSystemLeave).
		SetContent("system leave message").
		SaveX(ctx)

	err := service.CreateReport(ctx, reporter.ID, model.CreateReportRequest{
		TargetType: "message",
		Reason:     "inappropriate system text",
		MessageID:  &msg.ID,
	})
	require.NoError(t, err)
}
