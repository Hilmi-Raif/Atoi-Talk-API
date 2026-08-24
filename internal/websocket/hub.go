package websocket

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/ent/userblock"
	"AtoiTalkAPI/internal/adapter"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/google/uuid"
)

const cacheTTL = 5 * time.Minute
const typingThrottle = 2 * time.Second
const typingUserThrottle = 300 * time.Millisecond
const emptyMembersCacheTTL = 30 * time.Second
const pubSubChannel = "events:broadcast"
const onlineUserTTL = 70 * time.Second
const maxConcurrentFanouts = 32

type Hub struct {
	clients     map[*Client]bool
	userClients map[uuid.UUID]map[*Client]bool
	mu          sync.RWMutex

	Register   chan *Client
	Unregister chan *Client

	db              *ent.Client
	redis           *adapter.RedisAdapter
	fanoutSemaphore chan struct{}

	cacheMu        sync.Mutex
	memberFlights  map[uuid.UUID]*cacheFetch
	contactFlights map[uuid.UUID]*cacheFetch
}

type cacheFetch struct {
	done    chan struct{}
	userIDs []uuid.UUID
}

type redisPayload struct {
	TargetUserID uuid.UUID `json:"target_user_id"`
	EventData    []byte    `json:"event_data"`
}

func NewHub(db *ent.Client, redis *adapter.RedisAdapter) *Hub {
	hub := &Hub{
		clients:         make(map[*Client]bool),
		userClients:     make(map[uuid.UUID]map[*Client]bool),
		Register:        make(chan *Client, 256),
		Unregister:      make(chan *Client, 256),
		db:              db,
		redis:           redis,
		fanoutSemaphore: make(chan struct{}, maxConcurrentFanouts),
		memberFlights:   make(map[uuid.UUID]*cacheFetch),
		contactFlights:  make(map[uuid.UUID]*cacheFetch),
	}

	go hub.listenToRedis()
	return hub
}

func (h *Hub) listenToRedis() {
	ctx := context.Background()
	pubsub := h.redis.Client().Subscribe(ctx, pubSubChannel)
	defer func() { _ = pubsub.Close() }()

	ch := pubsub.Channel()

	for msg := range ch {
		var payload redisPayload
		if err := json.Unmarshal([]byte(msg.Payload), &payload); err != nil {
			slog.Error("Failed to unmarshal redis pubsub payload", "error", err)
			continue
		}

		h.deliverToLocalClients(payload.TargetUserID, payload.EventData)
	}
}

func (h *Hub) deliverToLocalClients(userID uuid.UUID, data []byte) {
	h.mu.RLock()
	defer h.mu.RUnlock()

	if clients, ok := h.userClients[userID]; ok {
		for client := range clients {
			select {
			case client.Send <- data:
			default:
				select {
				case h.Unregister <- client:
				default:
				}
			}
		}
	}
}

func (h *Hub) Run() {
	for {
		select {
		case client := <-h.Register:
			h.mu.Lock()
			h.clients[client] = true
			if _, ok := h.userClients[client.UserID]; !ok {
				h.userClients[client.UserID] = make(map[*Client]bool)
				go h.broadcastUserStatus(client.UserID, true)
			}
			h.userClients[client.UserID][client] = true

			go h.KeepAlive(client.UserID)

			h.mu.Unlock()

		case client := <-h.Unregister:
			h.mu.Lock()
			if _, ok := h.clients[client]; ok {
				delete(h.clients, client)
				close(client.Send)

				if userSet, ok := h.userClients[client.UserID]; ok {
					delete(userSet, client)
					if len(userSet) == 0 {
						delete(h.userClients, client.UserID)
						go h.broadcastUserStatus(client.UserID, false)
					}
				}
			}
			h.mu.Unlock()
		}
	}
}

func (h *Hub) BroadcastToUser(userID uuid.UUID, event Event) {
	h.fanoutSemaphore <- struct{}{}
	defer func() { <-h.fanoutSemaphore }()

	eventData, err := json.Marshal(event)
	if err != nil {
		slog.Error("Failed to marshal event for redis publish", "error", err)
		return
	}

	payload := redisPayload{
		TargetUserID: userID,
		EventData:    eventData,
	}

	payloadData, err := json.Marshal(payload)
	if err != nil {
		slog.Error("Failed to marshal redis payload", "error", err)
		return
	}

	if err := h.redis.Client().Publish(context.Background(), pubSubChannel, payloadData).Err(); err != nil {
		slog.Error("Failed to publish websocket event to redis, falling back to local delivery", "error", err, "targetUserID", userID)
		h.deliverToLocalClients(userID, eventData)
	}
}

