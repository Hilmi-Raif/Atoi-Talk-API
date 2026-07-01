package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/report"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/adapter"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/repository"
	"AtoiTalkAPI/internal/websocket"
	"context"
	"encoding/base64"
	"log/slog"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

type AdminService struct {
	client         *ent.Client
	cfg            *config.AppConfig
	validator      *validator.Validate
	wsHub          *websocket.Hub
	repo           *repository.Repository
	storageAdapter *adapter.StorageAdapter
}

func NewAdminService(client *ent.Client, cfg *config.AppConfig, validator *validator.Validate, wsHub *websocket.Hub, repo *repository.Repository, storageAdapter *adapter.StorageAdapter) *AdminService {
	return &AdminService{
		client:         client,
		cfg:            cfg,
		validator:      validator,
		wsHub:          wsHub,
		repo:           repo,
		storageAdapter: storageAdapter,
	}
}

func (s *AdminService) BanUser(ctx context.Context, adminID uuid.UUID, req model.BanUserRequest) error {
	if err := s.validator.Struct(req); err != nil {
		return helper.NewBadRequestError("")
	}

	req.Reason = strings.TrimSpace(req.Reason)

	targetUser, err := s.client.User.Query().
		Where(user.ID(req.TargetUserID)).
		Select(user.FieldID, user.FieldRole, user.FieldIsBanned, user.FieldBannedUntil, user.FieldBanReason).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return helper.NewNotFoundError("User not found")
		}
		return helper.NewInternalServerError("")
	}

	if targetUser.Role == user.RoleAdmin {
		return helper.NewForbiddenError("Cannot ban another admin")
	}

	var newBannedUntil *time.Time
	if req.DurationHours > 0 {
		until := time.Now().UTC().Add(time.Duration(req.DurationHours) * time.Hour)
		newBannedUntil = &until
	}

	if targetUser.IsBanned && targetUser.BanReason != nil && *targetUser.BanReason == req.Reason {
		if (targetUser.BannedUntil == nil && newBannedUntil == nil) ||
			(targetUser.BannedUntil != nil && newBannedUntil != nil && targetUser.BannedUntil.Equal(*newBannedUntil)) {
			return nil
		}
	}

	tx, err := s.client.Tx(ctx)
	if err != nil {
		slog.Error("Failed to start transaction", "error", err)
		return helper.NewInternalServerError("")
	}
	defer tx.Rollback()

	update := tx.User.UpdateOneID(req.TargetUserID).
		SetIsBanned(true).
		SetBanReason(req.Reason)

	if newBannedUntil != nil {
		update.SetBannedUntil(*newBannedUntil)
	} else {
		update.ClearBannedUntil()
	}

	if err := update.Exec(ctx); err != nil {
		slog.Error("Failed to ban user", "error", err)
		return helper.NewInternalServerError("")
	}

	revokeExpected, revokeSnapshot, err := helper.RevokeSessionsForTransaction(ctx, s.repo.Session, req.TargetUserID)
	if err != nil {
		slog.Error("Failed to revoke sessions for banned user", "error", err, "userID", req.TargetUserID)
		return helper.NewServiceUnavailableError("Session service unavailable")
	}

	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction for banning user", "error", err)
		helper.RollbackSessionRevokeIfNeeded(s.repo.Session, req.TargetUserID, revokeExpected, revokeSnapshot)
		return helper.NewInternalServerError("")
	}

	if s.wsHub != nil {
		event := websocket.Event{
			Type: websocket.EventUserBanned,
			Payload: map[string]interface{}{
				"user_id": req.TargetUserID,
				"reason":  req.Reason,
			},
			Meta: &websocket.EventMeta{
				Timestamp: time.Now().UTC().UnixMilli(),
				SenderID:  adminID,
			},
		}

		s.wsHub.BroadcastToUser(req.TargetUserID, event)
		s.wsHub.BroadcastToContacts(req.TargetUserID, event)

		s.wsHub.DisconnectUser(req.TargetUserID)
	}

	return nil
}

