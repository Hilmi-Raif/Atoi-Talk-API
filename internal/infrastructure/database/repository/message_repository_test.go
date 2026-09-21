package repository

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/ent/message"
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestRunAroundQueriesExecutesPreviousAndNextSequentially(t *testing.T) {
	firstStarted := make(chan struct{}, 1)
	releaseFirst := make(chan struct{})
	secondStarted := make(chan struct{}, 1)
	var active atomic.Int32
	var maxActive atomic.Int32
	var calls atomic.Int32

	query := func(context.Context) ([]*ent.Message, error) {
		current := active.Add(1)
		for {
			previous := maxActive.Load()
			if current <= previous || maxActive.CompareAndSwap(previous, current) {
				break
			}
		}
		if calls.Add(1) == 1 {
			firstStarted <- struct{}{}
			<-releaseFirst
		} else {
			secondStarted <- struct{}{}
		}
		active.Add(-1)
		return nil, nil
	}

	done := make(chan error, 1)
	go func() {
		_, _, err := runAroundQueries(context.Background(), query, query)
		done <- err
	}()

	select {
	case <-firstStarted:
	case <-time.After(time.Second):
		t.Fatal("expected first around query to start")
	}
	select {
	case <-secondStarted:
		t.Fatal("second around query started before the first completed")
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFirst)
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("run around queries: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("sequential around queries did not finish")
	}
	if maxActive.Load() != 1 {
		t.Fatalf("expected around queries to use one connection at a time, max active=%d", maxActive.Load())
	}
}

func TestMessageRepositoryGetMessagesLoadsRelationsAndPaginates(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:message-repository-list?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	sender := client.User.Create().SetUsername("sender").SetFullName("Sender").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	firstID := uuid.MustParse("00000000-0000-7000-8000-000000000001")
	secondID := uuid.MustParse("00000000-0000-7000-8000-000000000002")
	thirdID := uuid.MustParse("00000000-0000-7000-8000-000000000003")
	firstTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(time.Minute)
	thirdTime := secondTime.Add(time.Minute)

	first := client.Message.Create().
		SetID(firstID).
		SetChatID(chatEntity.ID).
		SetSenderID(sender.ID).
		SetType(message.TypeRegular).
		SetContent("first").
		SetCreatedAt(firstTime).
		SaveX(ctx)
	client.Media.Create().
		SetFileName("first.jpg").
		SetOriginalName("first.jpg").
		SetFileSize(10).
		SetMimeType("image/jpeg").
		SetCategory(media.CategoryMessageAttachment).
		SetMessageID(first.ID).
		SaveX(ctx)
	client.Message.Create().
		SetID(secondID).
		SetChatID(chatEntity.ID).
		SetSenderID(sender.ID).
		SetType(message.TypeRegular).
		SetContent("second").
		SetReplyToID(first.ID).
		SetCreatedAt(secondTime).
		SaveX(ctx)
	client.Message.Create().
		SetID(thirdID).
		SetChatID(chatEntity.ID).
		SetSenderID(sender.ID).
		SetType(message.TypeRegular).
		SetContent("third").
		SetCreatedAt(thirdTime).
		SaveX(ctx)

	repo := NewMessageRepository(client)
	messages, err := repo.GetMessages(ctx, chatEntity.ID, nil, uuid.Nil, 2, "older")
	if err != nil {
		t.Fatalf("get messages: %v", err)
	}
	if len(messages) != 3 || messages[0].ID != thirdID || messages[1].ID != secondID {
		t.Fatalf("expected newest page plus one, got %v", messages)
	}
	if messages[0].Edges.Sender == nil || messages[0].Edges.Sender.ID != sender.ID {
		t.Fatal("expected sender to be eager loaded")
	}
	if len(messages[2].Edges.Attachments) != 1 {
		t.Fatalf("expected attachment to be eager loaded, got %d", len(messages[2].Edges.Attachments))
	}
	if messages[1].Edges.ReplyTo == nil || messages[1].Edges.ReplyTo.ID != firstID {
		t.Fatal("expected reply relation to be eager loaded")
	}

	newer, err := repo.GetMessages(ctx, chatEntity.ID, nil, firstID, 10, "newer")
	if err != nil {
		t.Fatalf("get newer messages: %v", err)
	}
	if len(newer) != 2 || newer[0].ID != secondID || newer[1].ID != thirdID {
		t.Fatalf("unexpected newer messages: %v", newer)
	}
}

