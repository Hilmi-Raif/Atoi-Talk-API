package pushworker

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chatnotificationpreference"
	"AtoiTalkAPI/ent/user"
	"context"
	"github.com/google/uuid"
	"time"
)

func NotificationRecipients(ctx context.Context, c *ent.Client, chatID uuid.UUID, ids []uuid.UUID) (map[uuid.UUID]bool, error) {
	result := make(map[uuid.UUID]bool)
	if len(ids) == 0 {
		return result, nil
	}
	users, err := c.User.Query().Where(user.IDIn(ids...), user.MessageNotificationsEnabled(true)).Select(user.FieldID).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, u := range users {
		result[u.ID] = true
	}
	muted, err := c.ChatNotificationPreference.Query().Where(chatnotificationpreference.ChatID(chatID), chatnotificationpreference.UserIDIn(ids...), chatnotificationpreference.Or(chatnotificationpreference.MutedForever(true), chatnotificationpreference.MutedUntilGT(time.Now().UTC()))).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, p := range muted {
		delete(result, p.UserID)
	}
	return result, nil
}
