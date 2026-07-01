package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/ent/userblock"
	"AtoiTalkAPI/internal/adapter"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/repository"
	"AtoiTalkAPI/internal/websocket"
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

type GroupChatService struct {
	client         *ent.Client
	repo           *repository.Repository
	cfg            *config.AppConfig
	validator      *validator.Validate
	wsHub          *websocket.Hub
	storageAdapter *adapter.StorageAdapter
	redisAdapter   *adapter.RedisAdapter
}

func (s *GroupChatService) buildGroupChatListResponse(ctx context.Context, gc *ent.GroupChat, role *groupmember.Role, lastMessage *model.MessageResponse) model.ChatListResponse {
	avatarURL := ""
	if gc.Edges.Avatar != nil {
		avatarURL = s.storageAdapter.GetPublicURL(gc.Edges.Avatar.FileName)
	}

	var inviteExpiresAt *string
	if gc.InviteExpiresAt != nil {
		t := gc.InviteExpiresAt.Format(time.RFC3339)
		inviteExpiresAt = &t
	}

	memberCount, err := s.client.GroupMember.Query().
		Where(groupmember.GroupChatID(gc.ID), groupmember.HasUserWith(user.DeletedAtIsNil())).
		Count(ctx)
	if err != nil {
		slog.Error("Failed to count group members", "error", err, "groupID", gc.ID)
	}

	resp := model.ChatListResponse{
		ID:              gc.ChatID,
		Type:            string(chat.TypeGroup),
		Name:            gc.Name,
		Description:     gc.Description,
		IsPublic:        &gc.IsPublic,
		Avatar:          avatarURL,
		LastMessage:     lastMessage,
		UnreadCount:     0,
		MemberCount:     memberCount,
		InviteExpiresAt: inviteExpiresAt,
	}

	if role != nil {
		roleStr := string(*role)
		resp.MyRole = &roleStr
	}
	if gc.IsPublic || (role != nil && (*role == groupmember.RoleOwner || *role == groupmember.RoleAdmin)) {
		resp.InviteCode = &gc.InviteCode
	}
	if !gc.IsPublic && (role == nil || (*role != groupmember.RoleOwner && *role != groupmember.RoleAdmin)) {
		resp.InviteExpiresAt = nil
	}

	return resp
}

func (s *GroupChatService) getGroupLastMessageResponse(ctx context.Context, chatID uuid.UUID) *model.MessageResponse {
	lastMsg, err := s.client.Message.Query().
		Where(message.ChatID(chatID)).
		Order(ent.Desc(message.FieldCreatedAt)).
		WithSender().
		WithAttachments().
		WithReplyTo(func(q *ent.MessageQuery) {
			q.WithSender()
		}).
		First(ctx)
	if err != nil {
		if !ent.IsNotFound(err) {
			slog.Error("Failed to fetch group last message", "error", err, "chatID", chatID)
		}
		return nil
	}

	return helper.ToMessageResponse(lastMsg, s.storageAdapter, nil, "")
}

func NewGroupChatService(client *ent.Client, repo *repository.Repository, cfg *config.AppConfig, validator *validator.Validate, wsHub *websocket.Hub, storageAdapter *adapter.StorageAdapter, redisAdapter *adapter.RedisAdapter) *GroupChatService {
	return &GroupChatService{
		client:         client,
		repo:           repo,
		cfg:            cfg,
		validator:      validator,
		wsHub:          wsHub,
		storageAdapter: storageAdapter,
		redisAdapter:   redisAdapter,
	}
}

