package pushworker

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/ent/pushdelivery"
	"AtoiTalkAPI/ent/pushdevice"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/infrastructure/database/realtimequery"
	"context"
	"entgo.io/ent/dialect/sql"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"time"
)

func Eligible(ctx context.Context, client *ent.Client, msg *ent.Message, recipient uuid.UUID) (bool, error) {
	if msg.DeletedAt != nil || msg.SenderID == nil || *msg.SenderID == recipient || msg.Type != message.TypeRegular {
		return false, nil
	}
	active, err := client.User.Query().Where(user.ID(recipient), user.DeletedAtIsNil(), user.Or(user.IsBanned(false), user.BannedUntilLTE(time.Now().UTC()))).Exist(ctx)
	if err != nil || !active {
		return false, err
	}
	exists, err := client.Chat.Query().Where(chat.ID(msg.ChatID), chat.DeletedAtIsNil()).Exist(ctx)
	if err != nil || !exists {
		return false, err
	}
	blocked, err := realtimequery.New(client).IsBlocked(ctx, *msg.SenderID, recipient)
	if err != nil || blocked {
		return false, err
	}
	pc, err := client.PrivateChat.Query().Where(privatechat.ChatID(msg.ChatID)).Only(ctx)
	if err == nil {
		if pc.User1ID != nil && *pc.User1ID == recipient {
			return pc.User1HiddenAt == nil || msg.CreatedAt.After(*pc.User1HiddenAt), nil
		}
		if pc.User2ID != nil && *pc.User2ID == recipient {
			return pc.User2HiddenAt == nil || msg.CreatedAt.After(*pc.User2HiddenAt), nil
		}
		return false, nil
	}
	if !ent.IsNotFound(err) {
		return false, err
	}
	return client.GroupMember.Query().Where(groupmember.UserID(recipient), groupmember.JoinedAtLTE(msg.CreatedAt), groupmember.HasGroupChatWith(groupchat.ChatID(msg.ChatID))).Exist(ctx)
}

func Enqueue(ctx context.Context, tx *ent.Tx, msg *ent.Message, event uuid.UUID) error {
	if msg.DeletedAt != nil || msg.SenderID == nil || msg.Type != message.TypeRegular {
		return nil
	}
	client := tx.Client()
	members, err := realtimequery.New(client).ChatMembers(ctx, msg.ChatID)
	if err != nil {
		return err
	}
	allowedRecipients, err := NotificationRecipients(ctx, client, msg.ChatID, members)
	if err != nil {
		return err
	}
	activeUsers, err := client.User.Query().Where(user.IDIn(members...), user.DeletedAtIsNil(), user.Or(user.IsBanned(false), user.BannedUntilLTE(time.Now().UTC()))).IDs(ctx)
	if err != nil {
		return err
	}
	blocked, err := realtimequery.New(client).BlockedContacts(ctx, *msg.SenderID)
	if err != nil {
		return err
	}
	pc, err := client.PrivateChat.Query().Where(privatechat.ChatID(msg.ChatID)).Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return err
	}
	joined := map[uuid.UUID]bool{}
	if pc == nil {
		rows, queryErr := client.GroupMember.Query().Where(groupmember.UserIDIn(members...), groupmember.JoinedAtLTE(msg.CreatedAt), groupmember.HasGroupChatWith(groupchat.ChatID(msg.ChatID))).All(ctx)
		if queryErr != nil {
			return queryErr
		}
		for _, row := range rows {
			joined[row.UserID] = true
		}
	}
	recipients := make([]uuid.UUID, 0, len(activeUsers))
	for _, recipient := range activeUsers {
		if recipient == *msg.SenderID || !allowedRecipients[recipient] || blocked[recipient] {
			continue
		}
		if pc != nil {
			if pc.User1ID != nil && *pc.User1ID == recipient {
				if pc.User1HiddenAt != nil && !msg.CreatedAt.After(*pc.User1HiddenAt) {
					continue
				}
			} else if pc.User2ID != nil && *pc.User2ID == recipient {
				if pc.User2HiddenAt != nil && !msg.CreatedAt.After(*pc.User2HiddenAt) {
					continue
				}
			} else {
				continue
			}
		} else if !joined[recipient] {
			continue
		}
		recipients = append(recipients, recipient)
	}
	if len(recipients) == 0 {
		return nil
	}
	devices, err := client.PushDevice.Query().Where(pushdevice.UserIDIn(recipients...), pushdevice.DisabledAtIsNil(), pushdevice.ExpiresAtGT(time.Now().UTC())).All(ctx)
	if err != nil {
		return err
	}
	deliveries := make([]*ent.PushDeliveryCreate, 0, len(devices))
	for _, device := range devices {
		deliveries = append(deliveries, client.PushDelivery.Create().SetEventID(event).SetDeviceID(device.ID).SetBindingID(device.BindingID).SetUserID(device.UserID).SetMessageID(msg.ID).SetChatID(msg.ChatID).SetExpiresAt(msg.CreatedAt.Add(time.Hour)))
	}
	for start := 0; start < len(deliveries); start += 100 {
		end := min(start+100, len(deliveries))
		if err := client.PushDelivery.CreateBulk(deliveries[start:end]...).OnConflict(sql.ConflictColumns(pushdelivery.FieldEventID, pushdelivery.FieldDeviceID)).Ignore().Exec(ctx); err != nil {
			return err
		}
	}
	return nil
}

var ErrLeaseLost = errors.New("push delivery lease lost")

type Store struct {
	Client  *ent.Client
	Dialect string
}

func (s *Store) Claim(ctx context.Context) (*ent.PushDelivery, error) {
	tx, err := s.Client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	now := time.Now().UTC()
	q := tx.PushDelivery.Query().Where(pushdelivery.FinishedAtIsNil(), pushdelivery.AvailableAtLTE(now), pushdelivery.Or(pushdelivery.LockedAtIsNil(), pushdelivery.LockedAtLT(now.Add(-time.Minute)))).Order(ent.Asc(pushdelivery.FieldCreatedAt)).Limit(1)
	if s.Dialect == "postgres" {
		q.ForUpdate(sql.WithLockAction(sql.SkipLocked))
	}
	row, err := q.Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	token := uuid.New()
	row, err = tx.PushDelivery.UpdateOne(row).SetLockedAt(now).SetLockToken(token).AddAttemptCount(1).Save(ctx)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return row.Unwrap(), nil
}
func (s *Store) Finish(ctx context.Context, row *ent.PushDelivery, outcome string, cause error, retry time.Duration) error {
	update := s.Client.PushDelivery.Update().Where(pushdelivery.ID(row.ID), pushdelivery.LockToken(*row.LockToken), pushdelivery.FinishedAtIsNil()).ClearLockedAt().ClearLockToken()
	if retry > 0 {
		update.SetAvailableAt(time.Now().UTC().Add(retry))
	} else {
		update.SetFinishedAt(time.Now().UTC()).SetOutcome(outcome)
		if outcome == "sent" {
			update.SetSentAt(time.Now().UTC())
		}
	}
	if cause != nil {
		update.SetLastError(fmt.Sprintf("%.256s", cause.Error()))
	}
	n, err := update.Save(ctx)
	if err != nil {
		return err
	}
	if n != 1 {
		return ErrLeaseLost
	}
	return nil
}
