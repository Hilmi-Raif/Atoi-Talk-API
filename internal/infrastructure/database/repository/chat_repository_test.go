package repository

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/media"
	"context"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

func TestChatRepositoryGetChatsLoadsPrivateParticipantsInOneBatch(t *testing.T) {
	var userQueryCount atomic.Int32
	client := enttest.Open(t, dialect.SQLite, "file:chat-repository-private-user-batch?mode=memory&cache=shared&_fk=1", enttest.WithOptions(
		ent.Debug(),
		ent.Log(func(values ...any) {
			if len(values) > 0 && strings.Contains(fmt.Sprint(values...), "SELECT `users`") {
				userQueryCount.Add(1)
			}
		}),
	))
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	currentUser := client.User.Create().SetUsername("current-batch").SaveX(ctx)
	otherUser := client.User.Create().SetUsername("other-batch").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SetLastMessageAt(time.Now()).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(currentUser.ID).SetUser2ID(otherUser.ID).SaveX(ctx)

	queryCountBefore := userQueryCount.Load()
	chats, _, _, err := NewChatRepository(client).GetChats(ctx, currentUser.ID, "", "", 20)

	if err != nil {
		t.Fatalf("get chats: %v", err)
	}
	if len(chats) != 1 || chats[0].Edges.PrivateChat == nil {
		t.Fatalf("expected one private chat, got %#v", chats)
	}
	if chats[0].Edges.PrivateChat.Edges.User1 != nil && chats[0].Edges.PrivateChat.Edges.User2 != nil {
		t.Fatal("expected only the other participant to be loaded")
	}
	if chats[0].Edges.PrivateChat.Edges.User1 == nil && chats[0].Edges.PrivateChat.Edges.User2 == nil {
		t.Fatal("expected the other participant to be loaded")
	}
	if got := userQueryCount.Load() - queryCountBefore; got != 1 {
		t.Fatalf("expected one batched participant query, got %d", got)
	}
}

func TestChatRepositoryGetChatsLoadsAvatarsInOneBatch(t *testing.T) {
	var mediaQueryCount atomic.Int32
	client := enttest.Open(t, dialect.SQLite, "file:chat-repository-avatar-batch?mode=memory&cache=shared&_fk=1", enttest.WithOptions(
		ent.Debug(),
		ent.Log(func(values ...any) {
			if len(values) > 0 && strings.Contains(fmt.Sprint(values...), "SELECT `media`") {
				mediaQueryCount.Add(1)
			}
		}),
	))
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	currentUser := client.User.Create().SetUsername("avatar-current").SaveX(ctx)
	privateAvatar := client.Media.Create().
		SetFileName("avatars/private.png").
		SetOriginalName("private.png").
		SetFileSize(10).
		SetMimeType("image/png").
		SetCategory(media.CategoryUserAvatar).
		SaveX(ctx)
	otherUser := client.User.Create().SetUsername("avatar-other").SetAvatarID(privateAvatar.ID).SaveX(ctx)
	privateChat := client.Chat.Create().SetType(chat.TypePrivate).SetLastMessageAt(time.Now()).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(privateChat.ID).SetUser1ID(currentUser.ID).SetUser2ID(otherUser.ID).SaveX(ctx)

	groupAvatar := client.Media.Create().
		SetFileName("avatars/group.png").
		SetOriginalName("group.png").
		SetFileSize(10).
		SetMimeType("image/png").
		SetCategory(media.CategoryGroupAvatar).
		SaveX(ctx)
	senderAvatar := client.Media.Create().
		SetFileName("avatars/sender.png").
		SetOriginalName("sender.png").
		SetFileSize(10).
		SetMimeType("image/png").
		SetCategory(media.CategoryUserAvatar).
		SaveX(ctx)
	sender := client.User.Create().SetUsername("avatar-sender").SetAvatarID(senderAvatar.ID).SaveX(ctx)
	groupChat := client.Chat.Create().SetType(chat.TypeGroup).SetLastMessageAt(time.Now().Add(time.Second)).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(groupChat.ID).SetName("Avatar Group").SetInviteCode("avatar-group").SetAvatarID(groupAvatar.ID).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(currentUser.ID).SaveX(ctx)
	lastMessage := client.Message.Create().SetChatID(groupChat.ID).SetSenderID(sender.ID).SaveX(ctx)
	client.Chat.UpdateOne(groupChat).SetLastMessageID(lastMessage.ID).SaveX(ctx)

	queryCountBefore := mediaQueryCount.Load()
	chats, _, _, err := NewChatRepository(client).GetChats(ctx, currentUser.ID, "", "", 20)
	if err != nil {
		t.Fatalf("get chats: %v", err)
	}
	if got := mediaQueryCount.Load() - queryCountBefore; got != 2 {
		t.Fatalf("expected one avatar query plus one attachment query, got %d media queries", got)
	}

	var gotPrivateAvatar, gotGroupAvatar, gotSenderAvatar bool
	for _, chatEntity := range chats {
		if chatEntity.Edges.PrivateChat != nil && chatEntity.Edges.PrivateChat.Edges.User2 != nil {
			avatar := chatEntity.Edges.PrivateChat.Edges.User2.Edges.Avatar
			gotPrivateAvatar = avatar != nil && avatar.ID == privateAvatar.ID
		}
		if chatEntity.Edges.GroupChat != nil {
			avatar := chatEntity.Edges.GroupChat.Edges.Avatar
			gotGroupAvatar = avatar != nil && avatar.ID == groupAvatar.ID
		}
		if chatEntity.Edges.LastMessage != nil && chatEntity.Edges.LastMessage.Edges.Sender != nil {
			avatar := chatEntity.Edges.LastMessage.Edges.Sender.Edges.Avatar
			gotSenderAvatar = avatar != nil && avatar.ID == senderAvatar.ID
		}
	}
	if !gotPrivateAvatar || !gotGroupAvatar || !gotSenderAvatar {
		t.Fatalf("expected all avatars to be populated, private=%t group=%t sender=%t", gotPrivateAvatar, gotGroupAvatar, gotSenderAvatar)
	}
}

