package schema

import (
	"context"
	"fmt"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
	"github.com/google/uuid"
)

type PrivateChat struct {
	ent.Schema
}

func (PrivateChat) Fields() []ent.Field {
	return []ent.Field{
		field.UUID("id", uuid.UUID{}).Default(newUUIDv7),
		field.UUID("chat_id", uuid.UUID{}).Unique(),
		field.UUID("user1_id", uuid.UUID{}).Optional().Nillable(),
		field.UUID("user2_id", uuid.UUID{}).Optional().Nillable(),
		field.Time("user1_last_read_at").Optional().Nillable(),
		field.Time("user2_last_read_at").Optional().Nillable(),
		field.Time("user1_hidden_at").Optional().Nillable(),
		field.Time("user2_hidden_at").Optional().Nillable(),
		field.Int("user1_unread_count").Default(0),
		field.Int("user2_unread_count").Default(0),
	}
}

func (PrivateChat) Hooks() []ent.Hook {
	return []ent.Hook{
		func(next ent.Mutator) ent.Mutator {
			return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
				if m.Op().Is(ent.OpCreate) {
					v1, exists1 := m.Field("user1_id")
					v2, exists2 := m.Field("user2_id")
					if exists1 && exists2 {
						id1, ok1 := v1.(uuid.UUID)
						id2, ok2 := v2.(uuid.UUID)
						if ok1 && ok2 && id1.String() > id2.String() {
							if err := m.SetField("user1_id", id2); err != nil {
								return nil, fmt.Errorf("set private chat user1_id: %w", err)
							}
							if err := m.SetField("user2_id", id1); err != nil {
								return nil, fmt.Errorf("set private chat user2_id: %w", err)
							}
						}
					}
				}
				return next.Mutate(ctx, m)
			})
		},
	}
}

func (PrivateChat) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("chat", Chat.Type).Ref("private_chat").Field("chat_id").Unique().Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.From("user1", User.Type).Ref("private_chats_as_user1").Field("user1_id").Unique().
			Annotations(entsql.OnDelete(entsql.SetNull)),
		edge.From("user2", User.Type).Ref("private_chats_as_user2").Field("user2_id").Unique().
			Annotations(entsql.OnDelete(entsql.SetNull)),
	}
}

func (PrivateChat) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("user1_id", "user2_id").Unique().StorageKey("unique_user_pair"),
		index.Fields("user2_id"),
	}
}
