package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
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
	"github.com/redis/go-redis/v9"
)

type ChatService struct {
	client         *ent.Client
	repo           *repository.Repository
	cfg            *config.AppConfig
	validator      *validator.Validate
	wsHub          *websocket.Hub
	storageAdapter *adapter.StorageAdapter
	redisAdapter   *adapter.RedisAdapter
}

func NewChatService(client *ent.Client, repo *repository.Repository, cfg *config.AppConfig, validator *validator.Validate, wsHub *websocket.Hub, storageAdapter *adapter.StorageAdapter, redisAdapter *adapter.RedisAdapter) *ChatService {
	return &ChatService{
		client:         client,
		repo:           repo,
		cfg:            cfg,
		validator:      validator,
		wsHub:          wsHub,
		storageAdapter: storageAdapter,
		redisAdapter:   redisAdapter,
	}
}

func (s *ChatService) GetChatByID(ctx context.Context, userID, chatID uuid.UUID) (*model.ChatListResponse, error) {
	c, err := s.repo.Chat.GetChatByID(ctx, userID, chatID)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("")
		}
		slog.Error("Failed to get chat by ID", "error", err, "chatID", chatID)
		return nil, helper.NewInternalServerError("")
	}

	if c.Type == chat.TypeGroup && c.Edges.GroupChat != nil && c.Edges.GroupChat.IsPublic && len(c.Edges.GroupChat.Edges.Members) == 0 {
		c.Edges.LastMessage = nil
	}

	blockedMap := make(map[uuid.UUID]helper.BlockStatus)
	var otherUserID uuid.UUID

	if c.Type == chat.TypePrivate && c.Edges.PrivateChat != nil {
		if c.Edges.PrivateChat.User1ID != nil && *c.Edges.PrivateChat.User1ID == userID {
			if c.Edges.PrivateChat.User2ID != nil {
				otherUserID = *c.Edges.PrivateChat.User2ID
			}
		} else if c.Edges.PrivateChat.User1ID != nil {
			otherUserID = *c.Edges.PrivateChat.User1ID
		}

		blocks, err := s.client.UserBlock.Query().
			Where(
				userblock.Or(
					userblock.And(userblock.BlockerID(userID), userblock.BlockedID(otherUserID)),
					userblock.And(userblock.BlockerID(otherUserID), userblock.BlockedID(userID)),
				),
			).
			All(ctx)

		if err == nil {
			for _, b := range blocks {
				status := blockedMap[otherUserID]
				if b.BlockerID == userID {
					status.BlockedByMe = true
				} else {
					status.BlockedByOther = true
				}
				blockedMap[otherUserID] = status
			}
		}
	}

	onlineMap := make(map[uuid.UUID]bool)
	if otherUserID != uuid.Nil {
		key := fmt.Sprintf("online:%s", otherUserID)
		exists, _ := s.redisAdapter.Client().Exists(ctx, key).Result()
		onlineMap[otherUserID] = exists > 0
	}

	resp := helper.MapChatToResponse(userID, c, blockedMap, onlineMap, s.storageAdapter)
	if resp != nil && c.Type == chat.TypeGroup && c.Edges.GroupChat != nil {
		memberCount, err := s.client.GroupMember.Query().
			Where(groupmember.GroupChatID(c.Edges.GroupChat.ID), groupmember.HasUserWith(user.DeletedAtIsNil())).
			Count(ctx)
		if err == nil {
			resp.MemberCount = memberCount
		} else {
			slog.Error("Failed to count group members", "error", err, "groupID", c.Edges.GroupChat.ID)
		}
	}

	if resp != nil {

		if resp.LastMessage != nil && resp.LastMessage.ActionData != nil {
			userIDsToResolve := make(map[uuid.UUID]bool)
			if targetIDStr, ok := resp.LastMessage.ActionData["target_id"].(string); ok {
				if id, err := uuid.Parse(targetIDStr); err == nil {
					userIDsToResolve[id] = true
				}
			}
			if actorIDStr, ok := resp.LastMessage.ActionData["actor_id"].(string); ok {
				if id, err := uuid.Parse(actorIDStr); err == nil {
					userIDsToResolve[id] = true
				}
			}

			if len(userIDsToResolve) > 0 {
				ids := make([]uuid.UUID, 0, len(userIDsToResolve))
				for id := range userIDsToResolve {
					ids = append(ids, id)
				}
				users, err := s.client.User.Query().
					Where(user.IDIn(ids...)).
					Select(user.FieldID, user.FieldFullName, user.FieldDeletedAt).
					All(ctx)

				if err == nil {
					userMap := make(map[uuid.UUID]*ent.User)
					for _, u := range users {
						userMap[u.ID] = u
					}

					if targetIDStr, ok := resp.LastMessage.ActionData["target_id"].(string); ok {
						if id, err := uuid.Parse(targetIDStr); err == nil {
							if u, exists := userMap[id]; exists {
								if u.DeletedAt != nil {
									delete(resp.LastMessage.ActionData, "target_id")
									resp.LastMessage.ActionData["target_name"] = "Deleted User"
								} else if u.FullName != nil {
									resp.LastMessage.ActionData["target_name"] = *u.FullName
								}
							}
						}
					}
					if actorIDStr, ok := resp.LastMessage.ActionData["actor_id"].(string); ok {
						if id, err := uuid.Parse(actorIDStr); err == nil {
							if u, exists := userMap[id]; exists {
								if u.DeletedAt != nil {
									delete(resp.LastMessage.ActionData, "actor_id")
									resp.LastMessage.ActionData["actor_name"] = "Deleted User"
								} else if u.FullName != nil {
									resp.LastMessage.ActionData["actor_name"] = *u.FullName
								}
							}
						}
					}
				}
			}
		}

		if c.Type == chat.TypeGroup && c.Edges.GroupChat != nil {
			count, err := s.client.GroupMember.Query().
				Where(
					groupmember.GroupChatID(c.Edges.GroupChat.ID),
					groupmember.HasUserWith(user.DeletedAtIsNil()),
				).
				Count(ctx)
			if err == nil {
				resp.MemberCount = count
			} else {
				slog.Error("Failed to count group members", "error", err)
			}
		}
	}

	return resp, nil
}

