package messageworker

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/messageoutbox"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/ent/userblock"
	"AtoiTalkAPI/internal/infrastructure/database/mapper"
	objectstorage "AtoiTalkAPI/internal/infrastructure/object_storage"
	"AtoiTalkAPI/internal/infrastructure/observability"
	"AtoiTalkAPI/internal/messaging/events"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
)

const maxStoredOutboxErrorLength = 2048

var ErrOutboxLeaseLost = errors.New("outbox lease ownership lost")

type EntOutboxStore struct {
	client  *ent.Client
	dialect string
	now     func() time.Time
	urlGen  objectstorage.URLGenerator
}

func NewEntOutboxStore(client *ent.Client) *EntOutboxStore {
	return NewEntOutboxStoreWithDialect(client, dialect.Postgres)
}

func NewEntOutboxStoreWithDialect(client *ent.Client, driverDialect string) *EntOutboxStore {
	return &EntOutboxStore{client: client, dialect: driverDialect, now: time.Now}
}

func NewEntOutboxStoreWithURLGenerator(client *ent.Client, urlGen objectstorage.URLGenerator) *EntOutboxStore {
	store := NewEntOutboxStore(client)
	store.urlGen = urlGen
	return store
}

func (s *EntOutboxStore) BuildEvents(ctx context.Context, record OutboxRecord) ([]events.MessageEvent, error) {
	if record.EventType != string(events.EventMessageNew) && record.EventType != string(events.EventMessageUpdate) && record.EventType != string(events.EventMessageDelete) {
		return nil, fmt.Errorf("unsupported outbox event type %q", record.EventType)
	}

	if record.MessageID == uuid.Nil || record.ChatID == uuid.Nil {
		return nil, errors.New("message and chat IDs are required to build events")
	}

	targets, err := s.eventTargets(ctx, record)
	if err != nil {
		return nil, err
	}

	var payloadData interface{}
	if record.EventType == string(events.EventMessageDelete) {
		payloadData = map[string]uuid.UUID{"message_id": record.MessageID}
	} else {
		msg, loadErr := s.client.Message.Query().
			Where(message.ID(record.MessageID)).
			WithSender(func(query *ent.UserQuery) {
				query.Select(user.FieldID, user.FieldFullName, user.FieldDeletedAt)
				query.WithAvatar()
			}).
			WithAttachments().
			WithReplyTo(func(query *ent.MessageQuery) {
				query.WithSender(func(senderQuery *ent.UserQuery) {
					senderQuery.Select(user.FieldID, user.FieldFullName, user.FieldDeletedAt)
				})
			}).
			Only(ctx)
		if loadErr != nil {
			return nil, fmt.Errorf("load message %s for events: %w", record.MessageID, loadErr)
		}
		if msg.Edges.Sender == nil && record.SenderID != uuid.Nil {
			return nil, fmt.Errorf("message %s sender is unavailable", record.MessageID)
		}
		payloadData = mapper.ToMessageResponse(msg, s.urlGen, nil, "")
	}

	unreadMap, countErr := s.batchRecipientsUnreadCount(ctx, record.ChatID, targets)
	if countErr != nil {
		return nil, countErr
	}

	built := make([]events.MessageEvent, 0, len(targets))
	for _, target := range targets {
		unreadCount := unreadMap[target]
		payload, marshalErr := json.Marshal(events.Event{
			Type:    events.EventType(record.EventType),
			Payload: payloadData,
			Meta: &events.EventMeta{
				Timestamp:   s.now().UTC().UnixMilli(),
				ChatID:      record.ChatID,
				SenderID:    record.SenderID,
				UnreadCount: unreadCount,
			},
		})
		if marshalErr != nil {
			return nil, fmt.Errorf("marshal client event for message %s: %w", record.MessageID, marshalErr)
		}
		built = append(built, events.MessageEvent{
			ID:           uuid.NewSHA1(uuid.Nil, []byte(record.ID.String()+":"+target.String())),
			OutboxID:     record.ID,
			Type:         record.EventType,
			MessageID:    record.MessageID,
			ChatID:       record.ChatID,
			SenderID:     record.SenderID,
			TargetUserID: target,
			Payload:      payload,
		})
	}
	return built, nil
}

