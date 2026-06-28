package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/ent/privatechat"
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

type UserService struct {
	client         *ent.Client
	repo           *repository.Repository
	cfg            *config.AppConfig
	validator      *validator.Validate
	storageAdapter *adapter.StorageAdapter
	wsHub          *websocket.Hub
	redisAdapter   *adapter.RedisAdapter
}

func NewUserService(client *ent.Client, repo *repository.Repository, cfg *config.AppConfig, validator *validator.Validate, storageAdapter *adapter.StorageAdapter, wsHub *websocket.Hub, redisAdapter *adapter.RedisAdapter) *UserService {
	return &UserService{
		client:         client,
		repo:           repo,
		cfg:            cfg,
		validator:      validator,
		storageAdapter: storageAdapter,
		wsHub:          wsHub,
		redisAdapter:   redisAdapter,
	}
}

func (s *UserService) GetCurrentUser(ctx context.Context, userID uuid.UUID) (*model.UserDTO, error) {
	u, err := s.client.User.Query().
		Where(
			user.ID(userID),
			user.DeletedAtIsNil(),
		).
		WithAvatar().
		Only(ctx)

	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("")
		}
		slog.Error("Failed to query user", "error", err, "userID", userID)
		return nil, helper.NewInternalServerError("")
	}

	avatarURL := ""
	if u.Edges.Avatar != nil {
		avatarURL = s.storageAdapter.GetPublicURL(u.Edges.Avatar.FileName)
	}

	bio := ""
	if u.Bio != nil {
		bio = *u.Bio
	}

	email := ""
	if u.Email != nil {
		email = *u.Email
	}
	username := ""
	if u.Username != nil {
		username = *u.Username
	}

	fullName := ""
	if u.FullName != nil {
		fullName = *u.FullName
	}

	key := fmt.Sprintf("online:%s", u.ID)
	exists, _ := s.redisAdapter.Client().Exists(ctx, key).Result()
	isOnline := exists > 0

	return &model.UserDTO{
		ID:          u.ID,
		Email:       email,
		Username:    username,
		FullName:    fullName,
		Avatar:      avatarURL,
		Bio:         bio,
		Role:        string(u.Role),
		HasPassword: u.PasswordHash != nil,
		IsOnline:    &isOnline,
	}, nil
}

func (s *UserService) GetUserProfile(ctx context.Context, currentUserID uuid.UUID, targetUserID uuid.UUID) (*model.UserDTO, error) {
	blocks, err := s.client.UserBlock.Query().
		Where(
			userblock.Or(
				userblock.And(
					userblock.BlockerID(currentUserID),
					userblock.BlockedID(targetUserID),
				),
				userblock.And(
					userblock.BlockerID(targetUserID),
					userblock.BlockedID(currentUserID),
				),
			),
		).
		All(ctx)

	if err != nil {
		slog.Error("Failed to check block status", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	isBlockedByMe := false
	isBlockedByOther := false

	for _, b := range blocks {
		if b.BlockerID == currentUserID {
			isBlockedByMe = true
		}
		if b.BlockerID == targetUserID {
			isBlockedByOther = true
		}
	}

	u, err := s.client.User.Query().
		Where(
			user.ID(targetUserID),
			user.DeletedAtIsNil(),
		).
		Select(user.FieldID, user.FieldUsername, user.FieldFullName, user.FieldBio, user.FieldLastSeenAt, user.FieldAvatarID, user.FieldIsBanned, user.FieldBannedUntil, user.FieldRole).
		WithAvatar().
		Only(ctx)

	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("User not found")
		}
		slog.Error("Failed to query user profile", "error", err, "targetUserID", targetUserID)
		return nil, helper.NewInternalServerError("")
	}

	avatarURL := ""
	if u.Edges.Avatar != nil {
		avatarURL = s.storageAdapter.GetPublicURL(u.Edges.Avatar.FileName)
	}

	bio := ""
	if u.Bio != nil {
		bio = *u.Bio
	}

	var lastSeenAt *string

	key := fmt.Sprintf("online:%s", u.ID)
	exists, _ := s.redisAdapter.Client().Exists(ctx, key).Result()
	isOnline := exists > 0

	username := ""
	if u.Username != nil {
		username = *u.Username
	}

	isBanned := u.IsBanned
	if isBanned && u.BannedUntil != nil && time.Now().UTC().After(*u.BannedUntil) {
		isBanned = false
	}

	if isBlockedByMe || isBlockedByOther || isBanned {
		isOnline = false
		lastSeenAt = nil
	} else {
		if u.LastSeenAt != nil {
			t := u.LastSeenAt.Format(time.RFC3339)
			lastSeenAt = &t
		}
	}

	fullName := ""
	if u.FullName != nil {
		fullName = *u.FullName
	}

	return &model.UserDTO{
		ID:               u.ID,
		Username:         username,
		FullName:         fullName,
		Avatar:           avatarURL,
		Bio:              bio,
		Role:             string(u.Role),
		HasPassword:      false,
		IsBlockedByMe:    &isBlockedByMe,
		IsBlockedByOther: &isBlockedByOther,
		IsOnline:         &isOnline,
		LastSeenAt:       lastSeenAt,
	}, nil
}

