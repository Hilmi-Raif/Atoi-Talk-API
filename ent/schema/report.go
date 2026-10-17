package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type Report struct {
	ent.Schema
}

func (Report) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(newUUIDv7),

		field.Enum("target_type").
			Values("message", "group", "user").
			Immutable(),

		field.String("reason").NotEmpty(),
		field.Text("description").Optional().Nillable(),

		field.JSON("evidence_snapshot", map[string]interface{}{}).Optional(),

		field.Enum("status").
			Values("pending", "reviewed", "resolved", "rejected").
			Default("pending"),

		field.UUID("reporter_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("message_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("group_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("target_user_id", uuid.UUID{}).Optional().Nillable(),

		field.Text("resolution_notes").Optional().Nillable(),
		field.Time("resolved_at").Optional().Nillable(),
		field.UUID("resolved_by_id", uuid.UUID{}).Optional().Nillable(),

		field.Time("created_at").Default(nowUTC).Immutable(),
		field.Time("updated_at").Default(nowUTC).UpdateDefault(nowUTC),
	}
}

func (Report) Edges() []ent.Edge {
	return []ent.Edge{

		edge.From("reporter", User.Type).
			Ref("reports_made").
			Field("reporter_id").
			Unique().
			Annotations(entsql.OnDelete(entsql.SetNull)),

		edge.To("message", Message.Type).
			Field("message_id").
			Unique().
			Annotations(entsql.OnDelete(entsql.SetNull)),

		edge.To("group", GroupChat.Type).
			Field("group_id").
			Unique().
			Annotations(entsql.OnDelete(entsql.SetNull)),

		edge.To("target_user", User.Type).
			Field("target_user_id").
			Unique().
			Annotations(entsql.OnDelete(entsql.SetNull)),

		edge.To("evidence_media", Media.Type).
			Annotations(entsql.OnDelete(entsql.SetNull)),

		edge.To("resolved_by", User.Type).
			Field("resolved_by_id").
			Unique().
			Annotations(entsql.OnDelete(entsql.SetNull)),
	}
}

func (Report) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status"),
		index.Fields("created_at"),
		index.Fields("reporter_id"),
	}
}
