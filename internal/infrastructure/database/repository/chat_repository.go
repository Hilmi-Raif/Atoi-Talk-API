package repository

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/ent/message"
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
			func(s *sql.Selector) {
				privateAsUser1 := sql.Table(privatechat.Table).As("pc_user1")
				privateUser1Chats := sql.Table(chat.Table).As("c_user1")
				privateAsUser2 := sql.Table(privatechat.Table).As("pc_user2")
				privateUser2Chats := sql.Table(chat.Table).As("c_user2")
				members := sql.Table(groupmember.Table).As("gm_candidate")
				groups := sql.Table(groupchat.Table).As("gc_candidate")

				privateUser1Candidates := s.New().
					Select(privateAsUser1.C(privatechat.FieldChatID)).
					From(privateAsUser1).
					Join(privateUser1Chats).
					On(privateAsUser1.C(privatechat.FieldChatID), privateUser1Chats.C(chat.FieldID)).
					Where(sql.And(
						sql.EQ(privateAsUser1.C(privatechat.FieldUser1ID), userID),
						sql.Or(
							sql.IsNull(privateAsUser1.C(privatechat.FieldUser1HiddenAt)),
							sql.ColumnsLT(privateAsUser1.C(privatechat.FieldUser1HiddenAt), privateUser1Chats.C(chat.FieldLastMessageAt)),
						),
					))

				privateUser2Candidates := s.New().
					Select(privateAsUser2.C(privatechat.FieldChatID)).
					From(privateAsUser2).
					Join(privateUser2Chats).
					On(privateAsUser2.C(privatechat.FieldChatID), privateUser2Chats.C(chat.FieldID)).
					Where(sql.And(
						sql.EQ(privateAsUser2.C(privatechat.FieldUser2ID), userID),
						sql.Or(
							sql.IsNull(privateAsUser2.C(privatechat.FieldUser2HiddenAt)),
							sql.ColumnsLT(privateAsUser2.C(privatechat.FieldUser2HiddenAt), privateUser2Chats.C(chat.FieldLastMessageAt)),
						),
					))

				groupCandidates := s.New().
					Select(groups.C(groupchat.FieldChatID)).
					From(members).
					Join(groups).
					On(members.C(groupmember.FieldGroupChatID), groups.C(groupchat.FieldID)).
					Where(sql.EQ(members.C(groupmember.FieldUserID), userID))

				privateUser1Candidates.Union(privateUser2Candidates).Union(groupCandidates)
				s.Where(sql.In(s.C(chat.FieldID), privateUser1Candidates))
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
		Select(
			chat.FieldID,
			chat.FieldType,
			chat.FieldLastMessageID,
			chat.FieldLastMessageAt,
		).
		Order(ent.Desc(chat.FieldLastMessageAt), ent.Desc(chat.FieldID)).
		Limit(fetchLimit + 1).
		WithGroupChat(func(q *ent.GroupChatQuery) {
			q.Select(
				groupchat.FieldID,
				groupchat.FieldChatID,
				groupchat.FieldName,
				groupchat.FieldDescription,
				groupchat.FieldAvatarID,
				groupchat.FieldIsPublic,
				groupchat.FieldInviteCode,
				groupchat.FieldInviteExpiresAt,
			)
			q.WithMembers(func(mq *ent.GroupMemberQuery) {
				mq.Select(
					groupmember.FieldID,
					groupmember.FieldGroupChatID,
					groupmember.FieldUserID,
					groupmember.FieldRole,
					groupmember.FieldLastReadAt,
					groupmember.FieldUnreadCount,
				)
				mq.Where(groupmember.UserID(userID))
			})
		}).
		WithLastMessage(func(q *ent.MessageQuery) {
			q.Select(
				message.FieldID,
				message.FieldCreatedAt,
				message.FieldChatID,
				message.FieldSenderID,
				message.FieldReplyToID,
				message.FieldType,
				message.FieldContent,
				message.FieldActionData,
				message.FieldDeletedAt,
				message.FieldEditedAt,
			)
			q.WithAttachments(func(aq *ent.MediaQuery) {
				aq.Select(
					media.FieldID,
					media.FieldFileName,
					media.FieldOriginalName,
					media.FieldFileSize,
					media.FieldMimeType,
					media.FieldMessageID,
				)
				aq.Limit(1)
			})
		}).
		All(ctx)

	if err != nil {
		return nil, "", false, err
	}

	chatIDs := make([]uuid.UUID, 0, len(chats))
	hasPrivateChat := false
	for _, c := range chats {
		chatIDs = append(chatIDs, c.ID)
		hasPrivateChat = hasPrivateChat || c.Type == chat.TypePrivate
	}
	if hasPrivateChat {
		privateChats, err := r.client.PrivateChat.Query().
			Where(privatechat.ChatIDIn(chatIDs...)).
			All(ctx)
		if err != nil {
			return nil, "", false, err
		}
		privateChatsByChatID := make(map[uuid.UUID]*ent.PrivateChat, len(privateChats))
		for _, privateChat := range privateChats {
			privateChatsByChatID[privateChat.ChatID] = privateChat
		}
		for _, c := range chats {
			c.Edges.PrivateChat = privateChatsByChatID[c.ID]
		}
	}

	userIDs := make(map[uuid.UUID]struct{})
	avatarIDs := make(map[uuid.UUID]struct{})
	for _, c := range chats {
		if c.Edges.GroupChat != nil && c.Edges.GroupChat.AvatarID != nil {
			avatarIDs[*c.Edges.GroupChat.AvatarID] = struct{}{}
		}
		if c.Edges.LastMessage != nil && c.Edges.LastMessage.SenderID != nil {
			userIDs[*c.Edges.LastMessage.SenderID] = struct{}{}
		}
		if c.Edges.PrivateChat == nil {
			continue
		}
		pc := c.Edges.PrivateChat
		if pc.User1ID != nil && *pc.User1ID != userID {
			userIDs[*pc.User1ID] = struct{}{}
		}
		if pc.User2ID != nil && *pc.User2ID != userID {
			userIDs[*pc.User2ID] = struct{}{}
		}
	}
	if len(userIDs) > 0 {
		ids := make([]uuid.UUID, 0, len(userIDs))
		for id := range userIDs {
			ids = append(ids, id)
		}
		users, err := r.client.User.Query().
			Where(user.IDIn(ids...)).
			Select(user.FieldID, user.FieldUsername, user.FieldFullName, user.FieldAvatarID, user.FieldDeletedAt, user.FieldIsBanned, user.FieldBannedUntil).
			All(ctx)
		if err != nil {
			return nil, "", false, err
		}
		usersByID := make(map[uuid.UUID]*ent.User, len(users))
		for _, loadedUser := range users {
			usersByID[loadedUser.ID] = loadedUser
			if loadedUser.AvatarID != nil {
				avatarIDs[*loadedUser.AvatarID] = struct{}{}
			}
		}
		for _, c := range chats {
			if c.Edges.LastMessage != nil && c.Edges.LastMessage.SenderID != nil {
				c.Edges.LastMessage.Edges.Sender = usersByID[*c.Edges.LastMessage.SenderID]
			}
			if c.Edges.PrivateChat == nil {
				continue
			}
			pc := c.Edges.PrivateChat
			if pc.User1ID != nil && *pc.User1ID != userID {
				pc.Edges.User1 = usersByID[*pc.User1ID]
			}
			if pc.User2ID != nil && *pc.User2ID != userID {
				pc.Edges.User2 = usersByID[*pc.User2ID]
			}
		}
	}

	if len(avatarIDs) > 0 {
		ids := make([]uuid.UUID, 0, len(avatarIDs))
		for id := range avatarIDs {
			ids = append(ids, id)
		}
		avatars, err := r.client.Media.Query().
			Where(media.IDIn(ids...)).
			Select(media.FieldID, media.FieldFileName).
			All(ctx)
		if err != nil {
			return nil, "", false, err
		}
		avatarsByID := make(map[uuid.UUID]*ent.Media, len(avatars))
		for _, avatar := range avatars {
			avatarsByID[avatar.ID] = avatar
		}
		for _, c := range chats {
			if c.Edges.GroupChat != nil && c.Edges.GroupChat.AvatarID != nil {
				c.Edges.GroupChat.Edges.Avatar = avatarsByID[*c.Edges.GroupChat.AvatarID]
			}
			if c.Edges.LastMessage != nil && c.Edges.LastMessage.Edges.Sender != nil && c.Edges.LastMessage.Edges.Sender.AvatarID != nil {
				c.Edges.LastMessage.Edges.Sender.Edges.Avatar = avatarsByID[*c.Edges.LastMessage.Edges.Sender.AvatarID]
			}
			if c.Edges.PrivateChat == nil {
				continue
			}
			pc := c.Edges.PrivateChat
			if pc.Edges.User1 != nil && pc.Edges.User1.AvatarID != nil {
				pc.Edges.User1.Edges.Avatar = avatarsByID[*pc.Edges.User1.AvatarID]
			}
			if pc.Edges.User2 != nil && pc.Edges.User2.AvatarID != nil {
				pc.Edges.User2.Edges.Avatar = avatarsByID[*pc.Edges.User2.AvatarID]
			}
		}
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
