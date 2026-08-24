package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/ent/userblock"
	"AtoiTalkAPI/internal/adapter"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/websocket"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

type PrivateChatService struct {
	client         *ent.Client
	cfg            *config.AppConfig
	validator      *validator.Validate
	wsHub          websocket.Publisher
	redisAdapter   privateChatCache
	storageAdapter privateChatStorage
}

type privateChatCache = adapter.RedisCache
type privateChatStorage = adapter.PublicURLGenerator

func NewPrivateChatService(client *ent.Client, cfg *config.AppConfig, validator *validator.Validate, wsHub websocket.Publisher, redisAdapter privateChatCache, storageAdapter privateChatStorage) *PrivateChatService {
	return &PrivateChatService{
		client:         client,
		cfg:            cfg,
		validator:      validator,
		wsHub:          wsHub,
		redisAdapter:   redisAdapter,
		storageAdapter: storageAdapter,
	}
}

func (s *PrivateChatService) findExistingPrivateChat(ctx context.Context, userID uuid.UUID, targetUserID uuid.UUID) (*ent.PrivateChat, error) {
	return s.client.PrivateChat.Query().
		Where(
			privatechat.Or(
				privatechat.And(
					privatechat.User1ID(userID),
					privatechat.User2ID(targetUserID),
				),
				privatechat.And(
					privatechat.User1ID(targetUserID),
					privatechat.User2ID(userID),
				),
			),
		).
		WithChat().
		Only(ctx)
}

func privateChatToResponse(pc *ent.PrivateChat) *model.ChatResponse {
	if pc == nil || pc.Edges.Chat == nil {
		return nil
	}

	return &model.ChatResponse{
		ID:        pc.Edges.Chat.ID,
		Type:      string(pc.Edges.Chat.Type),
		CreatedAt: pc.Edges.Chat.CreatedAt.Format(time.RFC3339),
	}
}