func (h *Hub) BroadcastToChat(chatID uuid.UUID, event Event) {
	switch event.Type {
	case EventTyping:
		h.broadcastTyping(chatID, event)
	case EventChatRead:
		h.broadcastRead(chatID, event)
	default:
		h.broadcastHeavy(chatID, event)
	}
}

func (h *Hub) broadcastTyping(chatID uuid.UUID, event Event) {
	ctx := context.Background()

	senderID := uuid.Nil
	if event.Meta != nil {
		senderID = event.Meta.SenderID
	}

	if senderID == uuid.Nil {
		return
	}

	userThrottleKey := fmt.Sprintf("typing_user:%s", senderID)
	userAllowed, err := h.redis.Client().SetNX(ctx, userThrottleKey, 1, typingUserThrottle).Result()
	if err != nil || !userAllowed {
		return
	}

	chatThrottleKey := fmt.Sprintf("typing:%s:%s", senderID, chatID)
	chatAllowed, err := h.redis.Client().SetNX(ctx, chatThrottleKey, 1, typingThrottle).Result()
	if err != nil || !chatAllowed {
		return
	}

	targetUserIDs := h.getChatMembers(chatID)
	if len(targetUserIDs) == 0 {
		return
	}

	isMember := false
	for _, uid := range targetUserIDs {
		if uid == senderID {
			isMember = true
			break
		}
	}

	if !isMember {
		return
	}

	for _, uid := range targetUserIDs {
		if uid == senderID {
			continue
		}
		h.BroadcastToUser(uid, event)
	}
}

func (h *Hub) getChatMembers(chatID uuid.UUID) []uuid.UUID {
	key := fmt.Sprintf("chat_members:%s", chatID)
	ctx := context.Background()

	membersStr, err := h.redis.Get(ctx, key)
	if err == nil {
		var userIDs []uuid.UUID
		if json.Unmarshal([]byte(membersStr), &userIDs) == nil {
			return userIDs
		}
	}

	h.cacheMu.Lock()
	if flight, ok := h.memberFlights[chatID]; ok {
		h.cacheMu.Unlock()
		<-flight.done
		return flight.userIDs
	}
	flight := &cacheFetch{done: make(chan struct{})}
	h.memberFlights[chatID] = flight
	h.cacheMu.Unlock()

	userIDs := h.fetchAndCacheMembers(chatID)
	h.cacheMu.Lock()
	flight.userIDs = userIDs
	delete(h.memberFlights, chatID)
	close(flight.done)
	h.cacheMu.Unlock()
	return userIDs
}

func (h *Hub) fetchAndCacheMembers(chatID uuid.UUID) []uuid.UUID {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c, err := h.db.Chat.Query().
		Where(
			chat.ID(chatID),
			chat.DeletedAtIsNil(),
		).
		Select(chat.FieldType).
		Only(ctx)

	if err != nil {
		h.cacheChatMembers(ctx, chatID, nil, emptyMembersCacheTTL)
		return nil
	}

	var userIDs []uuid.UUID

	switch c.Type {
	case chat.TypePrivate:
		pc, err := h.db.PrivateChat.Query().
			Where(privatechat.ChatID(chatID)).
			Select(privatechat.FieldUser1ID, privatechat.FieldUser2ID).
			Only(ctx)
		if err == nil {
			if pc.User1ID != nil {
				userIDs = append(userIDs, *pc.User1ID)
			}
			if pc.User2ID != nil {
				userIDs = append(userIDs, *pc.User2ID)
			}
		}
	case chat.TypeGroup:
		gcID, err := h.db.GroupChat.Query().
			Where(groupchat.ChatID(chatID)).
			OnlyID(ctx)
		if err == nil {
			members, err := h.db.GroupMember.Query().
				Where(groupmember.GroupChatID(gcID)).
				Select(groupmember.FieldUserID).
				All(ctx)
			if err == nil {
				for _, m := range members {
					userIDs = append(userIDs, m.UserID)
				}
			}
		}
	}

	cacheTTLToUse := cacheTTL
	if len(userIDs) == 0 {
		cacheTTLToUse = emptyMembersCacheTTL
	}
	h.cacheChatMembers(ctx, chatID, userIDs, cacheTTLToUse)

	return userIDs
}

