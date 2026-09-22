package pubsub

import (
	"context"
	"encoding/json"

	"AtoiTalkAPI/internal/messaging/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	BroadcastChannel = "events:broadcast"
	ControlChannel   = "events:control"
)

type Payload struct {
	TargetUserID string `json:"target_user_id"`
	EventData    []byte `json:"event_data"`
}

func PublishToUser(ctx context.Context, client redis.UniversalClient, channel string, userID uuid.UUID, event events.Event) error {
	eventData, err := json.Marshal(event)
	if err != nil {
		return err
	}
	payload, err := json.Marshal(Payload{TargetUserID: userID.String(), EventData: eventData})
	if err != nil {
		return err
	}
	return client.Publish(ctx, channel, payload).Err()
}
