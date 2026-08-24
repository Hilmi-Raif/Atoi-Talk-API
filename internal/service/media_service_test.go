package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/internal/adapter"
	adaptermocks "AtoiTalkAPI/internal/adapter/mocks"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/model"
	"context"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newMediaService(t *testing.T) (*MediaService, *ent.Client, *adaptermocks.MockMediaStorage, *adaptermocks.MockCaptchaVerifier) {
	t.Helper()
	client := enttest.Open(t, dialect.SQLite, "file:media-service-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	storage := adaptermocks.NewMockMediaStorage(t)
	captcha := adaptermocks.NewMockCaptchaVerifier(t)
	service := NewMediaService(client, &config.AppConfig{}, config.NewValidator(), storage, captcha)
	return service, client, storage, captcha
}

func TestMediaServiceUploadMediaCreatesPendingRecordAndPresignedResponse(t *testing.T) {
	service, client, storage, captcha := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("owner").SaveX(ctx)
	captcha.EXPECT().Verify("captcha", "").Return(nil)
	storage.EXPECT().GetPresignedPutURL(mock.AnythingOfType("string"), "image/png", int64(100), true, 15*time.Minute).
		Return("https://upload.test", map[string]string{"Content-Type": "image/png"}, nil)
	storage.EXPECT().GetPublicURL(mock.AnythingOfType("string")).Return("https://cdn.test/avatar.png")

	result, err := service.UploadMedia(ctx, owner.ID, model.UploadMediaRequest{
		Usage:        "user_avatar",
		OriginalName: "avatar.png",
		FileSize:     100,
		MimeType:     "image/png",
		CaptchaToken: "captcha",
	})

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "https://upload.test", result.UploadURL)
	require.Equal(t, "PUT", result.UploadMethod)
	require.Equal(t, "https://cdn.test/avatar.png", result.Media.URL)
	require.Equal(t, "pending", result.Media.UploadStatus)
	require.True(t, client.Media.Query().Where(media.ID(result.Media.ID)).ExistX(ctx))
}

func TestMediaServiceUploadMediaRejectsCaptchaAndUnsupportedMetadata(t *testing.T) {
	service, client, _, captcha := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("owner").SaveX(ctx)
	captcha.EXPECT().Verify("bad", "").Return(assertionError("captcha failed"))

	result, err := service.UploadMedia(ctx, owner.ID, model.UploadMediaRequest{
		Usage:        "message_attachment",
		OriginalName: "file.txt",
		FileSize:     100,
		MimeType:     "text/plain",
		CaptchaToken: "bad",
	})
	require.Nil(t, result)
	require.Error(t, err)

	captcha.EXPECT().Verify("ok", "").Return(nil)
	result, err = service.UploadMedia(ctx, owner.ID, model.UploadMediaRequest{
		Usage:        "user_avatar",
		OriginalName: "avatar.png",
		FileSize:     3 * 1024 * 1024,
		MimeType:     "image/png",
		CaptchaToken: "ok",
	})
	require.Nil(t, result)
	require.Error(t, err)
}

