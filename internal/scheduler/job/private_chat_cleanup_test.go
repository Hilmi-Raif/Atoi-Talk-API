package job

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/internal/config"
	"context"
	"errors"
	"testing"

	"entgo.io/ent/dialect"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestRunPrivateChatCleanupDeletesAbandonedChats(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:private-chat-cleanup-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	abandonedChat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(abandonedChat.ID).SaveX(ctx)
	keptChat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(keptChat.ID).SetUser1ID(client.User.Create().SetUsername("user").SaveX(ctx).ID).SaveX(ctx)

	err := RunPrivateChatCleanup(ctx, client, &config.AppConfig{})

	require.NoError(t, err)
	_, err = client.Chat.Get(ctx, abandonedChat.ID)
	require.Error(t, err)
	_, err = client.Chat.Get(ctx, keptChat.ID)
	require.NoError(t, err)
	count, err := client.PrivateChat.Query().Where(privatechat.ChatID(keptChat.ID)).Count(ctx)
	require.NoError(t, err)
	require.Equal(t, 1, count)
}

func TestRunPrivateChatCleanupDoesNothingWithoutAbandonedChats(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:private-chat-cleanup-empty-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	require.NoError(t, RunPrivateChatCleanup(context.Background(), client, &config.AppConfig{}))
}

func TestRunPrivateChatCleanupReturnsQueryErrorOnCancelledContext(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:private-chat-cleanup-cancel-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RunPrivateChatCleanup(ctx, client, &config.AppConfig{})
	require.Error(t, err)
}

func TestRunPrivateChatCleanupStopsWhenChatDeleteFails(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:private-chat-cleanup-fail-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()

	abandonedChat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(abandonedChat.ID).SaveX(ctx)

	client.Chat.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Op().Is(ent.OpDeleteOne) {
				return nil, errors.New("delete chat failure")
			}
			return next.Mutate(ctx, m)
		})
	})

	err := RunPrivateChatCleanup(ctx, client, &config.AppConfig{})
	require.NoError(t, err)
}
