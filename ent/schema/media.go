package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"github.com/google/uuid"
)

type Media struct {
	ent.Schema
}

func (Media) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Media) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(newUUIDv7),
		field.String("file_name").Unique().MaxLen(255).NotEmpty(),
		field.String("original_name").MaxLen(255).NotEmpty(),
		field.Int64("file_size").Positive(),
		field.String("mime_type").MaxLen(100).NotEmpty(),
		field.Enum("category").Values("user_avatar", "group_avatar", "message_attachment").Default("message_attachment"),
		field.Enum("upload_status").Values("pending", "completed").Default("completed"),
		field.Time("upload_expires_at").Optional().Nillable(),
		field.Time("completed_at").Optional().Nillable(),
		field.UUID("message_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("uploaded_by_id", uuid.UUID{}).Optional().Nillable(),
	}
}
func (Media) Edges() []ent.Edge {
	return []ent.Edge{

		edge.From("message", Message.Type).
			Ref("attachments").
			Field("message_id").
			Unique(),

		edge.To("user_avatar", User.Type).
			Unique(),
		edge.To("group_avatar", GroupChat.Type).
			Unique(),

		edge.From("uploader", User.Type).
			Ref("uploaded_media").
			Field("uploaded_by_id").
			Unique(),

		edge.From("reports", Report.Type).
			Ref("evidence_media"),
	}
}
