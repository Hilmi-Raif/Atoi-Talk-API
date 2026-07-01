package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/websocket"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

func (s *GroupChatService) JoinGroupByInvite(ctx context.Context, userID uuid.UUID, inviteCode string) (*model.ChatListResponse, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		slog.Error("Failed to start transaction", "error", err)
		return nil, helper.NewInternalServerError("")
	}
	defer tx.Rollback()

	gc, err := tx.GroupChat.Query().
		Where(
			groupchat.InviteCode(inviteCode),
			groupchat.HasChatWith(chat.DeletedAtIsNil()),
		).
		Select(groupchat.FieldID, groupchat.FieldChatID, groupchat.FieldInviteExpiresAt, groupchat.FieldName, groupchat.FieldAvatarID, groupchat.FieldIsPublic, groupchat.FieldInviteCode).
		WithAvatar().
		WithChat().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("Invalid invite code")
		}
		slog.Error("Failed to query group chat by invite code", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if gc.InviteExpiresAt != nil && time.Now().UTC().After(*gc.InviteExpiresAt) {
		return nil, helper.NewBadRequestError("Invite code has expired")
	}

	isMember, err := tx.GroupMember.Query().
		Where(
			groupmember.GroupChatID(gc.ID),
			groupmember.UserID(userID),
		).
		Exist(ctx)
	if err != nil {
		slog.Error("Failed to check membership", "error", err)
		return nil, helper.NewInternalServerError("")
	}
	if isMember {
		return nil, helper.NewConflictError("You are already a member of this group")
	}

	_, err = tx.GroupMember.Create().
		SetGroupChat(gc).
		SetUserID(userID).
		SetRole(groupmember.RoleMember).
		Save(ctx)
	if err != nil {
		slog.Error("Failed to add member via invite", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	systemMsg, err := tx.Message.Create().
		SetChatID(gc.ChatID).
		SetSenderID(userID).
		SetType(message.TypeSystemJoin).
		Save(ctx)
	if err != nil {
		slog.Error("Failed to create system message for invite join", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	err = tx.Chat.UpdateOne(gc.Edges.Chat).
		SetLastMessage(systemMsg).
		SetLastMessageAt(systemMsg.CreatedAt).
		Exec(ctx)
	if err != nil {
		slog.Error("Failed to update chat with last message", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	s.redisAdapter.Del(context.Background(), fmt.Sprintf("chat_members:%s", gc.ChatID))

	avatarURL := ""
	if gc.Edges.Avatar != nil {
		avatarURL = s.storageAdapter.GetPublicURL(gc.Edges.Avatar.FileName)
	}

	fullMsg, err := s.client.Message.Query().
		Where(message.ID(systemMsg.ID)).
		WithSender().
		Only(ctx)

	var msgResponse *model.MessageResponse
	if err == nil {
		msgResponse = helper.ToMessageResponse(fullMsg, s.storageAdapter, nil, string(groupmember.RoleMember))
	}

	memberCount, err := s.client.GroupMember.Query().
		Where(
			groupmember.GroupChatID(gc.ID),
			groupmember.HasUserWith(user.DeletedAtIsNil()),
		).
		Count(ctx)
	if err != nil {
		slog.Error("Failed to count group members after join", "error", err)
	}

	if msgResponse != nil {
		msgResponse.MemberCount = &memberCount
	}

	chatListResponse := &model.ChatListResponse{
		ID:          gc.Edges.Chat.ID,
		Type:        string(chat.TypeGroup),
		Name:        gc.Name,
		Avatar:      avatarURL,
		LastMessage: msgResponse,
		UnreadCount: 0,
		MemberCount: memberCount,
		IsPublic:    &gc.IsPublic,
	}
	myRole := string(groupmember.RoleMember)
	chatListResponse.MyRole = &myRole
	if gc.IsPublic {
		chatListResponse.InviteCode = &gc.InviteCode
	}

	if s.wsHub != nil {
		go func() {

			s.wsHub.BroadcastToUser(userID, websocket.Event{
				Type:    websocket.EventChatNew,
				Payload: chatListResponse,
				Meta: &websocket.EventMeta{
					Timestamp: time.Now().UTC().UnixMilli(),
					ChatID:    gc.ChatID,
					SenderID:  userID,
				},
			})

			if msgResponse != nil {
				s.wsHub.BroadcastToChat(gc.ChatID, websocket.Event{
					Type:    websocket.EventMessageNew,
					Payload: msgResponse,
					Meta: &websocket.EventMeta{
						Timestamp: time.Now().UTC().UnixMilli(),
						ChatID:    gc.ChatID,
						SenderID:  userID,
					},
				})
			}
		}()
	}

	return chatListResponse, nil
}

func (s *GroupChatService) GetGroupByInviteCode(ctx context.Context, inviteCode string) (*model.GroupPreviewDTO, error) {
	gc, err := s.client.GroupChat.Query().
		Where(
			groupchat.InviteCode(inviteCode),
			groupchat.HasChatWith(chat.DeletedAtIsNil()),
		).
		Select(groupchat.FieldID, groupchat.FieldChatID, groupchat.FieldName, groupchat.FieldDescription, groupchat.FieldAvatarID, groupchat.FieldInviteExpiresAt, groupchat.FieldIsPublic).
		WithAvatar().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("Invalid invite code")
		}
		return nil, helper.NewInternalServerError("")
	}

	if gc.InviteExpiresAt != nil && time.Now().UTC().After(*gc.InviteExpiresAt) {
		return nil, helper.NewBadRequestError("Invite code has expired")
	}

	memberCount, err := s.client.GroupMember.Query().
		Where(
			groupmember.GroupChatID(gc.ID),
			groupmember.HasUserWith(user.DeletedAtIsNil()),
		).
		Count(ctx)
	if err != nil {
		slog.Error("Failed to count group members", "error", err)
	}

	avatarURL := ""
	if gc.Edges.Avatar != nil {
		avatarURL = s.storageAdapter.GetPublicURL(gc.Edges.Avatar.FileName)
	}

	description := ""
	if gc.Description != nil {
		description = *gc.Description
	}

	return &model.GroupPreviewDTO{
		ID:          gc.ChatID,
		Name:        gc.Name,
		Description: description,
		Avatar:      avatarURL,
		MemberCount: memberCount,
		IsPublic:    gc.IsPublic,
	}, nil
}

func (s *GroupChatService) ResetInviteCode(ctx context.Context, userID, groupID uuid.UUID) (*model.GroupInviteResponse, error) {
	gc, err := s.client.GroupChat.Query().
		Where(
			groupchat.ChatID(groupID),
			groupchat.HasChatWith(chat.DeletedAtIsNil()),
		).
		Select(groupchat.FieldID, groupchat.FieldChatID, groupchat.FieldIsPublic).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("Group not found")
		}
		return nil, helper.NewInternalServerError("")
	}

	member, err := s.client.GroupMember.Query().
		Where(
			groupmember.GroupChatID(gc.ID),
			groupmember.UserID(userID),
		).
		Only(ctx)
	if err != nil {
		return nil, helper.NewForbiddenError("You are not a member of this group")
	}

	if member.Role != groupmember.RoleOwner && member.Role != groupmember.RoleAdmin {
		return nil, helper.NewForbiddenError("Only admins can reset invite code")
	}

	newCode, err := helper.GenerateRandomString(12)
	if err != nil {
		slog.Error("Failed to generate new invite code", "error", err, "groupID", gc.ID)
		return nil, helper.NewInternalServerError("")
	}

	var expiresAt *time.Time
	if !gc.IsPublic {
		t := time.Now().UTC().Add(7 * 24 * time.Hour)
		expiresAt = &t
	}

	update := s.client.GroupChat.UpdateOne(gc).SetInviteCode(newCode)
	if expiresAt != nil {
		update.SetInviteExpiresAt(*expiresAt)
	} else {
		update.ClearInviteExpiresAt()
	}

	_, err = update.Save(ctx)
	if err != nil {
		slog.Error("Failed to reset invite code", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	var expiresAtStr *string
	if expiresAt != nil {
		t := expiresAt.Format(time.RFC3339)
		expiresAtStr = &t
	}

	if s.wsHub != nil {
		go func() {
			updatedGroup, err := s.client.GroupChat.Query().
				Where(groupchat.ID(gc.ID)).
				WithAvatar().
				Only(context.Background())
			if err != nil {
				slog.Error("Failed to fetch updated group for invite reset event", "error", err, "groupID", gc.ID)
				return
			}

			memberQuery := s.client.GroupMember.Query().
				Where(
					groupmember.GroupChatID(gc.ID),
				)
			if !gc.IsPublic {
				memberQuery = memberQuery.Where(groupmember.RoleIn(groupmember.RoleOwner, groupmember.RoleAdmin))
			}

			members, err := memberQuery.All(context.Background())
			if err != nil {
				slog.Error("Failed to fetch invite reset recipients", "error", err, "groupID", gc.ID)
				return
			}

			lastMessage := s.getGroupLastMessageResponse(context.Background(), gc.ChatID)
			for _, m := range members {
				role := m.Role
				payload := s.buildGroupChatListResponse(context.Background(), updatedGroup, &role, lastMessage)
				s.wsHub.BroadcastToUser(m.UserID, websocket.Event{
					Type:    websocket.EventChatUpdate,
					Payload: payload,
					Meta: &websocket.EventMeta{
						Timestamp: time.Now().UTC().UnixMilli(),
						ChatID:    gc.ChatID,
						SenderID:  userID,
					},
				})
			}
		}()
	}

	return &model.GroupInviteResponse{
		InviteCode: newCode,
		ExpiresAt:  expiresAtStr,
	}, nil
}