func (s *AdminService) UnbanUser(ctx context.Context, adminID uuid.UUID, targetUserID uuid.UUID) error {
	targetUser, err := s.client.User.Query().
		Where(user.ID(targetUserID)).
		Select(user.FieldID, user.FieldIsBanned).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return helper.NewNotFoundError("User not found")
		}
		return helper.NewInternalServerError("")
	}

	if !targetUser.IsBanned {
		return nil
	}

	err = s.client.User.UpdateOneID(targetUserID).
		SetIsBanned(false).
		ClearBannedUntil().
		ClearBanReason().
		Exec(ctx)

	if err != nil {
		slog.Error("Failed to unban user", "error", err)
		return helper.NewInternalServerError("")
	}

	if s.wsHub != nil {
		event := websocket.Event{
			Type: websocket.EventUserUnbanned,
			Payload: map[string]interface{}{
				"user_id": targetUserID,
			},
			Meta: &websocket.EventMeta{
				Timestamp: time.Now().UTC().UnixMilli(),
				SenderID:  adminID,
			},
		}

		s.wsHub.BroadcastToUser(targetUserID, event)
		s.wsHub.BroadcastToContacts(targetUserID, event)
	}

	return nil
}

func (s *AdminService) GetReports(ctx context.Context, req model.GetReportsRequest) ([]model.ReportListResponse, string, bool, error) {
	if err := s.validator.Struct(req); err != nil {
		return nil, "", false, helper.NewBadRequestError("")
	}

	if req.Limit == 0 {
		req.Limit = 20
	}

	query := s.client.Report.Query()

	if req.Status != "" {
		query = query.Where(report.StatusEQ(report.Status(req.Status)))
	}

	if req.Query != "" {
		lowerQuery := strings.ToLower(req.Query)
		query = query.Where(
			report.Or(
				report.ReasonContainsFold(req.Query),
				report.HasReporterWith(
					user.Or(
						func(s *sql.Selector) {
							s.Where(sql.HasPrefix(sql.Lower(s.C(user.FieldUsername)), lowerQuery))
						},
						func(s *sql.Selector) {
							s.Where(sql.HasPrefix(sql.Lower(s.C(user.FieldFullName)), lowerQuery))
						},
					),
				),
			),
		)
	}

	if req.Cursor != "" {
		decodedBytes, err := base64.URLEncoding.DecodeString(req.Cursor)
		if err == nil {
			if cursorID, err := uuid.Parse(string(decodedBytes)); err == nil {
				query = query.Where(report.IDLT(cursorID))
			}
		}
	}

	reports, err := query.
		Order(ent.Desc(report.FieldID)).
		Limit(req.Limit + 1).
		WithReporter().
		All(ctx)

	if err != nil {
		slog.Error("Failed to get reports", "error", err)
		return nil, "", false, helper.NewInternalServerError("")
	}

	hasNext := false
	var nextCursor string
	if len(reports) > req.Limit {
		hasNext = true
		reports = reports[:req.Limit]
		lastID := reports[len(reports)-1].ID
		nextCursor = base64.URLEncoding.EncodeToString([]byte(lastID.String()))
	}

	response := make([]model.ReportListResponse, 0)
	for _, r := range reports {
		reporterName := "Deleted User"
		if r.Edges.Reporter != nil {
			if r.Edges.Reporter.FullName != nil {
				reporterName = *r.Edges.Reporter.FullName
			}
		}

		response = append(response, model.ReportListResponse{
			ID:           r.ID,
			TargetType:   string(r.TargetType),
			Reason:       r.Reason,
			Status:       string(r.Status),
			ReporterName: reporterName,
			CreatedAt:    r.CreatedAt.Format(time.RFC3339),
		})
	}

	return response, nextCursor, hasNext, nil
}