func TestMessageRepositoryGetMessagesAppliesHiddenAt(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:message-repository-hidden?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	firstTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	secondTime := firstTime.Add(time.Minute)
	client.Message.Create().SetChatID(chatEntity.ID).SetContent("hidden").SetCreatedAt(firstTime).SaveX(ctx)
	visible := client.Message.Create().SetChatID(chatEntity.ID).SetContent("visible").SetCreatedAt(secondTime).SaveX(ctx)

	repo := NewMessageRepository(client)
	messages, err := repo.GetMessages(ctx, chatEntity.ID, &firstTime, uuid.Nil, 10, "older")
	if err != nil {
		t.Fatalf("get visible messages: %v", err)
	}
	if len(messages) != 1 || messages[0].ID != visible.ID {
		t.Fatalf("expected only message after hidden timestamp, got %v", messages)
	}
}

func TestMessageRepositoryGetMessagesDoesNotRevalidateChat(t *testing.T) {
	var statements []string
	client := enttest.Open(t, dialect.SQLite, "file:message-repository-no-chat-revalidation?mode=memory&cache=shared&_fk=1", enttest.WithOptions(
		ent.Debug(),
		ent.Log(func(values ...any) {
			statements = append(statements, fmt.Sprint(values...))
		}),
	))
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.Message.Create().SetChatID(chatEntity.ID).SetContent("visible").SaveX(ctx)

	statements = nil
	_, err := NewMessageRepository(client).GetMessages(ctx, chatEntity.ID, nil, uuid.Nil, 10, "older")
	require.NoError(t, err)
	for _, statement := range statements {
		upper := strings.ToUpper(statement)
		if strings.Contains(upper, "FROM `CHATS`") || strings.Contains(upper, "JOIN `CHATS`") {
			t.Fatalf("message page query must trust service access validation, got %s", statement)
		}
	}
}

func TestMessageRepositoryGetMessagesAroundReturnsOrderedWindow(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:message-repository-around?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	ctx := context.Background()
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	ids := []uuid.UUID{
		uuid.MustParse("00000000-0000-7000-8000-000000000011"),
		uuid.MustParse("00000000-0000-7000-8000-000000000012"),
		uuid.MustParse("00000000-0000-7000-8000-000000000013"),
		uuid.MustParse("00000000-0000-7000-8000-000000000014"),
		uuid.MustParse("00000000-0000-7000-8000-000000000015"),
	}
	for i, id := range ids {
		client.Message.Create().SetID(id).SetChatID(chatEntity.ID).SetContent("message").SetCreatedAt(time.Date(2026, 1, 1, 0, i, 0, 0, time.UTC)).SaveX(ctx)
	}

	repo := NewMessageRepository(client)
	messages, err := repo.GetMessagesAround(ctx, chatEntity.ID, nil, ids[2], 4)
	if err != nil {
		t.Fatalf("get messages around: %v", err)
	}
	if len(messages) != 5 {
		t.Fatalf("expected target plus two messages on each side, got %d", len(messages))
	}
	for i, messageEntity := range messages {
		if messageEntity.ID != ids[i] {
			t.Fatalf("unexpected message at index %d: got %s want %s", i, messageEntity.ID, ids[i])
		}
	}

	hiddenAt := time.Date(2026, 1, 1, 0, 2, 0, 0, time.UTC)
	if _, err := repo.GetMessagesAround(ctx, chatEntity.ID, &hiddenAt, ids[2], 4); err != ErrMessageNotFound {
		t.Fatalf("expected hidden target error, got %v", err)
	}
	if _, err := repo.GetMessagesAround(ctx, chatEntity.ID, nil, uuid.New(), 4); !ent.IsNotFound(err) {
		t.Fatalf("expected missing target error, got %v", err)
	}
}

