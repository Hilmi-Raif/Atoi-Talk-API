package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

// MessageOutbox stores durable message events before they are delivered to
// the asynchronous worker and WebSocket gateway.
type MessageOutbox struct {
	ent.Schema
}

func (MessageOutbox) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (MessageOutbox) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(newUUIDv7),
		field.String("event_type"),
		field.UUID("message_id", uuid.UUID{}),
		field.UUID("chat_id", uuid.UUID{}),
		field.UUID("sender_id", uuid.UUID{}).Optional().Nillable(),
		field.JSON("payload", map[string]interface{}{}).Optional(),
		field.Int("attempt_count").Default(0),
		field.Time("available_at").Default(nowUTC),
		field.Time("locked_at").Optional().Nillable(),
		field.UUID("lock_token", uuid.UUID{}).Optional().Nillable(),
		field.Time("projected_at").Optional().Nillable(),
		field.Time("published_at").Optional().Nillable(),
		field.Text("last_error").Optional().Nillable(),
	}
}

func (MessageOutbox) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("published_at", "available_at", "created_at"),
		index.Fields("message_id", "event_type"),
	}
}
