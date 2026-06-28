package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/internal/adapter"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"context"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

const presignedUploadExpiry = 15 * time.Minute

var avatarMIMETypes = map[string]bool{
	"image/jpeg": true,
	"image/png":  true,
	"image/webp": true,
}

type MediaService struct {
	client         *ent.Client
	cfg            *config.AppConfig
	validator      *validator.Validate
	storageAdapter *adapter.StorageAdapter
	captchaAdapter *adapter.CaptchaAdapter
}

func NewMediaService(client *ent.Client, cfg *config.AppConfig, validator *validator.Validate, storageAdapter *adapter.StorageAdapter, captchaAdapter *adapter.CaptchaAdapter) *MediaService {
	return &MediaService{
		client:         client,
		cfg:            cfg,
		validator:      validator,
		storageAdapter: storageAdapter,
		captchaAdapter: captchaAdapter,
	}
}

func (s *MediaService) UploadMedia(ctx context.Context, userID uuid.UUID, req model.UploadMediaRequest) (*model.UploadMediaResponse, error) {
	if err := s.validator.Struct(req); err != nil {
		slog.Warn("Validation failed", "error", err)
		return nil, helper.NewBadRequestError("")
	}
	req.OriginalName = strings.TrimSpace(req.OriginalName)
	req.MimeType = strings.ToLower(strings.TrimSpace(req.MimeType))
	if req.OriginalName == "" {
		return nil, helper.NewBadRequestError("")
	}

	if err := s.captchaAdapter.Verify(req.CaptchaToken, ""); err != nil {
		slog.Warn("Captcha verification failed", "error", err)
		return nil, helper.NewBadRequestError("")
	}

	category := media.Category(req.Usage)
	isPublic := category == media.CategoryUserAvatar || category == media.CategoryGroupAvatar
	if !isAllowedUpload(category, req.MimeType, req.FileSize) {
		return nil, helper.NewBadRequestError("Unsupported file metadata")
	}

	finalFileName := helper.GenerateUniqueFileName(req.OriginalName)
	expiresAt := time.Now().UTC().Add(presignedUploadExpiry)

	mediaRecord, err := s.client.Media.Create().
		SetFileName(finalFileName).
		SetOriginalName(req.OriginalName).
		SetFileSize(req.FileSize).
		SetMimeType(req.MimeType).
		SetCategory(category).
		SetUploadStatus(media.UploadStatusPending).
		SetUploadExpiresAt(expiresAt).
		SetUploaderID(userID).
		Save(ctx)

	if err != nil {
		slog.Error("Failed to create media record", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	uploadURL, uploadHeaders, err := s.storageAdapter.GetPresignedPutURL(finalFileName, req.MimeType, req.FileSize, isPublic, presignedUploadExpiry)
	if err != nil {
		slog.Error("Failed to generate presigned PUT URL", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	mediaURL := ""
	if isPublic {
		mediaURL = s.storageAdapter.GetPublicURL(finalFileName)
	}

	return &model.UploadMediaResponse{
		Media: model.MediaDTO{
			ID:           mediaRecord.ID,
			FileName:     mediaRecord.FileName,
			OriginalName: mediaRecord.OriginalName,
			FileSize:     mediaRecord.FileSize,
			MimeType:     mediaRecord.MimeType,
			Category:     string(mediaRecord.Category),
			UploadStatus: string(mediaRecord.UploadStatus),
			URL:          mediaURL,
		},
		UploadURL:     uploadURL,
		UploadMethod:  "PUT",
		UploadHeaders: uploadHeaders,
		ExpiresAt:     expiresAt,
	}, nil
}

func (s *MediaService) CompleteUpload(ctx context.Context, userID, mediaID uuid.UUID) (*model.MediaDTO, error) {
	m, err := s.client.Media.Query().
		Where(media.ID(mediaID)).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("Media not found")
		}
		slog.Error("Failed to query media for completion", "error", err, "mediaID", mediaID)
		return nil, helper.NewInternalServerError("")
	}

	if m.UploadedByID == nil || *m.UploadedByID != userID {
		return nil, helper.NewForbiddenError("You do not own this media")
	}
	if m.UploadStatus != media.UploadStatusPending {
		return nil, helper.NewBadRequestError("Media upload is not pending")
	}
	if m.UploadExpiresAt != nil && time.Now().UTC().After(*m.UploadExpiresAt) {
		return nil, helper.NewBadRequestError("Media upload has expired")
	}

	isPublic := m.Category == media.CategoryUserAvatar || m.Category == media.CategoryGroupAvatar
	objectInfo, err := s.storageAdapter.Head(m.FileName, isPublic)
	if err != nil {
		slog.Warn("Uploaded object not found or unreadable", "error", err, "mediaID", mediaID, "fileName", m.FileName)
		return nil, helper.NewBadRequestError("Uploaded object not found")
	}
	if objectInfo.Size != m.FileSize {
		return nil, helper.NewBadRequestError("Uploaded object size mismatch")
	}
	if objectInfo.ContentType != "" && !compatibleContentType(m.MimeType, objectInfo.ContentType) {
		return nil, helper.NewBadRequestError("Uploaded object content type mismatch")
	}

	now := time.Now().UTC()
	updated, err := s.client.Media.UpdateOneID(mediaID).
		SetUploadStatus(media.UploadStatusCompleted).
		SetCompletedAt(now).
		ClearUploadExpiresAt().
		Save(ctx)
	if err != nil {
		slog.Error("Failed to mark media upload completed", "error", err, "mediaID", mediaID)
		return nil, helper.NewInternalServerError("")
	}

	url := ""
	if isPublic {
		url = s.storageAdapter.GetPublicURL(updated.FileName)
	} else {
		url, err = s.storageAdapter.GetPresignedURL(updated.FileName, 15*time.Minute)
		if err != nil {
			slog.Error("Failed to generate presigned read URL after completion", "error", err, "mediaID", mediaID)
			url = ""
		}
	}

	return toMediaDTO(updated, url), nil
}

func (s *MediaService) GetMediaURL(ctx context.Context, userID, mediaID uuid.UUID) (*model.MediaURLResponse, error) {
	m, err := s.client.Media.Query().
		Where(media.ID(mediaID)).
		Select(media.FieldID, media.FieldFileName, media.FieldUploadStatus).
		WithMessage(func(q *ent.MessageQuery) {
			q.Select(message.FieldID, message.FieldChatID, message.FieldDeletedAt)
			q.WithChat(func(cq *ent.ChatQuery) {
				cq.Select(chat.FieldID, chat.FieldType)
				cq.WithPrivateChat(func(pq *ent.PrivateChatQuery) {
					pq.Select(privatechat.FieldUser1ID, privatechat.FieldUser2ID)
				})
				cq.WithGroupChat(func(gq *ent.GroupChatQuery) {
					gq.Select(groupchat.FieldID)
				})
			})
		}).
		Only(ctx)

	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("Media not found")
		}
		slog.Error("Failed to query media", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if m.Edges.Message == nil || m.Edges.Message.Edges.Chat == nil {
		return nil, helper.NewForbiddenError("Media is not associated with a chat")
	}
	if m.UploadStatus != media.UploadStatusCompleted {
		return nil, helper.NewBadRequestError("Media upload is not completed")
	}
	if m.Edges.Message.DeletedAt != nil {
		return nil, helper.NewForbiddenError("Media is no longer available")
	}

	c := m.Edges.Message.Edges.Chat
	isMember := false
	if c.Type == chat.TypePrivate && c.Edges.PrivateChat != nil {
		pc := c.Edges.PrivateChat
		if (pc.User1ID != nil && *pc.User1ID == userID) || (pc.User2ID != nil && *pc.User2ID == userID) {
			isMember = true
		}
	} else if c.Type == chat.TypeGroup && c.Edges.GroupChat != nil {
		exists, err := s.client.GroupMember.Query().
			Where(
				groupmember.GroupChatID(c.Edges.GroupChat.ID),
				groupmember.UserID(userID),
			).Exist(ctx)
		if err != nil {
			slog.Error("Failed to check group membership for media access", "error", err, "mediaID", mediaID, "groupID", c.Edges.GroupChat.ID, "userID", userID)
			return nil, helper.NewInternalServerError("")
		}
		if exists {
			isMember = true
		}
	}

	if !isMember {
		return nil, helper.NewForbiddenError("You do not have access to this media")
	}

	url, err := s.storageAdapter.GetPresignedURL(m.FileName, 15*time.Minute)
	if err != nil {
		slog.Error("Failed to generate presigned URL for refresh", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	return &model.MediaURLResponse{
		URL: url,
	}, nil
}

func isAllowedUpload(category media.Category, mimeType string, fileSize int64) bool {
	switch category {
	case media.CategoryMessageAttachment:
		return fileSize <= 20*1024*1024
	case media.CategoryUserAvatar, media.CategoryGroupAvatar:
		return fileSize <= 2*1024*1024 && avatarMIMETypes[mimeType]
	default:
		return false
	}
}

func compatibleContentType(expected, actual string) bool {
	expected = strings.ToLower(strings.TrimSpace(expected))
	actual = strings.ToLower(strings.TrimSpace(strings.Split(actual, ";")[0]))
	return expected == actual || (expected == "application/zip" && actual == "application/octet-stream")
}

func toMediaDTO(m *ent.Media, url string) *model.MediaDTO {
	return &model.MediaDTO{
		ID:           m.ID,
		FileName:     filepath.ToSlash(m.FileName),
		OriginalName: m.OriginalName,
		FileSize:     m.FileSize,
		MimeType:     m.MimeType,
		Category:     string(m.Category),
		UploadStatus: string(m.UploadStatus),
		URL:          url,
	}
}