func (s *PrivateChatService) CreatePrivateChat(ctx context.Context, userID uuid.UUID, req model.CreatePrivateChatRequest) (*model.ChatResponse, error) {
	if err := s.validator.Struct(req); err != nil {
		slog.Warn("Validation failed", "error", err, "userID", userID)
		return nil, helper.NewBadRequestError("")
	}

	if userID == req.TargetUserID {
		return nil, helper.NewBadRequestError("Cannot chat with yourself")
	}

	users, err := s.client.User.Query().
		Where(
			user.IDIn(userID, req.TargetUserID),
			user.DeletedAtIsNil(),
		).
		WithAvatar().
		Select(user.FieldID, user.FieldFullName, user.FieldIsBanned, user.FieldBannedUntil, user.FieldAvatarID).
		All(ctx)
	if err != nil {
		slog.Error("Failed to query users for private chat", "error", err)
		return nil, helper.NewInternalServerError("")
	}
	if len(users) != 2 {
		return nil, helper.NewNotFoundError("One or both users not found")
	}

	var creator, targetUser *ent.User
	for _, u := range users {
		switch u.ID {
		case userID:
			creator = u
		case req.TargetUserID:
			targetUser = u
		}
	}

	if creator == nil || targetUser == nil {
		return nil, helper.NewNotFoundError("User not found")
	}

	if targetUser.IsBanned {
		if targetUser.BannedUntil == nil || time.Now().UTC().Before(*targetUser.BannedUntil) {
			return nil, helper.NewForbiddenError("User is currently suspended/banned")
		}
	}

	isBlocked, err := s.client.UserBlock.Query().
		Where(
			userblock.Or(
				userblock.And(
					userblock.BlockerID(userID),
					userblock.BlockedID(req.TargetUserID),
				),
				userblock.And(
					userblock.BlockerID(req.TargetUserID),
					userblock.BlockedID(userID),
				),
			),
		).
		Exist(ctx)
	if err != nil {
		slog.Error("Failed to check block status", "error", err)
		return nil, helper.NewInternalServerError("")
	}
	if isBlocked {
		return nil, helper.NewForbiddenError("Cannot create chat with blocked user")
	}

	existingChat, err := s.findExistingPrivateChat(ctx, userID, req.TargetUserID)

	if err == nil {
		return privateChatToResponse(existingChat), nil
	} else if !ent.IsNotFound(err) {
		slog.Error("Failed to check existing private chat", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		slog.Error("Failed to start transaction", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	defer func() {
		_ = tx.Rollback()
		if v := recover(); v != nil {
			panic(v)
		}
	}()

	newChat, err := tx.Chat.Create().
		SetType(chat.TypePrivate).
		Save(ctx)
	if err != nil {
		slog.Error("Failed to create chat", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	_, err = tx.PrivateChat.Create().
		SetChat(newChat).
		SetUser1ID(userID).
		SetUser2ID(req.TargetUserID).
		Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			for i := 0; i < 3; i++ {
				if existing, findErr := s.findExistingPrivateChat(ctx, userID, req.TargetUserID); findErr == nil {
					return privateChatToResponse(existing), nil
				}
				time.Sleep(20 * time.Millisecond)
			}
			return nil, helper.NewConflictError("Private chat already exists")
		}
		slog.Error("Failed to create private chat", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	_ = s.redisAdapter.Del(context.Background(), fmt.Sprintf("contacts:%s", userID))
	_ = s.redisAdapter.Del(context.Background(), fmt.Sprintf("contacts:%s", req.TargetUserID))

	if s.wsHub != nil {
		go func() {
			creatorAvatarURL := ""
			if creator.Edges.Avatar != nil {
				creatorAvatarURL = s.storageAdapter.GetPublicURL(creator.Edges.Avatar.FileName)
			}

			creatorName := ""
			if creator.FullName != nil {
				creatorName = *creator.FullName
			}

			keyCreator := fmt.Sprintf("online:%s", creator.ID)
			creatorIsOnline, _ := s.redisAdapter.Exists(context.Background(), keyCreator)

			payloadForTarget := model.ChatListResponse{
				ID:          newChat.ID,
				Type:        string(newChat.Type),
				Name:        creatorName,
				Avatar:      creatorAvatarURL,
				LastMessage: nil,
				UnreadCount: 0,
				IsOnline:    creatorIsOnline,
				OtherUserID: &creator.ID,
			}
			s.wsHub.BroadcastToUser(targetUser.ID, websocket.Event{
				Type:    websocket.EventChatNew,
				Payload: payloadForTarget,
				Meta:    &websocket.EventMeta{Timestamp: time.Now().UTC().UnixMilli(), ChatID: newChat.ID, SenderID: userID},
			})

			targetAvatarURL := ""
			if targetUser.Edges.Avatar != nil {
				targetAvatarURL = s.storageAdapter.GetPublicURL(targetUser.Edges.Avatar.FileName)
			}

			targetName := ""
			if targetUser.FullName != nil {
				targetName = *targetUser.FullName
			}

			keyTarget := fmt.Sprintf("online:%s", targetUser.ID)
			targetUserIsOnline, _ := s.redisAdapter.Exists(context.Background(), keyTarget)

			payloadForCreator := model.ChatListResponse{
				ID:          newChat.ID,
				Type:        string(newChat.Type),
				Name:        targetName,
				Avatar:      targetAvatarURL,
				LastMessage: nil,
				UnreadCount: 0,
				IsOnline:    targetUserIsOnline,
				OtherUserID: &targetUser.ID,
			}
			s.wsHub.BroadcastToUser(creator.ID, websocket.Event{
				Type:    websocket.EventChatNew,
				Payload: payloadForCreator,
				Meta:    &websocket.EventMeta{Timestamp: time.Now().UTC().UnixMilli(), ChatID: newChat.ID, SenderID: userID},
			})
		}()
	}

	return &model.ChatResponse{
		ID:        newChat.ID,
		Type:      string(newChat.Type),
		CreatedAt: newChat.CreatedAt.Format(time.RFC3339),
	}, nil
}
