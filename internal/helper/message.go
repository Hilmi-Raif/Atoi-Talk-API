package helper

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/internal/model"
	"time"

	"github.com/google/uuid"
)

func ToMessageResponse(msg *ent.Message, urlGen URLGenerator, hiddenAt *time.Time, senderRole string) *model.MessageResponse {
	if msg == nil {
		return nil
	}

	isDeleted := msg.DeletedAt != nil
	var deletedAtStr *string
	var editedAtStr *string
	var content string
	attachments := make([]model.MediaDTO, 0)
	var actionData map[string]interface{}

	if isDeleted {
		t := msg.DeletedAt.Format(time.RFC3339)
		deletedAtStr = &t
	} else {
		if msg.Content != nil {
			content = *msg.Content
		}
		if msg.EditedAt != nil {
			t := msg.EditedAt.Format(time.RFC3339)
			editedAtStr = &t
		}
		if msg.ActionData != nil {
			actionData = msg.ActionData
		}
		for _, att := range msg.Edges.Attachments {

			url, _ := urlGen.GetPresignedURL(att.FileName, 15*time.Minute)
			attachments = append(attachments, model.MediaDTO{
				ID:           att.ID,
				FileName:     att.FileName,
				OriginalName: att.OriginalName,
				FileSize:     att.FileSize,
				MimeType:     att.MimeType,
				URL:          url,
			})
		}
	}

	var senderName string
	var senderAvatar string
	senderID := msg.SenderID

	if msg.Edges.Sender != nil {
		if msg.Edges.Sender.DeletedAt != nil {
			senderName = "Deleted User"
			senderAvatar = ""
			senderID = nil
		} else {
			if msg.Edges.Sender.FullName != nil {
				senderName = *msg.Edges.Sender.FullName
			}
			if msg.Edges.Sender.Edges.Avatar != nil {
				senderAvatar = urlGen.GetPublicURL(msg.Edges.Sender.Edges.Avatar.FileName)
			}
		}
	}

	var replyPreview *model.ReplyPreviewDTO
	if reply := msg.Edges.ReplyTo; reply != nil {
		replyContent := ""
		var replyDeletedAt *string
		var replyActionData map[string]interface{}
		replySenderName := ""
		var replySenderID *uuid.UUID

		if reply.Edges.Sender != nil {
			if reply.Edges.Sender.DeletedAt != nil {
				replySenderName = "Deleted User"
				replySenderID = nil
			} else {
				if reply.Edges.Sender.FullName != nil {
					replySenderName = *reply.Edges.Sender.FullName
				}
				replySenderID = &reply.Edges.Sender.ID
			}
		} else if reply.SenderID != nil {
			replySenderID = reply.SenderID
		}

		if reply.DeletedAt != nil {
			t := reply.DeletedAt.Format(time.RFC3339)
			replyDeletedAt = &t
		} else {
			if reply.Content != nil {
				replyContent = *reply.Content
			}
			if reply.ActionData != nil {
				replyActionData = reply.ActionData
			}
		}

		replyPreview = &model.ReplyPreviewDTO{
			ID:         reply.ID,
			SenderID:   replySenderID,
			SenderName: replySenderName,
			Type:       string(reply.Type),
			Content:    replyContent,
			ActionData: replyActionData,
			DeletedAt:  replyDeletedAt,
			CreatedAt:  reply.CreatedAt.Format(time.RFC3339),
		}
	}

	return &model.MessageResponse{
		ID:           msg.ID,
		ChatID:       msg.ChatID,
		SenderID:     senderID,
		SenderName:   senderName,
		SenderAvatar: senderAvatar,
		SenderRole:   senderRole,
		Type:         string(msg.Type),
		Content:      content,
		ActionData:   actionData,
		Attachments:  attachments,
		ReplyTo:      replyPreview,
		CreatedAt:    msg.CreatedAt.Format(time.RFC3339),
		DeletedAt:    deletedAtStr,
		EditedAt:     editedAtStr,
	}
}
