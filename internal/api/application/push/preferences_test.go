package push

import (
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/internal/infrastructure/config"
	"context"
	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestPreferencesMembershipExpiryAndGlobal(t *testing.T) {
	ctx := context.Background()
	c := enttest.Open(t, dialect.SQLite, "file:preferences?mode=memory&cache=shared&_fk=1")
	defer c.Close()
	a := c.User.Create().SaveX(ctx)
	b := c.User.Create().SaveX(ctx)
	outsider := c.User.Create().SaveX(ctx)
	chat := c.Chat.Create().SetType("private").SaveX(ctx)
	c.PrivateChat.Create().SetChatID(chat.ID).SetUser1ID(a.ID).SetUser2ID(b.ID).SaveX(ctx)
	s := NewService(c, &config.AppConfig{})
	require.Error(t, s.SetChatMute(ctx, outsider.ID, chat.ID, MuteRequest{MutedForever: true}))
	until := time.Now().UTC().Add(time.Hour)
	require.NoError(t, s.SetChatMute(ctx, a.ID, chat.ID, MuteRequest{MutedUntil: &until}))
	require.NoError(t, s.SetChatMute(ctx, a.ID, chat.ID, MuteRequest{MutedForever: true}))
	prefs, err := s.Preferences(ctx, a.ID)
	require.NoError(t, err)
	require.True(t, prefs.MessageNotificationsEnabled)
	rows, _, _, err := s.MutedChats(ctx, a.ID, "", 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.True(t, rows[0].MutedForever)
	require.Nil(t, rows[0].MutedUntil)
	require.NoError(t, s.SetGlobal(ctx, a.ID, false))
	prefs, err = s.Preferences(ctx, a.ID)
	require.NoError(t, err)
	require.False(t, prefs.MessageNotificationsEnabled)
	require.Len(t, rows, 1)
	require.NoError(t, s.SetChatMute(ctx, a.ID, chat.ID, MuteRequest{}))
	require.Zero(t, c.ChatNotificationPreference.Query().CountX(ctx))
	past := time.Now().UTC().Add(-time.Second)
	require.Error(t, s.SetChatMute(ctx, a.ID, chat.ID, MuteRequest{MutedUntil: &past}))
	require.Error(t, s.SetChatMute(ctx, a.ID, chat.ID, MuteRequest{MutedForever: true, MutedUntil: &until}))
	require.Error(t, s.SetChatMute(ctx, a.ID, uuid.New(), MuteRequest{}))
}

func TestMutedChatsPaginationFiltersExpiredAndNonmembers(t *testing.T) {
	ctx := context.Background()
	c := enttest.Open(t, dialect.SQLite, "file:muted-pagination?mode=memory&cache=shared&_fk=1")
	defer c.Close()
	a, outsider := c.User.Create().SaveX(ctx), c.User.Create().SaveX(ctx)
	s := NewService(c, &config.AppConfig{})
	for i := 0; i < 5; i++ {
		b := c.User.Create().SaveX(ctx)
		ch := c.Chat.Create().SetType("private").SaveX(ctx)
		participant := a
		if i == 4 {
			participant = outsider
		}
		c.PrivateChat.Create().SetChatID(ch.ID).SetUser1ID(participant.ID).SetUser2ID(b.ID).SaveX(ctx)
		q := c.ChatNotificationPreference.Create().SetUserID(a.ID).SetChatID(ch.ID).SetMutedForever(true)
		if i == 3 {
			q.SetMutedForever(false).SetMutedUntil(time.Now().UTC().Add(-time.Hour))
		}
		q.SaveX(ctx)
	}
	first, cursor, more, err := s.MutedChats(ctx, a.ID, "", 2)
	require.NoError(t, err)
	require.Len(t, first, 2)
	require.True(t, more)
	require.NotEmpty(t, cursor)
	second, next, more, err := s.MutedChats(ctx, a.ID, cursor, 2)
	require.NoError(t, err)
	require.Len(t, second, 1)
	require.False(t, more)
	require.Empty(t, next)
	require.NotEqual(t, first[0].ChatID, second[0].ChatID)
	require.NotEqual(t, first[1].ChatID, second[0].ChatID)
	_, _, _, err = s.MutedChats(ctx, a.ID, "invalid", 20)
	require.Error(t, err)
	_, _, _, err = s.MutedChats(ctx, a.ID, "", 51)
	require.Error(t, err)
}

func TestMutedGroupVisibilityTracksMembershipAndDeletion(t *testing.T) {
	ctx := context.Background()
	c := enttest.Open(t, dialect.SQLite, "file:muted-group-visibility?mode=memory&cache=shared&_fk=1")
	defer c.Close()
	owner := c.User.Create().SaveX(ctx)
	ch := c.Chat.Create().SetType("group").SaveX(ctx)
	group := c.GroupChat.Create().SetChatID(ch.ID).SetName("Project team").SetInviteCode("test-group-invite").SaveX(ctx)
	member := c.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole("owner").SaveX(ctx)
	s := NewService(c, &config.AppConfig{})
	require.NoError(t, s.SetChatMute(ctx, owner.ID, ch.ID, MuteRequest{MutedForever: true}))
	rows, _, _, err := s.MutedChats(ctx, owner.ID, "", 20)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "Project team", rows[0].Name)
	c.Chat.UpdateOne(ch).SetDeletedAt(time.Now().UTC()).SaveX(ctx)
	rows, _, _, err = s.MutedChats(ctx, owner.ID, "", 20)
	require.NoError(t, err)
	require.Empty(t, rows)
	c.Chat.UpdateOne(ch).ClearDeletedAt().SaveX(ctx)
	require.NoError(t, c.GroupMember.DeleteOne(member).Exec(ctx))
	rows, _, _, err = s.MutedChats(ctx, owner.ID, "", 20)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Error(t, s.SetChatMute(ctx, owner.ID, ch.ID, MuteRequest{}))
}