func (s *ChatService) GetChats(ctx context.Context, userID uuid.UUID, req model.GetChatsRequest) ([]model.ChatListResponse, string, bool, error) {
	if err := s.validator.Struct(req); err != nil {
		return nil, "", false, helper.NewBadRequestError("")
	}

	if req.Limit == 0 {
		req.Limit = 20
	}

	req.Query = strings.TrimSpace(req.Query)

	chats, nextCursor, hasNext, err := s.repo.Chat.GetChats(ctx, userID, req.Query, req.Cursor, req.Limit)
	if err != nil {
		slog.Error("Failed to get chats", "error", err)
		return nil, "", false, helper.NewInternalServerError("")
	}

	otherUserIDs := make([]uuid.UUID, 0)
	for _, c := range chats {
		if c.Type == chat.TypePrivate && c.Edges.PrivateChat != nil {
			if c.Edges.PrivateChat.User1ID != nil && *c.Edges.PrivateChat.User1ID == userID {
				if c.Edges.PrivateChat.User2ID != nil {
					otherUserIDs = append(otherUserIDs, *c.Edges.PrivateChat.User2ID)
				}
			} else if c.Edges.PrivateChat.User1ID != nil {
				otherUserIDs = append(otherUserIDs, *c.Edges.PrivateChat.User1ID)
			}
		}
	}

	blockedMap := make(map[uuid.UUID]helper.BlockStatus)
	if len(otherUserIDs) > 0 {
		blocks, err := s.client.UserBlock.Query().
			Where(
				userblock.Or(
					userblock.And(userblock.BlockerID(userID), userblock.BlockedIDIn(otherUserIDs...)),
					userblock.And(userblock.BlockerIDIn(otherUserIDs...), userblock.BlockedID(userID)),
				),
			).
			All(ctx)
		if err == nil {
			for _, b := range blocks {
				status := blockedMap[uuid.Nil]
				if b.BlockerID == userID {
					status = blockedMap[b.BlockedID]
					status.BlockedByMe = true
					blockedMap[b.BlockedID] = status
				} else {
					status = blockedMap[b.BlockerID]
					status.BlockedByOther = true
					blockedMap[b.BlockerID] = status
				}
			}
		}
	}

	onlineMap := make(map[uuid.UUID]bool)
	if len(otherUserIDs) > 0 {
		results, err := s.redisAdapter.Client().Pipelined(ctx, func(pipe redis.Pipeliner) error {
			for _, id := range otherUserIDs {
				key := fmt.Sprintf("online:%s", id)
				pipe.Exists(ctx, key)
			}
			return nil
		})
		if err == nil {
			for i, res := range results {
				if intCmd, ok := res.(*redis.IntCmd); ok {
					onlineMap[otherUserIDs[i]] = intCmd.Val() > 0
				}
			}
		}
	}

	userIDsToResolve := make(map[uuid.UUID]bool)
	for _, c := range chats {
		if c.Edges.LastMessage != nil && c.Edges.LastMessage.ActionData != nil {
			if targetIDStr, ok := c.Edges.LastMessage.ActionData["target_id"].(string); ok {
				if id, err := uuid.Parse(targetIDStr); err == nil {
					userIDsToResolve[id] = true
				}
			}
			if actorIDStr, ok := c.Edges.LastMessage.ActionData["actor_id"].(string); ok {
				if id, err := uuid.Parse(actorIDStr); err == nil {
					userIDsToResolve[id] = true
				}
			}
		}
	}

	userMap := make(map[uuid.UUID]*ent.User)
	if len(userIDsToResolve) > 0 {
		ids := make([]uuid.UUID, 0, len(userIDsToResolve))
		for id := range userIDsToResolve {
			ids = append(ids, id)
		}
		users, err := s.client.User.Query().
			Where(user.IDIn(ids...)).
			Select(user.FieldID, user.FieldFullName, user.FieldDeletedAt).
			All(ctx)
		if err == nil {
			for _, u := range users {
				userMap[u.ID] = u
			}
		}
	}

	memberCounts := make(map[uuid.UUID]int)
	groupIDs := make([]uuid.UUID, 0)
	for _, c := range chats {
		if c.Type == chat.TypeGroup && c.Edges.GroupChat != nil {
			groupIDs = append(groupIDs, c.Edges.GroupChat.ID)
		}
	}
	if len(groupIDs) > 0 {
		members, err := s.client.GroupMember.Query().
			Where(groupmember.GroupChatIDIn(groupIDs...), groupmember.HasUserWith(user.DeletedAtIsNil())).
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

	response := make([]model.ChatListResponse, 0)
	for _, c := range chats {
		resp := helper.MapChatToResponse(userID, c, blockedMap, onlineMap, s.storageAdapter)
		if resp != nil {
			if c.Type == chat.TypeGroup && c.Edges.GroupChat != nil {
				resp.MemberCount = memberCounts[c.Edges.GroupChat.ID]
			}

			if resp.LastMessage != nil && resp.LastMessage.ActionData != nil {
				if targetIDStr, ok := resp.LastMessage.ActionData["target_id"].(string); ok {
					if id, err := uuid.Parse(targetIDStr); err == nil {
						if u, exists := userMap[id]; exists {
							if u.DeletedAt != nil {
								delete(resp.LastMessage.ActionData, "target_id")
								resp.LastMessage.ActionData["target_name"] = "Deleted User"
							} else if u.FullName != nil {
								resp.LastMessage.ActionData["target_name"] = *u.FullName
							}
						}
					}
				}
				if actorIDStr, ok := resp.LastMessage.ActionData["actor_id"].(string); ok {
					if id, err := uuid.Parse(actorIDStr); err == nil {
						if u, exists := userMap[id]; exists {
							if u.DeletedAt != nil {
								delete(resp.LastMessage.ActionData, "actor_id")
								resp.LastMessage.ActionData["actor_name"] = "Deleted User"
							} else if u.FullName != nil {
								resp.LastMessage.ActionData["actor_name"] = *u.FullName
							}
						}
					}
				}
			}
			response = append(response, *resp)
		}
	}

	return response, nextCursor, hasNext, nil
}

func (s *ChatService) MarkAsRead(ctx context.Context, userID uuid.UUID, chatID uuid.UUID) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		slog.Error("Failed to start transaction", "error", err)
		return helper.NewInternalServerError("")
	}
	defer tx.Rollback()

	c, err := tx.Chat.Query().
		Where(
			chat.ID(chatID),
			chat.DeletedAtIsNil(),
		).
		ForUpdate().
		WithPrivateChat().
		WithGroupChat().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return helper.NewNotFoundError("")
		}
		slog.Error("Failed to query chat for update", "error", err)
		return helper.NewInternalServerError("")
	}

	var isBlocked bool
	var otherUserID uuid.UUID

	if c.Type == chat.TypePrivate && c.Edges.PrivateChat != nil {
		pc := c.Edges.PrivateChat
		update := tx.PrivateChat.UpdateOneID(pc.ID)

		if pc.User1ID != nil && *pc.User1ID == userID {
			if pc.User1UnreadCount == 0 {
				return nil
			}
			if pc.User2ID != nil {
				otherUserID = *pc.User2ID
			}
			update.SetUser1UnreadCount(0)
		} else if pc.User2ID != nil && *pc.User2ID == userID {
			if pc.User2UnreadCount == 0 {
				return nil
			}
			if pc.User1ID != nil {
				otherUserID = *pc.User1ID
			}
			update.SetUser2UnreadCount(0)
		} else {
			return helper.NewForbiddenError("")
		}

		blockExists, err := tx.UserBlock.Query().
			Where(
				userblock.Or(
					userblock.And(userblock.BlockerID(userID), userblock.BlockedID(otherUserID)),
					userblock.And(userblock.BlockerID(otherUserID), userblock.BlockedID(userID)),
				),
			).
			Exist(ctx)

		if err != nil {
			slog.Error("Failed to check block status in MarkAsRead", "error", err)
			return helper.NewServiceUnavailableError("Unable to verify block status")
		}
		isBlocked = blockExists

		if !isBlocked {
			if pc.User1ID != nil && *pc.User1ID == userID {
				update.SetUser1LastReadAt(time.Now().UTC())
			} else {
				update.SetUser2LastReadAt(time.Now().UTC())
			}
		}

		if err := update.Exec(ctx); err != nil {
			slog.Error("Failed to mark private chat as read", "error", err)
			return helper.NewInternalServerError("")
		}

	} else if c.Type == chat.TypeGroup && c.Edges.GroupChat != nil {
		member, err := tx.GroupMember.Query().
			Where(
				groupmember.GroupChatID(c.Edges.GroupChat.ID),
				groupmember.UserID(userID),
			).
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				return helper.NewForbiddenError("Not a member of this group")
			}
			slog.Error("Failed to query group member", "error", err)
			return helper.NewInternalServerError("")
		}

		if member.UnreadCount == 0 {
			return nil
		}

		err = tx.GroupMember.UpdateOne(member).
			SetUnreadCount(0).
			SetLastReadAt(time.Now().UTC()).
			Exec(ctx)
		if err != nil {
			slog.Error("Failed to mark group chat as read", "error", err)
			return helper.NewInternalServerError("")
		}
	}

	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		return helper.NewInternalServerError("")
	}

	if s.wsHub != nil && !isBlocked {
		go s.wsHub.BroadcastToChat(chatID, websocket.Event{
			Type: websocket.EventChatRead,
			Payload: map[string]interface{}{
				"chat_id": chatID,
				"user_id": userID,
			},
			Meta: &websocket.EventMeta{
				Timestamp: time.Now().UTC().UnixMilli(),
				ChatID:    chatID,
				SenderID:  userID,
			},
		})
	}

	return nil
}

