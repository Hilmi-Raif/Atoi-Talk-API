package websocket

import "github.com/google/uuid"

// Publisher is the service-facing WebSocket boundary.
//
// The concrete Hub remains responsible for connection lifecycle and delivery;
// services only need to publish events or disconnect a user.
type Publisher interface {
	BroadcastToUser(uuid.UUID, Event)
	BroadcastToChat(uuid.UUID, Event)
	BroadcastToContacts(uuid.UUID, Event)
	DisconnectUser(uuid.UUID)
}
