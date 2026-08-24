package repository

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/ent/user"
	"context"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"
)

type ChatRepository struct {
	client *ent.Client
}

func NewChatRepository(client *ent.Client) *ChatRepository {
	return &ChatRepository{
		client: client,
	}
}

func (r *ChatRepository) GetChatByID(ctx context.Context, userID, chatID uuid.UUID) (*ent.Chat, error) {
	return r.client.Chat.Query().
		Where(
			chat.ID(chatID),
			chat.DeletedAtIsNil(),
			chat.Or(

				chat.HasPrivateChatWith(privatechat.Or(privatechat.User1ID(userID), privatechat.User2ID(userID))),

				chat.HasGroupChatWith(
					groupchat.Or(
						groupchat.HasMembersWith(groupmember.UserID(userID)),
						groupchat.IsPublic(true),
					),
				),
			),
		).
		WithPrivateChat(func(q *ent.PrivateChatQuery) {
			q.WithUser1(func(uq *ent.UserQuery) {
				uq.Select(user.FieldID, user.FieldUsername, user.FieldFullName, user.FieldAvatarID, user.FieldDeletedAt, user.FieldIsBanned, user.FieldBannedUntil)
				uq.WithAvatar()
			})
			q.WithUser2(func(uq *ent.UserQuery) {
				uq.Select(user.FieldID, user.FieldUsername, user.FieldFullName, user.FieldAvatarID, user.FieldDeletedAt, user.FieldIsBanned, user.FieldBannedUntil)
				uq.WithAvatar()
			})
		}).
		WithGroupChat(func(q *ent.GroupChatQuery) {
			q.WithAvatar()

			q.WithMembers(func(mq *ent.GroupMemberQuery) {
				mq.Where(groupmember.UserID(userID))
			})
		}).
		WithLastMessage(func(q *ent.MessageQuery) {
			q.WithSender(func(uq *ent.UserQuery) {
				uq.Select(user.FieldID, user.FieldUsername, user.FieldFullName, user.FieldAvatarID)
				uq.WithAvatar()
			})
			q.WithAttachments(func(aq *ent.MediaQuery) {
				aq.Limit(1)
			})
		}).
		Only(ctx)
}

func (r *ChatRepository) GetChats(ctx context.Context, userID uuid.UUID, queryStr string, cursor string, limit int) ([]*ent.Chat, string, bool, error) {
	query := r.client.Chat.Query().
		Where(
			chat.DeletedAtIsNil(),
			chat.LastMessageAtNotNil(),
			chat.Or(
				chat.HasPrivateChatWith(privatechat.Or(privatechat.User1ID(userID), privatechat.User2ID(userID))),
				chat.HasGroupChatWith(groupchat.HasMembersWith(groupmember.UserID(userID))),
			),
			func(s *sql.Selector) {
				t := sql.Table(privatechat.Table)
				s.Where(
					sql.Not(
						sql.Exists(
							sql.Select(privatechat.FieldID).From(t).Where(
								sql.And(
									sql.ColumnsEQ(t.C(privatechat.FieldChatID), s.C(chat.FieldID)),
									sql.Or(
										sql.And(
											sql.EQ(t.C(privatechat.FieldUser1ID), userID),
											sql.NotNull(t.C(privatechat.FieldUser1HiddenAt)),
											sql.Or(
												sql.ColumnsGTE(t.C(privatechat.FieldUser1HiddenAt), s.C(chat.FieldLastMessageAt)),
												sql.IsNull(s.C(chat.FieldLastMessageAt)),
											),
										),
										sql.And(
											sql.EQ(t.C(privatechat.FieldUser2ID), userID),
											sql.NotNull(t.C(privatechat.FieldUser2HiddenAt)),
											sql.Or(
												sql.ColumnsGTE(t.C(privatechat.FieldUser2HiddenAt), s.C(chat.FieldLastMessageAt)),
												sql.IsNull(s.C(chat.FieldLastMessageAt)),
											),
										),
									),
								),
							),
						),
					),
				)
			},
		)

	if queryStr != "" {
		otherUserPredicate := user.Or(
			user.FullNameContainsFold(queryStr),
			user.UsernameContainsFold(queryStr),
		)
		query = query.Where(
			chat.Or(
				chat.HasPrivateChatWith(privatechat.Or(
					privatechat.And(
						privatechat.User1ID(userID),
						privatechat.HasUser2With(otherUserPredicate),
					),
					privatechat.And(
						privatechat.User2ID(userID),
						privatechat.HasUser1With(otherUserPredicate),
					),
				)),
				chat.HasGroupChatWith(groupchat.NameContainsFold(queryStr)),
			),
		)
	}

	if cursor != "" {
		decodedBytes, err := base64.URLEncoding.DecodeString(cursor)
		if err == nil {
			parts := strings.Split(string(decodedBytes), ",")
			if len(parts) == 2 {
				cursorTimeMicro, err1 := strconv.ParseInt(parts[0], 10, 64)
				cursorID, err2 := uuid.Parse(parts[1])
				if err1 == nil && err2 == nil {
					cursorTime := time.UnixMicro(cursorTimeMicro).UTC()
					query = query.Where(
						chat.Or(
							chat.LastMessageAtLT(cursorTime),
							chat.And(
								chat.LastMessageAtEQ(cursorTime),
								chat.IDLT(cursorID),
							),
						),
					)
				}
			}
		}
	}

	fetchLimit := limit

	chats, err := query.
		Order(ent.Desc(chat.FieldLastMessageAt), ent.Desc(chat.FieldID)).
		Limit(fetchLimit + 1).
		WithPrivateChat(func(q *ent.PrivateChatQuery) {
			q.WithUser1(func(uq *ent.UserQuery) {
				uq.Select(user.FieldID, user.FieldUsername, user.FieldFullName, user.FieldAvatarID, user.FieldDeletedAt, user.FieldIsBanned, user.FieldBannedUntil)
				uq.WithAvatar()
			})
			q.WithUser2(func(uq *ent.UserQuery) {
				uq.Select(user.FieldID, user.FieldUsername, user.FieldFullName, user.FieldAvatarID, user.FieldDeletedAt, user.FieldIsBanned, user.FieldBannedUntil)
				uq.WithAvatar()
			})
		}).
		WithGroupChat(func(q *ent.GroupChatQuery) {
			q.WithAvatar()
			q.WithMembers(func(mq *ent.GroupMemberQuery) {
				mq.Where(groupmember.UserID(userID))
			})
		}).
		WithLastMessage(func(q *ent.MessageQuery) {
			q.WithSender(func(uq *ent.UserQuery) {
				uq.Select(user.FieldID, user.FieldUsername, user.FieldFullName, user.FieldAvatarID)
				uq.WithAvatar()
			})
			q.WithAttachments(func(aq *ent.MediaQuery) {
				aq.Limit(1)
			})
		}).
		All(ctx)

	if err != nil {
		return nil, "", false, err
	}

	hasNext := false
	var nextCursor string
	if len(chats) > limit {
		hasNext = true
		chats = chats[:limit]
		lastChat := chats[len(chats)-1]

		var cursorTime int64
		if lastChat.LastMessageAt != nil {
			cursorTime = lastChat.LastMessageAt.UnixMicro()
		} else {
			cursorTime = 0
		}

		cursorString := fmt.Sprintf("%d,%s", cursorTime, lastChat.ID.String())
		nextCursor = base64.URLEncoding.EncodeToString([]byte(cursorString))
	}

	return chats, nextCursor, hasNext, nil
}
