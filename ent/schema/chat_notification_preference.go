package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type ChatNotificationPreference struct{ ent.Schema }

func (ChatNotificationPreference) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (ChatNotificationPreference) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(newUUIDv7),
		field.UUID("user_id", uuid.UUID{}), field.UUID("chat_id", uuid.UUID{}),
		field.Bool("muted_forever").Default(false), field.Time("muted_until").Optional().Nillable(),
	}
}
func (ChatNotificationPreference) Indexes() []ent.Index {
	return []ent.Index{index.Fields("user_id", "chat_id").Unique()}
}