func TestChatRepositoryGetChatsUsesSevenQueriesForMixedPage(t *testing.T) {
	var selectCount atomic.Int32
	client := enttest.Open(t, dialect.SQLite, "file:chat-repository-query-budget?mode=memory&cache=shared&_fk=1", enttest.WithOptions(
		ent.Debug(),
		ent.Log(func(values ...any) {
			if len(values) > 0 && strings.Contains(strings.ToUpper(fmt.Sprint(values...)), "SELECT ") {
				selectCount.Add(1)
			}
		}),
	))
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	currentUser := client.User.Create().SetUsername("budget-current").SaveX(ctx)
	otherUser := client.User.Create().SetUsername("budget-other").SaveX(ctx)
	privateChat := client.Chat.Create().SetType(chat.TypePrivate).SetLastMessageAt(time.Now()).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(privateChat.ID).SetUser1ID(currentUser.ID).SetUser2ID(otherUser.ID).SaveX(ctx)
	groupChat := client.Chat.Create().SetType(chat.TypeGroup).SetLastMessageAt(time.Now().Add(time.Second)).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(groupChat.ID).SetName("Budget Group").SetInviteCode("budget-group").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(currentUser.ID).SaveX(ctx)
	sender := client.User.Create().SetUsername("budget-sender").SaveX(ctx)
	lastMessage := client.Message.Create().SetChatID(groupChat.ID).SetSenderID(sender.ID).SaveX(ctx)
	client.Chat.UpdateOne(groupChat).SetLastMessageID(lastMessage.ID).SaveX(ctx)

	queryCountBefore := selectCount.Load()
	chats, _, _, err := NewChatRepository(client).GetChats(ctx, currentUser.ID, "", "", 20)
	if err != nil {
		t.Fatalf("get chats: %v", err)
	}
	if len(chats) != 2 {
		t.Fatalf("expected mixed private/group page, got %d chats", len(chats))
	}
	if got := selectCount.Load() - queryCountBefore; got != 7 {
		t.Fatalf("expected seven SELECT queries for mixed page, got %d", got)
	}
}

