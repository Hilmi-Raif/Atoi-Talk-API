package realtimequery

import (
	"context"
	"testing"

	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestQueryResolvesRealtimeRecipientsAndBlocks(t *testing.T) {
	ctx := context.Background()
	client := enttest.Open(t, dialect.SQLite, "file:realtime-query?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = client.Close() })
	query := New(client)
	first := client.User.Create().SetUsername("first").SaveX(ctx)
	second := client.User.Create().SetUsername("second").SaveX(ctx)
	third := client.User.Create().SetUsername("third").SaveX(ctx)
	privateEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(privateEntity.ID).SetUser1ID(first.ID).SetUser2ID(second.ID).SaveX(ctx)
	groupEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(groupEntity.ID).SetName("group").SetInviteCode("realtime-query").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(first.ID).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(third.ID).SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(second.ID).SetBlockedID(first.ID).SaveX(ctx)

	contacts, err := query.Contacts(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, []string{second.ID.String()}, stringifyIDs(contacts))
	members, err := query.ChatMembers(ctx, groupEntity.ID)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{first.ID.String(), third.ID.String()}, stringifyIDs(members))
	blocked, err := query.IsBlocked(ctx, first.ID, second.ID)
	require.NoError(t, err)
	require.True(t, blocked)
	blockedContacts, err := query.BlockedContacts(ctx, first.ID)
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]bool{second.ID: true}, blockedContacts)
}

func stringifyIDs(ids []uuid.UUID) []string {
	result := make([]string, 0, len(ids))
	for _, id := range ids {
		result = append(result, id.String())
	}
	return result
}
