package push

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/chatnotificationpreference"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/infrastructure/database/realtimequery"
	"context"
	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
	"time"
)

type ChatPreference struct {
	ChatID       uuid.UUID  `json:"chat_id"`
	Name         string     `json:"name"`
	MutedForever bool       `json:"muted_forever"`
	MutedUntil   *time.Time `json:"muted_until"`
}
type Preferences struct {
	MessageNotificationsEnabled bool `json:"message_notifications_enabled"`
}
type MuteRequest struct {
	MutedForever bool       `json:"muted_forever"`
	MutedUntil   *time.Time `json:"muted_until"`
}

func (s *Service) Preferences(ctx context.Context, id uuid.UUID) (*Preferences, error) {
	u, err := s.client.User.Get(ctx, id)
	if err != nil {
		return nil, helper.NewInternalServerError("")
	}
	return &Preferences{MessageNotificationsEnabled: u.MessageNotificationsEnabled}, nil
}

func (s *Service) MutedChats(ctx context.Context, id uuid.UUID, cursor string, limit int) ([]ChatPreference, string, bool, error) {
	if limit < 1 || limit > 50 {
		return nil, "", false, helper.NewBadRequestError("Invalid limit")
	}
	q := s.client.ChatNotificationPreference.Query().Where(chatnotificationpreference.UserID(id), chatnotificationpreference.Or(chatnotificationpreference.MutedForever(true), chatnotificationpreference.MutedUntilGT(time.Now().UTC())))
	if cursor != "" {
		after, err := uuid.Parse(cursor)
		if err != nil {
			return nil, "", false, helper.NewBadRequestError("Invalid cursor")
		}
		q.Where(chatnotificationpreference.IDGT(after))
	}
	q.Where(func(s *sql.Selector) {
		c, p, g, m := sql.Table("chats"), sql.Table("private_chats"), sql.Table("group_chats"), sql.Table("group_members")
		private := sql.Select(p.C("chat_id")).From(p).Where(sql.Or(sql.EQ(p.C("user1_id"), id), sql.EQ(p.C("user2_id"), id)))
		group := sql.Select(g.C("chat_id")).From(g).Join(m).On(g.C("id"), m.C("group_chat_id")).Where(sql.EQ(m.C("user_id"), id))
		visible := sql.Select(c.C("id")).From(c).Where(sql.And(sql.IsNull(c.C("deleted_at")), sql.Or(sql.In(c.C("id"), private), sql.In(c.C("id"), group))))
		s.Where(sql.In(s.C("chat_id"), visible))
	})
	rows, err := q.Order(ent.Asc(chatnotificationpreference.FieldID)).Limit(limit + 1).All(ctx)
	if err != nil {
		return nil, "", false, helper.NewInternalServerError("")
	}
	hasNext := len(rows) > limit
	if hasNext {
		rows = rows[:limit]
	}
	next := ""
	if hasNext {
		next = rows[len(rows)-1].ID.String()
	}
	result := []ChatPreference{}
	ids := make([]uuid.UUID, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ChatID)
	}
	if len(ids) > 0 {
		chats, queryErr := s.client.Chat.Query().Where(chat.IDIn(ids...), chat.DeletedAtIsNil()).WithGroupChat(func(q *ent.GroupChatQuery) {
			q.WithMembers(func(m *ent.GroupMemberQuery) { m.Where(groupmember.UserID(id)) })
		}).WithPrivateChat(func(q *ent.PrivateChatQuery) { q.WithUser1().WithUser2() }).All(ctx)
		if queryErr != nil {
			return nil, "", false, helper.NewInternalServerError("")
		}
		names := map[uuid.UUID]string{}
		for _, c := range chats {
			if g := c.Edges.GroupChat; g != nil && len(g.Edges.Members) > 0 {
				names[c.ID] = g.Name
			}
			if p := c.Edges.PrivateChat; p != nil {
				var other *ent.User
				if p.User1ID != nil && *p.User1ID == id {
					other = p.Edges.User2
				} else if p.User2ID != nil && *p.User2ID == id {
					other = p.Edges.User1
				}
				if other != nil {
					name := "Conversation"
					if other.FullName != nil {
						name = *other.FullName
					}
					if other.DeletedAt != nil {
						name = "Deleted Account"
					}
					names[c.ID] = name
				}
			}
		}
		for _, row := range rows {
			if name, ok := names[row.ChatID]; ok {
				result = append(result, ChatPreference{row.ChatID, name, row.MutedForever, row.MutedUntil})
			}
		}
	}
	return result, next, hasNext, nil
}
func (s *Service) SetGlobal(ctx context.Context, id uuid.UUID, enabled bool) error {
	_, err := s.client.User.Update().Where(user.ID(id), user.DeletedAtIsNil()).SetMessageNotificationsEnabled(enabled).Save(ctx)
	if err != nil {
		return helper.NewInternalServerError("")
	}
	return nil
}
func (s *Service) SetChatMute(ctx context.Context, id, chatID uuid.UUID, req MuteRequest) error {
	members, err := realtimequery.New(s.client).ChatMembers(ctx, chatID)
	if ent.IsNotFound(err) {
		return helper.NewNotFoundError("Chat not found")
	}
	if err != nil {
		return helper.NewInternalServerError("")
	}
	member := false
	for _, m := range members {
		if m == id {
			member = true
		}
	}
	if !member {
		return helper.NewForbiddenError("You are not a member of this chat")
	}
	if req.MutedForever && req.MutedUntil != nil {
		return helper.NewBadRequestError("Choose a duration or indefinite mute")
	}
	if req.MutedUntil != nil && (!req.MutedUntil.After(time.Now().UTC()) || req.MutedUntil.After(time.Now().UTC().Add(365*24*time.Hour))) {
		return helper.NewBadRequestError("Invalid mute expiry")
	}
	if !req.MutedForever && req.MutedUntil == nil {
		_, err = s.client.ChatNotificationPreference.Delete().Where(chatnotificationpreference.UserID(id), chatnotificationpreference.ChatID(chatID)).Exec(ctx)
	} else {
		q := s.client.ChatNotificationPreference.Create().SetUserID(id).SetChatID(chatID).SetMutedForever(req.MutedForever).SetNillableMutedUntil(req.MutedUntil)
		upsert := q.OnConflictColumns(chatnotificationpreference.FieldUserID, chatnotificationpreference.FieldChatID).UpdateNewValues()
		if req.MutedUntil == nil {
			upsert.ClearMutedUntil()
		}
		err = upsert.Exec(ctx)
	}
	if err != nil {
		return helper.NewInternalServerError("")
	}
	return nil
}
