package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type PushDevice struct{ ent.Schema }

func (PushDevice) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }
func (PushDevice) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(newUUIDv7),
		field.UUID("installation_id", uuid.UUID{}).Unique(),
		field.UUID("user_id", uuid.UUID{}),
		field.String("token").NotEmpty().MaxLen(4096).Unique().Sensitive(),
		field.Enum("platform").Values("android", "ios", "web"),
		field.String("session_hash").NotEmpty().Sensitive(),
		field.String("secret_hash").NotEmpty().Sensitive(),
		field.Int64("issued_at"),
		field.Time("expires_at"),
		field.UUID("binding_id", uuid.UUID{}),
		field.Time("disabled_at").Optional().Nillable(),
	}
}
func (PushDevice) Indexes() []ent.Index { return []ent.Index{index.Fields("user_id", "disabled_at")} }
