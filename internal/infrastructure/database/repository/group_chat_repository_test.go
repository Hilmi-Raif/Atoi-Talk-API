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

func TestGroupChatRepositorySearchesPublicGroupsByNameWithCursor(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:group-chat-repository-name?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	createGroup := func(name, description string, public bool) {
		parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
		client.GroupChat.Create().
			SetChatID(parentChat.ID).
			SetName(name).
			SetDescription(description).
			SetInviteCode(uuid.NewString()).
			SetIsPublic(public).
			SaveX(ctx)
	}
	createGroup("Alpha group", "first description", true)
	createGroup("Beta group", "second description", true)
	createGroup("Private group", "private description", false)

	repo := NewGroupChatRepository(client)
	groups, nextCursor, hasNext, err := repo.SearchPublicGroups(ctx, "", "", 1, "name")
	if err != nil {
		t.Fatalf("search public groups: %v", err)
	}
	if len(groups) != 1 || !hasNext || nextCursor == "" || groups[0].Name != "Alpha group" {
		t.Fatalf("unexpected first page: len=%d cursor=%q hasNext=%t groups=%v", len(groups), nextCursor, hasNext, groups)
	}

	nextGroups, nextNextCursor, hasNext, err := repo.SearchPublicGroups(ctx, "second", nextCursor, 1, "name")
	if err != nil {
		t.Fatalf("search next public groups: %v", err)
	}
	if len(nextGroups) != 1 || hasNext || nextNextCursor != "" || nextGroups[0].Name != "Beta group" {
		t.Fatalf("unexpected second page: len=%d cursor=%q hasNext=%t groups=%v", len(nextGroups), nextNextCursor, hasNext, nextGroups)
	}
}

func TestGroupChatRepositorySearchByNameRejectsInvalidCursor(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:group-chat-repository-invalid-name-cursor?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()

	repo := NewGroupChatRepository(client)
	_, _, _, err := repo.SearchPublicGroups(context.Background(), "", "invalid", 10, "name")
	if err == nil {
		t.Fatal("expected invalid name cursor error")
	}
}

func TestGroupChatRepositorySearchesPublicGroupsByMemberCount(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:group-chat-repository-count?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	activeUsers := make([]*ent.User, 0, 3)
	for i := 0; i < 3; i++ {
		activeUsers = append(activeUsers, client.User.Create().SetUsername("active-"+uuid.NewString()).SaveX(ctx))
	}
	deletedUser := client.User.Create().SetUsername("deleted").SaveX(ctx)
	deletedUser.Update().SetDeletedAt(time.Now().UTC()).SaveX(ctx)

	createGroup := func(name string) *ent.GroupChat {
		parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
		return client.GroupChat.Create().
			SetChatID(parentChat.ID).
			SetName(name).
			SetInviteCode(uuid.NewString()).
			SetIsPublic(true).
			SaveX(ctx)
	}
	mostMembers := createGroup("Most members")
	fewerMembers := createGroup("Fewer members")
	for _, member := range activeUsers {
		client.GroupMember.Create().SetGroupChatID(mostMembers.ID).SetUserID(member.ID).SaveX(ctx)
	}
	client.GroupMember.Create().SetGroupChatID(mostMembers.ID).SetUserID(deletedUser.ID).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(fewerMembers.ID).SetUserID(activeUsers[0].ID).SaveX(ctx)

	repo := NewGroupChatRepository(client)
	groups, nextCursor, hasNext, err := repo.SearchPublicGroups(ctx, "", "", 1, "member_count")
	if err != nil {
		t.Fatalf("search groups by member count: %v", err)
	}
	if len(groups) != 1 || !hasNext || nextCursor == "" || groups[0].ID != mostMembers.ID {
		t.Fatalf("unexpected member-count page: len=%d cursor=%q hasNext=%t groups=%v", len(groups), nextCursor, hasNext, groups)
	}

	nextGroups, nextNextCursor, hasNext, err := repo.SearchPublicGroups(ctx, "", nextCursor, 1, "member_count")
	if err != nil {
		t.Fatalf("search next groups by member count: %v", err)
	}
	if len(nextGroups) != 1 || hasNext || nextNextCursor != "" || nextGroups[0].ID != fewerMembers.ID {
		t.Fatalf("unexpected second member-count page: len=%d cursor=%q hasNext=%t groups=%v", len(nextGroups), nextNextCursor, hasNext, nextGroups)
	}
}

func TestGroupChatRepositorySearchByMemberCountRejectsInvalidCursor(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:group-chat-repository-invalid-count-cursor?mode=memory&cache=shared&_fk=1")
	defer func() { _ = client.Close() }()

	repo := NewGroupChatRepository(client)
	_, _, _, err := repo.SearchPublicGroups(context.Background(), "", "invalid", 10, "member_count")
	if err == nil {
		t.Fatal("expected invalid member-count cursor error")
	}
}
