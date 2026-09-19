package events

import "github.com/google/uuid"

type EventType string

const (
	EventMessageNew    EventType = "message.new"
	EventMessageUpdate EventType = "message.update"
	EventMessageDelete EventType = "message.delete"

	EventChatNew    EventType = "chat.new"
	EventChatRead   EventType = "chat.read"
	EventChatHide   EventType = "chat.hide"
	EventChatDelete EventType = "chat.delete"
	EventChatUpdate EventType = "chat.update"
	EventTyping     EventType = "chat.typing"

	EventUserOnline   EventType = "user.online"
	EventUserOffline  EventType = "user.offline"
	EventUserUpdate   EventType = "user.update"
	EventUserBlock    EventType = "user.block"
	EventUserUnblock  EventType = "user.unblock"
	EventUserBanned   EventType = "user.banned"
	EventUserUnbanned EventType = "user.unbanned"
	EventUserDeleted  EventType = "user.deleted"
)

type Event struct {
	Type    EventType   `json:"type"`
	Payload interface{} `json:"payload"`
	Meta    *EventMeta  `json:"meta,omitempty"`
}

type EventMeta struct {
	Timestamp   int64     `json:"timestamp"`
	ChatID      uuid.UUID `json:"chat_id,omitempty"`
	SenderID    uuid.UUID `json:"sender_id,omitempty"`
	UnreadCount int       `json:"unread_count"`
}

type Publisher interface {
	BroadcastToUser(uuid.UUID, Event)
	BroadcastToChat(uuid.UUID, Event)
	BroadcastToContacts(uuid.UUID, Event)
	DisconnectUser(uuid.UUID)
}
