package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/ent/useridentity"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/constant"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/repository"
	"AtoiTalkAPI/internal/websocket"
	"context"
	"log/slog"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

type AccountService struct {
	client       *ent.Client
	cfg          *config.AppConfig
	validator    *validator.Validate
	wsHub        websocket.Publisher
	otpService   authOTP
	sessionStore repository.SessionStore
}

func NewAccountService(client *ent.Client, cfg *config.AppConfig, validator *validator.Validate, wsHub websocket.Publisher, otpService authOTP, sessionStore repository.SessionStore) *AccountService {
	return &AccountService{
		client:       client,
		cfg:          cfg,
		validator:    validator,
		wsHub:        wsHub,
		otpService:   otpService,
		sessionStore: sessionStore,
	}
}

func (s *AccountService) ChangePassword(ctx context.Context, userID uuid.UUID, req model.ChangePasswordRequest) error {
	if err := s.validator.Struct(req); err != nil {
		slog.Warn("Validation failed", "error", err, "userID", userID)
		return helper.NewBadRequestError("")
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		slog.Error("Failed to start transaction", "error", err)
		return helper.NewInternalServerError("")
	}

	defer func() {
		_ = tx.Rollback()
		if v := recover(); v != nil {
			panic(v)
		}
	}()

	u, err := tx.User.Query().
		Where(user.ID(userID)).
		Select(user.FieldID, user.FieldPasswordHash).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return helper.NewNotFoundError("")
		}
		slog.Error("Failed to query user", "error", err, "userID", userID)
		return helper.NewInternalServerError("")
	}

	if u.PasswordHash != nil {

		if req.OldPassword == nil {
			return helper.NewBadRequestError("")
		}
		if !helper.CheckPasswordHash(*req.OldPassword, *u.PasswordHash) {
			return helper.NewBadRequestError("Invalid old password")
		}
	}

	hashedPassword, err := helper.HashPassword(req.NewPassword)
	if err != nil {
		slog.Error("Failed to hash password", "error", err, "userID", userID)
		return helper.NewInternalServerError("")
	}

	err = tx.User.UpdateOneID(userID).SetPasswordHash(hashedPassword).Exec(ctx)
	if err != nil {
		slog.Error("Failed to update password", "error", err, "userID", userID)
		return helper.NewInternalServerError("")
	}

	revokeExpected, revokeSnapshot, err := helper.RevokeSessionsForTransaction(ctx, s.sessionStore, userID)
	if err != nil {
		slog.Error("Failed to revoke sessions after password change", "error", err, "userID", userID)
		return helper.NewServiceUnavailableError("Session service unavailable")
	}

	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err, "userID", userID)
		helper.RollbackSessionRevokeIfNeeded(s.sessionStore, userID, revokeExpected, revokeSnapshot)
		return helper.NewInternalServerError("")
	}

	if s.wsHub != nil {
		go s.wsHub.DisconnectUser(userID)
	}

	return nil
}

func (s *AccountService) ChangeEmail(ctx context.Context, userID uuid.UUID, req model.ChangeEmailRequest) error {
	req.Email = helper.NormalizeEmail(req.Email)

	if err := s.validator.Struct(req); err != nil {
		slog.Warn("Validation failed", "error", err, "userID", userID)
		return helper.NewBadRequestError("")
	}

	u, err := s.client.User.Query().
		Where(user.ID(userID)).
		Select(user.FieldID, user.FieldPasswordHash, user.FieldEmail).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return helper.NewNotFoundError("")
		}
		slog.Error("Failed to query user", "error", err, "userID", userID)
		return helper.NewInternalServerError("")
	}

	if u.PasswordHash == nil {
		return helper.NewBadRequestError("You must set a password before changing your email address")
	}

	if u.Email != nil && *u.Email == req.Email {
		return helper.NewBadRequestError("")
	}

	if err := s.otpService.VerifyOTP(ctx, req.Email, req.Code, string(constant.ModeChangeEmail)); err != nil {
		return err
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		slog.Error("Failed to start transaction", "error", err)
		return helper.NewInternalServerError("")
	}

	defer func() {
		_ = tx.Rollback()
		if v := recover(); v != nil {
			panic(v)
		}
	}()

	exists, err := tx.User.Query().
		Where(user.Email(req.Email)).
		Exist(ctx)
	if err != nil {
		slog.Error("Failed to check user existence", "error", err)
		return helper.NewInternalServerError("")
	}
	if exists {
		return helper.NewConflictError("Email already registered")
	}

	err = tx.User.UpdateOneID(userID).SetEmail(req.Email).Exec(ctx)
	if err != nil {
		slog.Error("Failed to update email", "error", err, "userID", userID)
		return helper.NewInternalServerError("")
	}

	_, err = tx.UserIdentity.Delete().
		Where(useridentity.UserID(userID)).
		Exec(ctx)
	if err != nil {
		slog.Error("Failed to unlink user identities", "error", err, "userID", userID)
		return helper.NewInternalServerError("")
	}

	revokeExpected, revokeSnapshot, err := helper.RevokeSessionsForTransaction(ctx, s.sessionStore, userID)
	if err != nil {
		slog.Error("Failed to revoke sessions after email change", "error", err, "userID", userID)
		return helper.NewServiceUnavailableError("Session service unavailable")
	}

	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		helper.RollbackSessionRevokeIfNeeded(s.sessionStore, userID, revokeExpected, revokeSnapshot)
		return helper.NewInternalServerError("")
	}

	if s.wsHub != nil {
		go s.wsHub.DisconnectUser(userID)
	}

	return nil
}