func TestMediaServiceUploadMediaReturnsStorageError(t *testing.T) {
	service, client, storage, captcha := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("owner").SaveX(ctx)
	captcha.EXPECT().Verify("ok", "").Return(nil)
	storage.EXPECT().GetPresignedPutURL(mock.AnythingOfType("string"), "image/png", int64(100), true, 15*time.Minute).
		Return("", nil, errors.New("storage unavailable"))

	result, err := service.UploadMedia(ctx, owner.ID, model.UploadMediaRequest{
		Usage:        "user_avatar",
		OriginalName: "avatar.png",
		FileSize:     100,
		MimeType:     "image/png",
		CaptchaToken: "ok",
	})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestMediaServiceCompleteUploadMarksMediaCompleted(t *testing.T) {
	service, client, storage, _ := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("owner").SaveX(ctx)
	expiresAt := time.Now().UTC().Add(time.Minute)
	record := client.Media.Create().
		SetFileName("avatars/avatar.png").
		SetOriginalName("avatar.png").
		SetFileSize(100).
		SetMimeType("image/png").
		SetCategory(media.CategoryUserAvatar).
		SetUploadStatus(media.UploadStatusPending).
		SetUploadExpiresAt(expiresAt).
		SetUploaderID(owner.ID).
		SaveX(ctx)
	storage.EXPECT().Head("avatars/avatar.png", true).Return(&adapter.StorageObjectInfo{Size: 100, ContentType: "image/png"}, nil)
	storage.EXPECT().GetPublicURL("avatars/avatar.png").Return("https://cdn.test/avatar.png")

	result, err := service.CompleteUpload(ctx, owner.ID, record.ID)

	require.NoError(t, err)
	require.Equal(t, "completed", result.UploadStatus)
	require.Equal(t, "https://cdn.test/avatar.png", result.URL)
	require.False(t, client.Media.GetX(ctx, record.ID).UploadExpiresAt != nil)
}

func TestMediaServiceCompleteUploadRejectsOwnershipAndSizeMismatch(t *testing.T) {
	service, client, storage, _ := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("owner").SaveX(ctx)
	other := client.User.Create().SetUsername("other").SaveX(ctx)
	record := client.Media.Create().
		SetFileName("file.bin").
		SetOriginalName("file.bin").
		SetFileSize(100).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusPending).
		SetUploaderID(owner.ID).
		SaveX(ctx)

	result, err := service.CompleteUpload(ctx, other.ID, record.ID)
	require.Nil(t, result)
	require.Error(t, err)

	storage.EXPECT().Head("file.bin", false).Return(&adapter.StorageObjectInfo{Size: 99, ContentType: "application/octet-stream"}, nil)
	result, err = service.CompleteUpload(ctx, owner.ID, record.ID)
	require.Nil(t, result)
	require.Error(t, err)
}

