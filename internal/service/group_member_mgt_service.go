package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/ent/userblock"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/websocket"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

func (s *GroupChatService) AddMember(ctx context.Context, requestorID uuid.UUID, groupID uuid.UUID, req model.AddGroupMemberRequest) ([]*model.MessageResponse, error) {
	if err := s.validator.Struct(req); err != nil {
		return nil, helper.NewBadRequestError("")
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
		return nil, helper.NewForbiddenError("Only admins or owners can add members")
	}

	targetUsers, err := tx.User.Query().
		Where(
			user.IDIn(req.UserIDs...),
			user.DeletedAtIsNil(),
		).
		Select(user.FieldID, user.FieldIsBanned, user.FieldBannedUntil, user.FieldFullName).
		All(ctx)
	if err != nil {
		slog.Error("Failed to query target users", "error", err)
		return nil, helper.NewInternalServerError("")
	}
	if len(targetUsers) != len(req.UserIDs) {
		return nil, helper.NewNotFoundError("One or more users not found or deleted")
	}

	var targetUserIDs []uuid.UUID
	for _, u := range targetUsers {

		if u.IsBanned {
			if u.BannedUntil == nil || time.Now().UTC().Before(*u.BannedUntil) {
				return nil, helper.NewForbiddenError("Cannot add suspended/banned user to group")
			}
		}
		targetUserIDs = append(targetUserIDs, u.ID)
	}

	existingMembers, err := tx.GroupMember.Query().
		Where(
			groupmember.GroupChatID(gc.ID),
			groupmember.UserIDIn(targetUserIDs...),
		).
		All(ctx)
	if err != nil {
		slog.Error("Failed to check existing memberships", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	existingMemberIDs := make(map[uuid.UUID]bool)
	for _, m := range existingMembers {
		existingMemberIDs[m.UserID] = true
	}

	blocked, err := tx.UserBlock.Query().
		Where(
			userblock.Or(
				userblock.And(userblock.BlockerID(requestorID), userblock.BlockedIDIn(targetUserIDs...)),
				userblock.And(userblock.BlockerIDIn(targetUserIDs...), userblock.BlockedID(requestorID)),
			),
		).
		Exist(ctx)
	if err != nil {
		slog.Error("Failed to check block status", "error", err)
		return nil, helper.NewInternalServerError("")
	}
	if blocked {
		return nil, helper.NewForbiddenError("Cannot add a blocked user")
	}

	var memberCreates []*ent.GroupMemberCreate
	var newMembers []*ent.User

	for _, u := range targetUsers {
		if existingMemberIDs[u.ID] {
			continue
		}
		memberCreates = append(memberCreates, tx.GroupMember.Create().
			SetGroupChat(gc).
			SetUser(u).
			SetRole(groupmember.RoleMember))
		newMembers = append(newMembers, u)
	}

	if len(memberCreates) == 0 {
		return nil, helper.NewConflictError("All users are already members")
	}

	_, err = tx.GroupMember.CreateBulk(memberCreates...).Save(ctx)
	if err != nil {
		slog.Error("Failed to add group members", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	var msgCreates []*ent.MessageCreate
	for _, u := range newMembers {
		msgCreates = append(msgCreates, tx.Message.Create().
			SetChatID(gc.ChatID).
			SetSenderID(requestorID).
			SetType(message.TypeSystemAdd).
			SetActionData(map[string]interface{}{
				"target_id": u.ID,
				"actor_id":  requestorID,
			}))
	}

	msgs, err := tx.Message.CreateBulk(msgCreates...).Save(ctx)
	if err != nil {
		slog.Error("Failed to create system messages", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	lastSystemMsg := msgs[len(msgs)-1]

	err = tx.Chat.UpdateOne(gc.Edges.Chat).
		SetLastMessage(lastSystemMsg).
		SetLastMessageAt(lastSystemMsg.CreatedAt).
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

	memberCount, err := s.client.GroupMember.Query().
		Where(
			groupmember.GroupChatID(gc.ID),
			groupmember.HasUserWith(user.DeletedAtIsNil()),
		).
		Count(ctx)
	if err != nil {
		slog.Error("Failed to count group members after add", "error", err)
	}

	var responses []*model.MessageResponse
	var msgIDs []uuid.UUID
	for _, m := range msgs {
		msgIDs = append(msgIDs, m.ID)
	}

	fullMsgs, err := s.client.Message.Query().
		Where(message.IDIn(msgIDs...)).
		WithSender().
		Order(ent.Asc(message.FieldID)).
		All(ctx)

	if err == nil {
		for i, fullMsg := range fullMsgs {
			msgResponse := helper.ToMessageResponse(fullMsg, s.storageAdapter, nil, string(requestorMember.Role))
			msgResponse.MemberCount = &memberCount

			if i < len(newMembers) {
				targetUser := newMembers[i]
				if targetUser.FullName != nil {
					if msgResponse.ActionData == nil {
						msgResponse.ActionData = make(map[string]interface{})
					}
					msgResponse.ActionData["target_name"] = *targetUser.FullName
				}
			}
			responses = append(responses, msgResponse)
		}
	}

	if s.wsHub != nil {
		go func() {
			for _, resp := range responses {
				s.wsHub.BroadcastToChat(gc.ChatID, websocket.Event{
					Type:    websocket.EventMessageNew,
					Payload: resp,
					Meta: &websocket.EventMeta{
						Timestamp: time.Now().UTC().UnixMilli(),
						ChatID:    gc.ChatID,
						SenderID:  requestorID,
					},
				})
			}

			if len(responses) > 0 {
				lastMsgResponse := responses[len(responses)-1]
				memberRole := groupmember.RoleMember

				for _, u := range newMembers {
					chatPayload := s.buildGroupChatListResponse(context.Background(), gc, &memberRole, lastMsgResponse)
					chatPayload.UnreadCount = 1
					chatPayload.MemberCount = memberCount

					s.wsHub.BroadcastToUser(u.ID, websocket.Event{
						Type:    websocket.EventChatNew,
						Payload: chatPayload,
						Meta: &websocket.EventMeta{
							Timestamp: time.Now().UTC().UnixMilli(),
							ChatID:    groupID,
							SenderID:  requestorID,
						},
					})
				}
			}
		}()
	}

	return responses, nil
}

func (s *GroupChatService) LeaveGroup(ctx context.Context, userID uuid.UUID, groupID uuid.UUID) (*model.MessageResponse, error) {
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
		WithChat().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("Group chat not found")
		}
		slog.Error("Failed to query group chat", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	member, err := tx.GroupMember.Query().
		Where(
			groupmember.GroupChatID(gc.ID),
			groupmember.UserID(userID),
		).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewBadRequestError("You are not a member of this group")
		}
		slog.Error("Failed to query membership", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if member.Role == groupmember.RoleOwner {
		return nil, helper.NewBadRequestError("Owner cannot leave the group. Please transfer ownership first.")
	}

	err = tx.GroupMember.DeleteOne(member).Exec(ctx)
	if err != nil {
		slog.Error("Failed to delete group member", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	systemMsg, err := tx.Message.Create().
		SetChatID(gc.ChatID).
		SetSenderID(userID).
		SetType(message.TypeSystemLeave).
		Save(ctx)
	if err != nil {
		slog.Error("Failed to create system message for leave", "error", err)
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

	memberCount, err := s.client.GroupMember.Query().
		Where(
			groupmember.GroupChatID(gc.ID),
			groupmember.HasUserWith(user.DeletedAtIsNil()),
		).
		Count(ctx)
	if err != nil {
		slog.Error("Failed to count group members after leave", "error", err)
	}

	fullMsg, err := s.client.Message.Query().
		Where(message.ID(systemMsg.ID)).
		WithSender().
		Only(ctx)

	var msgResponse *model.MessageResponse
	if err == nil {
		msgResponse = helper.ToMessageResponse(fullMsg, s.storageAdapter, nil, string(groupmember.RoleMember))
		msgResponse.MemberCount = &memberCount
	}

	if s.wsHub != nil && msgResponse != nil {
		go func() {
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

	return msgResponse, nil
}

func (s *GroupChatService) KickMember(ctx context.Context, requestorID uuid.UUID, groupID uuid.UUID, targetUserID uuid.UUID) (*model.MessageResponse, error) {
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

	if requestorMember.Role == groupmember.RoleMember {
		return nil, helper.NewForbiddenError("Only admins or owners can kick members")
	}

	targetMember, err := tx.GroupMember.Query().
		Where(
			groupmember.GroupChatID(gc.ID),
			groupmember.UserID(targetUserID),
		).
		WithUser(func(q *ent.UserQuery) {
			q.Select(user.FieldID, user.FieldFullName)
		}).
		Only(ctx)
	if err != nil {
		return nil, helper.NewNotFoundError("Target user is not a member of this group")
	}

	if targetMember.UserID == requestorID {
		return nil, helper.NewBadRequestError("Cannot kick yourself")
	}

	if requestorMember.Role == groupmember.RoleAdmin {
		if targetMember.Role == groupmember.RoleAdmin || targetMember.Role == groupmember.RoleOwner {
			return nil, helper.NewForbiddenError("Admins cannot kick other admins or the owner")
		}
	}

	err = tx.GroupMember.DeleteOne(targetMember).Exec(ctx)
	if err != nil {
		slog.Error("Failed to delete group member", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	systemMsg, err := tx.Message.Create().
		SetChatID(gc.ChatID).
		SetSenderID(requestorID).
		SetType(message.TypeSystemKick).
		SetActionData(map[string]interface{}{
			"target_id": targetMember.UserID,
			"actor_id":  requestorID,
		}).
		Save(ctx)
	if err != nil {
		slog.Error("Failed to create system message for kick", "error", err)
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

	memberCount, err := s.client.GroupMember.Query().
		Where(
			groupmember.GroupChatID(gc.ID),
			groupmember.HasUserWith(user.DeletedAtIsNil()),
		).
		Count(ctx)
	if err != nil {
		slog.Error("Failed to count group members after kick", "error", err)
	}

	fullMsg, err := s.client.Message.Query().
		Where(message.ID(systemMsg.ID)).
		WithSender().
		Only(ctx)

	var msgResponse *model.MessageResponse
	if err == nil {
		msgResponse = helper.ToMessageResponse(fullMsg, s.storageAdapter, nil, string(requestorMember.Role))
		msgResponse.MemberCount = &memberCount
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

			s.wsHub.BroadcastToUser(targetUserID, websocket.Event{
				Type: websocket.EventChatDelete,
				Payload: map[string]interface{}{
					"chat_id": groupID,
				},
				Meta: &websocket.EventMeta{
					Timestamp: time.Now().UTC().UnixMilli(),
					ChatID:    groupID,
					SenderID:  requestorID,
				},
			})
		}()
	}

	return msgResponse, nil
}
