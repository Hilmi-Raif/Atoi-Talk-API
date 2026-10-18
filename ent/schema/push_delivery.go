package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type PushDelivery struct{ ent.Schema }

func (PushDelivery) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (PushDelivery) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(newUUIDv7),
		field.UUID("event_id", uuid.UUID{}),
		field.UUID("device_id", uuid.UUID{}),
		field.UUID("binding_id", uuid.UUID{}),
		field.UUID("user_id", uuid.UUID{}),
		field.UUID("message_id", uuid.UUID{}),
		field.UUID("chat_id", uuid.UUID{}),
		field.Time("available_at").Default(nowUTC),
		field.Time("expires_at"),
		field.Int("attempt_count").Default(0),
		field.Time("locked_at").Optional().Nillable(),
		field.UUID("lock_token", uuid.UUID{}).Optional().Nillable(),
		field.Time("sent_at").Optional().Nillable(),
		field.Time("finished_at").Optional().Nillable(),
		field.String("outcome").Default("pending"),
		field.Text("last_error").Optional().Nillable(),
	}
}
func (PushDelivery) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("event_id", "device_id").Unique(),
		index.Fields("finished_at", "available_at", "created_at"),
	}
}