func (s *UserService) UpdateProfile(ctx context.Context, userID uuid.UUID, req model.UpdateProfileRequest) (*model.UserDTO, error) {
	if req.DeleteAvatar && req.AvatarMediaID != nil {
		return nil, helper.NewBadRequestError("")
	}

	if err := s.validator.Struct(&req); err != nil {
		slog.Warn("Validation failed", "error", err, "userID", userID)
		return nil, helper.NewBadRequestError("")
	}

	req.FullName = strings.TrimSpace(req.FullName)
	req.Bio = strings.TrimSpace(req.Bio)

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

	u, err := tx.User.Query().
		Where(
			user.ID(userID),
			user.DeletedAtIsNil(),
		).
		WithAvatar().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("")
		}

		slog.Error("Failed to query user", "error", err, "userID", userID)
		return nil, helper.NewInternalServerError("")
	}

	update := tx.User.UpdateOneID(userID)
	hasChanges := false

	if req.FullName != "" && (u.FullName == nil || *u.FullName != req.FullName) {
		update.SetFullName(req.FullName)
		hasChanges = true
	}

	currentBio := ""
	if u.Bio != nil {
		currentBio = *u.Bio
	}
	if req.Bio != currentBio {
		if req.Bio != "" {
			update.SetBio(req.Bio)
		} else {
			update.ClearBio()
		}
		hasChanges = true
	}

	if req.Username != "" {
		normalizedUsername := helper.NormalizeUsername(req.Username)
		currentUsername := ""
		if u.Username != nil {
			currentUsername = *u.Username
		}

		if normalizedUsername != currentUsername {
			exists, err := tx.User.Query().
				Where(
					user.UsernameEQ(normalizedUsername),
					user.DeletedAtIsNil(),
				).
				Exist(ctx)
			if err != nil {
				slog.Error("Failed to check username existence", "error", err)
				return nil, helper.NewInternalServerError("")
			}
			if exists {
				return nil, helper.NewConflictError("Username already taken")
			}
			update.SetUsername(normalizedUsername)
			hasChanges = true
		}
	}

	if req.DeleteAvatar || req.AvatarMediaID != nil {
		hasChanges = true
	}

	key := fmt.Sprintf("online:%s", u.ID)
	exists, _ := s.redisAdapter.Client().Exists(ctx, key).Result()
	isOnline := exists > 0

	if !hasChanges {
		avatarURL := ""
		if u.Edges.Avatar != nil {
			avatarURL = s.storageAdapter.GetPublicURL(u.Edges.Avatar.FileName)
		}

		bio := ""
		if u.Bio != nil {
			bio = *u.Bio
		}

		email := ""
		if u.Email != nil {
			email = *u.Email
		}
		username := ""
		if u.Username != nil {
			username = *u.Username
		}

		fullName := ""
		if u.FullName != nil {
			fullName = *u.FullName
		}

		httpResp := &model.UserDTO{
			ID:          u.ID,
			Email:       email,
			Username:    username,
			FullName:    fullName,
			Avatar:      avatarURL,
			Bio:         bio,
			Role:        string(u.Role),
			HasPassword: u.PasswordHash != nil,
			IsOnline:    &isOnline,
		}
		if u.LastSeenAt != nil {
			t := u.LastSeenAt.Format(time.RFC3339)
			httpResp.LastSeenAt = &t
		}
		return httpResp, nil
	}

	var isAvatarUpdated bool
	var newAvatarFileName string

	if req.DeleteAvatar {
		update.ClearAvatar().ClearAvatarID()
		isAvatarUpdated = true
	} else if req.AvatarMediaID != nil {
		avatarMedia, err := tx.Media.Query().
			Where(
				media.ID(*req.AvatarMediaID),
				media.CategoryEQ(media.CategoryUserAvatar),
				media.UploadStatusEQ(media.UploadStatusCompleted),
				media.HasUploaderWith(user.ID(userID)),
				media.Not(media.HasUserAvatar()),
				media.Not(media.HasGroupAvatar()),
				media.MessageIDIsNil(),
			).
			Only(ctx)
		if err != nil {
			if ent.IsNotFound(err) {
				return nil, helper.NewBadRequestError("Invalid avatar media")
			}
			slog.Error("Failed to query avatar media", "error", err, "userID", userID, "mediaID", *req.AvatarMediaID)
			return nil, helper.NewInternalServerError("")
		}

		update.SetAvatar(avatarMedia)
		newAvatarFileName = avatarMedia.FileName
		isAvatarUpdated = true
	}

	updatedUser, err := update.Save(ctx)
	if err != nil {
		if ent.IsConstraintError(err) {
			return nil, helper.NewConflictError("Username already taken")
		}
		slog.Error("Failed to save user profile update", "error", err, "userID", userID)
		return nil, helper.NewInternalServerError("")
	}

	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	var avatarFileName string
	if isAvatarUpdated {
		avatarFileName = newAvatarFileName
	} else if u.Edges.Avatar != nil {
		avatarFileName = u.Edges.Avatar.FileName
	}

	avatarURL := ""
	if avatarFileName != "" {
		avatarURL = s.storageAdapter.GetPublicURL(avatarFileName)
	}

	bio := ""
	if updatedUser.Bio != nil {
		bio = *updatedUser.Bio
	}

	email := ""
	if updatedUser.Email != nil {
		email = *updatedUser.Email
	}
	username := ""
	if updatedUser.Username != nil {
		username = *updatedUser.Username
	}

	fullName := ""
	if updatedUser.FullName != nil {
		fullName = *updatedUser.FullName
	}

	httpResp := &model.UserDTO{
		ID:          updatedUser.ID,
		Email:       email,
		Username:    username,
		FullName:    fullName,
		Avatar:      avatarURL,
		Bio:         bio,
		Role:        string(updatedUser.Role),
		HasPassword: updatedUser.PasswordHash != nil,
		IsOnline:    &isOnline,
	}
	if updatedUser.LastSeenAt != nil {
		t := updatedUser.LastSeenAt.Format(time.RFC3339)
		httpResp.LastSeenAt = &t
	}

	if s.wsHub != nil {
		go func() {
			wsPayload := &model.UserUpdateEventPayload{
				ID:       updatedUser.ID,
				Username: username,
				FullName: fullName,
				Avatar:   avatarURL,
				Bio:      bio,
			}
			if updatedUser.LastSeenAt != nil {
				t := updatedUser.LastSeenAt.Format(time.RFC3339)
				wsPayload.LastSeenAt = &t
			}

			event := websocket.Event{
				Type:    websocket.EventUserUpdate,
				Payload: wsPayload,
				Meta: &websocket.EventMeta{
					Timestamp: time.Now().UTC().UnixMilli(),
					SenderID:  userID,
				},
			}

			s.wsHub.BroadcastToUser(userID, event)

			s.wsHub.BroadcastToContacts(userID, event)
		}()
	}

	return httpResp, nil
}