func (s *AdminService) GetReportDetail(ctx context.Context, reportID uuid.UUID) (*model.ReportDetailResponse, error) {
	r, err := s.client.Report.Query().
		Where(report.ID(reportID)).
		WithReporter(func(q *ent.UserQuery) {
			q.Select(user.FieldID, user.FieldFullName, user.FieldAvatarID, user.FieldDeletedAt, user.FieldIsBanned, user.FieldBannedUntil)
			q.WithAvatar()
		}).
		Only(ctx)

	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("Report not found")
		}
		slog.Error("Failed to get report detail", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	reporterName := "Deleted User"
	reporterAvatar := ""
	reporterIsDeleted := true
	reporterIsBanned := false

	if r.Edges.Reporter != nil {
		if r.Edges.Reporter.DeletedAt == nil {
			reporterIsDeleted = false
		}
		if r.Edges.Reporter.IsBanned {
			if r.Edges.Reporter.BannedUntil == nil || time.Now().Before(*r.Edges.Reporter.BannedUntil) {
				reporterIsBanned = true
			}
		}
		if r.Edges.Reporter.FullName != nil {
			reporterName = *r.Edges.Reporter.FullName
		}
		if r.Edges.Reporter.Edges.Avatar != nil {
			reporterAvatar = s.storageAdapter.GetPublicURL(r.Edges.Reporter.Edges.Avatar.FileName)
		}
	}

	if r.TargetType == report.TargetTypeMessage && r.EvidenceSnapshot != nil {
		if attachments, ok := r.EvidenceSnapshot["attachments"].([]interface{}); ok {
			var refreshedAttachments []interface{}
			for _, att := range attachments {

				if fileName, ok := att.(string); ok {
					url, err := s.storageAdapter.GetPresignedURL(fileName, 15*time.Minute)
					if err == nil {
						refreshedAttachments = append(refreshedAttachments, url)
					} else {
						refreshedAttachments = append(refreshedAttachments, fileName)
					}
				} else if attMap, ok := att.(map[string]interface{}); ok {

					if fileName, ok := attMap["file_name"].(string); ok {
						url, err := s.storageAdapter.GetPresignedURL(fileName, 15*time.Minute)
						if err == nil {
							attMap["url"] = url
						}
					}
					refreshedAttachments = append(refreshedAttachments, attMap)
				}
			}
			r.EvidenceSnapshot["attachments"] = refreshedAttachments
		}
	}

	var adminNotes *string
	if r.ResolutionNotes != nil {
		adminNotes = r.ResolutionNotes
	}

	var targetID *uuid.UUID
	targetIsDeleted := true
	targetIsBanned := false

	switch r.TargetType {
	case report.TargetTypeUser:
		if r.TargetUserID != nil {
			targetID = r.TargetUserID
			u, err := s.client.User.Query().
				Where(user.ID(*r.TargetUserID)).
				Select(user.FieldID, user.FieldDeletedAt, user.FieldIsBanned, user.FieldBannedUntil).
				Only(ctx)
			if err == nil {
				if u.DeletedAt == nil {
					targetIsDeleted = false
				}
				if u.IsBanned {
					if u.BannedUntil == nil || time.Now().Before(*u.BannedUntil) {
						targetIsBanned = true
					}
				}
			}
		}
	case report.TargetTypeGroup:
		if r.GroupID != nil {
			targetID = r.GroupID

			exists, existsErr := s.client.GroupChat.Query().
				Where(
					groupchat.ID(*r.GroupID),
					groupchat.HasChatWith(chat.DeletedAtIsNil()),
				).
				Exist(ctx)
			if existsErr != nil {
				slog.Error("Failed to check group existence for report detail", "error", existsErr, "groupID", *r.GroupID)
			}
			if exists {
				targetIsDeleted = false
			}
		}
	case report.TargetTypeMessage:
		if r.MessageID != nil {
			targetID = r.MessageID
			msg, err := s.client.Message.Query().
				Where(message.ID(*r.MessageID)).
				WithSender(func(q *ent.UserQuery) {
					q.Select(user.FieldID, user.FieldIsBanned, user.FieldBannedUntil)
				}).
				Only(ctx)
			if err == nil {
				if msg.DeletedAt == nil {
					targetIsDeleted = false
				}
				if msg.Edges.Sender != nil {
					if msg.Edges.Sender.IsBanned {
						if msg.Edges.Sender.BannedUntil == nil || time.Now().Before(*msg.Edges.Sender.BannedUntil) {
							targetIsBanned = true
						}
					}
				}
			}
		}
	}

	reporterID := uuid.Nil
	if r.ReporterID != nil {
		reporterID = *r.ReporterID
	}

	return &model.ReportDetailResponse{
		ID:                r.ID,
		TargetType:        string(r.TargetType),
		TargetID:          targetID,
		TargetIsDeleted:   targetIsDeleted,
		TargetIsBanned:    targetIsBanned,
		Reason:            r.Reason,
		Description:       r.Description,
		Status:            string(r.Status),
		ReporterID:        reporterID,
		ReporterName:      reporterName,
		ReporterAvatar:    reporterAvatar,
		ReporterIsDeleted: reporterIsDeleted,
		ReporterIsBanned:  reporterIsBanned,
		EvidenceSnapshot:  r.EvidenceSnapshot,
		AdminNotes:        adminNotes,
		CreatedAt:         r.CreatedAt.Format(time.RFC3339),
		UpdatedAt:         r.UpdatedAt.Format(time.RFC3339),
	}, nil
}

