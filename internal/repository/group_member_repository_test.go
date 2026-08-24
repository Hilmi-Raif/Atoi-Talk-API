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

func TestGroupMemberRepositoryCountActiveMembersReturnsActiveUsersOnly(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:group-member-repository?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	activeUser := client.User.Create().SetUsername("active").SaveX(ctx)
	deletedUser := client.User.Create().SetUsername("deleted").SaveX(ctx)
	deletedUser.Update().SetDeletedAt(time.Now().UTC()).SaveX(ctx)
	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("group").
		SetInviteCode("invite").
		SetIsPublic(true).
		SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(activeUser.ID).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(deletedUser.ID).SaveX(ctx)

	repo := NewGroupMemberRepository(client)
	counts, err := repo.CountActiveMembersByGroupIDs(ctx, group.ID)
	if err != nil {
		t.Fatalf("count active members: %v", err)
	}
	if counts[group.ID] != 1 {
		t.Fatalf("expected one active member, got %v", counts)
	}
}

func TestGroupMemberRepositoryCountActiveMembersWithNoGroupsAvoidsDatabase(t *testing.T) {
	repo := NewGroupMemberRepository(nil)

	counts, err := repo.CountActiveMembersByGroupIDs(context.Background())
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if len(counts) != 0 {
		t.Fatalf("expected empty counts, got %v", counts)
	}
}

func TestGroupMemberRepositorySearchRejectsInvalidCursorBeforeQuery(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:group-member-cursor?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	repo := NewGroupMemberRepository(client)

	for _, invalid := range []string{
		"invalid",
		"2026-01-01T00:00:00Z|not-a-uuid",
		"invalid-time|00000000-0000-0000-0000-000000000001",
	} {
		_, _, _, err := repo.SearchGroupMembers(context.Background(), uuid.New(), "", invalid, 10)
		if err == nil {
			t.Fatalf("expected error for cursor %q", invalid)
		}
	}
}

func TestGroupMemberRepositorySearchFiltersQuery(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:group-member-query?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("group").
		SetInviteCode("invite").
		SaveX(ctx)
	user := client.User.Create().SetUsername("charlie").SetFullName("Charlie Brown").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(user.ID).SaveX(ctx)

	repo := NewGroupMemberRepository(client)
	members, _, _, err := repo.SearchGroupMembers(ctx, group.ID, "char", "", 10)
	if err != nil {
		t.Fatalf("search with matching query: %v", err)
	}
	if len(members) != 1 {
		t.Fatalf("expected 1 match, got %d", len(members))
	}

	noMembers, _, _, err := repo.SearchGroupMembers(ctx, group.ID, "xyz", "", 10)
	if err != nil {
		t.Fatalf("search with non-matching query: %v", err)
	}
	if len(noMembers) != 0 {
		t.Fatalf("expected 0 matches, got %d", len(noMembers))
	}
}

func TestGroupMemberRepositorySearchPaginatesMembers(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:group-member-search?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("group").
		SetInviteCode("invite").
		SaveX(ctx)
	firstUser := client.User.Create().SetUsername("alpha").SetFullName("Alpha").SaveX(ctx)
	secondUser := client.User.Create().SetUsername("beta").SetFullName("Beta").SaveX(ctx)
	firstJoinedAt := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	secondJoinedAt := firstJoinedAt.Add(time.Minute)
	client.GroupMember.Create().
		SetGroupChatID(group.ID).
		SetUserID(firstUser.ID).
		SetJoinedAt(firstJoinedAt).
		SaveX(ctx)
	client.GroupMember.Create().
		SetGroupChatID(group.ID).
		SetUserID(secondUser.ID).
		SetJoinedAt(secondJoinedAt).
		SaveX(ctx)

	repo := NewGroupMemberRepository(client)
	members, nextCursor, hasNext, err := repo.SearchGroupMembers(ctx, group.ID, "", "", 1)
	if err != nil {
		t.Fatalf("search group members: %v", err)
	}
	if len(members) != 1 || !hasNext || nextCursor == "" {
		t.Fatalf("expected one member and next cursor, got len=%d cursor=%q hasNext=%t", len(members), nextCursor, hasNext)
	}
	if members[0].Edges.User == nil || members[0].Edges.User.ID != firstUser.ID {
		t.Fatalf("expected first user edge, got %+v", members[0].Edges.User)
	}

	nextMembers, nextNextCursor, hasNext, err := repo.SearchGroupMembers(ctx, group.ID, "", nextCursor, 1)
	if err != nil {
		t.Fatalf("search next page: %v", err)
	}
	if len(nextMembers) != 1 || hasNext || nextNextCursor != "" || nextMembers[0].Edges.User.ID != secondUser.ID {
		t.Fatalf("unexpected second page: len=%d cursor=%q hasNext=%t user=%v", len(nextMembers), nextNextCursor, hasNext, nextMembers[0].Edges.User)
	}
}
