package job

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/internal/infrastructure/config"
	"context"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestRunEntityCleanupDeletesExpiredUsersAndChats(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:entity-cleanup-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	cutoff := time.Now().UTC().Add(-31 * 24 * time.Hour)

	oldUser := client.User.Create().SetUsername("old-user").SetDeletedAt(cutoff).SaveX(ctx)
	activeUser := client.User.Create().SetUsername("active-user").SaveX(ctx)
	oldChat := client.Chat.Create().SetType(chat.TypePrivate).SetDeletedAt(cutoff).SaveX(ctx)
	activeChat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)

	err := RunEntityCleanup(ctx, client, &config.AppConfig{SoftDeleteRetentionDays: 30})

	require.NoError(t, err)
	_, err = client.User.Get(ctx, oldUser.ID)
	require.Error(t, err)
	_, err = client.Chat.Get(ctx, oldChat.ID)
	require.Error(t, err)
	_, err = client.User.Get(ctx, activeUser.ID)
	require.NoError(t, err)
	_, err = client.Chat.Get(ctx, activeChat.ID)
	require.NoError(t, err)
}

func TestRunEntityCleanupUsesDefaultRetentionForNegativeConfig(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:entity-cleanup-default-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	require.NoError(t, RunEntityCleanup(context.Background(), client, &config.AppConfig{SoftDeleteRetentionDays: -1}))
}

func TestRunEntityCleanupDeletesOnlyExpiredTerminalOutboxRows(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:entity-cleanup-outbox-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	old := time.Now().UTC().Add(-8 * 24 * time.Hour)
	recent := time.Now().UTC().Add(-6 * 24 * time.Hour)
	create := func() *ent.MessageOutboxCreate {
		return client.MessageOutbox.Create().
			SetEventType("message.new").
			SetMessageID(uuid.New()).
			SetChatID(uuid.New()).
			SetCreatedAt(old)
	}
	published := create().SetPublishedAt(old).SaveX(ctx)
	deadLetter := create().SetDeadLetteredAt(old).SaveX(ctx)
	recentPublished := create().SetCreatedAt(recent).SetPublishedAt(recent).SaveX(ctx)
	recentDeadLetter := create().SetDeadLetteredAt(recent).SaveX(ctx)
	pending := create().SaveX(ctx)

	err := RunEntityCleanup(ctx, client, &config.AppConfig{SoftDeleteRetentionDays: 30, MessageOutboxRetentionDays: 7})

	require.NoError(t, err)
	for _, id := range []uuid.UUID{published.ID, deadLetter.ID} {
		_, err = client.MessageOutbox.Get(ctx, id)
		require.True(t, ent.IsNotFound(err))
	}
	for _, id := range []uuid.UUID{recentPublished.ID, recentDeadLetter.ID, pending.ID} {
		_, err = client.MessageOutbox.Get(ctx, id)
		require.NoError(t, err)
	}
}

func TestRunEntityCleanupReturnsQueryErrorOnCancelledContext(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:entity-cleanup-cancel-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RunEntityCleanup(ctx, client, &config.AppConfig{SoftDeleteRetentionDays: 30})
	require.Error(t, err)
}

func TestRunEntityCleanupReturnsChatQueryError(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:entity-cleanup-chat-cancel-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond)

	err := RunEntityCleanup(ctx, client, &config.AppConfig{SoftDeleteRetentionDays: 30})
	require.Error(t, err)
}

func TestRunEntityCleanupStopsWhenUserDeleteFails(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:entity-cleanup-fail-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()
	ctx := context.Background()
	cutoff := time.Now().UTC().Add(-31 * 24 * time.Hour)

	client.User.Create().SetUsername("old-user-fail").SetDeletedAt(cutoff).SaveX(ctx)
	client.Chat.Create().SetType(chat.TypePrivate).SetDeletedAt(cutoff).SaveX(ctx)

	client.User.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Op().Is(ent.OpDeleteOne) {
				return nil, errors.New("delete user failure")
			}
			return next.Mutate(ctx, m)
		})
	})
	client.Chat.Use(func(next ent.Mutator) ent.Mutator {
		return ent.MutateFunc(func(ctx context.Context, m ent.Mutation) (ent.Value, error) {
			if m.Op().Is(ent.OpDeleteOne) {
				return nil, errors.New("delete chat failure")
			}
			return next.Mutate(ctx, m)
		})
	})

	err := RunEntityCleanup(ctx, client, &config.AppConfig{SoftDeleteRetentionDays: 30})
	require.NoError(t, err)
}
