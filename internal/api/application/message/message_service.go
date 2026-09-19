package message

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/ent/userblock"
	"AtoiTalkAPI/internal/domain/constant"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/domain/model"
	"AtoiTalkAPI/internal/infrastructure/config"
	"AtoiTalkAPI/internal/infrastructure/database/mapper"
	"AtoiTalkAPI/internal/infrastructure/database/repository"
	objectstorage "AtoiTalkAPI/internal/infrastructure/object_storage"
	"AtoiTalkAPI/internal/infrastructure/observability"
	"AtoiTalkAPI/internal/messaging/events"
	"context"
	"encoding/base64"
	"errors"
	"log/slog"
	"strings"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

type MessageService struct {
	client         *ent.Client
	messageRepo    repository.MessageReader
	cfg            *config.AppConfig
	validator      *validator.Validate
	storageAdapter objectstorage.URLGenerator
	wsHub          events.Publisher
}

func runPaginationAvailabilityChecks(ctx context.Context, nextCheck, prevCheck func(context.Context) (bool, error)) (hasNext, hasPrev bool, err error) {
	hasNext, err = nextCheck(ctx)
	if err != nil {
		return false, false, err
	}
	hasPrev, err = prevCheck(ctx)
	if err != nil {
		return false, false, err
	}
	return hasNext, hasPrev, nil
}

func NewMessageService(client *ent.Client, messageRepo repository.MessageReader, cfg *config.AppConfig, validator *validator.Validate, storageAdapter objectstorage.URLGenerator, wsHub events.Publisher) *MessageService {
	return &MessageService{
		client:         client,
		messageRepo:    messageRepo,
		cfg:            cfg,
		validator:      validator,
		storageAdapter: storageAdapter,
		wsHub:          wsHub,
	}
}