func (s *EntOutboxStore) batchRecipientsUnreadCount(ctx context.Context, chatID uuid.UUID, targets []uuid.UUID) (map[uuid.UUID]int, error) {
	unreadMap := make(map[uuid.UUID]int, len(targets))
	if len(targets) == 0 {
		return unreadMap, nil
	}

	private, err := s.client.PrivateChat.Query().Where(privatechat.ChatID(chatID)).Only(ctx)
	if err == nil {
		for _, target := range targets {
			if private.User1ID != nil && *private.User1ID == target {
				unreadMap[target] = private.User1UnreadCount
			} else if private.User2ID != nil && *private.User2ID == target {
				unreadMap[target] = private.User2UnreadCount
			}
		}
		return unreadMap, nil
	}
	if !ent.IsNotFound(err) {
		return nil, fmt.Errorf("load private unread state for %s: %w", chatID, err)
	}

	group, err := s.client.GroupChat.Query().Where(groupchat.ChatID(chatID)).Select(groupchat.FieldID).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("load group chat for unread state %s: %w", chatID, err)
	}

	members, err := s.client.GroupMember.Query().
		Where(
			groupmember.GroupChatID(group.ID),
			groupmember.UserIDIn(targets...),
		).
		Select(groupmember.FieldUserID, groupmember.FieldUnreadCount).
		All(ctx)
	if err != nil {
		return nil, fmt.Errorf("batch load recipient unread state for group %s: %w", group.ID, err)
	}

	for _, m := range members {
		unreadMap[m.UserID] = m.UnreadCount
	}
	return unreadMap, nil
}

func (s *EntOutboxStore) eventTargets(ctx context.Context, record OutboxRecord) ([]uuid.UUID, error) {
	chatEntity, err := s.client.Chat.Query().Where(chat.ID(record.ChatID)).Select(chat.FieldType).Only(ctx)
	if err != nil {
		return nil, fmt.Errorf("load chat %s for events: %w", record.ChatID, err)
	}

	var candidates []uuid.UUID
	switch chatEntity.Type {
	case chat.TypePrivate:
		private, err := s.client.PrivateChat.Query().Where(privatechat.ChatID(record.ChatID)).Only(ctx)
		if err != nil {
			return nil, fmt.Errorf("load private chat %s for events: %w", record.ChatID, err)
		}
		if private.User1ID != nil {
			candidates = append(candidates, *private.User1ID)
		}
		if private.User2ID != nil {
			candidates = append(candidates, *private.User2ID)
		}
	case chat.TypeGroup:
		group, err := s.client.GroupChat.Query().Where(groupchat.ChatID(record.ChatID)).Only(ctx)
		if err != nil {
			return nil, fmt.Errorf("load group chat %s for events: %w", record.ChatID, err)
		}
		members, err := s.client.GroupMember.Query().
			Where(groupmember.GroupChatID(group.ID)).
			Select(groupmember.FieldUserID).
			All(ctx)
		if err != nil {
			return nil, fmt.Errorf("load group members %s for events: %w", record.ChatID, err)
		}
		for _, member := range members {
			candidates = append(candidates, member.UserID)
		}
	default:
		return nil, fmt.Errorf("unsupported chat type %q", chatEntity.Type)
	}

	targets := make([]uuid.UUID, 0, len(candidates))
	if len(candidates) > 0 && record.SenderID != uuid.Nil {
		blocks, err := s.client.UserBlock.Query().Where(
			userblock.Or(
				userblock.And(userblock.BlockerID(record.SenderID), userblock.BlockedIDIn(candidates...)),
				userblock.And(userblock.BlockerIDIn(candidates...), userblock.BlockedID(record.SenderID)),
			),
		).Select(userblock.FieldBlockerID, userblock.FieldBlockedID).All(ctx)
		if err != nil {
			return nil, fmt.Errorf("batch check message recipient block state: %w", err)
		}

		blockedSet := make(map[uuid.UUID]bool, len(blocks))
		for _, b := range blocks {
			if b.BlockerID == record.SenderID {
				blockedSet[b.BlockedID] = true
			} else {
				blockedSet[b.BlockerID] = true
			}
		}

		for _, candidate := range candidates {
			if candidate != record.SenderID && candidate != uuid.Nil && !blockedSet[candidate] {
				targets = append(targets, candidate)
			}
		}
	}
	return targets, nil
}

