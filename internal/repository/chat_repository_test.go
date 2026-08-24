package repository

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

func TestChatRepositoryGetChatByIDLoadsAuthorizedPrivateChat(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:chat-repository-detail?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	user1 := client.User.Create().SetUsername("user-one").SaveX(ctx)
	user2 := client.User.Create().SetUsername("user-two").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().
		SetChatID(chatEntity.ID).
		SetUser1ID(user1.ID).
		SetUser2ID(user2.ID).
		SaveX(ctx)

	repo := NewChatRepository(client)
	got, err := repo.GetChatByID(ctx, user1.ID, chatEntity.ID)
	if err != nil {
		t.Fatalf("get private chat: %v", err)
	}
	if got.ID != chatEntity.ID || got.Edges.PrivateChat == nil {
		t.Fatalf("unexpected chat result: id=%s private=%v", got.ID, got.Edges.PrivateChat)
	}
	if got.Edges.PrivateChat.Edges.User1 == nil || got.Edges.PrivateChat.Edges.User2 == nil {
		t.Fatal("expected both private chat users to be eager loaded")
	}
}

func TestChatRepositoryGetChatsPaginatesPrivateChats(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:chat-repository-list?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	currentUser := client.User.Create().SetUsername("current").SaveX(ctx)
	otherUser1 := client.User.Create().SetUsername("other-one").SaveX(ctx)
	otherUser2 := client.User.Create().SetUsername("other-two").SaveX(ctx)
	createPrivateChat := func(otherUserID uuid.UUID, lastMessageAt time.Time) {
		chatEntity := client.Chat.Create().
			SetType(chat.TypePrivate).
			SetLastMessageAt(lastMessageAt).
			SaveX(ctx)
		client.PrivateChat.Create().
			SetChatID(chatEntity.ID).
			SetUser1ID(currentUser.ID).
			SetUser2ID(otherUserID).
			SaveX(ctx)
	}
	firstTime := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	secondTime := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	createPrivateChat(otherUser1.ID, firstTime)
	createPrivateChat(otherUser2.ID, secondTime)

	repo := NewChatRepository(client)
	chats, nextCursor, hasNext, err := repo.GetChats(ctx, currentUser.ID, "", "", 1)
	if err != nil {
		t.Fatalf("get chats: %v", err)
	}
	if len(chats) != 1 || !hasNext || nextCursor == "" {
		t.Fatalf("expected first page with cursor, got len=%d cursor=%q hasNext=%t", len(chats), nextCursor, hasNext)
	}
	if chats[0].Edges.PrivateChat == nil || chats[0].Edges.PrivateChat.Edges.User2 == nil {
		t.Fatal("expected private chat and other user to be eager loaded")
	}

	nextChats, nextNextCursor, hasNext, err := repo.GetChats(ctx, currentUser.ID, "", nextCursor, 1)
	if err != nil {
		t.Fatalf("get next chats: %v", err)
	}
	if len(nextChats) != 1 || hasNext || nextNextCursor != "" {
		t.Fatalf("unexpected second page: len=%d cursor=%q hasNext=%t", len(nextChats), nextNextCursor, hasNext)
	}
	if nextChats[0].ID == chats[0].ID {
		t.Fatal("expected cursor to advance to a different chat")
	}
}

func TestChatRepositoryGetChatByIDRejectsUnauthorizedPrivateChat(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:chat-repository-auth?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	user1 := client.User.Create().SetUsername("user-one").SaveX(ctx)
	user2 := client.User.Create().SetUsername("user-two").SaveX(ctx)
	stranger := client.User.Create().SetUsername("stranger").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().
		SetChatID(chatEntity.ID).
		SetUser1ID(user1.ID).
		SetUser2ID(user2.ID).
		SaveX(ctx)

	repo := NewChatRepository(client)
	_, err := repo.GetChatByID(ctx, stranger.ID, chatEntity.ID)
	if err == nil {
		t.Fatal("expected unauthorized private chat lookup to fail")
	}
	if !ent.IsNotFound(err) {
		t.Fatalf("expected ent not found, got %v", err)
	}
}
