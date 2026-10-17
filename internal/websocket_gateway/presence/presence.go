package presence

import (
	"context"
	"log/slog"
	"time"

	"AtoiTalkAPI/internal/messaging/events"
	messagingpubsub "AtoiTalkAPI/internal/messaging/pubsub"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type Contacts func(context.Context, uuid.UUID) ([]uuid.UUID, error)
type BlockChecker func(context.Context, uuid.UUID, uuid.UUID) (bool, error)

const presenceOnlineTTL = 70 * time.Second

const (
	presenceLeaseRetention = 2 * presenceOnlineTTL
	presenceScanLockTTL    = 30 * time.Second
)

var connectScript = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now_ms)
local was_online = redis.call('EXISTS', KEYS[2]) == 1
local expires_at = now_ms + tonumber(ARGV[2])
redis.call('ZADD', KEYS[1], expires_at, ARGV[1])
local latest = redis.call('ZRANGE', KEYS[1], -1, -1, 'WITHSCORES')
redis.call('PEXPIREAT', KEYS[1], latest[2] + tonumber(ARGV[3]))
redis.call('SET', KEYS[2], 'true')
redis.call('PEXPIREAT', KEYS[2], latest[2])
if was_online then return 0 end
return 1
`)

var reconcileScript = redis.NewScript(`
local lease_exists = redis.call('EXISTS', KEYS[1]) == 1
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now_ms)
if redis.call('ZCARD', KEYS[1]) > 0 or redis.call('EXISTS', KEYS[2]) == 1 then
  return 0
end
redis.call('DEL', KEYS[1])
if not lease_exists then return 0 end
return 1
`)

var releaseReconcileLockScript = redis.NewScript(`
if redis.call('GET', KEYS[1]) == ARGV[1] then
  return redis.call('DEL', KEYS[1])
end
return 0
`)

var disconnectScript = redis.NewScript(`
local now = redis.call('TIME')
local now_ms = now[1] * 1000 + math.floor(now[2] / 1000)
local was_online = redis.call('EXISTS', KEYS[2]) == 1
redis.call('ZREMRANGEBYSCORE', KEYS[1], '-inf', now_ms)
redis.call('ZREM', KEYS[1], ARGV[1])
local latest = redis.call('ZRANGE', KEYS[1], -1, -1, 'WITHSCORES')
if #latest == 0 then
  redis.call('DEL', KEYS[1], KEYS[2])
  if was_online then return 1 end
  return 0
end
redis.call('PEXPIREAT', KEYS[1], latest[2] + tonumber(ARGV[2]))
redis.call('SET', KEYS[2], 'true')
redis.call('PEXPIREAT', KEYS[2], latest[2])
return 0
`)

type PresenceManager struct {
	client     redis.UniversalClient
	instanceID string
	lastSeen   func(context.Context, uuid.UUID) error
	contacts   Contacts
	blocked    BlockChecker
}

func NewManager(client redis.UniversalClient, instanceID string, lastSeen func(context.Context, uuid.UUID) error, contacts Contacts, blocked BlockChecker) *PresenceManager {
	return &PresenceManager{client: client, instanceID: instanceID, lastSeen: lastSeen, contacts: contacts, blocked: blocked}
}

func (p *PresenceManager) Connected(ctx context.Context, userID uuid.UUID) {
	if p == nil || p.client == nil || p.instanceID == "" || userID == uuid.Nil {
		return
	}
	becameOnline, err := connectScript.Run(ctx, p.client, p.keys(userID), p.instanceID, presenceOnlineTTL.Milliseconds(), presenceLeaseRetention.Milliseconds()).Int64()
	if err == nil && becameOnline == 1 {
		p.publish(ctx, userID, events.EventUserOnline, true)
	}
}

func (p *PresenceManager) Refresh(ctx context.Context, userID uuid.UUID) {
	if p == nil || p.client == nil || p.instanceID == "" || userID == uuid.Nil {
		return
	}
	becameOnline, err := connectScript.Run(ctx, p.client, p.keys(userID), p.instanceID, presenceOnlineTTL.Milliseconds(), presenceLeaseRetention.Milliseconds()).Int64()
	if err == nil && becameOnline == 1 {
		p.publish(ctx, userID, events.EventUserOnline, true)
	}
}

func (p *PresenceManager) ReconcileExpired(ctx context.Context) error {
	if p == nil || p.client == nil {
		return nil
	}
	lockToken := uuid.NewString()
	locked, err := p.client.SetNX(ctx, "presence:reconcile:lock", lockToken, presenceScanLockTTL).Result()
	if err != nil || !locked {
		return err
	}
	defer func() {
		_, _ = releaseReconcileLockScript.Run(context.Background(), p.client, []string{"presence:reconcile:lock"}, lockToken).Result()
	}()

	var cursor uint64
	for {
		keys, next, err := p.client.Scan(ctx, cursor, "presence:instances:*", 100).Result()
		if err != nil {
			return err
		}
		for _, key := range keys {
			userID, err := uuid.Parse(key[len("presence:instances:"):])
			if err != nil {
				continue
			}
			claimed, err := reconcileScript.Run(ctx, p.client, []string{key, "online:" + userID.String()}).Int64()
			if err != nil {
				return err
			}
			if claimed == 1 {
				if p.lastSeen != nil {
					if err := p.lastSeen(ctx, userID); err != nil {
						slog.WarnContext(ctx, "failed to update last seen during presence reconciliation", "error", err, "userID", userID)
					}
				}
				p.publish(ctx, userID, events.EventUserOffline, false)
			}
		}
		cursor = next
		if cursor == 0 {
			return nil
		}
	}
}

func (p *PresenceManager) RunReconciler(ctx context.Context, interval time.Duration) {
	if interval <= 0 {
		interval = presenceOnlineTTL
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := p.ReconcileExpired(ctx); err != nil && ctx.Err() == nil {
				slog.WarnContext(ctx, "failed to reconcile expired presence", "error", err)
			}
		}
	}
}

func (p *PresenceManager) Disconnected(ctx context.Context, userID uuid.UUID) {
	if p == nil || p.client == nil || p.instanceID == "" || userID == uuid.Nil {
		return
	}
	becameOffline, err := disconnectScript.Run(ctx, p.client, p.keys(userID), p.instanceID, presenceLeaseRetention.Milliseconds()).Int64()
	if err != nil || becameOffline != 1 {
		return
	}
	if p.lastSeen != nil {
		if err := p.lastSeen(ctx, userID); err != nil {
			slog.WarnContext(ctx, "failed to update last seen during disconnect", "error", err, "userID", userID)
		}
	}
	p.publish(ctx, userID, events.EventUserOffline, false)
}

func (p *PresenceManager) keys(userID uuid.UUID) []string {
	id := userID.String()
	return []string{"presence:instances:" + id, "online:" + id}
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
	event := events.Event{
		Type: eventType,
		Payload: map[string]interface{}{
			"user_id":      userID,
			"is_online":    online,
			"last_seen_at": now,
		},
		Meta: &events.EventMeta{Timestamp: now, SenderID: userID},
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
		if err := messagingpubsub.PublishToUser(ctx, p.client, messagingpubsub.BroadcastChannel, contactID, event); err != nil {
			slog.WarnContext(ctx, "failed to publish presence event", "error", err, "targetUserID", contactID)
		}
	}
}
