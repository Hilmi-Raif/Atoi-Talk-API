package outbox

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/internal/messaging/events"
	"context"

	"github.com/google/uuid"
)

func PersistMessageOutbox(ctx context.Context, tx *ent.Tx, msg *ent.Message, eventType events.EventType, senderID uuid.UUID) error {
	payload := map[string]interface{}{
		"content":     msg.Content,
		"action_data": msg.ActionData,
		"edited_at":   msg.EditedAt,
		"deleted_at":  msg.DeletedAt,
	}
	if eventType == events.EventMessageDelete {
		payload = map[string]interface{}{"message_id": msg.ID}
	}
	return PersistMessageOutboxWithPayload(ctx, tx, msg, eventType, senderID, payload)
}

func PersistMessageOutboxWithPayload(ctx context.Context, tx *ent.Tx, msg *ent.Message, eventType events.EventType, senderID uuid.UUID, payload map[string]interface{}) error {
	create := tx.MessageOutbox.Create().
		SetEventType(string(eventType)).
		SetMessageID(msg.ID).
		SetChatID(msg.ChatID).
		SetSenderID(senderID)
	if payload != nil {
		create.SetPayload(payload)
	}
	_, err := create.Save(ctx)
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
