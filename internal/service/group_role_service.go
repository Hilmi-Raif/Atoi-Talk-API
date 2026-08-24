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

func (s *GroupChatService) UpdateMemberRole(ctx context.Context, requestorID uuid.UUID, groupID uuid.UUID, targetUserID uuid.UUID, req model.UpdateGroupMemberRoleRequest) (*model.MessageResponse, error) {
	if err := s.validator.Struct(req); err != nil {
		return nil, helper.NewBadRequestError("")
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		slog.Error("Failed to start transaction", "error", err)
		return nil, helper.NewInternalServerError("")
	}
	defer func() { _ = tx.Rollback() }()

	gc, err := tx.GroupChat.Query().
		Where(
			groupchat.ChatID(groupID),
			groupchat.HasChatWith(chat.DeletedAtIsNil()),
		).
		Select(groupchat.FieldID, groupchat.FieldChatID).
		WithChat().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("Group chat not found")
		}
		slog.Error("Failed to query group chat", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	requestorMember, err := tx.GroupMember.Query().
		Where(
			groupmember.GroupChatID(gc.ID),
			groupmember.UserID(requestorID),
		).
		Only(ctx)
	if err != nil {
		return nil, helper.NewForbiddenError("You are not a member of this group")
	}

	if requestorMember.Role != groupmember.RoleOwner {
		return nil, helper.NewForbiddenError("Only owner can change member roles")
	}

	targetMember, err := tx.GroupMember.Query().
		Where(
			groupmember.GroupChatID(gc.ID),
			groupmember.UserID(targetUserID),
		).
		WithUser(func(q *ent.UserQuery) {
			q.Select(user.FieldID, user.FieldFullName, user.FieldIsBanned, user.FieldBannedUntil, user.FieldDeletedAt)
		}).
		Only(ctx)
	if err != nil {
		return nil, helper.NewNotFoundError("Target user is not a member of this group")
	}

	if targetMember.UserID == requestorID {
		return nil, helper.NewBadRequestError("Cannot change your own role")
	}

	if targetMember.Edges.User != nil {
		if targetMember.Edges.User.DeletedAt != nil {
			return nil, helper.NewBadRequestError("Cannot change role of a deleted user")
		}
		if targetMember.Edges.User.IsBanned {
			if targetMember.Edges.User.BannedUntil == nil || time.Now().UTC().Before(*targetMember.Edges.User.BannedUntil) {
				return nil, helper.NewForbiddenError("Cannot promote a suspended/banned user")
			}
		}
	}

	newRole := groupmember.Role(req.Role)
	if targetMember.Role == newRole {
		return nil, helper.NewBadRequestError("Target user already has that role")
	}

	err = tx.GroupMember.UpdateOne(targetMember).SetRole(newRole).Exec(ctx)
	if err != nil {
		slog.Error("Failed to update member role", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	msgType := message.TypeSystemPromote
	if newRole == groupmember.RoleMember {
		msgType = message.TypeSystemDemote
	}

	systemMsg, err := tx.Message.Create().
		SetChatID(gc.ChatID).
		SetSenderID(requestorID).
		SetType(msgType).
		SetActionData(map[string]interface{}{
			"target_id": targetMember.UserID,
			"actor_id":  requestorID,
			"new_role":  newRole,
		}).
		Save(ctx)
	if err != nil {
		slog.Error("Failed to create system message for role update", "error", err)
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

	_ = s.redisAdapter.Del(context.Background(), fmt.Sprintf("chat_members:%s", groupID))

	fullMsg, err := s.client.Message.Query().
		Where(message.ID(systemMsg.ID)).
		WithSender().
		Only(ctx)

	var msgResponse *model.MessageResponse
	if err == nil {
		msgResponse = helper.ToMessageResponse(fullMsg, s.storageAdapter, nil, string(groupmember.RoleOwner))
		if targetMember.Edges.User != nil && targetMember.Edges.User.FullName != nil {
			if msgResponse.ActionData == nil {
				msgResponse.ActionData = make(map[string]interface{})
			}
			msgResponse.ActionData["target_name"] = *targetMember.Edges.User.FullName
		}
	}

	if s.wsHub != nil && msgResponse != nil {
		go func() {
			s.wsHub.BroadcastToChat(gc.ChatID, websocket.Event{
				Type:    websocket.EventMessageNew,
				Payload: msgResponse,
				Meta: &websocket.EventMeta{
					Timestamp: time.Now().UTC().UnixMilli(),
					ChatID:    gc.ChatID,
					SenderID:  requestorID,
				},
			})
		}()
	}

	return msgResponse, nil
}

func (s *GroupChatService) TransferOwnership(ctx context.Context, requestorID uuid.UUID, groupID uuid.UUID, req model.TransferGroupOwnershipRequest) (*model.MessageResponse, error) {
	if err := s.validator.Struct(req); err != nil {
		return nil, helper.NewBadRequestError("")
	}

	if requestorID == req.NewOwnerID {
		return nil, helper.NewBadRequestError("Cannot transfer ownership to yourself")
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		slog.Error("Failed to start transaction", "error", err)
		return nil, helper.NewInternalServerError("")
	}
	defer func() { _ = tx.Rollback() }()

	gc, err := tx.GroupChat.Query().
		Where(
			groupchat.ChatID(groupID),
			groupchat.HasChatWith(chat.DeletedAtIsNil()),
		).
		Select(groupchat.FieldID, groupchat.FieldChatID).
		WithChat().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("Group chat not found")
		}
		slog.Error("Failed to query group chat", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	requestorMember, err := tx.GroupMember.Query().
		Where(
			groupmember.GroupChatID(gc.ID),
			groupmember.UserID(requestorID),
		).
		Only(ctx)
	if err != nil {
		return nil, helper.NewForbiddenError("You are not a member of this group")
	}

	if requestorMember.Role != groupmember.RoleOwner {
		return nil, helper.NewForbiddenError("Only owner can transfer ownership")
	}

	targetMember, err := tx.GroupMember.Query().
		Where(
			groupmember.GroupChatID(gc.ID),
			groupmember.UserID(req.NewOwnerID),
		).
		WithUser(func(q *ent.UserQuery) {
			q.Select(user.FieldID, user.FieldFullName, user.FieldIsBanned, user.FieldBannedUntil, user.FieldDeletedAt)
		}).
		Only(ctx)
	if err != nil {
		return nil, helper.NewNotFoundError("Target user is not a member of this group")
	}

	if targetMember.Role == groupmember.RoleOwner {
		return nil, helper.NewBadRequestError("Target user is already the owner")
	}

	if targetMember.Edges.User != nil {
		if targetMember.Edges.User.DeletedAt != nil {
			return nil, helper.NewBadRequestError("Cannot transfer ownership to a deleted user")
		}
		if targetMember.Edges.User.IsBanned {
			if targetMember.Edges.User.BannedUntil == nil || time.Now().UTC().Before(*targetMember.Edges.User.BannedUntil) {
				return nil, helper.NewForbiddenError("Cannot transfer ownership to a suspended/banned user")
			}
		}
	}

	err = tx.GroupMember.UpdateOne(requestorMember).SetRole(groupmember.RoleAdmin).Exec(ctx)
	if err != nil {
		slog.Error("Failed to demote old owner", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	err = tx.GroupMember.UpdateOne(targetMember).SetRole(groupmember.RoleOwner).Exec(ctx)
	if err != nil {
		slog.Error("Failed to promote new owner", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	systemMsg, err := tx.Message.Create().
		SetChatID(gc.ChatID).
		SetSenderID(requestorID).
		SetType(message.TypeSystemPromote).
		SetActionData(map[string]interface{}{
			"target_id": targetMember.UserID,
			"actor_id":  requestorID,
			"new_role":  "owner",
			"action":    "ownership_transferred",
		}).
		Save(ctx)
	if err != nil {
		slog.Error("Failed to create system message for ownership transfer", "error", err)
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

	_ = s.redisAdapter.Del(context.Background(), fmt.Sprintf("chat_members:%s", groupID))

	fullMsg, err := s.client.Message.Query().
		Where(message.ID(systemMsg.ID)).
		WithSender().
		Only(ctx)

	var msgResponse *model.MessageResponse
	if err == nil {
		msgResponse = helper.ToMessageResponse(fullMsg, s.storageAdapter, nil, string(groupmember.RoleOwner))
		if targetMember.Edges.User != nil && targetMember.Edges.User.FullName != nil {
			if msgResponse.ActionData == nil {
				msgResponse.ActionData = make(map[string]interface{})
			}
			msgResponse.ActionData["target_name"] = *targetMember.Edges.User.FullName
		}
	}

	if s.wsHub != nil && msgResponse != nil {
		go func() {
			s.wsHub.BroadcastToChat(gc.ChatID, websocket.Event{
				Type:    websocket.EventMessageNew,
				Payload: msgResponse,
				Meta: &websocket.EventMeta{
					Timestamp: time.Now().UTC().UnixMilli(),
					ChatID:    gc.ChatID,
					SenderID:  requestorID,
				},
			})
		}()
	}

	return msgResponse, nil
}