func (s *UserService) SearchUsers(ctx context.Context, currentUserID uuid.UUID, req model.SearchUserRequest) ([]model.UserDTO, string, bool, error) {
	if err := s.validator.Struct(req); err != nil {
		return nil, "", false, helper.NewBadRequestError("")
	}

	if req.Limit == 0 {
		req.Limit = 10
	}

	users, nextCursor, hasNext, err := s.repo.User.SearchUsers(ctx, currentUserID, req.Query, req.Cursor, req.Limit, req.ExcludeChatID)
	if err != nil {
		if strings.Contains(err.Error(), "invalid cursor format") {
			slog.Warn("Invalid cursor format in SearchUsers", "error", err)
			return nil, "", false, helper.NewBadRequestError("")
		}
		slog.Error("Failed to search users", "error", err)
		return nil, "", false, helper.NewInternalServerError("")
	}

	privateChatMap := make(map[uuid.UUID]uuid.UUID)

	if req.IncludeChatID && len(users) > 0 {
		userIDs := make([]uuid.UUID, len(users))
		for i, u := range users {
			userIDs[i] = u.ID
		}

		chats, err := s.client.PrivateChat.Query().
			Where(
				privatechat.Or(
					privatechat.And(
						privatechat.User1ID(currentUserID),
						privatechat.User2IDIn(userIDs...),
					),
					privatechat.And(
						privatechat.User1IDIn(userIDs...),
						privatechat.User2ID(currentUserID),
					),
				),
			).
			All(ctx)

		if err != nil {
			slog.Error("Failed to fetch private chats for search results", "error", err)
		} else {
			for _, pc := range chats {
				var targetID uuid.UUID
				if pc.User1ID != nil && *pc.User1ID == currentUserID {
					if pc.User2ID != nil {
						targetID = *pc.User2ID
					}
				} else {
					if pc.User1ID != nil {
						targetID = *pc.User1ID
					}
				}
				if targetID != uuid.Nil {
					privateChatMap[targetID] = pc.ChatID
				}
			}
		}
	}

	userDTOs := make([]model.UserDTO, 0)
	for _, u := range users {

		avatarURL := ""
		if u.Edges.Avatar != nil {
			avatarURL = s.storageAdapter.GetPublicURL(u.Edges.Avatar.FileName)
		}
		bio := ""
		if u.Bio != nil {
			bio = *u.Bio
		}
		username := ""
		if u.Username != nil {
			username = *u.Username
		}

		fullName := ""
		if u.FullName != nil {
			fullName = *u.FullName
		}

		dto := model.UserDTO{
			ID:          u.ID,
			Username:    username,
			FullName:    fullName,
			Avatar:      avatarURL,
			Bio:         bio,
			Role:        string(u.Role),
			HasPassword: false,
		}

		if chatID, exists := privateChatMap[u.ID]; exists {
			dto.PrivateChatID = &chatID
		}

		userDTOs = append(userDTOs, dto)
	}

	return userDTOs, nextCursor, hasNext, nil
}

