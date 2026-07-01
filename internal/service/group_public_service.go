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
	"strings"
	"time"

	"github.com/google/uuid"
)

func (s *GroupChatService) SearchPublicGroups(ctx context.Context, userID uuid.UUID, req model.SearchPublicGroupsRequest) ([]model.PublicGroupDTO, string, bool, error) {
	if err := s.validator.Struct(req); err != nil {
		return nil, "", false, helper.NewBadRequestError("")
	}

	if req.Limit == 0 {
		req.Limit = 20
	}

	if req.SortBy == "" {
		req.SortBy = "name"
	}

	groups, nextCursor, hasNext, err := s.repo.GroupChat.SearchPublicGroups(ctx, req.Query, req.Cursor, req.Limit, req.SortBy)
	if err != nil {
		if strings.Contains(err.Error(), "invalid cursor format") {
			slog.Warn("Invalid cursor format in SearchPublicGroups", "error", err)
			return nil, "", false, helper.NewBadRequestError("")
		}
		slog.Error("Failed to search public groups", "error", err)
		return nil, "", false, helper.NewInternalServerError("")
	}

	groupIDs := make([]uuid.UUID, len(groups))
	for i, g := range groups {
		groupIDs[i] = g.ID
	}

	joinedGroups := make(map[uuid.UUID]bool)
	if len(groupIDs) > 0 {
		memberships, err := s.client.GroupMember.Query().
			Where(
				groupmember.UserID(userID),
				groupmember.GroupChatIDIn(groupIDs...),
			).
			Select(groupmember.FieldGroupChatID).
			All(ctx)

		if err == nil {
			for _, m := range memberships {
				joinedGroups[m.GroupChatID] = true
			}
		} else {
			slog.Error("Failed to batch check group memberships", "error", err)
		}
	}

	memberCounts := make(map[uuid.UUID]int)
	if len(groupIDs) > 0 {
		members, err := s.client.GroupMember.Query().
			Where(
				groupmember.GroupChatIDIn(groupIDs...),
				groupmember.HasUserWith(user.DeletedAtIsNil()),
			).
			Select(groupmember.FieldGroupChatID).
			All(ctx)
		if err == nil {
			for _, m := range members {
				memberCounts[m.GroupChatID]++
			}
		} else {
			slog.Error("Failed to batch count group members", "error", err)
		}
	}

	groupDTOs := make([]model.PublicGroupDTO, 0)
	for _, g := range groups {
		avatarURL := ""
		if g.Edges.Avatar != nil {
			avatarURL = s.storageAdapter.GetPublicURL(g.Edges.Avatar.FileName)
		}

		description := ""
		if g.Description != nil {
			description = *g.Description
		}

		groupDTOs = append(groupDTOs, model.PublicGroupDTO{
			ID:          g.ID,
			ChatID:      g.ChatID,
			Name:        g.Name,
			Description: description,
			Avatar:      avatarURL,
			MemberCount: memberCounts[g.ID],
			IsMember:    joinedGroups[g.ID],
		})
	}

	return groupDTOs, nextCursor, hasNext, nil
}

func (s *GroupChatService) JoinPublicGroup(ctx context.Context, userID uuid.UUID, groupID uuid.UUID) (*model.ChatListResponse, error) {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		slog.Error("Failed to start transaction", "error", err)
		return nil, helper.NewInternalServerError("")
	}
	defer tx.Rollback()

	gc, err := tx.GroupChat.Query().
		Where(
			groupchat.ChatID(groupID),
			groupchat.HasChatWith(chat.DeletedAtIsNil()),
		).
		Select(groupchat.FieldID, groupchat.FieldChatID, groupchat.FieldName, groupchat.FieldIsPublic, groupchat.FieldAvatarID, groupchat.FieldInviteCode).
		WithAvatar().
		WithChat().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("Group chat not found")
		}
		slog.Error("Failed to query group chat", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if !gc.IsPublic {
		return nil, helper.NewForbiddenError("This group is private. You must be added by an admin.")
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
		slog.Error("Failed to add member", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	systemMsg, err := tx.Message.Create().
		SetChatID(gc.ChatID).
		SetSenderID(userID).
		SetType(message.TypeSystemJoin).
		Save(ctx)
	if err != nil {
		slog.Error("Failed to create system message for join", "error", err)
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

	s.redisAdapter.Del(context.Background(), fmt.Sprintf("chat_members:%s", groupID))

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
		slog.Error("Failed to count group members after public join", "error", err)
	}

	if msgResponse != nil {
		msgResponse.MemberCount = &memberCount
	}

	avatarURL := ""
	if gc.Edges.Avatar != nil {
		avatarURL = s.storageAdapter.GetPublicURL(gc.Edges.Avatar.FileName)
	}

	chatResponse := &model.ChatListResponse{
		ID:          gc.Edges.Chat.ID,
		Type:        string(chat.TypeGroup),
		Name:        gc.Name,
		Avatar:      avatarURL,
		LastMessage: msgResponse,
		UnreadCount: 0,
		MemberCount: memberCount,
		IsPublic:    &gc.IsPublic,
		InviteCode:  &gc.InviteCode,
	}
	myRole := string(groupmember.RoleMember)
	chatResponse.MyRole = &myRole

	if s.wsHub != nil && msgResponse != nil {
		go func() {
			s.wsHub.BroadcastToUser(userID, websocket.Event{
				Type:    websocket.EventChatNew,
				Payload: chatResponse,
				Meta: &websocket.EventMeta{
					Timestamp: time.Now().UTC().UnixMilli(),
					ChatID:    groupID,
					SenderID:  userID,
				},
			})

			s.wsHub.BroadcastToChat(gc.ChatID, websocket.Event{
				Type:    websocket.EventMessageNew,
				Payload: msgResponse,
				Meta: &websocket.EventMeta{
					Timestamp: time.Now().UTC().UnixMilli(),
					ChatID:    gc.ChatID,
					SenderID:  userID,
				},
			})
		}()
	}

	return chatResponse, nil
}