func (h *Hub) cacheChatMembers(ctx context.Context, chatID uuid.UUID, userIDs []uuid.UUID, ttl time.Duration) {
	key := fmt.Sprintf("chat_members:%s", chatID)
	data, err := json.Marshal(userIDs)
	if err != nil {
		return
	}
	_ = h.redis.Set(ctx, key, data, ttl)
}

func (h *Hub) broadcastRead(chatID uuid.UUID, event Event) {
	targetUserIDs := h.getChatMembers(chatID)
	for _, uid := range targetUserIDs {
		h.BroadcastToUser(uid, event)
	}
}

func (h *Hub) broadcastHeavy(chatID uuid.UUID, event Event) {
	ctx := context.Background()

	c, err := h.db.Chat.Query().
		Where(chat.ID(chatID)).
		WithPrivateChat().
		WithGroupChat(func(q *ent.GroupChatQuery) {
			q.Select(groupchat.FieldID)
			q.WithMembers(func(mq *ent.GroupMemberQuery) {
				mq.Select(groupmember.FieldUserID, groupmember.FieldUnreadCount)
			})
		}).
		Only(ctx)

	if err != nil {
		slog.Error("Failed to fetch chat members for broadcast", "error", err, "chatID", chatID)
		return
	}

	memberUnreadMap := make(map[uuid.UUID]int)

	if c.Type == chat.TypePrivate && c.Edges.PrivateChat != nil {
		pc := c.Edges.PrivateChat
		if pc.User1ID != nil {
			memberUnreadMap[*pc.User1ID] = pc.User1UnreadCount
		}
		if pc.User2ID != nil {
			memberUnreadMap[*pc.User2ID] = pc.User2UnreadCount
		}
	} else if c.Type == chat.TypeGroup && c.Edges.GroupChat != nil {
		for _, m := range c.Edges.GroupChat.Edges.Members {
			memberUnreadMap[m.UserID] = m.UnreadCount
		}
	}

	blockedUserIDs := make(map[uuid.UUID]bool)
	if event.Meta != nil && event.Meta.SenderID != uuid.Nil {
		senderID := event.Meta.SenderID

		blocks, err := h.db.UserBlock.Query().
			Where(
				userblock.Or(
					userblock.BlockerID(senderID),
					userblock.BlockedID(senderID),
				),
			).
			Select(userblock.FieldBlockerID, userblock.FieldBlockedID).
			All(ctx)

		if err == nil {
			for _, b := range blocks {
				if b.BlockerID == senderID {
					blockedUserIDs[b.BlockedID] = true
				} else {
					blockedUserIDs[b.BlockerID] = true
				}
			}
		} else {
			slog.Error("Failed to fetch block list for heavy broadcast", "error", err)
		}
	}

	for uid, unreadCount := range memberUnreadMap {
		if blockedUserIDs[uid] {
			continue
		}

		personalEvent := event

		if event.Meta != nil {
			newMeta := *event.Meta
			newMeta.UnreadCount = unreadCount
			personalEvent.Meta = &newMeta
		} else {
			personalEvent.Meta = &EventMeta{
				UnreadCount: unreadCount,
			}
		}

		h.BroadcastToUser(uid, personalEvent)
	}
}

func (h *Hub) getContacts(userID uuid.UUID) []uuid.UUID {
	key := fmt.Sprintf("contacts:%s", userID)
	ctx := context.Background()

	contactsStr, err := h.redis.Get(ctx, key)
	if err == nil {
		var userIDs []uuid.UUID
		if json.Unmarshal([]byte(contactsStr), &userIDs) == nil {
			return userIDs
		}
	}

	h.cacheMu.Lock()
	if flight, ok := h.contactFlights[userID]; ok {
		h.cacheMu.Unlock()
		<-flight.done
		return flight.userIDs
	}
	flight := &cacheFetch{done: make(chan struct{})}
	h.contactFlights[userID] = flight
	h.cacheMu.Unlock()

	contacts := h.fetchAndCacheContacts(userID)
	h.cacheMu.Lock()
	flight.userIDs = contacts
	delete(h.contactFlights, userID)
	close(flight.done)
	h.cacheMu.Unlock()
	return contacts
}

