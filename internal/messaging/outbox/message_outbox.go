package outbox

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/internal/messaging/events"
	"context"

	"github.com/google/uuid"
)

func PersistMessageOutbox(ctx context.Context, tx *ent.Tx, msg *ent.Message, eventType events.EventType, senderID uuid.UUID) error {
	_, err := tx.MessageOutbox.Create().
		SetEventType(string(eventType)).
		SetMessageID(msg.ID).
		SetChatID(msg.ChatID).
		SetSenderID(senderID).
		Save(ctx)
	return err
}

func PersistMessageOutboxes(ctx context.Context, tx *ent.Tx, messages []*ent.Message, eventType events.EventType, senderID uuid.UUID) error {
	for _, msg := range messages {
		if err := PersistMessageOutbox(ctx, tx, msg, eventType, senderID); err != nil {
			return err
		}
	}
	return nil
}
