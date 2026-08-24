package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

type GroupChat struct {
	ent.Schema
}

func (GroupChat) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(newUUIDv7),
		field.UUID("chat_id", uuid.UUID{}).Unique(),
		field.UUID("created_by", uuid.UUID{}).Optional().Nillable(),
		field.String("name").MaxLen(100).NotEmpty(),
		field.Text("description").Optional().Nillable(),
		field.UUID("avatar_id", uuid.UUID{}).Optional().Nillable(),
		field.Bool("is_public").Default(false),

		field.String("invite_code").MaxLen(50).Unique().NotEmpty(),
		field.Time("invite_expires_at").Optional().Nillable(),
	}
}

func (GroupChat) Edges() []ent.Edge {
	return []ent.Edge{

		edge.From("avatar", Media.Type).
			Ref("group_avatar").
			Unique().
			Field("avatar_id"),

		edge.From("chat", Chat.Type).Ref("group_chat").Field("chat_id").Unique().Required(),
		edge.From("creator", User.Type).Ref("created_groups").Field("created_by").Unique(),
		edge.To("members", GroupMember.Type).Annotations(entsql.OnDelete(entsql.Cascade)),

		edge.From("reports", Report.Type).Ref("group"),
	}
}
