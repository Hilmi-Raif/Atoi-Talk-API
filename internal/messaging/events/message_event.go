package events

import (
	"context"
	"encoding/json"

	"github.com/google/uuid"
)

type StreamMessage struct {
	ID   string
	Data []byte
}

type MessageEvent struct {
	ID           uuid.UUID `json:"id"`
	OutboxID     uuid.UUID `json:"outbox_id"`
	Type         string    `json:"type"`
	MessageID    uuid.UUID `json:"message_id"`
	ChatID       uuid.UUID `json:"chat_id"`
	SenderID     uuid.UUID `json:"sender_id"`
	TargetUserID uuid.UUID `json:"target_user_id"`
	Payload      []byte    `json:"payload"`
}

func (e MessageEvent) Marshal() ([]byte, error) {
	return e.MarshalContext(context.Background())
}

func (e MessageEvent) MarshalContext(context.Context) ([]byte, error) {
	return json.Marshal(e)
}

func UnmarshalMessageEvent(data []byte) (MessageEvent, error) {
	var event MessageEvent
	if err := json.Unmarshal(data, &event); err != nil {
		return MessageEvent{}, err
	}
	return event, nil
}