func (s *MessageService) SendMessage(ctx context.Context, userID uuid.UUID, req model.SendMessageRequest) (*model.MessageResponse, error) {
	ctx, span := observability.StartServiceSpan(ctx, "service.message.send")
	defer span.End()

	if err := s.validator.Struct(req); err != nil {
		observability.RecordError(span, err)
		slog.Warn("Validation failed", "error", err, "userID", userID)
		return nil, helper.NewBadRequestError("")
	}
	if len(req.AttachmentIDs) > constant.MaxMessageAttachments {
		return nil, helper.NewBadRequestError("")
	}
	if err := helper.EnsureUniqueUUIDs(req.AttachmentIDs); err != nil {
		return nil, helper.NewBadRequestError("")
	}

	req.Content = strings.TrimSpace(req.Content)

	var msg *ent.Message

	txCtx, txSpan := observability.StartServiceSpan(ctx, "service.message.transaction")
	txSpanEnded := false
	defer func() {
		if !txSpanEnded {
			txSpan.End()
		}
	}()
	tx, err := s.client.Tx(txCtx)
	if err != nil {
		observability.RecordError(txSpan, err)
		observability.RecordError(span, err)
		slog.Error("Failed to start transaction", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	defer func() {
		_ = tx.Rollback()
		if v := recover(); v != nil {
			panic(v)
		}
	}()

	chatType, err := tx.Chat.Query().
		Where(
			chat.ID(req.ChatID),
			chat.DeletedAtIsNil(),
		).
		Select(chat.FieldID, chat.FieldType).
		Only(txCtx)

	if err != nil {
		observability.RecordError(span, err)
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("Chat not found or deleted")
		}
		slog.Error("Failed to query chat info", "error", err, "chatID", req.ChatID)
		return nil, helper.NewInternalServerError("")
	}

	chatQuery := tx.Chat.Query().Where(
		chat.ID(req.ChatID),
		chat.DeletedAtIsNil(),
	)
	switch chatType.Type {
	case chat.TypePrivate:
		chatQuery.WithPrivateChat(func(q *ent.PrivateChatQuery) {
			q.WithUser1(func(uq *ent.UserQuery) {
				uq.Select(user.FieldID, user.FieldDeletedAt, user.FieldIsBanned, user.FieldBannedUntil)
			})
			q.WithUser2(func(uq *ent.UserQuery) {
				uq.Select(user.FieldID, user.FieldDeletedAt, user.FieldIsBanned, user.FieldBannedUntil)
			})
		})
	case chat.TypeGroup:
		chatQuery.WithGroupChat(func(q *ent.GroupChatQuery) {
			q.WithMembers(func(mq *ent.GroupMemberQuery) {
				mq.Where(groupmember.UserID(userID))
			})
		})
	}

	chatInfo, err := chatQuery.Only(txCtx)
	if err != nil {
		observability.RecordError(span, err)
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("Chat not found or deleted")
		}
		slog.Error("Failed to query chat details", "error", err, "chatID", req.ChatID)
		return nil, helper.NewInternalServerError("")
	}

	var senderRole string

	if chatInfo.Type == chat.TypePrivate && chatInfo.Edges.PrivateChat != nil {
		pc := chatInfo.Edges.PrivateChat
		var otherUserID uuid.UUID
		var otherUser *ent.User

		if pc.User1ID != nil && *pc.User1ID == userID {
			if pc.User2ID != nil {
				otherUserID = *pc.User2ID
				otherUser = pc.Edges.User2
			}
		} else if pc.User2ID != nil && *pc.User2ID == userID {
			if pc.User1ID != nil {
				otherUserID = *pc.User1ID
				otherUser = pc.Edges.User1
			}
		} else {
			return nil, helper.NewForbiddenError("")
		}

		if otherUserID == uuid.Nil {
			return nil, helper.NewForbiddenError("User does not exist")
		}

		if otherUser != nil && otherUser.DeletedAt != nil {
			return nil, helper.NewForbiddenError("User is deleted")
		}

		if otherUser != nil && otherUser.IsBanned {
			if otherUser.BannedUntil == nil || time.Now().UTC().Before(*otherUser.BannedUntil) {
				return nil, helper.NewForbiddenError("User is currently suspended/banned")
			}
		}

		isBlocked, err := tx.UserBlock.Query().
			Where(
				userblock.Or(
					userblock.And(
						userblock.BlockerID(userID),
						userblock.BlockedID(otherUserID),
					),
					userblock.And(
						userblock.BlockerID(otherUserID),
						userblock.BlockedID(userID),
					),
				),
			).
			Exist(txCtx)
		if err != nil {
			slog.Error("Failed to check block status", "error", err)
			return nil, helper.NewInternalServerError("")
		}
		if isBlocked {
			return nil, helper.NewForbiddenError("")
		}

	} else if chatInfo.Type == chat.TypeGroup && chatInfo.Edges.GroupChat != nil {
		if len(chatInfo.Edges.GroupChat.Edges.Members) == 0 {
			return nil, helper.NewForbiddenError("")
		}
		senderRole = string(chatInfo.Edges.GroupChat.Edges.Members[0].Role)
	} else {
		return nil, helper.NewInternalServerError("")
	}

	if req.ReplyToID != nil {
		replyMsgExists, err := tx.Message.Query().
			Where(
				message.ID(*req.ReplyToID),
				message.ChatID(req.ChatID),
				message.DeletedAtIsNil(),
				message.TypeEQ(message.TypeRegular),
			).
			Exist(txCtx)

		if err != nil {
			slog.Error("Failed to check reply message existence", "error", err)
			return nil, helper.NewInternalServerError("")
		}
		if !replyMsgExists {
			return nil, helper.NewBadRequestError("Cannot reply to this message")
		}
	}

	if len(req.AttachmentIDs) > 0 {
		count, err := tx.Media.Query().
			Where(
				media.IDIn(req.AttachmentIDs...),
				media.MessageIDIsNil(),
				media.CategoryEQ(media.CategoryMessageAttachment),
				media.UploadStatusEQ(media.UploadStatusCompleted),
				media.HasUploaderWith(user.ID(userID)),
			).
			Count(txCtx)

		if err != nil {
			slog.Error("Failed to count valid media", "error", err)
			return nil, helper.NewInternalServerError("")
		}

		if count != len(req.AttachmentIDs) {
			return nil, helper.NewBadRequestError("")
		}
	}

	msgCreate := tx.Message.Create().
		SetChatID(req.ChatID).
		SetSenderID(userID).
		SetType(message.TypeRegular).
		SetContent(req.Content)

	if req.ReplyToID != nil {
		msgCreate.SetReplyToID(*req.ReplyToID)
	}

	if len(req.AttachmentIDs) > 0 {
		msgCreate.AddAttachmentIDs(req.AttachmentIDs...)
	}

	msg, err = msgCreate.Save(txCtx)
	if err != nil {
		observability.RecordError(span, err)
		slog.Error("Failed to save message", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if _, err := tx.MessageOutbox.Create().
		SetEventType(string(events.EventMessageNew)).
		SetMessageID(msg.ID).
		SetChatID(req.ChatID).
		SetSenderID(userID).
		Save(txCtx); err != nil {
		observability.RecordError(span, err)
		slog.Error("Failed to persist message outbox event", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if err := tx.Commit(); err != nil {
		observability.RecordError(txSpan, err)
		observability.RecordError(span, err)
		txSpan.End()
		txSpanEnded = true
		slog.Error("Failed to commit transaction", "error", err)
		return nil, helper.NewInternalServerError("")
	}
	txSpan.End()
	txSpanEnded = true

	responseCtx, responseSpan := observability.StartServiceSpan(ctx, "service.message.response.fetch")
	fullMsg := msg
	if len(req.AttachmentIDs) == 0 && req.ReplyToID == nil {
		sender, err := s.client.User.Query().
			Where(user.ID(userID)).
			WithAvatar().
			Only(responseCtx)
		if err != nil {
			observability.RecordError(responseSpan, err)
			observability.RecordError(span, err)
			responseSpan.End()
			slog.Error("Failed to fetch message sender for response", "error", err)
			return nil, helper.NewInternalServerError("")
		}
		msg.Edges.Sender = sender
	} else {
		fullMsgQuery := s.client.Message.Query().
			Where(message.ID(msg.ID)).
			WithSender(func(uq *ent.UserQuery) {
				uq.WithAvatar()
			})
		if len(req.AttachmentIDs) > 0 {
			fullMsgQuery.WithAttachments()
		}
		if req.ReplyToID != nil {
			fullMsgQuery.WithReplyTo(func(q *ent.MessageQuery) {
				q.WithSender(func(uq *ent.UserQuery) {
					uq.WithAvatar()
				})
				q.WithAttachments(func(aq *ent.MediaQuery) {
					aq.Limit(1)
				})
			})
		}
		fullMsg, err = fullMsgQuery.Only(responseCtx)
		if err != nil {
			observability.RecordError(responseSpan, err)
			observability.RecordError(span, err)
			responseSpan.End()
			slog.Error("Failed to fetch full message for response", "error", err)
			return nil, helper.NewInternalServerError("")
		}
	}
	responseSpan.End()

	resp := mapper.ToMessageResponse(fullMsg, s.storageAdapter, nil, senderRole)

	return resp, nil
}

func (s *MessageService) EditMessage(ctx context.Context, userID uuid.UUID, messageID uuid.UUID, req model.EditMessageRequest) (*model.MessageResponse, error) {
	if err := s.validator.Struct(req); err != nil {
		slog.Warn("Validation failed", "error", err, "userID", userID)
		return nil, helper.NewBadRequestError("")
	}
	if len(req.AttachmentIDs) > constant.MaxMessageAttachments {
		return nil, helper.NewBadRequestError("")
	}
	if req.HasAttachmentIDs {
		if err := helper.EnsureUniqueUUIDs(req.AttachmentIDs); err != nil {
			return nil, helper.NewBadRequestError("")
		}
	}

	req.Content = strings.TrimSpace(req.Content)

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

	msg, err := tx.Message.Query().
		Where(message.ID(messageID)).
		WithChat(func(q *ent.ChatQuery) {
			q.WithGroupChat()
		}).
		WithAttachments().
		Only(ctx)

	if err != nil {
		if ent.IsNotFound(err) {
			return nil, helper.NewNotFoundError("")
		}
		slog.Error("Failed to query message", "error", err, "messageID", messageID)
		return nil, helper.NewInternalServerError("")
	}

	if msg.Edges.Chat != nil && msg.Edges.Chat.DeletedAt != nil {
		return nil, helper.NewBadRequestError("Chat is deleted")
	}

	if msg.SenderID == nil || *msg.SenderID != userID {
		return nil, helper.NewForbiddenError("")
	}

	if msg.Type != message.TypeRegular {
		return nil, helper.NewBadRequestError("Cannot edit this type of message")
	}

	if msg.DeletedAt != nil {
		return nil, helper.NewBadRequestError("Cannot edit a deleted message")
	}

	if time.Since(msg.CreatedAt) > 15*time.Minute {
		return nil, helper.NewBadRequestError("Message is too old to edit")
	}

	var senderRole string
	if msg.Edges.Chat.Type == chat.TypeGroup && msg.Edges.Chat.Edges.GroupChat != nil {
		member, err := tx.GroupMember.Query().
			Where(
				groupmember.GroupChatID(msg.Edges.Chat.Edges.GroupChat.ID),
				groupmember.UserID(userID),
			).
			Only(ctx)
		if err == nil {
			senderRole = string(member.Role)
		}
	}

	currentAttachmentIDs := make(map[uuid.UUID]bool)
	for _, att := range msg.Edges.Attachments {
		currentAttachmentIDs[att.ID] = true
	}

	var toUnlink []uuid.UUID
	var toLink []uuid.UUID
	if req.HasAttachmentIDs {
		newAttachmentIDs := make(map[uuid.UUID]bool)
		for _, id := range req.AttachmentIDs {
			newAttachmentIDs[id] = true
		}

		for id := range currentAttachmentIDs {
			if !newAttachmentIDs[id] {
				toUnlink = append(toUnlink, id)
			}
		}

		for id := range newAttachmentIDs {
			if !currentAttachmentIDs[id] {
				toLink = append(toLink, id)
			}
		}
	}

	contentChanged := req.Content != *msg.Content
	attachmentsChanged := len(toUnlink) > 0 || len(toLink) > 0

	if !contentChanged && !attachmentsChanged {
		fullMsg, err := s.client.Message.Query().
			Where(message.ID(msg.ID)).
			WithSender(func(uq *ent.UserQuery) {
				uq.WithAvatar()
			}).
			WithAttachments().
			WithReplyTo(func(q *ent.MessageQuery) {
				q.WithSender(func(uq *ent.UserQuery) {
					uq.WithAvatar()
				})
				q.WithAttachments(func(aq *ent.MediaQuery) {
					aq.Limit(1)
				})
			}).
			Only(ctx)

		if err != nil {
			slog.Error("Failed to fetch full message for response", "error", err)
			return nil, helper.NewInternalServerError("")
		}

		return mapper.ToMessageResponse(fullMsg, s.storageAdapter, nil, senderRole), nil
	}

	if len(toLink) > 0 {
		count, err := tx.Media.Query().
			Where(
				media.IDIn(toLink...),
				media.MessageIDIsNil(),
				media.Not(media.HasUserAvatar()),
				media.Not(media.HasGroupAvatar()),
				media.HasUploaderWith(user.ID(userID)),
			).
			Count(ctx)

		if err != nil {
			slog.Error("Failed to validate new attachments", "error", err)
			return nil, helper.NewInternalServerError("")
		}

		if count != len(toLink) {
			return nil, helper.NewBadRequestError("Invalid attachments")
		}
	}

	update := tx.Message.UpdateOne(msg).
		SetContent(req.Content).
		SetEditedAt(time.Now().UTC())

	if len(toUnlink) > 0 {
		update.RemoveAttachmentIDs(toUnlink...)
	}
	if len(toLink) > 0 {
		update.AddAttachmentIDs(toLink...)
	}

	msg, err = update.Save(ctx)
	if err != nil {
		slog.Error("Failed to update message", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if _, err := tx.MessageOutbox.Create().
		SetEventType(string(events.EventMessageUpdate)).
		SetMessageID(msg.ID).
		SetChatID(msg.ChatID).
		SetSenderID(userID).
		Save(ctx); err != nil {
		slog.Error("Failed to persist message update outbox event", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	fullMsg, err := s.client.Message.Query().
		Where(message.ID(msg.ID)).
		WithSender(func(uq *ent.UserQuery) {
			uq.WithAvatar()
		}).
		WithAttachments().
		WithReplyTo(func(q *ent.MessageQuery) {
			q.WithSender(func(uq *ent.UserQuery) {
				uq.WithAvatar()
			})
			q.WithAttachments(func(aq *ent.MediaQuery) {
				aq.Limit(1)
			})
		}).
		Only(ctx)

	if err != nil {
		slog.Error("Failed to fetch full message for response", "error", err)
		return nil, helper.NewInternalServerError("")
	}

	resp := mapper.ToMessageResponse(fullMsg, s.storageAdapter, nil, senderRole)

	return resp, nil
}

func (s *MessageService) GetMessages(ctx context.Context, userID uuid.UUID, req model.GetMessagesRequest) ([]model.MessageResponse, string, bool, string, bool, error) {
	ctx, span := observability.StartServiceSpan(ctx, "service.message.get_messages")
	defer span.End()

	if err := s.validator.Struct(req); err != nil {
		observability.RecordError(span, err)
		return nil, "", false, "", false, helper.NewBadRequestError("")
	}

	if req.Limit == 0 {
		req.Limit = 20
	}

	accessCtx, accessSpan := observability.StartServiceSpan(ctx, "service.message.database.chat_access")
	chatInfo, err := s.client.Chat.Query().
		Where(
			chat.ID(req.ChatID),
			chat.DeletedAtIsNil(),
		).
		WithPrivateChat().
		Only(accessCtx)
	if err != nil {
		observability.RecordError(accessSpan, err)
		accessSpan.End()
		observability.RecordError(span, err)
		if ent.IsNotFound(err) {
			return nil, "", false, "", false, helper.NewNotFoundError("Chat not found or deleted")
		}
		slog.Error("Failed to query chat details", "error", err, "chatID", req.ChatID)
		return nil, "", false, "", false, helper.NewInternalServerError("")
	}

	isMember := false
	var hiddenAt *time.Time
	var groupChat *ent.GroupChat

	if chatInfo.Type == chat.TypePrivate && chatInfo.Edges.PrivateChat != nil {
		pc := chatInfo.Edges.PrivateChat
		if pc.User1ID != nil && *pc.User1ID == userID {
			isMember = true
			hiddenAt = pc.User1HiddenAt
		} else if pc.User2ID != nil && *pc.User2ID == userID {
			isMember = true
			hiddenAt = pc.User2HiddenAt
		}
	} else if chatInfo.Type == chat.TypeGroup {
		var errGroup error
		groupChat, errGroup = s.client.GroupChat.Query().
			Where(groupchat.ChatID(req.ChatID)).
			WithMembers(func(mq *ent.GroupMemberQuery) {
				mq.Where(groupmember.UserID(userID))
			}).
			Only(accessCtx)
		if errGroup != nil {
			observability.RecordError(accessSpan, errGroup)
			accessSpan.End()
			observability.RecordError(span, errGroup)
			slog.Error("Failed to query group details", "error", errGroup, "chatID", req.ChatID)
			return nil, "", false, "", false, helper.NewInternalServerError("")
		}
		if len(groupChat.Edges.Members) > 0 {
			isMember = true
		}
	}
	accessSpan.End()

	if !isMember {
		return nil, "", false, "", false, helper.NewForbiddenError("")
	}

	var messages []*ent.Message
	var errRepo error
	var aroundPage *repository.MessageAroundPage

	repositoryCtx, repositorySpan := observability.StartServiceSpan(ctx, "service.message.repository.fetch")
	if req.AroundMessageID != nil {
		if pageReader, ok := s.messageRepo.(repository.MessageAroundPageReader); ok {
			aroundPage, errRepo = pageReader.GetMessagesAroundPage(repositoryCtx, req.ChatID, hiddenAt, *req.AroundMessageID, req.Limit)
			if aroundPage != nil {
				messages = aroundPage.Messages
			}
		} else {
			messages, errRepo = s.messageRepo.GetMessagesAround(repositoryCtx, req.ChatID, hiddenAt, *req.AroundMessageID, req.Limit)
		}
	} else {

		var cursorID uuid.UUID
		if req.Cursor != "" {
			decodedBytes, err := base64.URLEncoding.DecodeString(req.Cursor)
			if err != nil {
				return nil, "", false, "", false, helper.NewBadRequestError("Invalid cursor format")
			}
			cursorID, err = uuid.Parse(string(decodedBytes))
			if err != nil {
				return nil, "", false, "", false, helper.NewBadRequestError("Invalid cursor format")
			}
		}
		messages, errRepo = s.messageRepo.GetMessages(repositoryCtx, req.ChatID, hiddenAt, cursorID, req.Limit, req.Direction)
	}
	if errRepo != nil {
		observability.RecordError(repositorySpan, errRepo)
		repositorySpan.End()
		observability.RecordError(span, errRepo)
		if ent.IsNotFound(errRepo) || errors.Is(errRepo, repository.ErrMessageNotFound) {
			return nil, "", false, "", false, helper.NewNotFoundError("Message not found")
		}
		slog.Error("Failed to get messages", "error", errRepo)
		return nil, "", false, "", false, helper.NewInternalServerError("")
	}
	repositorySpan.End()

	userIDsToResolve := make(map[uuid.UUID]bool)
	for _, msg := range messages {
		if msg.ActionData != nil {
			if targetIDStr, ok := msg.ActionData["target_id"].(string); ok {
				if id, err := uuid.Parse(targetIDStr); err == nil {
					userIDsToResolve[id] = true
				}
			}
			if actorIDStr, ok := msg.ActionData["actor_id"].(string); ok {
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

		if err != nil {
			slog.Error("Failed to resolve users for system messages", "error", err)
		} else {
			for _, u := range users {
				userMap[u.ID] = u
			}
		}
	}

	senderRoleMap := make(map[uuid.UUID]string)
	if chatInfo.Type == chat.TypeGroup && groupChat != nil {
		senderIDs := make([]uuid.UUID, 0)
		seenSenders := make(map[uuid.UUID]bool)
		for _, m := range messages {
			if m.SenderID != nil && !seenSenders[*m.SenderID] {
				senderIDs = append(senderIDs, *m.SenderID)
				seenSenders[*m.SenderID] = true
			}
		}

		if len(senderIDs) > 0 {
			members, err := s.client.GroupMember.Query().
				Where(
					groupmember.GroupChatID(groupChat.ID),
					groupmember.UserIDIn(senderIDs...),
					groupmember.HasUserWith(user.DeletedAtIsNil()),
				).
				Select(groupmember.FieldUserID, groupmember.FieldRole).
				All(ctx)

			if err == nil {
				for _, m := range members {
					senderRoleMap[m.UserID] = string(m.Role)
				}
			} else {
				slog.Error("Failed to batch fetch sender roles", "error", err)
			}
		}
	}

	hasNext := false
	var nextCursor string
	hasPrev := false
	var prevCursor string

	if len(messages) > 0 {
		paginationCtx, paginationSpan := observability.StartServiceSpan(ctx, "service.message.database.pagination")
		hasExtraMessage := req.AroundMessageID == nil && len(messages) > req.Limit

		if hasExtraMessage {
			if req.Direction == "newer" {
				messages = messages[:req.Limit]
			} else {
				messages = messages[:req.Limit]
			}
		}

		if req.AroundMessageID == nil && req.Direction != "newer" {

			for i, j := 0, len(messages)-1; i < j; i, j = i+1, j-1 {
				messages[i], messages[j] = messages[j], messages[i]
			}
		}

		firstMsg := messages[0]
		lastMsg := messages[len(messages)-1]

		nextCursor = base64.URLEncoding.EncodeToString([]byte(firstMsg.ID.String()))
		prevCursor = base64.URLEncoding.EncodeToString([]byte(lastMsg.ID.String()))

		if hasExtraMessage {
			if req.Direction == "newer" {
				hasPrev = true
			} else {
				hasNext = true
			}
		}

		needsNextCheck := !hasExtraMessage
		needsPrevCheck := !hasExtraMessage || req.Direction != "newer"
		if aroundPage != nil {
			hasNext = aroundPage.HasOlder
			hasPrev = aroundPage.HasNewer
			needsNextCheck = false
			needsPrevCheck = false
		}
		if needsNextCheck && needsPrevCheck {
			hasNext, hasPrev, err = runPaginationAvailabilityChecks(
				paginationCtx,
				func(ctx context.Context) (bool, error) {
					nextQuery := s.client.Message.Query().
						Where(
							message.ChatID(req.ChatID),
							message.IDLT(firstMsg.ID),
						)
					if hiddenAt != nil {
						nextQuery = nextQuery.Where(message.CreatedAtGT(*hiddenAt))
					}
					return nextQuery.Exist(ctx)
				},
				func(ctx context.Context) (bool, error) {
					prevQuery := s.client.Message.Query().
						Where(
							message.ChatID(req.ChatID),
							message.IDGT(lastMsg.ID),
						)
					if hiddenAt != nil {
						prevQuery = prevQuery.Where(message.CreatedAtGT(*hiddenAt))
					}
					return prevQuery.Exist(ctx)
				},
			)
			if err != nil {
				observability.RecordError(paginationSpan, err)
				observability.RecordError(span, err)
				paginationSpan.End()
				slog.Error("Failed to evaluate message page availability", "error", err, "chatID", req.ChatID)
				return nil, "", false, "", false, helper.NewInternalServerError("")
			}
		} else {
			if needsNextCheck {
				nextQuery := s.client.Message.Query().
					Where(
						message.ChatID(req.ChatID),
						message.IDLT(firstMsg.ID),
					)
				if hiddenAt != nil {
					nextQuery = nextQuery.Where(message.CreatedAtGT(*hiddenAt))
				}
				hasNext, err = nextQuery.Exist(paginationCtx)
				if err != nil {
					observability.RecordError(paginationSpan, err)
					observability.RecordError(span, err)
					paginationSpan.End()
					slog.Error("Failed to evaluate next page availability", "error", err, "chatID", req.ChatID)
					return nil, "", false, "", false, helper.NewInternalServerError("")
				}
			}
			if needsPrevCheck {
				prevQuery := s.client.Message.Query().
					Where(
						message.ChatID(req.ChatID),
						message.IDGT(lastMsg.ID),
					)
				if hiddenAt != nil {
					prevQuery = prevQuery.Where(message.CreatedAtGT(*hiddenAt))
				}
				hasPrev, err = prevQuery.Exist(paginationCtx)
				if err != nil {
					observability.RecordError(paginationSpan, err)
					observability.RecordError(span, err)
					paginationSpan.End()
					slog.Error("Failed to evaluate previous page availability", "error", err, "chatID", req.ChatID)
					return nil, "", false, "", false, helper.NewInternalServerError("")
				}
			}
		}
		paginationSpan.End()
	}

	response := make([]model.MessageResponse, 0, len(messages))
	for _, msg := range messages {
		var role string
		if msg.SenderID != nil {
			role = senderRoleMap[*msg.SenderID]
		}
		resp := mapper.ToMessageResponse(msg, s.storageAdapter, hiddenAt, role)
		if resp != nil {

			if resp.ActionData != nil {

				if targetIDStr, ok := resp.ActionData["target_id"].(string); ok {
					if id, err := uuid.Parse(targetIDStr); err == nil {
						if u, exists := userMap[id]; exists {
							if u.DeletedAt != nil {
								delete(resp.ActionData, "target_id")
								resp.ActionData["target_name"] = "Deleted User"
							} else if u.FullName != nil {
								resp.ActionData["target_name"] = *u.FullName
							}
						}
					}
				}

				if actorIDStr, ok := resp.ActionData["actor_id"].(string); ok {
					if id, err := uuid.Parse(actorIDStr); err == nil {
						if u, exists := userMap[id]; exists {
							if u.DeletedAt != nil {
								delete(resp.ActionData, "actor_id")
								resp.ActionData["actor_name"] = "Deleted User"
							} else if u.FullName != nil {
								resp.ActionData["actor_name"] = *u.FullName
							}
						}
					}
				}
			}
			response = append(response, *resp)
		}
	}

	return response, nextCursor, hasNext, prevCursor, hasPrev, nil
}

func (s *MessageService) DeleteMessage(ctx context.Context, userID uuid.UUID, messageID uuid.UUID) error {
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

	msg, err := tx.Message.Query().
		Where(message.ID(messageID)).
		WithChat().
		Only(ctx)

	if err != nil {
		if ent.IsNotFound(err) {
			return helper.NewNotFoundError("")
		}
		slog.Error("Failed to query message", "error", err, "messageID", messageID)
		return helper.NewInternalServerError("")
	}

	if msg.Edges.Chat != nil && msg.Edges.Chat.DeletedAt != nil {
		return helper.NewBadRequestError("Chat is deleted")
	}

	canDelete := false
	if msg.SenderID != nil && *msg.SenderID == userID {
		canDelete = true
	} else if msg.Edges.Chat != nil && msg.Edges.Chat.Type == chat.TypeGroup {

		count, err := tx.GroupMember.Query().
			Where(
				groupmember.UserID(userID),
				groupmember.RoleIn(groupmember.RoleAdmin, groupmember.RoleOwner),
				groupmember.HasGroupChatWith(groupchat.ChatID(msg.ChatID)),
			).
			Count(ctx)
		if err == nil && count > 0 {
			canDelete = true
		}
	}

	if !canDelete {
		return helper.NewForbiddenError("")
	}

	if msg.Type != message.TypeRegular {
		return helper.NewBadRequestError("Cannot delete this type of message")
	}

	if msg.DeletedAt != nil {
		return helper.NewBadRequestError("Message already deleted")
	}

	err = tx.Message.UpdateOne(msg).
		SetDeletedAt(time.Now().UTC()).
		ClearContent().
		ClearAttachments().
		Exec(ctx)

	if err != nil {
		slog.Error("Failed to delete message", "error", err, "messageID", messageID)
		return helper.NewInternalServerError("")
	}

	if _, err := tx.MessageOutbox.Create().
		SetEventType(string(events.EventMessageDelete)).
		SetMessageID(msg.ID).
		SetChatID(msg.ChatID).
		SetSenderID(userID).
		Save(ctx); err != nil {
		slog.Error("Failed to persist message delete outbox event", "error", err)
		return helper.NewInternalServerError("")
	}

	if err := tx.Commit(); err != nil {
		slog.Error("Failed to commit transaction", "error", err)
		return helper.NewInternalServerError("")
	}

	return nil
}
