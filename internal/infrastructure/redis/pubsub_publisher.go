package redis

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/internal/infrastructure/database/realtimequery"
	"AtoiTalkAPI/internal/messaging/events"
	messagingpubsub "AtoiTalkAPI/internal/messaging/pubsub"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type PubSubPublisher struct {
	db             *ent.Client
	recipientQuery *realtimequery.Query
	redis          redis.UniversalClient
}

func NewPubSubPublisher(db *ent.Client, redisClient redis.UniversalClient) *PubSubPublisher {
	return &PubSubPublisher{db: db, recipientQuery: realtimequery.New(db), redis: redisClient}
}

func (p *PubSubPublisher) BroadcastToUser(userID uuid.UUID, event events.Event) {
	if p == nil || p.redis == nil || userID == uuid.Nil || isDurableMessageEvent(event.Type) {
		return
	}
	publishToUser(context.Background(), p.redis, messagingpubsub.BroadcastChannel, userID, event)
}

func (p *PubSubPublisher) BroadcastToChat(chatID uuid.UUID, event events.Event) {
	if p == nil || p.db == nil || p.redis == nil || chatID == uuid.Nil || isDurableMessageEvent(event.Type) {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	members, err := p.recipientQuery.ChatMembers(ctx, chatID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to fetch chat members for Pub/Sub event", "error", err, "chat_id", chatID)
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

	contacts, err := p.recipientQuery.Contacts(ctx, userID)
	if err != nil {
		slog.ErrorContext(ctx, "failed to fetch contacts for Pub/Sub event", "error", err, "user_id", userID)
		return
	}
	blockedContacts := map[uuid.UUID]bool(nil)
	if event.Type == events.EventUserOnline || event.Type == events.EventUserOffline {
		blockedContacts, err = p.recipientQuery.BlockedContacts(ctx, userID)
		if err != nil {
			slog.ErrorContext(ctx, "failed to fetch blocked contacts for Pub/Sub event", "error", err, "user_id", userID)
			blockedContacts = nil
		}
	}

	for _, targetID := range contacts {
		if blockedContacts[targetID] {
			continue
		}
		p.BroadcastToUser(targetID, event)
	}
}

func (p *PubSubPublisher) DisconnectUser(userID uuid.UUID) {
	if p == nil || p.redis == nil || userID == uuid.Nil {
		return
	}
	publishToUser(context.Background(), p.redis, messagingpubsub.ControlChannel, userID, events.Event{
		Type: events.EventUserDeleted,
		Payload: map[string]interface{}{
			"disconnect": true,
		},
	})
}

func publishToUser(ctx context.Context, client redis.UniversalClient, channel string, userID uuid.UUID, event events.Event) {
	if err := messagingpubsub.PublishToUser(ctx, client, channel, userID, event); err != nil {
		slog.ErrorContext(ctx, "failed to publish non-durable event to Redis Pub/Sub", "error", err)
	}
}

func isDurableMessageEvent(eventType events.EventType) bool {
	return eventType == events.EventMessageNew || eventType == events.EventMessageUpdate || eventType == events.EventMessageDelete
}

func (p *PubSubPublisher) String() string {
	return fmt.Sprintf("PubSubPublisher(%p)", p)
}
