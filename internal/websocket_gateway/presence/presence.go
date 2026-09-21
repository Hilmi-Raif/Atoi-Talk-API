package presence

import (
	"context"
	"encoding/json"
	"time"

	"AtoiTalkAPI/internal/messaging/events"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Contacts func(context.Context, uuid.UUID) ([]uuid.UUID, error)
type BlockChecker func(context.Context, uuid.UUID, uuid.UUID) (bool, error)

type PubSubPayload struct {
	TargetUserID string `json:"target_user_id"`
	EventData    []byte `json:"event_data"`
}

const legacyPubSubChannel = "events:broadcast"

const presenceOnlineTTL = 70 * time.Second

type PresenceManager struct {
	client   redis.UniversalClient
	lastSeen func(context.Context, uuid.UUID) error
	contacts Contacts
	blocked  BlockChecker
}

func NewManager(client redis.UniversalClient, lastSeen func(context.Context, uuid.UUID) error, contacts Contacts, blocked BlockChecker) *PresenceManager {
	return &PresenceManager{client: client, lastSeen: lastSeen, contacts: contacts, blocked: blocked}
}

func (p *PresenceManager) Connected(ctx context.Context, userID uuid.UUID) {
	if p == nil || p.client == nil || userID == uuid.Nil {
		return
	}
	_ = p.client.Set(ctx, "online:"+userID.String(), "true", presenceOnlineTTL).Err()
	p.publish(ctx, userID, events.EventUserOnline, true)
}

func (p *PresenceManager) Disconnected(ctx context.Context, userID uuid.UUID) {
	if p == nil || p.client == nil || userID == uuid.Nil {
		return
	}
	_ = p.client.Del(ctx, "online:"+userID.String()).Err()
	if p.lastSeen != nil {
		_ = p.lastSeen(ctx, userID)
	}
	p.publish(ctx, userID, events.EventUserOffline, false)
}

func (p *PresenceManager) publish(ctx context.Context, userID uuid.UUID, eventType events.EventType, online bool) {
	if p.contacts == nil {
		return
	}
	contactIDs, err := p.contacts(ctx, userID)
	if err != nil {
		return
	}
	now := time.Now().UTC().UnixMilli()
	eventData, err := json.Marshal(events.Event{
		Type: eventType,
		Payload: map[string]interface{}{
			"user_id":      userID,
			"is_online":    online,
			"last_seen_at": now,
		},
		Meta: &events.EventMeta{Timestamp: now, SenderID: userID},
	})
	if err != nil {
		return
	}
	for _, contactID := range contactIDs {
		if contactID == uuid.Nil || contactID == userID {
			continue
		}
		if p.blocked != nil {
			isBlocked, err := p.blocked(ctx, userID, contactID)
			if err != nil || isBlocked {
				continue
			}
		}
		payload, err := json.Marshal(PubSubPayload{TargetUserID: contactID.String(), EventData: eventData})
		if err != nil {
			continue
		}
		_ = p.client.Publish(ctx, legacyPubSubChannel, payload).Err()
	}
}