func (s *EntOutboxStore) Claim(ctx context.Context, limit int, leaseDuration time.Duration) ([]OutboxRecord, error) {
	if limit <= 0 {
		return nil, nil
	}
	if leaseDuration <= 0 {
		leaseDuration = 5 * time.Minute
	}

	now := s.now().UTC()
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return nil, fmt.Errorf("begin outbox claim transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	query := tx.MessageOutbox.Query().
		Where(
			messageoutbox.PublishedAtIsNil(),
			messageoutbox.AvailableAtLTE(now),
			messageoutbox.Or(
				messageoutbox.LockedAtIsNil(),
				messageoutbox.LockedAtLTE(now.Add(-leaseDuration)),
			),
		).
		Order(messageoutbox.ByCreatedAt()).
		Limit(limit)
	if s.dialect == dialect.Postgres {
		query.ForUpdate(entsql.WithLockAction(entsql.SkipLocked))
	}
	rows, err := query.All(ctx)
	if err != nil {
		return nil, fmt.Errorf("select outbox rows for claim: %w", err)
	}

	claimed := make([]OutboxRecord, 0, len(rows))
	if len(rows) > 0 {
		rowIDs := make([]uuid.UUID, len(rows))
		for i, row := range rows {
			rowIDs[i] = row.ID
		}
		lockToken := uuid.New()
		updated, err := tx.MessageOutbox.Update().
			Where(messageoutbox.IDIn(rowIDs...), messageoutbox.PublishedAtIsNil()).
			SetLockedAt(now).
			SetLockToken(lockToken).
			AddAttemptCount(1).
			ClearLastError().
			Save(ctx)
		if err != nil {
			return nil, fmt.Errorf("lease outbox batch: %w", err)
		}
		if updated != len(rows) {
			return nil, fmt.Errorf("lease outbox batch: updated %d of %d rows", updated, len(rows))
		}
		for _, row := range rows {
			record := outboxRecord(row)
			record.LockToken = lockToken
			record.AttemptCount++
			claimed = append(claimed, record)
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit outbox claim transaction: %w", err)
	}
	return claimed, nil
}

func (s *EntOutboxStore) Backlog(ctx context.Context) (observability.MessageWorkerBacklog, error) {
	var backlog observability.MessageWorkerBacklog
	var err error
	if backlog.Pending, err = s.client.MessageOutbox.Query().Where(messageoutbox.PublishedAtIsNil()).Count(ctx); err != nil {
		return backlog, fmt.Errorf("count pending outbox rows: %w", err)
	}
	if backlog.Locked, err = s.client.MessageOutbox.Query().Where(
		messageoutbox.PublishedAtIsNil(),
		messageoutbox.LockedAtNotNil(),
	).Count(ctx); err != nil {
		return backlog, fmt.Errorf("count locked outbox rows: %w", err)
	}
	if backlog.Unprojected, err = s.client.MessageOutbox.Query().Where(
		messageoutbox.PublishedAtIsNil(),
		messageoutbox.ProjectedAtIsNil(),
	).Count(ctx); err != nil {
		return backlog, fmt.Errorf("count unprojected outbox rows: %w", err)
	}
	backlog.Unpublished = backlog.Pending
	return backlog, nil
}

func (s *EntOutboxStore) MarkPublished(ctx context.Context, id, lockToken uuid.UUID, publishedAt time.Time) error {
	affected, err := s.client.MessageOutbox.Update().
		Where(
			messageoutbox.ID(id),
			messageoutbox.LockToken(lockToken),
			messageoutbox.PublishedAtIsNil(),
		).
		SetPublishedAt(publishedAt.UTC()).
		ClearLockedAt().
		ClearLockToken().
		ClearLastError().
		Save(ctx)
	if err != nil {
		return fmt.Errorf("mark outbox row %s published: %w", id, err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: mark published for %s", ErrOutboxLeaseLost, id)
	}
	return nil
}

func (s *EntOutboxStore) Project(ctx context.Context, record OutboxRecord) error {
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return fmt.Errorf("begin outbox projection transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	outbox, err := tx.MessageOutbox.Query().Where(
		messageoutbox.ID(record.ID),
		messageoutbox.LockToken(record.LockToken),
		messageoutbox.PublishedAtIsNil(),
	).Only(ctx)
	if err != nil {
		return fmt.Errorf("load outbox row %s for projection: %w", record.ID, err)
	}
	if outbox.ProjectedAt != nil {
		return nil
	}

	if record.EventType == string(events.EventMessageNew) {
		msg, err := tx.Message.Query().Where(message.ID(record.MessageID)).Only(ctx)
		if err != nil {
			return fmt.Errorf("load message %s for projection: %w", record.MessageID, err)
		}
		if _, err := tx.Chat.Update().Where(
			chat.ID(record.ChatID),
			chat.Or(chat.LastMessageAtIsNil(), chat.LastMessageAtLT(msg.CreatedAt)),
		).SetLastMessageID(msg.ID).SetLastMessageAt(msg.CreatedAt).Save(ctx); err != nil {
			return fmt.Errorf("project chat metadata for %s: %w", record.ChatID, err)
		}
		if err := s.projectUnread(ctx, tx, record); err != nil {
			return err
		}
	}

	if _, err := tx.MessageOutbox.UpdateOne(outbox).SetProjectedAt(s.now().UTC()).Save(ctx); err != nil {
		return fmt.Errorf("mark outbox row %s projected: %w", record.ID, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit outbox projection transaction: %w", err)
	}
	return nil
}

func (s *EntOutboxStore) projectUnread(ctx context.Context, tx *ent.Tx, record OutboxRecord) error {
	private, err := tx.PrivateChat.Query().Where(privatechat.ChatID(record.ChatID)).Only(ctx)
	if err == nil {
		now := s.now().UTC()
		switch {
		case private.User1ID != nil && *private.User1ID == record.SenderID:
			_, err = tx.PrivateChat.UpdateOne(private).
				SetUser1UnreadCount(0).
				SetUser1LastReadAt(now).
				AddUser2UnreadCount(1).
				Save(ctx)
		case private.User2ID != nil && *private.User2ID == record.SenderID:
			_, err = tx.PrivateChat.UpdateOne(private).
				SetUser2UnreadCount(0).
				SetUser2LastReadAt(now).
				AddUser1UnreadCount(1).
				Save(ctx)
		default:
			return fmt.Errorf("sender %s is not a member of private chat %s", record.SenderID, record.ChatID)
		}
		if err != nil {
			return fmt.Errorf("project private chat unread state for %s: %w", record.ChatID, err)
		}
		return nil
	}
	if !ent.IsNotFound(err) {
		return fmt.Errorf("load private chat for %s: %w", record.ChatID, err)
	}

	group, err := tx.GroupChat.Query().Where(groupchat.ChatID(record.ChatID)).Only(ctx)
	if err != nil {
		return fmt.Errorf("load group chat for %s: %w", record.ChatID, err)
	}
	if _, err := tx.GroupMember.Update().Where(groupmember.GroupChatID(group.ID)).Modify(func(builder *entsql.UpdateBuilder) {
		builder.Set(groupmember.FieldUnreadCount, entsql.ExprFunc(func(b *entsql.Builder) {
			b.WriteString("CASE WHEN ").Ident(groupmember.FieldUserID).WriteString(" = ").Arg(record.SenderID)
			b.WriteString(" THEN 0 ELSE ").Ident(groupmember.FieldUnreadCount).WriteString(" + 1 END")
		}))
		builder.Set(groupmember.FieldLastReadAt, entsql.ExprFunc(func(b *entsql.Builder) {
			b.WriteString("CASE WHEN ").Ident(groupmember.FieldUserID).WriteString(" = ").Arg(record.SenderID)
			b.WriteString(" THEN ").Arg(s.now().UTC()).WriteString(" ELSE ").Ident(groupmember.FieldLastReadAt).WriteString(" END")
		}))
	}).Save(ctx); err != nil {
		return fmt.Errorf("project group unread state for %s: %w", record.ChatID, err)
	}
	return nil
}

func (s *EntOutboxStore) MarkFailed(ctx context.Context, id, lockToken uuid.UUID, processingErr error, availableAt time.Time) error {
	lastError := truncateOutboxError(processingErr)
	affected, err := s.client.MessageOutbox.Update().
		Where(
			messageoutbox.ID(id),
			messageoutbox.LockToken(lockToken),
			messageoutbox.PublishedAtIsNil(),
		).
		SetAvailableAt(availableAt.UTC()).
		SetLastError(lastError).
		ClearLockedAt().
		ClearLockToken().
		Save(ctx)
	if err != nil {
		return fmt.Errorf("mark outbox row %s failed: %w", id, err)
	}
	if affected != 1 {
		return fmt.Errorf("%w: mark failed for %s", ErrOutboxLeaseLost, id)
	}
	return nil
}

func outboxRecord(row *ent.MessageOutbox) OutboxRecord {
	record := OutboxRecord{
		ID:           row.ID,
		EventType:    row.EventType,
		MessageID:    row.MessageID,
		ChatID:       row.ChatID,
		AttemptCount: row.AttemptCount,
	}
	if row.SenderID != nil {
		record.SenderID = *row.SenderID
	}
	if row.LockToken != nil {
		record.LockToken = *row.LockToken
	}
	return record
}

func truncateOutboxError(err error) string {
	if err == nil {
		return "outbox processing failed"
	}
	message := strings.TrimSpace(err.Error())
	if message == "" {
		return "outbox processing failed"
	}
	if len(message) > maxStoredOutboxErrorLength {
		return message[:maxStoredOutboxErrorLength]
	}
	return message
}
