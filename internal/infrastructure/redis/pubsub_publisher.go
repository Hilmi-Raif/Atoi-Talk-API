package redis

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/ent/userblock"
	"AtoiTalkAPI/internal/messaging/events"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

const (
	pubSubChannel        = "events:broadcast"
	pubSubControlChannel = "events:control"
)

type pubSubPayload struct {
	TargetUserID string `json:"target_user_id"`
	EventData    []byte `json:"event_data"`
}

type PubSubPublisher struct {
	db    *ent.Client
	redis redis.UniversalClient
}

func NewPubSubPublisher(db *ent.Client, redisClient redis.UniversalClient) *PubSubPublisher {
	return &PubSubPublisher{db: db, redis: redisClient}
}

func (p *PubSubPublisher) BroadcastToUser(userID uuid.UUID, event events.Event) {
	if p == nil || p.redis == nil || userID == uuid.Nil || isDurableMessageEvent(event.Type) {
		return
	}
	publishToUser(context.Background(), p.redis, pubSubChannel, userID, event)
}

func (p *PubSubPublisher) BroadcastToChat(chatID uuid.UUID, event events.Event) {
	if p == nil || p.db == nil || p.redis == nil || chatID == uuid.Nil || isDurableMessageEvent(event.Type) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	members, err := p.chatMembers(ctx, chatID)
	if err != nil {
		slog.Error("failed to fetch chat members for Pub/Sub event", "error", err)
		return
	}
	for _, userID := range members {
		if event.Type == events.EventChatRead || event.Type == events.EventChatUpdate || event.Type == events.EventChatNew || event.Type == events.EventChatHide || event.Type == events.EventChatDelete {
			p.BroadcastToUser(userID, event)
		}
	}
}

func (p *PubSubPublisher) BroadcastToContacts(userID uuid.UUID, event events.Event) {
	if p == nil || p.db == nil || p.redis == nil || userID == uuid.Nil || isDurableMessageEvent(event.Type) {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	privateChats, err := p.db.PrivateChat.Query().Where(
		privatechat.Or(privatechat.User1ID(userID), privatechat.User2ID(userID)),
	).Select(privatechat.FieldUser1ID, privatechat.FieldUser2ID).All(ctx)
	if err != nil {
		slog.Error("failed to fetch contacts for Pub/Sub event", "error", err)
		return
	}

	blocked := map[uuid.UUID]bool{}
	if event.Type == events.EventUserOnline || event.Type == events.EventUserOffline {
		blocks, blockErr := p.db.UserBlock.Query().Where(
			userblock.Or(userblock.BlockerID(userID), userblock.BlockedID(userID)),
		).Select(userblock.FieldBlockerID, userblock.FieldBlockedID).All(ctx)
		if blockErr == nil {
			for _, block := range blocks {
				if block.BlockerID == userID {
					blocked[block.BlockedID] = true
				} else {
					blocked[block.BlockerID] = true
				}
			}
		}
	}

	for _, privateChat := range privateChats {
		var targetID uuid.UUID
		if privateChat.User1ID != nil && *privateChat.User1ID == userID && privateChat.User2ID != nil {
			targetID = *privateChat.User2ID
		} else if privateChat.User2ID != nil && *privateChat.User2ID == userID && privateChat.User1ID != nil {
			targetID = *privateChat.User1ID
		}
		if targetID != uuid.Nil && !blocked[targetID] {
			p.BroadcastToUser(targetID, event)
		}
	}
}

func (p *PubSubPublisher) DisconnectUser(userID uuid.UUID) {
	if p == nil || p.redis == nil || userID == uuid.Nil {
		return
	}
	publishToUser(context.Background(), p.redis, pubSubControlChannel, userID, events.Event{
		Type: events.EventUserDeleted,
		Payload: map[string]interface{}{
			"disconnect": true,
		},
	})
}

func (p *PubSubPublisher) chatMembers(ctx context.Context, chatID uuid.UUID) ([]uuid.UUID, error) {
	entity, err := p.db.Chat.Query().Where(chat.ID(chatID), chat.DeletedAtIsNil()).Select(chat.FieldType).Only(ctx)
	if err != nil {
		return nil, err
	}
	switch entity.Type {
	case chat.TypePrivate:
		privateEntity, err := p.db.PrivateChat.Query().Where(privatechat.ChatID(chatID)).Select(privatechat.FieldUser1ID, privatechat.FieldUser2ID).Only(ctx)
		if err != nil {
			return nil, err
		}
		members := make([]uuid.UUID, 0, 2)
		if privateEntity.User1ID != nil {
			members = append(members, *privateEntity.User1ID)
		}
		if privateEntity.User2ID != nil {
			members = append(members, *privateEntity.User2ID)
		}
		return members, nil
	case chat.TypeGroup:
		groupID, err := p.db.GroupChat.Query().Where(groupchat.ChatID(chatID)).OnlyID(ctx)
		if err != nil {
			return nil, err
		}
		members, err := p.db.GroupMember.Query().Where(groupmember.GroupChatID(groupID)).Select(groupmember.FieldUserID).All(ctx)
		if err != nil {
			return nil, err
		}
		result := make([]uuid.UUID, 0, len(members))
		for _, member := range members {
			result = append(result, member.UserID)
		}
		return result, nil
	default:
		return nil, nil
	}
}

func publishToUser(ctx context.Context, client redis.UniversalClient, channel string, userID uuid.UUID, event events.Event) {
	eventData, err := json.Marshal(event)
	if err != nil {
		return
	}
	payload, err := json.Marshal(pubSubPayload{TargetUserID: userID.String(), EventData: eventData})
	if err != nil {
		return
	}
	if err := client.Publish(ctx, channel, payload).Err(); err != nil {
		slog.Error("failed to publish non-durable event to Redis Pub/Sub", "error", err)
	}
}

func isDurableMessageEvent(eventType events.EventType) bool {
	return eventType == events.EventMessageNew || eventType == events.EventMessageUpdate || eventType == events.EventMessageDelete
}

func (p *PubSubPublisher) String() string {
	return fmt.Sprintf("PubSubPublisher(%p)", p)
}
