package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/internal/model"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGroupChatServiceSearchGroupMembersMapsResultsForAdmin(t *testing.T) {
	service, client, groupMemberRepo, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("group").SetInviteCode("invite").SaveX(ctx)
	memberUser := client.User.Create().SetUsername("member").SetFullName("Member").SaveX(ctx)
	member := client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(memberUser.ID).SetRole(groupmember.RoleMember).SaveX(ctx)
	loadedMember := client.GroupMember.Query().Where(groupmember.ID(member.ID)).WithUser().OnlyX(ctx)

	groupMemberRepo.EXPECT().SearchGroupMembers(mock.Anything, groupEntity.ID, "mem", "", 10).Return([]*ent.GroupMember{loadedMember}, "next-cursor", true, nil)

	req := model.SearchGroupMembersRequest{
		GroupID: group.ID,
		Query:   "mem",
		Cursor:  "",
		Limit:   10,
	}
	resp, nextCursor, hasMore, err := service.SearchGroupMembers(ctx, memberUser.ID, req, false)
	require.NoError(t, err)
	require.Len(t, resp, 1)
	require.Equal(t, memberUser.ID, resp[0].UserID)
	require.Equal(t, "next-cursor", nextCursor)
	require.True(t, hasMore)
}

func TestGroupChatServiceSearchGroupMembersRequiresMembership(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	client.GroupChat.Create().SetChatID(group.ID).SetName("group").SetInviteCode("invite").SaveX(ctx)
	nonMember := client.User.Create().SetUsername("nonmember").SetFullName("Non Member").SaveX(ctx)

	req := model.SearchGroupMembersRequest{
		GroupID: group.ID,
		Limit:   10,
	}
	_, _, _, err := service.SearchGroupMembers(ctx, nonMember.ID, req, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a member")
}

func TestGroupChatServiceSearchGroupMembersAllowsAdminWhenNotMember(t *testing.T) {
	service, client, groupMemberRepo, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("group").SetInviteCode("invite").SaveX(ctx)
	adminUser := client.User.Create().SetUsername("admin_user").SetFullName("Admin User").SaveX(ctx)

	groupMemberRepo.EXPECT().SearchGroupMembers(mock.Anything, groupEntity.ID, "", "", 20).Return([]*ent.GroupMember{}, "", false, nil)

	req := model.SearchGroupMembersRequest{
		GroupID: group.ID,
	}
	resp, _, hasMore, err := service.SearchGroupMembers(ctx, adminUser.ID, req, true)
	require.NoError(t, err)
	require.Empty(t, resp)
	require.False(t, hasMore)
}

func TestGroupChatServiceSearchGroupMembersReturnsRepoError(t *testing.T) {
	service, client, groupMemberRepo, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("group").SetInviteCode("invite").SaveX(ctx)
	adminUser := client.User.Create().SetUsername("admin_err").SetFullName("Admin Err").SaveX(ctx)

	groupMemberRepo.EXPECT().SearchGroupMembers(mock.Anything, groupEntity.ID, "", "", 20).Return(nil, "", false, errors.New("db error"))

	req := model.SearchGroupMembersRequest{
		GroupID: group.ID,
	}
	_, _, _, err := service.SearchGroupMembers(ctx, adminUser.ID, req, true)
	require.Error(t, err)
}

func TestGroupChatServiceSearchGroupMembersInvalidRequestAndNotFound(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	_, _, _, err := service.SearchGroupMembers(ctx, uuid.New(), model.SearchGroupMembersRequest{GroupID: uuid.Nil}, false)
	require.Error(t, err)

	user := client.User.Create().SetUsername("user_nf").SetFullName("User").SaveX(ctx)
	_, _, _, err = service.SearchGroupMembers(ctx, user.ID, model.SearchGroupMembersRequest{GroupID: uuid.New(), Limit: 10}, false)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestGroupChatServiceSearchGroupMembersSkipsDeletedMembers(t *testing.T) {
	service, client, groupMemberRepo, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(parent.ID).SetName("Group With Deleted").SetInviteCode("del_mem").SaveX(ctx)

	activeUser := client.User.Create().SetUsername("active_member").SetFullName("Active Member").SaveX(ctx)
	deletedUser := client.User.Create().SetUsername("deleted_member").SetFullName("Deleted Member").SetDeletedAt(time.Now()).SaveX(ctx)

	activeMember := client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(activeUser.ID).SetRole(groupmember.RoleMember).SaveX(ctx)
	deletedMember := client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(deletedUser.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	loadedActive := client.GroupMember.Query().Where(groupmember.ID(activeMember.ID)).WithUser().OnlyX(ctx)
	loadedDeleted := client.GroupMember.Query().Where(groupmember.ID(deletedMember.ID)).WithUser().OnlyX(ctx)

	groupMemberRepo.EXPECT().SearchGroupMembers(mock.Anything, group.ID, "", "", 20).Return([]*ent.GroupMember{loadedActive, loadedDeleted}, "", false, nil)

	req := model.SearchGroupMembersRequest{
		GroupID: parent.ID,
	}
	resp, _, hasMore, err := service.SearchGroupMembers(ctx, activeUser.ID, req, false)
	require.NoError(t, err)
	require.Len(t, resp, 1)
	require.Equal(t, activeUser.ID, resp[0].UserID)
	require.False(t, hasMore)
}
