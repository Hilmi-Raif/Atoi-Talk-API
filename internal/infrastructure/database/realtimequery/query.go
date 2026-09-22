package realtimequery

import (
	"context"

	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/ent/userblock"
	"github.com/google/uuid"
)

type Query struct {
	client *ent.Client
}

func New(client *ent.Client) *Query {
	return &Query{client: client}
}

func (q *Query) Contacts(ctx context.Context, userID uuid.UUID) ([]uuid.UUID, error) {
	privateChats, err := q.client.PrivateChat.Query().Where(
		privatechat.Or(privatechat.User1ID(userID), privatechat.User2ID(userID)),
	).Select(privatechat.FieldUser1ID, privatechat.FieldUser2ID).All(ctx)
	if err != nil {
		return nil, err
	}
	contacts := make([]uuid.UUID, 0, len(privateChats))
	for _, privateChat := range privateChats {
		if privateChat.User1ID != nil && *privateChat.User1ID == userID && privateChat.User2ID != nil {
			contacts = append(contacts, *privateChat.User2ID)
		} else if privateChat.User2ID != nil && *privateChat.User2ID == userID && privateChat.User1ID != nil {
			contacts = append(contacts, *privateChat.User1ID)
		}
	}
	return contacts, nil
}

func (q *Query) ChatMembers(ctx context.Context, chatID uuid.UUID) ([]uuid.UUID, error) {
	entity, err := q.client.Chat.Query().Where(chat.ID(chatID), chat.DeletedAtIsNil()).Select(chat.FieldType).Only(ctx)
	if err != nil {
		return nil, err
	}
	switch entity.Type {
	case chat.TypePrivate:
		privateEntity, err := q.client.PrivateChat.Query().Where(privatechat.ChatID(chatID)).Select(privatechat.FieldUser1ID, privatechat.FieldUser2ID).Only(ctx)
		if err != nil {
			return nil, err
		}
		members := make([]uuid.UUID, 0, 2)
		if privateEntity.User1ID != nil {
			members = append(members, *privateEntity.User1ID)
		}
		if privateEntity.User2ID != nil {
			members = append(members, *privateEntity.User2ID)
		}
		return members, nil
	case chat.TypeGroup:
		groupID, err := q.client.GroupChat.Query().Where(groupchat.ChatID(chatID)).OnlyID(ctx)
		if err != nil {
			return nil, err
		}
		members, err := q.client.GroupMember.Query().Where(groupmember.GroupChatID(groupID)).Select(groupmember.FieldUserID).All(ctx)
		if err != nil {
			return nil, err
		}
		userIDs := make([]uuid.UUID, 0, len(members))
		for _, member := range members {
			userIDs = append(userIDs, member.UserID)
		}
		return userIDs, nil
	default:
		return nil, nil
	}
}

func (q *Query) IsBlocked(ctx context.Context, firstID, secondID uuid.UUID) (bool, error) {
	return q.client.UserBlock.Query().Where(
		userblock.Or(userblock.BlockerID(firstID), userblock.BlockedID(firstID)),
		userblock.Or(userblock.BlockerID(secondID), userblock.BlockedID(secondID)),
	).Exist(ctx)
}

func (q *Query) BlockedContacts(ctx context.Context, userID uuid.UUID) (map[uuid.UUID]bool, error) {
	blocks, err := q.client.UserBlock.Query().Where(
		userblock.Or(userblock.BlockerID(userID), userblock.BlockedID(userID)),
	).Select(userblock.FieldBlockerID, userblock.FieldBlockedID).All(ctx)
	if err != nil {
		return nil, err
	}
	blocked := make(map[uuid.UUID]bool, len(blocks))
	for _, block := range blocks {
		if block.BlockerID == userID {
			blocked[block.BlockedID] = true
		} else {
			blocked[block.BlockerID] = true
		}
	}
	return blocked, nil
}