func TestChatRepositoryGetChatsSkipsPrivateRelationForGroupOnlyPage(t *testing.T) {
	var privateQueryCount atomic.Int32
	client := enttest.Open(t, dialect.SQLite, "file:chat-repository-group-only-query-budget?mode=memory&cache=shared&_fk=1", enttest.WithOptions(
		ent.Debug(),
		ent.Log(func(values ...any) {
			statement := strings.ToUpper(fmt.Sprint(values...))
			if strings.Contains(statement, "SELECT `PRIVATE_CHATS`") && strings.Contains(statement, "WHERE `PRIVATE_CHATS`.`CHAT_ID` IN") {
				privateQueryCount.Add(1)
			}
		}),
	))
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	currentUser := client.User.Create().SetUsername("group-only-current").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SetLastMessageAt(time.Now()).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Group Only").SetInviteCode("group-only").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(currentUser.ID).SaveX(ctx)

	chats, _, _, err := NewChatRepository(client).GetChats(ctx, currentUser.ID, "", "", 20)
	if err != nil {
		t.Fatalf("get chats: %v", err)
	}
	if len(chats) != 1 || chats[0].Edges.GroupChat == nil || chats[0].Edges.GroupChat.Edges.Members == nil {
		t.Fatalf("expected group chat with current member, got %#v", chats)
	}
	if got := privateQueryCount.Load(); got != 0 {
		t.Fatalf("expected no private relation query for group-only page, got %d", got)
	}
}

func TestChatRepositoryGetChatsUsesSixQueriesForCompleteGroupPage(t *testing.T) {
	var selectCount atomic.Int32
	client := enttest.Open(t, dialect.SQLite, "file:chat-repository-group-only-complete-budget?mode=memory&cache=shared&_fk=1", enttest.WithOptions(
		ent.Debug(),
		ent.Log(func(values ...any) {
			if len(values) > 0 && strings.Contains(strings.ToUpper(fmt.Sprint(values...)), "SELECT ") {
				selectCount.Add(1)
			}
		}),
	))
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	currentUser := client.User.Create().SetUsername("complete-current").SaveX(ctx)
	groupChat := client.Chat.Create().SetType(chat.TypeGroup).SetLastMessageAt(time.Now()).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(groupChat.ID).SetName("Complete Group").SetInviteCode("complete-group").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(currentUser.ID).SaveX(ctx)
	sender := client.User.Create().SetUsername("complete-sender").SaveX(ctx)
	lastMessage := client.Message.Create().SetChatID(groupChat.ID).SetSenderID(sender.ID).SaveX(ctx)
	client.Chat.UpdateOne(groupChat).SetLastMessageID(lastMessage.ID).SaveX(ctx)

	queryCountBefore := selectCount.Load()
	chats, _, _, err := NewChatRepository(client).GetChats(ctx, currentUser.ID, "", "", 20)
	if err != nil {
		t.Fatalf("get chats: %v", err)
	}
	if len(chats) != 1 {
		t.Fatalf("expected one group chat, got %d", len(chats))
	}
	if got := selectCount.Load() - queryCountBefore; got != 6 {
		t.Fatalf("expected six SELECT queries for complete group page, got %d", got)
	}
}