func (s *AdminService) ResolveReport(ctx context.Context, adminID uuid.UUID, reportID uuid.UUID, req model.ResolveReportRequest) error {
	if err := s.validator.Struct(req); err != nil {
		return helper.NewBadRequestError("")
	}

	req.Notes = strings.TrimSpace(req.Notes)

	r, err := s.client.Report.Query().
		Where(report.ID(reportID)).
		Select(report.FieldID, report.FieldStatus).
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return helper.NewNotFoundError("Report not found")
		}
		slog.Error("Failed to query report", "error", err)
		return helper.NewInternalServerError("")
	}

	if r.Status == report.Status(req.Status) {
		return nil
	}

	update := s.client.Report.UpdateOneID(reportID).
		SetStatus(report.Status(req.Status)).
		SetResolvedByID(adminID).
		SetResolvedAt(time.Now().UTC())

	if req.Notes != "" {
		update.SetResolutionNotes(req.Notes)
	}

	err = update.Exec(ctx)

	if err != nil {
		slog.Error("Failed to resolve report", "error", err)
		return helper.NewInternalServerError("")
	}

	if report.Status(req.Status) == report.StatusResolved {
		fullReport, err := s.client.Report.Query().
			Where(report.ID(reportID)).
			Only(ctx)

		if err == nil && fullReport.TargetType == report.TargetTypeMessage && fullReport.MessageID != nil {
			msgID := *fullReport.MessageID

			msg, err := s.client.Message.Query().
				Where(message.ID(msgID)).
				WithChat().
				Only(ctx)

			if err == nil {
				now := time.Now().UTC()
				shouldBroadcastDelete := msg.DeletedAt == nil
				updateMsg := s.client.Message.UpdateOne(msg).
					ClearContent().
					ClearAttachments()

				if shouldBroadcastDelete {
					updateMsg.SetDeletedAt(now)
				}

				err = updateMsg.Exec(ctx)
				if err == nil && shouldBroadcastDelete && s.wsHub != nil {
					go s.wsHub.BroadcastToChat(msg.ChatID, websocket.Event{
						Type: websocket.EventMessageDelete,
						Payload: map[string]uuid.UUID{
							"message_id": msgID,
						},
						Meta: &websocket.EventMeta{
							Timestamp: now.UnixMilli(),
							ChatID:    msg.ChatID,
						},
					})
				}
			}
		}
	}

	return nil
}

func (s *AdminService) DeleteReport(ctx context.Context, reportID uuid.UUID) error {
	r, err := s.client.Report.Query().Where(report.ID(reportID)).Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return helper.NewNotFoundError("Report not found")
		}
		slog.Error("Failed to fetch report for deletion", "error", err)
		return helper.NewInternalServerError("Failed to delete report")
	}

	if r.Status != report.StatusResolved && r.Status != report.StatusRejected {
		return helper.NewBadRequestError("Cannot delete report that is not resolved or rejected")
	}

	err = s.client.Report.DeleteOneID(reportID).Exec(ctx)
	if err != nil {
		slog.Error("Failed to delete report", "error", err)
		return helper.NewInternalServerError("Failed to delete report")
	}
	return nil
}