func TestMediaServiceCompleteUploadRejectsInvalidStateAndContentType(t *testing.T) {
	service, client, storage, _ := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("owner").SaveX(ctx)
	completed := client.Media.Create().
		SetFileName("completed.bin").
		SetOriginalName("completed.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusCompleted).
		SetUploaderID(owner.ID).
		SaveX(ctx)
	result, err := service.CompleteUpload(ctx, owner.ID, completed.ID)
	require.Nil(t, result)
	require.Error(t, err)

	expired := time.Now().UTC().Add(-time.Minute)
	expiredMedia := client.Media.Create().
		SetFileName("expired.bin").
		SetOriginalName("expired.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusPending).
		SetUploadExpiresAt(expired).
		SetUploaderID(owner.ID).
		SaveX(ctx)
	result, err = service.CompleteUpload(ctx, owner.ID, expiredMedia.ID)
	require.Nil(t, result)
	require.Error(t, err)

	mismatch := client.Media.Create().
		SetFileName("mismatch.png").
		SetOriginalName("mismatch.png").
		SetFileSize(10).
		SetMimeType("image/png").
		SetCategory(media.CategoryUserAvatar).
		SetUploadStatus(media.UploadStatusPending).
		SetUploaderID(owner.ID).
		SaveX(ctx)
	storage.EXPECT().Head("mismatch.png", true).Return(&adapter.StorageObjectInfo{Size: 10, ContentType: "image/jpeg"}, nil)
	result, err = service.CompleteUpload(ctx, owner.ID, mismatch.ID)
	require.Nil(t, result)
	require.Error(t, err)
}

func TestMediaServiceCompleteUploadReturnsObjectNotFoundError(t *testing.T) {
	service, client, storage, _ := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("owner").SaveX(ctx)
	record := client.Media.Create().
		SetFileName("missing.bin").
		SetOriginalName("missing.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusPending).
		SetUploaderID(owner.ID).
		SaveX(ctx)
	storage.EXPECT().Head("missing.bin", false).Return(nil, errors.New("object missing"))

	result, err := service.CompleteUpload(ctx, owner.ID, record.ID)

	require.Nil(t, result)
	require.Error(t, err)
	require.Equal(t, string(media.UploadStatusPending), client.Media.GetX(ctx, record.ID).UploadStatus.String())
}

func TestMediaServiceCompleteUploadAcceptsCompatibleAndEmptyContentType(t *testing.T) {
	service, client, storage, _ := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("owner").SaveX(ctx)

	zipRecord := client.Media.Create().
		SetFileName("archive.zip").
		SetOriginalName("archive.zip").
		SetFileSize(10).
		SetMimeType("application/zip").
		SetCategory(media.CategoryMessageAttachment).
		SetUploadStatus(media.UploadStatusPending).
		SetUploaderID(owner.ID).
		SaveX(ctx)
	storage.EXPECT().Head("archive.zip", false).Return(&adapter.StorageObjectInfo{Size: 10, ContentType: "application/octet-stream"}, nil)
	storage.EXPECT().GetPresignedURL("archive.zip", 15*time.Minute).Return("https://download.test/archive", nil)
	result, err := service.CompleteUpload(ctx, owner.ID, zipRecord.ID)
	require.NoError(t, err)
	require.Equal(t, "https://download.test/archive", result.URL)

	emptyTypeRecord := client.Media.Create().
		SetFileName("unknown.bin").
		SetOriginalName("unknown.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetCategory(media.CategoryMessageAttachment).
		SetUploadStatus(media.UploadStatusPending).
		SetUploaderID(owner.ID).
		SaveX(ctx)
	storage.EXPECT().Head("unknown.bin", false).Return(&adapter.StorageObjectInfo{Size: 10, ContentType: ""}, nil)
	storage.EXPECT().GetPresignedURL("unknown.bin", 15*time.Minute).Return("https://download.test/unknown", nil)
	result, err = service.CompleteUpload(ctx, owner.ID, emptyTypeRecord.ID)
	require.NoError(t, err)
	require.Equal(t, "https://download.test/unknown", result.URL)
}

func TestMediaServiceCompleteUploadReturnsPrivateURLAndHandlesURLFailure(t *testing.T) {
	service, client, storage, _ := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("owner").SaveX(ctx)
	record := client.Media.Create().
		SetFileName("file.bin").
		SetOriginalName("file.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusPending).
		SetUploaderID(owner.ID).
		SaveX(ctx)
	storage.EXPECT().Head("file.bin", false).Return(&adapter.StorageObjectInfo{Size: 10, ContentType: "application/octet-stream"}, nil)
	storage.EXPECT().GetPresignedURL("file.bin", 15*time.Minute).Return("", errors.New("presign failed"))

	result, err := service.CompleteUpload(ctx, owner.ID, record.ID)

	require.NoError(t, err)
	require.Empty(t, result.URL)
}

func TestMediaServiceGetMediaURLAllowsPrivateChatMember(t *testing.T) {
	service, client, storage, _ := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("owner").SaveX(ctx)
	other := client.User.Create().SetUsername("other").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(owner.ID).SetUser2ID(other.ID).SaveX(ctx)
	messageEntity := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(owner.ID).SetContent("hello").SaveX(ctx)
	mediaEntity := client.Media.Create().
		SetFileName("attachments/file.bin").
		SetOriginalName("file.bin").
		SetFileSize(100).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusCompleted).
		SetMessageID(messageEntity.ID).
		SaveX(ctx)
	storage.EXPECT().GetPresignedURL("attachments/file.bin", 15*time.Minute).Return("https://download.test/file", nil)

	result, err := service.GetMediaURL(ctx, other.ID, mediaEntity.ID)

	require.NoError(t, err)
	require.Equal(t, "https://download.test/file", result.URL)
}

func TestMediaServiceGetMediaURLRejectsNonMember(t *testing.T) {
	service, client, _, _ := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("owner").SaveX(ctx)
	other := client.User.Create().SetUsername("other").SaveX(ctx)
	stranger := client.User.Create().SetUsername("stranger").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(owner.ID).SetUser2ID(other.ID).SaveX(ctx)
	messageEntity := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(owner.ID).SetContent("hello").SaveX(ctx)
	mediaEntity := client.Media.Create().
		SetFileName("attachments/file.bin").
		SetOriginalName("file.bin").
		SetFileSize(100).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusCompleted).
		SetMessageID(messageEntity.ID).
		SaveX(ctx)

	result, err := service.GetMediaURL(ctx, stranger.ID, mediaEntity.ID)

	require.Nil(t, result)
	require.Error(t, err)
}

func TestMediaServiceGetMediaURLRejectsMissingOrUnavailableMedia(t *testing.T) {
	service, client, _, _ := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	userEntity := client.User.Create().SetUsername("user").SaveX(ctx)

	result, err := service.GetMediaURL(ctx, userEntity.ID, uuid.New())
	require.Nil(t, result)
	require.Error(t, err)

	unlinked := client.Media.Create().
		SetFileName("unlinked.bin").
		SetOriginalName("unlinked.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusCompleted).
		SaveX(ctx)
	result, err = service.GetMediaURL(ctx, userEntity.ID, unlinked.ID)
	require.Nil(t, result)
	require.Error(t, err)
}

func TestMediaServiceGetMediaURLRejectsIncompleteAndDeletedMessages(t *testing.T) {
	service, client, _, _ := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("owner").SaveX(ctx)
	other := client.User.Create().SetUsername("other").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(owner.ID).SetUser2ID(other.ID).SaveX(ctx)
	messageEntity := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(owner.ID).SetContent("hello").SaveX(ctx)
	incomplete := client.Media.Create().
		SetFileName("incomplete.bin").
		SetOriginalName("incomplete.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusPending).
		SetMessageID(messageEntity.ID).
		SaveX(ctx)
	result, err := service.GetMediaURL(ctx, owner.ID, incomplete.ID)
	require.Nil(t, result)
	require.Error(t, err)

	deletedAt := time.Now().UTC()
	deletedMessage := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(owner.ID).SetContent("deleted").SetDeletedAt(deletedAt).SaveX(ctx)
	deletedMedia := client.Media.Create().
		SetFileName("deleted.bin").
		SetOriginalName("deleted.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusCompleted).
		SetMessageID(deletedMessage.ID).
		SaveX(ctx)
	result, err = service.GetMediaURL(ctx, owner.ID, deletedMedia.ID)
	require.Nil(t, result)
	require.Error(t, err)
}

func TestMediaServiceGetMediaURLAllowsGroupMemberAndReturnsStorageError(t *testing.T) {
	service, client, storage, _ := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("owner").SaveX(ctx)
	member := client.User.Create().SetUsername("member").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("group").SetInviteCode("group-invite").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)
	messageEntity := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(owner.ID).SetContent("hello").SaveX(ctx)
	mediaEntity := client.Media.Create().
		SetFileName("attachments/group.bin").
		SetOriginalName("group.bin").
		SetFileSize(100).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusCompleted).
		SetMessageID(messageEntity.ID).
		SaveX(ctx)
	storage.EXPECT().GetPresignedURL("attachments/group.bin", 15*time.Minute).Return("", errors.New("presign failed"))

	result, err := service.GetMediaURL(ctx, member.ID, mediaEntity.ID)

	require.Nil(t, result)
	require.Error(t, err)
}

func TestMediaServiceUploadMediaGroupAvatarAndMessageAttachment(t *testing.T) {
	service, client, storage, captcha := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("owner-grp-av").SaveX(ctx)

	captcha.EXPECT().Verify("cap1", "").Return(nil)
	storage.EXPECT().GetPresignedPutURL(mock.AnythingOfType("string"), "image/jpeg", int64(200), true, 15*time.Minute).
		Return("https://upload.test/grp", map[string]string{"Content-Type": "image/jpeg"}, nil)
	storage.EXPECT().GetPublicURL(mock.AnythingOfType("string")).Return("https://cdn.test/grp-avatar.jpg")

	res1, err := service.UploadMedia(ctx, owner.ID, model.UploadMediaRequest{
		Usage:        "group_avatar",
		OriginalName: "grp.jpg",
		FileSize:     200,
		MimeType:     "image/jpeg",
		CaptchaToken: "cap1",
	})
	require.NoError(t, err)
	require.NotNil(t, res1)
	require.Equal(t, "https://cdn.test/grp-avatar.jpg", res1.Media.URL)

	captcha.EXPECT().Verify("cap2", "").Return(nil)
	storage.EXPECT().GetPresignedPutURL(mock.AnythingOfType("string"), "application/pdf", int64(1024), false, 15*time.Minute).
		Return("https://upload.test/doc", map[string]string{"Content-Type": "application/pdf"}, nil)

	res2, err := service.UploadMedia(ctx, owner.ID, model.UploadMediaRequest{
		Usage:        "message_attachment",
		OriginalName: "doc.pdf",
		FileSize:     1024,
		MimeType:     "application/pdf",
		CaptchaToken: "cap2",
	})
	require.NoError(t, err)
	require.NotNil(t, res2)
	require.Empty(t, res2.Media.URL)
}

func TestMediaServiceUploadMediaErrorBranches(t *testing.T) {
	service, client, storage, captcha := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("owner-media-err").SaveX(ctx)

	captcha.EXPECT().Verify("cap-err", "").Return(nil).Maybe()
	storage.EXPECT().GetPresignedPutURL(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return("", nil, errors.New("s3 presign put err")).Maybe()

	_, err := service.UploadMedia(ctx, owner.ID, model.UploadMediaRequest{
		Usage:        "user_avatar",
		OriginalName: "avatar.png",
		FileSize:     100,
		MimeType:     "image/png",
		CaptchaToken: "cap-err",
	})
	require.Error(t, err)

	_, err = service.UploadMedia(ctx, owner.ID, model.UploadMediaRequest{
		Usage:        "user_avatar",
		OriginalName: "   ",
		FileSize:     100,
		MimeType:     "image/png",
		CaptchaToken: "cap-err",
	})
	require.Error(t, err)
}

func TestMediaServiceCompleteUploadPublicAvatarReturnsPublicURL(t *testing.T) {
	service, client, storage, _ := newMediaService(t)
	defer client.Close()
	ctx := context.Background()
	owner := client.User.Create().SetUsername("avatar-owner").SaveX(ctx)

	avatarRecord := client.Media.Create().
		SetFileName("avatars/user.png").
		SetOriginalName("user.png").
		SetFileSize(120).
		SetMimeType("image/png").
		SetCategory(media.CategoryUserAvatar).
		SetUploadStatus(media.UploadStatusPending).
		SetUploaderID(owner.ID).
		SaveX(ctx)

	storage.EXPECT().Head("avatars/user.png", true).Return(&adapter.StorageObjectInfo{
		Size:        120,
		ContentType: "image/png",
	}, nil).Once()
	storage.EXPECT().GetPublicURL("avatars/user.png").Return("https://cdn.example.com/avatars/user.png").Once()

	res, err := service.CompleteUpload(ctx, owner.ID, avatarRecord.ID)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "https://cdn.example.com/avatars/user.png", res.URL)
}

func TestMediaServiceGetMediaURLPrivateChatUser2(t *testing.T) {
	service, client, storage, _ := newMediaService(t)
	defer client.Close()
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("u1-media").SaveX(ctx)
	u2 := client.User.Create().SetUsername("u2-media").SaveX(ctx)
	pChat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChat(pChat).SetUser1ID(u1.ID).SetUser2ID(u2.ID).SaveX(ctx)

	msg := client.Message.Create().SetChatID(pChat.ID).SetSenderID(u1.ID).SetContent("file").SaveX(ctx)
	record := client.Media.Create().
		SetFileName("att.png").
		SetOriginalName("att.png").
		SetFileSize(100).
		SetMimeType("image/png").
		SetCategory(media.CategoryMessageAttachment).
		SetUploadStatus(media.UploadStatusCompleted).
		SetMessageID(msg.ID).
		SaveX(ctx)

	storage.EXPECT().GetPresignedURL("att.png", 15*time.Minute).Return("https://presigned.test/att.png", nil).Once()

	res, err := service.GetMediaURL(ctx, u2.ID, record.ID)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "https://presigned.test/att.png", res.URL)
}

type assertionError string

func (e assertionError) Error() string { return string(e) }