func TestChatRepositoryGetChatsProjectsListRelations(t *testing.T) {
	var statements []string
	client := enttest.Open(t, dialect.SQLite, "file:chat-repository-list-projection?mode=memory&cache=shared&_fk=1", enttest.WithOptions(
		ent.Debug(),
		ent.Log(func(values ...any) {
			statement := fmt.Sprint(values...)
			if strings.Contains(strings.ToUpper(statement), "SELECT ") {
				statements = append(statements, statement)
			}
		}),
	))
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	currentUser := client.User.Create().SetUsername("projection-current").SaveX(ctx)
	groupChat := client.Chat.Create().SetType(chat.TypeGroup).SetLastMessageAt(time.Now()).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(groupChat.ID).SetName("Projection Group").SetInviteCode("projection-group").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(currentUser.ID).SetUnreadCount(3).SaveX(ctx)
	sender := client.User.Create().SetUsername("projection-sender").SetFullName("Projection Sender").SaveX(ctx)
	lastMessage := client.Message.Create().SetChatID(groupChat.ID).SetSenderID(sender.ID).SetContent("latest").SaveX(ctx)
	client.Chat.UpdateOne(groupChat).SetLastMessageID(lastMessage.ID).SaveX(ctx)

	statements = nil
	chats, _, _, err := NewChatRepository(client).GetChats(ctx, currentUser.ID, "", "", 20)
	if err != nil {
		t.Fatalf("get chats: %v", err)
	}
	if len(chats) != 1 || chats[0].Edges.GroupChat.Name != "Projection Group" || chats[0].Edges.GroupChat.Edges.Members[0].UnreadCount != 3 {
		t.Fatalf("expected projected list fields, got %#v", chats)
	}

	for _, statement := range statements {
		upper := strings.ToUpper(statement)
		if strings.Contains(upper, "FROM `CHATS`") && strings.Contains(upper, "`UPDATED_AT`") {
			t.Fatalf("chat list query selected unused updated_at: %s", statement)
		}
		if strings.Contains(upper, "FROM `GROUP_CHATS`") && strings.Contains(upper, "`UPDATED_AT`") {
			t.Fatalf("group chat list query selected unused updated_at: %s", statement)
		}
		if strings.Contains(upper, "FROM `GROUP_MEMBERS`") && strings.Contains(upper, "`JOINED_AT`") {
			t.Fatalf("group member list query selected unused joined_at: %s", statement)
		}
		if strings.Contains(upper, "FROM `MESSAGES`") && strings.Contains(upper, "`UPDATED_AT`") {
			t.Fatalf("message list query selected unused updated_at: %s", statement)
		}
	}
}

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
	if chats[0].Edges.PrivateChat.Edges.User1 != nil {
		t.Fatal("expected current user edge to be omitted from the private chat list")
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

func TestChatRepositoryGetChatsAppliesPrivateVisibilityWithoutNegativeSubquery(t *testing.T) {
	var statements []string
	client := enttest.Open(t, dialect.SQLite, "file:chat-repository-private-visibility?mode=memory&cache=shared&_fk=1", enttest.WithOptions(
		ent.Debug(),
		ent.Log(func(values ...any) {
			statements = append(statements, fmt.Sprint(values...))
		}),
	))
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	currentUser := client.User.Create().SetUsername("visibility-current").SaveX(ctx)
	otherUser := client.User.Create().SetUsername("visibility-other").SaveX(ctx)
	lastMessageAt := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	visibleChat := client.Chat.Create().SetType(chat.TypePrivate).SetLastMessageAt(lastMessageAt).SaveX(ctx)
	client.PrivateChat.Create().
		SetChatID(visibleChat.ID).
		SetUser1ID(currentUser.ID).
		SetUser2ID(otherUser.ID).
		SetUser1HiddenAt(lastMessageAt.Add(-time.Second)).
		SaveX(ctx)
	hiddenChat := client.Chat.Create().SetType(chat.TypePrivate).SetLastMessageAt(lastMessageAt).SaveX(ctx)
	client.PrivateChat.Create().
		SetChatID(hiddenChat.ID).
		SetUser1ID(currentUser.ID).
		SetUser2ID(client.User.Create().SetUsername("visibility-hidden-other").SaveX(ctx).ID).
		SetUser1HiddenAt(lastMessageAt).
		SaveX(ctx)

	statements = nil
	chats, _, _, err := NewChatRepository(client).GetChats(ctx, currentUser.ID, "", "", 20)
	if err != nil {
		t.Fatalf("get chats: %v", err)
	}
	if len(chats) != 1 || chats[0].ID != visibleChat.ID {
		t.Fatalf("expected only visible private chat, got %#v", chats)
	}
	for _, statement := range statements {
		if strings.Contains(strings.ToUpper(statement), "NOT EXISTS") {
			t.Fatalf("inbox membership query must use positive indexable branches, got %s", statement)
		}
	}
}

func TestChatRepositoryGetChatsDiscoversCandidatesFromMembershipRelations(t *testing.T) {
	var statement string
	client := enttest.Open(t, dialect.SQLite, "file:chat-repository-visibility-type?mode=memory&cache=shared&_fk=1", enttest.WithOptions(
		ent.Debug(),
		ent.Log(func(values ...any) {
			candidate := fmt.Sprint(values...)
			if strings.Contains(strings.ToUpper(candidate), "FROM `CHATS`") {
				statement = candidate
			}
		}),
	))
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	currentUser := client.User.Create().SetUsername("visibility-type-current").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SetLastMessageAt(time.Now()).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Visibility Type Group").SetInviteCode("visibility-type-group").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(currentUser.ID).SaveX(ctx)

	if _, _, _, err := NewChatRepository(client).GetChats(ctx, currentUser.ID, "", "", 20); err != nil {
		t.Fatalf("get chats: %v", err)
	}
	upper := strings.ToUpper(statement)
	if !strings.Contains(upper, " UNION ") {
		t.Fatalf("expected membership-first UNION candidate query, got %s", statement)
	}
	if strings.Contains(upper, "EXISTS (SELECT") {
		t.Fatalf("expected no correlated EXISTS candidate branches, got %s", statement)
	}
	if !strings.Contains(upper, "FROM `PRIVATE_CHATS`") || !strings.Contains(upper, "FROM `GROUP_MEMBERS`") {
		t.Fatalf("expected candidates from private chats and group memberships, got %s", statement)
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