func (s *AdminService) GetDashboardStats(ctx context.Context) (*model.DashboardStatsResponse, error) {
	totalUsers, err := s.client.User.Query().Count(ctx)
	if err != nil {
		slog.Error("Failed to count users", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	totalGroups, err := s.client.GroupChat.Query().Count(ctx)
	if err != nil {
		slog.Error("Failed to count groups", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	totalMessages, err := s.client.Message.Query().Count(ctx)
	if err != nil {
		slog.Error("Failed to count messages", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	activeReports, err := s.client.Report.Query().
		Where(report.StatusEQ(report.StatusPending)).
		Count(ctx)
	if err != nil {
		slog.Error("Failed to count active reports", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	return &model.DashboardStatsResponse{
		TotalUsers:    totalUsers,
		TotalGroups:   totalGroups,
		TotalMessages: totalMessages,
		ActiveReports: activeReports,
	}, nil
}

func (s *AdminService) GetUsers(ctx context.Context, req model.AdminGetUserListRequest) ([]model.AdminUserListResponse, string, bool, error) {
	if err := s.validator.Struct(req); err != nil {
		return nil, "", false, helper.NewBadRequestError("")
	}

	if req.Limit == 0 {
		req.Limit = 20
	}

	query := s.client.User.Query().
		Where(user.DeletedAtIsNil())

	if req.Query != "" {
		query = query.Where(user.Or(
			user.UsernameContainsFold(req.Query),
			user.EmailContainsFold(req.Query),
			user.FullNameContainsFold(req.Query),
		))
	}

	if req.Role != "" {
		query = query.Where(user.RoleEQ(user.Role(req.Role)))
	}

	if req.Cursor != "" {
		decodedBytes, err := base64.URLEncoding.DecodeString(req.Cursor)
		if err == nil {
			if cursorID, err := uuid.Parse(string(decodedBytes)); err == nil {
				query = query.Where(user.IDLT(cursorID))
			}
		}
	}

	query = query.Order(ent.Desc(user.FieldID))

	usersList, err := query.Limit(req.Limit + 1).All(ctx)
	if err != nil {
		slog.Error("Failed to fetch users", "error", err)
		return nil, "", false, helper.NewInternalServerError("")
	}

	hasNext := false
	var nextCursor string
	if len(usersList) > req.Limit {
		hasNext = true
		usersList = usersList[:req.Limit]
		lastID := usersList[len(usersList)-1].ID
		nextCursor = base64.URLEncoding.EncodeToString([]byte(lastID.String()))
	}

	data := make([]model.AdminUserListResponse, 0)
	for _, u := range usersList {
		username := ""
		if u.Username != nil {
			username = *u.Username
		}
		email := ""
		if u.Email != nil {
			email = *u.Email
		}
		fullName := ""
		if u.FullName != nil {
			fullName = *u.FullName
		}

		isBanned := u.IsBanned
		if isBanned && u.BannedUntil != nil && time.Now().After(*u.BannedUntil) {
			isBanned = false
		}

		data = append(data, model.AdminUserListResponse{
			ID:        u.ID,
			Username:  username,
			Email:     &email,
			FullName:  &fullName,
			Role:      string(u.Role),
			IsBanned:  isBanned,
			CreatedAt: u.CreatedAt.Format(time.RFC3339),
		})
	}

	return data, nextCursor, hasNext, nil
}

func (s *AdminService) GetUserDetail(ctx context.Context, userID uuid.UUID) (*model.AdminUserDetailResponse, error) {
	u, err := s.client.User.Query().
		Where(user.ID(userID)).
		WithAvatar().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("User not found")
		}
		slog.Error("Failed to fetching user detail", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	msgCount, err := s.client.Message.Query().Where(message.SenderID(u.ID)).Count(ctx)
	if err != nil {
		slog.Error("Failed to count user messages", "error", err, "userID", u.ID)
		return nil, helper.NewInternalServerError("")
	}

	groupCount, err := s.client.GroupMember.Query().Where(groupmember.UserID(u.ID)).Count(ctx)
	if err != nil {
		slog.Error("Failed to count user groups", "error", err, "userID", u.ID)
		return nil, helper.NewInternalServerError("")
	}

	var username, email, fullName, bio string
	if u.Username != nil {
		username = *u.Username
	}
	if u.Email != nil {
		email = *u.Email
	}
	if u.FullName != nil {
		fullName = *u.FullName
	}
	if u.Bio != nil {
		bio = *u.Bio
	}
	isBanned := u.IsBanned
	if isBanned && u.BannedUntil != nil && time.Now().UTC().After(*u.BannedUntil) {
		isBanned = false
	}

	resp := &model.AdminUserDetailResponse{
		ID:            u.ID,
		Username:      username,
		Email:         &email,
		FullName:      &fullName,
		Bio:           &bio,
		Role:          string(u.Role),
		IsBanned:      isBanned,
		BanReason:     u.BanReason,
		CreatedAt:     u.CreatedAt.Format(time.RFC3339),
		TotalMessages: msgCount,
		TotalGroups:   groupCount,
	}

	if u.Edges.Avatar != nil {
		url := s.storageAdapter.GetPublicURL(u.Edges.Avatar.FileName)
		resp.Avatar = url
	}

	if u.BannedUntil != nil {
		tStr := u.BannedUntil.Format(time.RFC3339)
		resp.BannedUntil = &tStr
	}

	if u.LastSeenAt != nil {
		tStr := u.LastSeenAt.Format(time.RFC3339)
		resp.LastSeenAt = &tStr
	}

	return resp, nil
}

func (s *AdminService) ResetUserInfo(ctx context.Context, req model.ResetUserInfoRequest) error {
	uQuery := s.client.User.Query().Where(user.ID(req.TargetUserID)).WithAvatar()
	u, err := uQuery.Only(ctx)
	if err != nil {
		return helper.NewNotFoundError("User not found")
	}

	if u.Role == user.RoleAdmin {
		return helper.NewForbiddenError("Cannot reset info of another admin")
	}

	update := s.client.User.UpdateOne(u)
	hasChanges := false

	if req.ResetBio && u.Bio != nil {
		update.ClearBio()
		hasChanges = true
	}

	if req.ResetName {
		defaultName := "User " + u.ID.String()[:8]
		if u.FullName == nil || *u.FullName != defaultName {
			update.SetFullName(defaultName)
			hasChanges = true
		}
	}

	if req.ResetAvatar && u.Edges.Avatar != nil {
		update.ClearAvatar()
		hasChanges = true
	}

	if !hasChanges {
		return nil
	}

	if err := update.Exec(ctx); err != nil {
		slog.Error("Failed to reset user info", "error", err)
		return helper.NewInternalServerError("Failed to reset info")
	}

	if s.wsHub != nil {
		updatedUser, err := s.client.User.Query().
			Where(user.ID(req.TargetUserID)).
			WithAvatar().
			Only(ctx)

		if err == nil {
			avatarURL := ""
			if updatedUser.Edges.Avatar != nil {
				avatarURL = s.storageAdapter.GetPublicURL(updatedUser.Edges.Avatar.FileName)
			}

			fullName := ""
			if updatedUser.FullName != nil {
				fullName = *updatedUser.FullName
			}
			username := ""
			if updatedUser.Username != nil {
				username = *updatedUser.Username
			}
			bio := ""
			if updatedUser.Bio != nil {
				bio = *updatedUser.Bio
			}

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
					SenderID:  req.TargetUserID,
				},
			}

			s.wsHub.BroadcastToUser(req.TargetUserID, event)
			s.wsHub.BroadcastToContacts(req.TargetUserID, event)
		}
	}

	return nil
}

func (s *AdminService) GetGroups(ctx context.Context, req model.AdminGetGroupListRequest) ([]model.AdminGroupListResponse, string, bool, error) {
	if req.Limit <= 0 {
		req.Limit = 20
	}

	query := s.client.GroupChat.Query().
		Where(groupchat.HasChatWith(chat.DeletedAtIsNil())).
		WithChat()

	if req.Query != "" {
		query = query.Where(groupchat.NameContainsFold(req.Query))
	}

	if req.Cursor != "" {
		decoded, err := base64.URLEncoding.DecodeString(req.Cursor)
		if err == nil {
			cursorID, err := uuid.Parse(string(decoded))
			if err == nil {
				query = query.Where(groupchat.IDGT(cursorID))
			}
		}
	}

	query = query.Order(ent.Asc(groupchat.FieldID))

	groups, err := query.Limit(req.Limit + 1).All(ctx)
	if err != nil {
		slog.Error("Failed to fetch groups", "error", err)
		return nil, "", false, helper.NewInternalServerError("Failed to fetch groups")
	}

	hasNext := false
	var nextCursor string
	if len(groups) > req.Limit {
		hasNext = true
		groups = groups[:req.Limit]
		lastID := groups[len(groups)-1].ID
		nextCursor = base64.URLEncoding.EncodeToString([]byte(lastID.String()))
	}

	data := make([]model.AdminGroupListResponse, 0, len(groups))
	groupIDs := make([]uuid.UUID, 0, len(groups))
	memberCounts := make(map[uuid.UUID]int, len(groups))
	for _, g := range groups {
		groupIDs = append(groupIDs, g.ID)
	}
	if len(groupIDs) > 0 {
		members, err := s.client.GroupMember.Query().
			Where(
				groupmember.GroupChatIDIn(groupIDs...),
				groupmember.HasUserWith(user.DeletedAtIsNil()),
			).
			Select(groupmember.FieldGroupChatID).
			All(ctx)
		if err != nil {
			slog.Error("Failed to count group members", "error", err)
			return nil, "", false, helper.NewInternalServerError("Failed to fetch groups")
		}
		for _, member := range members {
			memberCounts[member.GroupChatID]++
		}
	}

	for _, g := range groups {
		createdAt := ""
		if g.Edges.Chat != nil {
			createdAt = g.Edges.Chat.CreatedAt.Format(time.RFC3339)
		}

		data = append(data, model.AdminGroupListResponse{
			ID:          g.ID,
			ChatID:      g.ChatID,
			Name:        g.Name,
			IsPublic:    g.IsPublic,
			MemberCount: memberCounts[g.ID],
			CreatedAt:   createdAt,
		})
	}

	return data, nextCursor, hasNext, nil
}

func (s *AdminService) GetGroupDetail(ctx context.Context, groupID uuid.UUID) (*model.AdminGroupDetailResponse, error) {
	g, err := s.client.GroupChat.Query().
		Where(groupchat.ChatID(groupID)).
		WithChat().
		WithCreator().
		WithAvatar().
		Only(ctx)
	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("Group not found")
		}
		slog.Error("Failed to get group detail", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	memberCount, err := g.QueryMembers().
		Where(groupmember.HasUserWith(user.DeletedAtIsNil())).
		Count(ctx)
	if err != nil {
		slog.Error("Failed to count group members", "error", err, "groupID", g.ID)
		return nil, helper.NewInternalServerError("")
	}

	messageCount := 0
	if g.Edges.Chat != nil {
		messageCount, err = s.client.Message.Query().
			Where(message.ChatID(g.Edges.Chat.ID)).
			Count(ctx)
		if err != nil {
			slog.Error("Failed to count group messages", "error", err, "groupID", g.ID, "chatID", g.Edges.Chat.ID)
			return nil, helper.NewInternalServerError("")
		}
	}

	resp := &model.AdminGroupDetailResponse{
		ID:            g.ID,
		ChatID:        g.ChatID,
		Name:          g.Name,
		Description:   g.Description,
		IsPublic:      g.IsPublic,
		MemberCount:   memberCount,
		TotalMessages: messageCount,
	}

	if g.Edges.Chat != nil {
		resp.CreatedAt = g.Edges.Chat.CreatedAt.Format(time.RFC3339)
	}

	if g.CreatedBy != nil {
		resp.CreatorID = g.CreatedBy
	}

	if g.Edges.Creator != nil && g.Edges.Creator.FullName != nil {
		resp.CreatorName = g.Edges.Creator.FullName
	}

	if g.Edges.Avatar != nil {
		resp.Avatar = s.storageAdapter.GetPublicURL(g.Edges.Avatar.FileName)
	}

	return resp, nil
}
