package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type User struct {
	ent.Schema
}

func (User) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(newUUIDv7),
		field.String("email").MaxLen(255).Unique().Optional().Nillable(),
		field.String("username").MaxLen(50).Unique().Optional().Nillable(),
		field.String("password_hash").MaxLen(255).Optional().Nillable().Sensitive(),
		field.String("full_name").MaxLen(100).Optional().Nillable(),
		field.String("bio").MaxLen(255).Optional().Nillable(),
		field.UUID("avatar_id", uuid.UUID{}).Optional().Nillable(),

		field.Time("last_seen_at").Optional().Nillable(),
		field.Time("deleted_at").Optional().Nillable(),

		field.Enum("role").Values("user", "admin").Default("user"),
		field.Bool("is_banned").Default(false),
		field.Bool("message_notifications_enabled").Default(true),
		field.Time("banned_until").Optional().Nillable(),
		field.String("ban_reason").Optional().Nillable(),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("avatar", Media.Type).
			Ref("user_avatar").
			Unique().
			Field("avatar_id"),
		edge.To("identities", UserIdentity.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("sent_messages", Message.Type).
			Annotations(entsql.OnDelete(entsql.SetNull)),
		edge.To("created_groups", GroupChat.Type).
			Annotations(entsql.OnDelete(entsql.SetNull)),
		edge.To("group_memberships", GroupMember.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("private_chats_as_user1", PrivateChat.Type).
			Annotations(entsql.OnDelete(entsql.SetNull)),
		edge.To("private_chats_as_user2", PrivateChat.Type).
			Annotations(entsql.OnDelete(entsql.SetNull)),
		edge.To("uploaded_media", Media.Type).
			Annotations(entsql.OnDelete(entsql.SetNull)),
		edge.To("blocked_users_rel", UserBlock.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("blocked_by_rel", UserBlock.Type).
			Annotations(entsql.OnDelete(entsql.Cascade)),

		edge.To("reports_made", Report.Type),
		edge.To("reports_received", Report.Type),
	}
}

func (User) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("full_name"),
	}
}