func TestMessageRepositoryGetMessagesAroundBatchesRelationHydration(t *testing.T) {
	var senderQueries atomic.Int32
	client := enttest.Open(t, dialect.SQLite, "file:message-repository-around-relation-batch?mode=memory&cache=shared&_fk=1", enttest.WithOptions(
		ent.Debug(),
		ent.Log(func(values ...any) {
			statement := fmt.Sprint(values...)
			if strings.Contains(statement, "FROM `users`") {
				senderQueries.Add(1)
			}
		}),
	))
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	sender := client.User.Create().SetUsername("around-relation-sender").SetFullName("Around Relation Sender").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	ids := []uuid.UUID{
		uuid.MustParse("00000000-0000-7000-8000-000000000021"),
		uuid.MustParse("00000000-0000-7000-8000-000000000022"),
		uuid.MustParse("00000000-0000-7000-8000-000000000023"),
		uuid.MustParse("00000000-0000-7000-8000-000000000024"),
		uuid.MustParse("00000000-0000-7000-8000-000000000025"),
	}
	for _, id := range ids {
		client.Message.Create().
			SetID(id).
			SetChatID(chatEntity.ID).
			SetSenderID(sender.ID).
			SetContent("message").
			SetCreatedAt(time.Now().UTC()).
			SaveX(ctx)
	}

	senderQueries.Store(0)
	messages, err := NewMessageRepository(client).GetMessagesAround(ctx, chatEntity.ID, nil, ids[2], 4)
	require.NoError(t, err)
	require.Len(t, messages, 5)
	require.Equal(t, int32(1), senderQueries.Load(), "target and surrounding messages should hydrate sender relations in one batch")
}

func TestMessageRepositoryGetMessagesAroundReturnsAvailabilityMetadata(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:message-repository-around-pagination-metadata?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	ids := make([]uuid.UUID, 7)
	for i := range ids {
		ids[i] = uuid.MustParse(fmt.Sprintf("00000000-0000-7000-8000-0000000000%02d", i+31))
		client.Message.Create().
			SetID(ids[i]).
			SetChatID(chatEntity.ID).
			SetContent("message").
			SetCreatedAt(time.Now().UTC()).
			SaveX(ctx)
	}

	page, err := NewMessageRepository(client).GetMessagesAroundPage(ctx, chatEntity.ID, nil, ids[3], 4)
	require.NoError(t, err)
	require.Len(t, page.Messages, 5)
	require.True(t, page.HasOlder)
	require.True(t, page.HasNewer)
	require.Equal(t, ids[1], page.Messages[0].ID)
	require.Equal(t, ids[5], page.Messages[4].ID)
}

func TestMessageRepositoryGetMessagesAroundUsesThreeMessageQueries(t *testing.T) {
	var messageQueries atomic.Int32
	client := enttest.Open(t, dialect.SQLite, "file:message-repository-around-window-query?mode=memory&cache=shared&_fk=1", enttest.WithOptions(
		ent.Debug(),
		ent.Log(func(values ...any) {
			statement := strings.ToUpper(fmt.Sprint(values...))
			if strings.Contains(statement, "DRIVER.QUERY") && strings.Contains(statement, "FROM `MESSAGES`") {
				messageQueries.Add(1)
			}
		}),
	))
	defer func() { _ = client.Close() }()

	ctx := context.Background()
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	ids := make([]uuid.UUID, 7)
	for i := range ids {
		ids[i] = uuid.MustParse(fmt.Sprintf("00000000-0000-7000-8000-0000000001%02d", i+1))
		client.Message.Create().
			SetID(ids[i]).
			SetChatID(chatEntity.ID).
			SetContent("message").
			SetCreatedAt(time.Now().UTC()).
			SaveX(ctx)
	}

	messageQueries.Store(0)
	page, err := NewMessageRepository(client).GetMessagesAroundPage(ctx, chatEntity.ID, nil, ids[3], 4)
	require.NoError(t, err)
	require.Len(t, page.Messages, 5)
	require.Equal(t, int32(3), messageQueries.Load(), "around page should select target/previous together, next messages, then hydrate relations")
}