func (s *AccountService) DeleteAccount(ctx context.Context, userID uuid.UUID, req model.DeleteAccountRequest) error {
	u, err := s.client.User.Query().
		Where(user.ID(userID)).
		Select(user.FieldID, user.FieldPasswordHash).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return helper.NewNotFoundError("")
		}
		slog.Error("Failed to query user", "error", err, "userID", userID)
		return helper.NewInternalServerError("")
	}

	if u.PasswordHash != nil {
		if req.Password == nil {
			return helper.NewBadRequestError("Password is required to delete account")
		}
		if !helper.CheckPasswordHash(*req.Password, *u.PasswordHash) {
			return helper.NewBadRequestError("Invalid password")
		}
	}

	ownedGroupsCount, err := s.client.GroupMember.Query().
		Where(
			groupmember.UserID(userID),
			groupmember.RoleEQ(groupmember.RoleOwner),
			groupmember.HasGroupChatWith(
				groupchat.HasChatWith(chat.DeletedAtIsNil()),
			),
		).
		Count(ctx)

	if err != nil {
		slog.Error("Failed to check group ownership", "error", err)
		return helper.NewInternalServerError("")
	}

	if ownedGroupsCount > 0 {
		return helper.NewForbiddenError("You must transfer ownership of your groups or delete them before deleting your account.")
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		slog.Error("Failed to start transaction", "error", err)
		return helper.NewInternalServerError("")
	}

	defer func() {
		_ = tx.Rollback()
		if v := recover(); v != nil {
			panic(v)
		}
	}()

	_, err = tx.UserIdentity.Delete().
		Where(useridentity.UserID(userID)).
		Exec(ctx)
	if err != nil {
		slog.Error("Failed to delete user identities", "error", err)
		return helper.NewInternalServerError("")
	}

	err = tx.User.UpdateOneID(userID).
		ClearEmail().
		ClearUsername().
		ClearFullName().
		ClearBio().
		ClearAvatar().
		ClearPasswordHash().
		SetDeletedAt(time.Now().UTC()).
		Exec(ctx)

	if err != nil {
		slog.Error("Failed to anonymize user", "error", err)
		return helper.NewInternalServerError("")
	}

	revokeExpected, revokeSnapshot, err := helper.RevokeSessionsForTransaction(ctx, s.sessionStore, userID)
	if err != nil {
		slog.Error("Failed to revoke sessions after account deletion", "error", err, "userID", userID)
		return helper.NewServiceUnavailableError("Session service unavailable")
	}

	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		helper.RollbackSessionRevokeIfNeeded(s.sessionStore, userID, revokeExpected, revokeSnapshot)
		return helper.NewInternalServerError("")
	}

	if s.wsHub != nil {
		go func() {
			event := websocket.Event{
				Type:    websocket.EventUserDeleted,
				Payload: map[string]uuid.UUID{"user_id": userID},
				Meta: &websocket.EventMeta{
					Timestamp: time.Now().UTC().UnixMilli(),
					SenderID:  userID,
				},
			}

			s.wsHub.BroadcastToContacts(userID, event)
			s.wsHub.DisconnectUser(userID)
		}()
	}

	return nil
}