func (s *ChatService) HideChat(ctx context.Context, userID uuid.UUID, chatID uuid.UUID) error {
	c, err := s.client.Chat.Query().
		Where(
			chat.ID(chatID),
			chat.DeletedAtIsNil(),
		).
		WithPrivateChat().
		Only(ctx)

	if err != nil {
		if ent.IsNotFound(err) {
			return helper.NewNotFoundError("")
		}
		slog.Error("Failed to query chat", "error", err, "chatID", chatID)
		return helper.NewInternalServerError("")
	}

	if c.Type != chat.TypePrivate {
		return helper.NewBadRequestError("")
	}

	if c.Edges.PrivateChat == nil {
		return helper.NewInternalServerError("")
	}

	pc := c.Edges.PrivateChat
	update := s.client.PrivateChat.UpdateOneID(pc.ID)

	if pc.User1ID != nil && *pc.User1ID == userID {
		update.SetUser1HiddenAt(time.Now().UTC()).SetUser1UnreadCount(0)
	} else if pc.User2ID != nil && *pc.User2ID == userID {
		update.SetUser2HiddenAt(time.Now().UTC()).SetUser2UnreadCount(0)
	} else {
		return helper.NewForbiddenError("")
	}

	if err := update.Exec(ctx); err != nil {
		slog.Error("Failed to hide chat", "error", err, "chatID", chatID)
		return helper.NewInternalServerError("")
	}

	if s.wsHub != nil {
		go s.wsHub.BroadcastToUser(userID, websocket.Event{
			Type: websocket.EventChatHide,
			Payload: map[string]interface{}{
				"chat_id": chatID,
			},
			Meta: &websocket.EventMeta{
				Timestamp: time.Now().UTC().UnixMilli(),
				ChatID:    chatID,
				SenderID:  userID,
			},
		})
	}

	return nil
}