func (s *UserService) GetBlockedUsers(ctx context.Context, currentUserID uuid.UUID, req model.GetBlockedUsersRequest) ([]model.UserDTO, string, bool, error) {
	if err := s.validator.Struct(req); err != nil {
		return nil, "", false, helper.NewBadRequestError("")
	}

	if req.Limit == 0 {
		req.Limit = 10
	}

	users, nextCursor, hasNext, err := s.repo.User.GetBlockedUsers(ctx, currentUserID, req.Query, req.Cursor, req.Limit)
	if err != nil {
		if strings.Contains(err.Error(), "invalid cursor format") {
			slog.Warn("Invalid cursor format in GetBlockedUsers", "error", err)
			return nil, "", false, helper.NewBadRequestError("")
		}
		slog.Error("Failed to get blocked users", "error", err)
		return nil, "", false, helper.NewInternalServerError("")
	}

	userDTOs := make([]model.UserDTO, 0)
	for _, u := range users {

		avatarURL := ""
		if u.Edges.Avatar != nil {
			avatarURL = s.storageAdapter.GetPublicURL(u.Edges.Avatar.FileName)
		}
		bio := ""
		if u.Bio != nil {
			bio = *u.Bio
		}
		username := ""
		if u.Username != nil {
			username = *u.Username
		}

		fullName := ""
		if u.FullName != nil {
			fullName = *u.FullName
		}

		isBlockedByMe := true
		userDTOs = append(userDTOs, model.UserDTO{
			ID:            u.ID,
			Username:      username,
			FullName:      fullName,
			Avatar:        avatarURL,
			Bio:           bio,
			Role:          string(u.Role),
			IsBlockedByMe: &isBlockedByMe,
		})
	}

	return userDTOs, nextCursor, hasNext, nil
}