func (h *Hub) fetchAndCacheContacts(userID uuid.UUID) []uuid.UUID {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	chats, err := h.db.PrivateChat.Query().
		Where(
			privatechat.Or(
				privatechat.User1ID(userID),
				privatechat.User2ID(userID),
			),
		).
		Select(privatechat.FieldUser1ID, privatechat.FieldUser2ID).
		All(ctx)

	if err != nil {
		slog.Error("Failed to fetch contacts for cache", "error", err)
		return nil
	}

	targetUserIDs := make([]uuid.UUID, 0, len(chats))
	for _, pc := range chats {
		if pc.User1ID != nil && *pc.User1ID == userID {
			if pc.User2ID != nil {
				targetUserIDs = append(targetUserIDs, *pc.User2ID)
			}
		} else if pc.User2ID != nil && *pc.User2ID == userID {
			if pc.User1ID != nil {
				targetUserIDs = append(targetUserIDs, *pc.User1ID)
			}
		}
	}

	key := fmt.Sprintf("contacts:%s", userID)
	data, _ := json.Marshal(targetUserIDs)
	_ = h.redis.Set(ctx, key, data, cacheTTL)

	return targetUserIDs
}

func (h *Hub) BroadcastToContacts(userID uuid.UUID, event Event) {
	ctx := context.Background()

	targetUserIDs := h.getContacts(userID)

	blockedUserIDs := make(map[uuid.UUID]bool)

	if event.Type == EventUserOnline || event.Type == EventUserOffline {
		blocks, err := h.db.UserBlock.Query().
			Where(
				userblock.Or(
					userblock.BlockerID(userID),
					userblock.BlockedID(userID),
				),
			).
			Select(userblock.FieldBlockerID, userblock.FieldBlockedID).
			All(ctx)

		if err == nil {
			for _, b := range blocks {
				if b.BlockerID == userID {
					blockedUserIDs[b.BlockedID] = true
				} else {
					blockedUserIDs[b.BlockerID] = true
				}
			}
		}
	}

	for _, targetID := range targetUserIDs {
		if (event.Type == EventUserOnline || event.Type == EventUserOffline) && blockedUserIDs[targetID] {
			continue
		}
		h.BroadcastToUser(targetID, event)
	}
}

func (h *Hub) broadcastUserStatus(userID uuid.UUID, isOnline bool) {
	ctx := context.Background()
	key := fmt.Sprintf("online:%s", userID)

	if isOnline {

		_ = h.redis.Set(ctx, key, "true", onlineUserTTL)
	} else {
		_ = h.redis.Del(ctx, key)

		if err := h.db.User.UpdateOneID(userID).SetLastSeenAt(time.Now().UTC()).Exec(ctx); err != nil {
			slog.Error("Failed to update user last_seen_at in DB", "error", err)
		}
	}

	eventType := EventUserOffline
	if isOnline {
		eventType = EventUserOnline
	}

	event := Event{
		Type: eventType,
		Payload: map[string]interface{}{
			"user_id":      userID,
			"is_online":    isOnline,
			"last_seen_at": time.Now().UTC().UnixMilli(),
		},
		Meta: &EventMeta{
			Timestamp: time.Now().UTC().UnixMilli(),
			SenderID:  userID,
		},
	}

	h.BroadcastToContacts(userID, event)
}

func (h *Hub) KeepAlive(userID uuid.UUID) {
	ctx := context.Background()
	key := fmt.Sprintf("online:%s", userID)
	_ = h.redis.Set(ctx, key, "true", onlineUserTTL)
}

func (h *Hub) DisconnectUser(userID uuid.UUID) {
	h.mu.Lock()
	defer h.mu.Unlock()

	if clients, ok := h.userClients[userID]; ok {
		for client := range clients {
			delete(h.clients, client)
			close(client.Send)
			_ = client.Conn.Close()
		}
		delete(h.userClients, userID)
		go h.broadcastUserStatus(userID, false)
	}
}
