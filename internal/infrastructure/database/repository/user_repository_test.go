package repository

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
)

func TestUserRepositorySearchUsersFiltersBlockedDeletedBannedAndGroupMembers(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:user-repository-search?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	current := client.User.Create().SetUsername("current").SetFullName("Current").SaveX(ctx)
	allowed := client.User.Create().SetUsername("allowed").SetFullName("Allowed").SaveX(ctx)
	blocked := client.User.Create().SetUsername("blocked").SetFullName("Blocked").SaveX(ctx)
	blockedBy := client.User.Create().SetUsername("blocked-by").SetFullName("Blocked By").SaveX(ctx)
	client.User.Create().SetUsername("deleted").SetFullName("Deleted").SetDeletedAt(time.Now().UTC()).SaveX(ctx)
	client.User.Create().SetUsername("active-ban").SetFullName("Active Ban").SetIsBanned(true).SetBannedUntil(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
	expiredBan := client.User.Create().SetUsername("expired-ban").SetFullName("Expired Ban").SetIsBanned(true).SetBannedUntil(time.Now().UTC().Add(-time.Hour)).SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(current.ID).SetBlockedID(blocked.ID).SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(blockedBy.ID).SetBlockedID(current.ID).SaveX(ctx)

	groupChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(groupChat.ID).SetName("excluded").SetInviteCode(uuid.NewString()).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(allowed.ID).SaveX(ctx)

	repo := NewUserRepository(client)
	users, nextCursor, hasNext, err := repo.SearchUsers(ctx, current.ID, "", "", 10, &groupChat.ID)
	if err != nil {
		t.Fatalf("search users: %v", err)
	}
	if hasNext || nextCursor != "" {
		t.Fatalf("did not expect pagination cursor: cursor=%q hasNext=%t", nextCursor, hasNext)
	}
	if len(users) != 1 || users[0].ID != expiredBan.ID {
		t.Fatalf("expected only expired-ban user, got %v", users)
	}
}

func TestUserRepositorySearchUsersPaginatesAndMatchesQuery(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:user-repository-search-page?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	current := client.User.Create().SetUsername("current").SetFullName("Current").SaveX(ctx)
	first := client.User.Create().SetUsername("alpha").SetFullName("Alpha").SaveX(ctx)
	second := client.User.Create().SetUsername("alpine").SetFullName("Alpine").SaveX(ctx)
	client.User.Create().SetUsername("beta").SetFullName("Beta").SaveX(ctx)

	repo := NewUserRepository(client)
	users, nextCursor, hasNext, err := repo.SearchUsers(ctx, current.ID, "al", "", 1, nil)
	if err != nil {
		t.Fatalf("search first page: %v", err)
	}
	if len(users) != 1 || users[0].ID != first.ID || !hasNext || nextCursor == "" {
		t.Fatalf("unexpected first page: users=%v cursor=%q hasNext=%t", users, nextCursor, hasNext)
	}

	nextUsers, nextNextCursor, hasNext, err := repo.SearchUsers(ctx, current.ID, "al", nextCursor, 1, nil)
	if err != nil {
		t.Fatalf("search second page: %v", err)
	}
	if len(nextUsers) != 1 || nextUsers[0].ID != second.ID || hasNext || nextNextCursor != "" {
		t.Fatalf("unexpected second page: users=%v cursor=%q hasNext=%t", nextUsers, nextNextCursor, hasNext)
	}
}

func TestUserRepositorySearchUsersRejectsInvalidCursor(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:user-repository-invalid-cursor?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()

	repo := NewUserRepository(client)
	_, _, _, err := repo.SearchUsers(context.Background(), uuid.New(), "", "invalid", 10, nil)
	if err == nil {
		t.Fatal("expected invalid user cursor error")
	}
}

func TestUserRepositoryGetBlockedUsersFiltersAndPaginates(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:user-repository-blocked?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	current := client.User.Create().SetUsername("current").SetFullName("Current").SaveX(ctx)
	first := client.User.Create().SetUsername("blocked-alpha").SetFullName("Alpha").SaveX(ctx)
	second := client.User.Create().SetUsername("blocked-beta").SetFullName("Beta").SaveX(ctx)
	third := client.User.Create().SetUsername("blocked-gamma").SetFullName("Gamma").SaveX(ctx)
	deleted := client.User.Create().SetUsername("blocked-deleted").SetFullName("Deleted").SetDeletedAt(time.Now().UTC()).SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(current.ID).SetBlockedID(first.ID).SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(current.ID).SetBlockedID(second.ID).SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(current.ID).SetBlockedID(third.ID).SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(current.ID).SetBlockedID(deleted.ID).SaveX(ctx)

	repo := NewUserRepository(client)
	users, nextCursor, hasNext, err := repo.GetBlockedUsers(ctx, current.ID, "blocked", "", 2)
	if err != nil {
		t.Fatalf("get blocked users: %v", err)
	}
	if len(users) != 2 || users[0].ID != first.ID || users[1].ID != second.ID || !hasNext || nextCursor == "" {
		t.Fatalf("unexpected blocked first page: users=%v cursor=%q hasNext=%t", users, nextCursor, hasNext)
	}

	nextUsers, nextNextCursor, hasNext, err := repo.GetBlockedUsers(ctx, current.ID, "blocked", nextCursor, 2)
	if err != nil {
		t.Fatalf("get next blocked users: %v", err)
	}
	if len(nextUsers) != 1 || nextUsers[0].ID != third.ID || hasNext || nextNextCursor != "" {
		t.Fatalf("unexpected blocked second page: users=%v cursor=%q hasNext=%t", nextUsers, nextNextCursor, hasNext)
	}
}

func TestUserRepositoryGetBlockedUsersRejectsInvalidCursor(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:user-repository-invalid-blocked-cursor?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()

	repo := NewUserRepository(client)
	_, _, _, err := repo.GetBlockedUsers(context.Background(), uuid.New(), "", "invalid", 10)
	if err == nil {
		t.Fatal("expected invalid blocked-user cursor error")
	}
}