func (s *UserService) BlockUser(ctx context.Context, blockerID uuid.UUID, blockedID uuid.UUID) error {
	if blockerID == blockedID {
		return helper.NewBadRequestError("Cannot block yourself")
	}

	exists, err := s.client.User.Query().
		Where(
			user.ID(blockedID),
			user.DeletedAtIsNil(),
		).
		Exist(ctx)
	if err != nil {
		slog.Error("Failed to check user existence", "error", err)
		return helper.NewInternalServerError("")
	}
	if !exists {
		return helper.NewNotFoundError("")
	}

	_, err = s.client.UserBlock.Create().
		SetBlockerID(blockerID).
		SetBlockedID(blockedID).
		Save(ctx)

	if err != nil {
		if ent.IsConstraintError(err) {
			return nil
		}
		slog.Error("Failed to block user", "error", err)
		return helper.NewInternalServerError("")
	}

	if s.wsHub != nil {
		go func() {
			event := websocket.Event{
				Type: websocket.EventUserBlock,
				Payload: map[string]uuid.UUID{
					"blocker_id": blockerID,
					"blocked_id": blockedID,
				},
				Meta: &websocket.EventMeta{
					Timestamp: time.Now().UTC().UnixMilli(),
					SenderID:  blockerID,
				},
			}

			s.wsHub.BroadcastToUser(blockedID, event)

			s.wsHub.BroadcastToUser(blockerID, event)
		}()
	}

	return nil
}

func (s *UserService) UnblockUser(ctx context.Context, blockerID uuid.UUID, blockedID uuid.UUID) error {
	_, err := s.client.UserBlock.Delete().
		Where(
			userblock.BlockerID(blockerID),
			userblock.BlockedID(blockedID),
		).
		Exec(ctx)

	if err != nil {
		slog.Error("Failed to unblock user", "error", err)
		return helper.NewInternalServerError("")
	}

	if s.wsHub != nil {
		go func() {
			event := websocket.Event{
				Type: websocket.EventUserUnblock,
				Payload: map[string]uuid.UUID{
					"blocker_id": blockerID,
					"blocked_id": blockedID,
				},
				Meta: &websocket.EventMeta{
					Timestamp: time.Now().UTC().UnixMilli(),
					SenderID:  blockerID,
				},
			}

			s.wsHub.BroadcastToUser(blockedID, event)

			s.wsHub.BroadcastToUser(blockerID, event)
		}()
	}

	return nil
}
