package typing

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"AtoiTalkAPI/internal/messaging/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	legacyPubSubChannel = "events:broadcast"
	typingThrottle      = 2 * time.Second
	typingUserThrottle  = 300 * time.Millisecond
)

type ChatMembers func(context.Context, uuid.UUID) ([]uuid.UUID, error)
type BlockChecker func(context.Context, uuid.UUID, uuid.UUID) (bool, error)

type PubSubPayload struct {
	TargetUserID string `json:"target_user_id"`
	EventData    []byte `json:"event_data"`
}

type Router struct {
	client  redis.UniversalClient
	members ChatMembers
	blocked BlockChecker
}

func NewRouter(client redis.UniversalClient, members ChatMembers) *Router {
	return NewRouterWithBlockChecker(client, members, nil)
}

func NewRouterWithBlockChecker(client redis.UniversalClient, members ChatMembers, blocked BlockChecker) *Router {
	return &Router{client: client, members: members, blocked: blocked}
}

func (r *Router) Publish(ctx context.Context, senderID uuid.UUID, event events.Event) error {
	if r == nil || r.client == nil || r.members == nil || senderID == uuid.Nil {
		return errors.New("typing router is not configured")
	}
	if event.Type != events.EventTyping || event.Meta == nil || event.Meta.ChatID == uuid.Nil {
		return nil
	}

	chatID := event.Meta.ChatID
	memberIDs, err := r.members(ctx, chatID)
	if err != nil {
		return err
	}
	isMember := false
	for _, memberID := range memberIDs {
		if memberID == senderID {
			isMember = true
			break
		}
	}
	if !isMember {
		return nil
	}

	userKey := "typing_user:" + senderID.String()
	allowed, err := r.client.SetNX(ctx, userKey, 1, typingUserThrottle).Result()
	if err != nil || !allowed {
		return err
	}
	chatKey := "typing:" + senderID.String() + ":" + chatID.String()
	allowed, err = r.client.SetNX(ctx, chatKey, 1, typingThrottle).Result()
	if err != nil || !allowed {
		return err
	}

	eventCopy := event
	metaCopy := *event.Meta
	metaCopy.SenderID = senderID
	metaCopy.Timestamp = time.Now().UTC().UnixMilli()
	eventCopy.Meta = &metaCopy
	eventData, err := json.Marshal(eventCopy)
	if err != nil {
		return err
	}
	for _, targetID := range memberIDs {
		if targetID == uuid.Nil || targetID == senderID {
			continue
		}
		if r.blocked != nil {
			blocked, err := r.blocked(ctx, senderID, targetID)
			if err != nil {
				return err
			}
			if blocked {
				continue
			}
		}
		payload, err := json.Marshal(PubSubPayload{
			TargetUserID: targetID.String(),
			EventData:    eventData,
		})
		if err != nil {
			return err
		}
		if err := r.client.Publish(ctx, legacyPubSubChannel, payload).Err(); err != nil {
			return err
		}
	}
	return nil
}