func (s *GroupChatService) CreateGroupChat(ctx context.Context, creatorID uuid.UUID, req model.CreateGroupChatRequest) (*model.ChatListResponse, error) {

	if err := s.validator.Struct(req); err != nil {
		slog.Warn("Validation failed for CreateGroupChat", "error", err)
		return nil, helper.NewBadRequestError("")
	}

	req.Name = strings.TrimSpace(req.Name)
	req.Description = strings.TrimSpace(req.Description)

	users, err := s.client.User.Query().
		Where(
			user.IDIn(req.MemberIDs...),
			user.DeletedAtIsNil(),
		).
		Select(user.FieldID, user.FieldIsBanned, user.FieldBannedUntil).
		All(ctx)
	if err != nil {
		slog.Error("Failed to query users for group creation", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if len(users) != len(req.MemberIDs) {
		return nil, helper.NewBadRequestError("One or more members not found or deleted.")
	}

	var memberIDs []uuid.UUID
	for _, u := range users {
		if u.ID == creatorID {
			return nil, helper.NewBadRequestError("Cannot add yourself to the member list.")
		}

		if u.IsBanned {
			if u.BannedUntil == nil || time.Now().UTC().Before(*u.BannedUntil) {
				return nil, helper.NewForbiddenError("Cannot add suspended/banned user to group")
			}
		}
		memberIDs = append(memberIDs, u.ID)
	}
	allMemberIDs := append(memberIDs, creatorID)

	blocked, err := s.client.UserBlock.Query().Where(
		userblock.Or(
			userblock.And(userblock.BlockerID(creatorID), userblock.BlockedIDIn(memberIDs...)),
			userblock.And(userblock.BlockerIDIn(memberIDs...), userblock.BlockedID(creatorID)),
		),
	).Exist(ctx)
	if err != nil {
		slog.Error("Failed to check block status for group creation", "error", err)
		return nil, helper.NewInternalServerError("")
	}
	if blocked {
		return nil, helper.NewForbiddenError("Cannot create a group with a blocked user.")
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		slog.Error("Failed to start transaction", "error", err)
		return nil, helper.NewInternalServerError("")
	}
	defer tx.Rollback()

	var avatarMedia *ent.Media

	if req.AvatarMediaID != nil {
		avatarMedia, err = tx.Media.Query().
			Where(
				media.ID(*req.AvatarMediaID),
				media.CategoryEQ(media.CategoryGroupAvatar),
				media.UploadStatusEQ(media.UploadStatusCompleted),
				media.HasUploaderWith(user.ID(creatorID)),
				media.Not(media.HasUserAvatar()),
				media.Not(media.HasGroupAvatar()),
				media.MessageIDIsNil(),
			).
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				return nil, helper.NewBadRequestError("Invalid avatar media")
			}
			slog.Error("Failed to query group avatar media", "error", err, "mediaID", *req.AvatarMediaID)
			return nil, helper.NewInternalServerError("")
		}
	}

	newChat, err := tx.Chat.Create().SetType(chat.TypeGroup).Save(ctx)
	if err != nil {
		slog.Error("Failed to create chat entity for group", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	inviteCode, err := helper.GenerateRandomString(12)
	if err != nil {
		slog.Error("Failed to generate invite code for group creation", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	groupCreate := tx.GroupChat.Create().
		SetChat(newChat).
		SetCreatorID(creatorID).
		SetName(req.Name).
		SetNillableDescription(&req.Description).
		SetIsPublic(req.IsPublic).
		SetInviteCode(inviteCode)

	if !req.IsPublic {
		groupCreate.SetInviteExpiresAt(time.Now().UTC().Add(7 * 24 * time.Hour))
	}

	if avatarMedia != nil {
		groupCreate.SetAvatar(avatarMedia)
	}

	newGroupChat, err := groupCreate.Save(ctx)
	if err != nil {
		slog.Error("Failed to create group chat", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	var memberCreates []*ent.GroupMemberCreate

	memberCreates = append(memberCreates, tx.GroupMember.Create().
		SetGroupChat(newGroupChat).
		SetUserID(creatorID).
		SetRole(groupmember.RoleOwner))

	for _, memberID := range memberIDs {
		memberCreates = append(memberCreates, tx.GroupMember.Create().
			SetGroupChat(newGroupChat).
			SetUserID(memberID).
			SetRole(groupmember.RoleMember))
	}
	if err := tx.GroupMember.CreateBulk(memberCreates...).Exec(ctx); err != nil {
		slog.Error("Failed to create group members", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	systemMsg, err := tx.Message.Create().
		SetChatID(newChat.ID).
		SetSenderID(creatorID).
		SetType(message.TypeSystemCreate).
		SetActionData(map[string]interface{}{
			"initial_name": req.Name,
		}).
		Save(ctx)
	if err != nil {
		slog.Error("Failed to create system message for group creation", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	err = tx.Chat.UpdateOne(newChat).
		SetLastMessage(systemMsg).
		SetLastMessageAt(systemMsg.CreatedAt).
		Exec(ctx)
	if err != nil {
		slog.Error("Failed to update chat with last message", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction for group creation", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	avatarURL := ""
	if avatarMedia != nil {
		avatarURL = s.storageAdapter.GetPublicURL(avatarMedia.FileName)
	}

	fullMsg, err := s.client.Message.Query().
		Where(message.ID(systemMsg.ID)).
		WithSender().
		Only(context.Background())

	var lastMsgResp *model.MessageResponse
	if err == nil {
		lastMsgResp = helper.ToMessageResponse(fullMsg, s.storageAdapter, nil, string(groupmember.RoleOwner))
	}

	var inviteExpiresAt *string
	if newGroupChat.InviteExpiresAt != nil {
		t := newGroupChat.InviteExpiresAt.Format(time.RFC3339)
		inviteExpiresAt = &t
	}

	myRole := string(groupmember.RoleOwner)
	chatListResponse := &model.ChatListResponse{
		ID:              newChat.ID,
		Type:            string(newChat.Type),
		Name:            newGroupChat.Name,
		Description:     newGroupChat.Description,
		IsPublic:        &newGroupChat.IsPublic,
		InviteCode:      &newGroupChat.InviteCode,
		InviteExpiresAt: inviteExpiresAt,
		Avatar:          avatarURL,
		LastMessage:     lastMsgResp,
		UnreadCount:     0,
		MyRole:          &myRole,
		MemberCount:     len(allMemberIDs),
	}

	if s.wsHub != nil {
		go func() {
			event := websocket.Event{
				Type:    websocket.EventChatNew,
				Payload: chatListResponse,
				Meta: &websocket.EventMeta{
					Timestamp: time.Now().UTC().UnixMilli(),
					ChatID:    newChat.ID,
					SenderID:  creatorID,
				},
			}

			for _, memberID := range allMemberIDs {

				s.wsHub.BroadcastToUser(memberID, event)
			}
		}()
	}

	return chatListResponse, nil
}

func (s *GroupChatService) UpdateGroupChat(ctx context.Context, requestorID uuid.UUID, groupID uuid.UUID, req model.UpdateGroupChatRequest, isAdmin bool) (*model.ChatListResponse, error) {
	if err := s.validator.Struct(req); err != nil {
		return nil, helper.NewBadRequestError("")
	}

	if req.Name != nil {
		trimmed := strings.TrimSpace(*req.Name)
		req.Name = &trimmed
	}
	if req.Description != nil {
		trimmed := strings.TrimSpace(*req.Description)
		req.Description = &trimmed
	}

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

	var requestorRole groupmember.Role
	if !isAdmin {
		requestorMember, err := tx.GroupMember.Query().
			Where(
				groupmember.GroupChatID(gc.ID),
				groupmember.UserID(requestorID),
			).
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				return nil, helper.NewForbiddenError("You are not a member of this group")
			}
			slog.Error("Failed to query requestor membership", "error", err)
			return nil, helper.NewInternalServerError("")
		}

		if requestorMember.Role != groupmember.RoleOwner && requestorMember.Role != groupmember.RoleAdmin {
			return nil, helper.NewForbiddenError("Only admins or owners can update group info")
		}
		requestorRole = requestorMember.Role
	} else {
		requestorRole = groupmember.RoleOwner
	}

	update := tx.GroupChat.UpdateOne(gc)
	var systemMessages []*ent.MessageCreate
	hasChanges := false

	if req.Name != nil && *req.Name != gc.Name {
		update.SetName(*req.Name)
		systemMessages = append(systemMessages, tx.Message.Create().
			SetChatID(gc.ChatID).
			SetSenderID(requestorID).
			SetType(message.TypeSystemRename).
			SetActionData(map[string]interface{}{
				"old_name": gc.Name,
				"new_name": *req.Name,
			}))
		hasChanges = true
	}

	if req.Description != nil {
		oldDesc := ""
		if gc.Description != nil {
			oldDesc = *gc.Description
		}
		if *req.Description != oldDesc {
			update.SetDescription(*req.Description)
			systemMessages = append(systemMessages, tx.Message.Create().
				SetChatID(gc.ChatID).
				SetSenderID(requestorID).
				SetType(message.TypeSystemDescription).
				SetActionData(map[string]interface{}{
					"old_description": oldDesc,
					"new_description": *req.Description,
				}))
			hasChanges = true
		}
	}

	if req.IsPublic != nil && *req.IsPublic != gc.IsPublic {
		update.SetIsPublic(*req.IsPublic)

		newVisibility := "private"
		if *req.IsPublic {
			newVisibility = "public"
			update.ClearInviteExpiresAt()
		} else {

			newCode, err := helper.GenerateRandomString(12)
			if err != nil {
				slog.Error("Failed to generate invite code while switching group to private", "error", err)
				return nil, helper.NewInternalServerError("")
			}
			update.SetInviteCode(newCode)
			update.SetInviteExpiresAt(time.Now().UTC().Add(7 * 24 * time.Hour))
		}

		systemMessages = append(systemMessages, tx.Message.Create().
			SetChatID(gc.ChatID).
			SetSenderID(requestorID).
			SetType(message.TypeSystemVisibility).
			SetActionData(map[string]interface{}{
				"new_visibility": newVisibility,
			}))
		hasChanges = true
	}

	var avatarMedia *ent.Media

	if req.DeleteAvatar && gc.Edges.Avatar != nil {
		update.ClearAvatar()
		systemMessages = append(systemMessages, tx.Message.Create().
			SetChatID(gc.ChatID).
			SetSenderID(requestorID).
			SetType(message.TypeSystemAvatar).
			SetActionData(map[string]interface{}{
				"action": "removed",
			}))
		hasChanges = true
	} else if req.AvatarMediaID != nil {
		avatarMedia, err = tx.Media.Query().
			Where(
				media.ID(*req.AvatarMediaID),
				media.CategoryEQ(media.CategoryGroupAvatar),
				media.UploadStatusEQ(media.UploadStatusCompleted),
				media.HasUploaderWith(user.ID(requestorID)),
				media.Not(media.HasUserAvatar()),
				media.Not(media.HasGroupAvatar()),
				media.MessageIDIsNil(),
			).
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				return nil, helper.NewBadRequestError("Invalid avatar media")
			}
			slog.Error("Failed to query group avatar media", "error", err, "mediaID", *req.AvatarMediaID)
			return nil, helper.NewInternalServerError("")
		}

		update.SetAvatar(avatarMedia)

		systemMessages = append(systemMessages, tx.Message.Create().
			SetChatID(gc.ChatID).
			SetSenderID(requestorID).
			SetType(message.TypeSystemAvatar).
			SetActionData(map[string]interface{}{
				"action": "updated",
			}))
		hasChanges = true
	}

	if !hasChanges {
		resp := s.buildGroupChatListResponse(ctx, gc, &requestorRole, nil)
		return &resp, nil
	}

	updatedGroup, err := update.Save(ctx)
	if err != nil {
		slog.Error("Failed to update group chat", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	var createdSystemMessages []*ent.Message
	if len(systemMessages) > 0 {
		msgs, err := tx.Message.CreateBulk(systemMessages...).Save(ctx)
		if err != nil {
			slog.Error("Failed to create system messages", "error", err)
			return nil, helper.NewInternalServerError("")
		}
		createdSystemMessages = msgs
		lastSystemMsg := msgs[len(msgs)-1]

		err = tx.Chat.UpdateOne(gc.Edges.Chat).
			SetLastMessage(lastSystemMsg).
			SetLastMessageAt(lastSystemMsg.CreatedAt).
			Exec(ctx)
		if err != nil {
			slog.Error("Failed to update chat last message", "error", err)
			return nil, helper.NewInternalServerError("")
		}
	}

	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if s.wsHub != nil && len(createdSystemMessages) > 0 {
		go func() {
			msgIDs := make([]uuid.UUID, 0, len(createdSystemMessages))
			for _, sysMsg := range createdSystemMessages {
				msgIDs = append(msgIDs, sysMsg.ID)
			}

			fullMsgs, err := s.client.Message.Query().
				Where(message.IDIn(msgIDs...)).
				WithSender().
				All(context.Background())
			if err != nil {
				slog.Error("Failed to fetch system messages for broadcast", "error", err, "messageIDs", msgIDs)
				return
			}

			fullMsgByID := make(map[uuid.UUID]*ent.Message, len(fullMsgs))
			for _, fullMsg := range fullMsgs {
				fullMsgByID[fullMsg.ID] = fullMsg
			}

			for _, sysMsg := range createdSystemMessages {
				fullMsg := fullMsgByID[sysMsg.ID]
				if fullMsg == nil {
					slog.Error("System message missing from broadcast query result", "messageID", sysMsg.ID)
					continue
				}

				msgResponse := helper.ToMessageResponse(fullMsg, s.storageAdapter, nil, string(requestorRole))

				s.wsHub.BroadcastToChat(gc.ChatID, websocket.Event{
					Type:    websocket.EventMessageNew,
					Payload: msgResponse,
					Meta: &websocket.EventMeta{
						Timestamp: time.Now().UTC().UnixMilli(),
						ChatID:    gc.ChatID,
						SenderID:  requestorID,
					},
				})
			}

			updatedGroupWithAvatar, err := s.client.GroupChat.Query().
				Where(groupchat.ID(updatedGroup.ID)).
				WithAvatar().
				Only(context.Background())

			if err != nil {
				slog.Error("Failed to fetch updated group for broadcast", "error", err)
				return
			}

			lastMsgResponse := helper.ToMessageResponse(fullMsgByID[createdSystemMessages[len(createdSystemMessages)-1].ID], s.storageAdapter, nil, string(requestorRole))
			members, err := s.client.GroupMember.Query().
				Where(groupmember.GroupChatID(gc.ID)).
				Select(groupmember.FieldUserID, groupmember.FieldRole).
				All(context.Background())

			if err != nil {
				slog.Error("Failed to fetch group members for chat update broadcast", "error", err, "groupID", gc.ID)
				return
			}

			for _, m := range members {
				role := m.Role
				payload := s.buildGroupChatListResponse(context.Background(), updatedGroupWithAvatar, &role, lastMsgResponse)
				s.wsHub.BroadcastToUser(m.UserID, websocket.Event{
					Type:    websocket.EventChatUpdate,
					Payload: payload,
					Meta: &websocket.EventMeta{
						Timestamp: time.Now().UTC().UnixMilli(),
						ChatID:    gc.ChatID,
						SenderID:  requestorID,
					},
				})
			}
		}()
	}

	updatedGroupWithAvatar, err := s.client.GroupChat.Query().
		Where(groupchat.ID(updatedGroup.ID)).
		WithAvatar().
		Only(context.Background())
	if err != nil {
		slog.Error("Failed to fetch updated group for response", "error", err, "groupID", updatedGroup.ID)
		return nil, helper.NewInternalServerError("")
	}
	var lastMsgResponse *model.MessageResponse
	if len(createdSystemMessages) > 0 {
		lastMsgResponse = s.getGroupLastMessageResponse(context.Background(), gc.ChatID)
	}
	resp := s.buildGroupChatListResponse(ctx, updatedGroupWithAvatar, &requestorRole, lastMsgResponse)
	return &resp, nil
}

func (s *GroupChatService) DeleteGroup(ctx context.Context, userID, groupID uuid.UUID, isAdmin bool) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		slog.Error("Failed to start transaction", "error", err)
		return helper.NewInternalServerError("")
	}
	defer tx.Rollback()

	gc, err := tx.GroupChat.Query().
		Where(
			groupchat.ChatID(groupID),
			groupchat.HasChatWith(chat.DeletedAtIsNil()),
		).
		WithChat().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return helper.NewNotFoundError("Group chat not found")
		}
		slog.Error("Failed to query group chat", "error", err)
		return helper.NewInternalServerError("")
	}

	if !isAdmin {
		member, err := tx.GroupMember.Query().
			Where(
				groupmember.GroupChatID(gc.ID),
				groupmember.UserID(userID),
			).
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				return helper.NewForbiddenError("You are not a member of this group")
			}
			slog.Error("Failed to query requestor membership", "error", err)
			return helper.NewInternalServerError("")
		}

		if member.Role != groupmember.RoleOwner {
			return helper.NewForbiddenError("Only owner can delete the group")
		}
	}

	err = tx.Chat.UpdateOneID(groupID).SetDeletedAt(time.Now().UTC()).Exec(ctx)
	if err != nil {
		slog.Error("Failed to soft delete chat", "error", err)
		return helper.NewInternalServerError("")
	}

	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		return helper.NewInternalServerError("")
	}

	if s.redisAdapter != nil {
		s.redisAdapter.Del(context.Background(), fmt.Sprintf("chat_members:%s", groupID))
	}

	members, err := s.client.GroupMember.Query().
		Where(groupmember.GroupChatID(gc.ID)).
		All(context.Background())
	if err != nil {
		slog.Error("Failed to fetch group members after delete", "error", err, "groupID", groupID)
		return nil
	}

	for _, m := range members {
		if s.wsHub != nil {
			s.wsHub.BroadcastToUser(m.UserID, websocket.Event{
				Type: websocket.EventChatDelete,
				Payload: map[string]interface{}{
					"chat_id": groupID,
				},
				Meta: &websocket.EventMeta{
					Timestamp: time.Now().UTC().UnixMilli(),
					ChatID:    groupID,
					SenderID:  userID,
				},
			})
		}
	}

	return nil
}
