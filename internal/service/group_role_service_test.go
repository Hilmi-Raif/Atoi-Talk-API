package service

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/websocket"
	websocketmocks "AtoiTalkAPI/internal/websocket/mocks"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGroupChatServiceUpdateMemberRolePromotesToAdmin(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("role group").SetInviteCode("role123").SaveX(ctx)
	owner := client.User.Create().SetUsername("owner_role").SetFullName("Owner Role").SaveX(ctx)
	member := client.User.Create().SetUsername("member_role").SetFullName("Member Role").SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	done := make(chan struct{}, 1)
	publisher.EXPECT().BroadcastToChat(group.ID, mock.MatchedBy(func(evt websocket.Event) bool {
		return evt.Type == websocket.EventMessageNew
	})).Run(func(_ uuid.UUID, _ websocket.Event) {
		done <- struct{}{}
	}).Once()
	service.wsHub = publisher

	req := model.UpdateGroupMemberRoleRequest{Role: "admin"}
	msg, err := service.UpdateMemberRole(ctx, owner.ID, group.ID, member.ID, req)
	require.NoError(t, err)
	require.NotNil(t, msg)
	require.Equal(t, string(message.TypeSystemPromote), msg.Type)
	require.NotNil(t, msg.ActionData)
	require.Equal(t, "Member Role", msg.ActionData["target_name"])

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("expected websocket broadcast")
	}

	updated := client.GroupMember.Query().Where(groupmember.GroupChatID(groupEntity.ID), groupmember.UserID(member.ID)).OnlyX(ctx)
	require.Equal(t, groupmember.RoleAdmin, updated.Role)
}

func TestGroupChatServiceUpdateMemberRoleDemotesToMember(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("role group").SetInviteCode("role1234").SaveX(ctx)
	owner := client.User.Create().SetUsername("owner_role2").SetFullName("Owner Role").SaveX(ctx)
	admin := client.User.Create().SetUsername("admin_role2").SetFullName("Admin Role").SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(admin.ID).SetRole(groupmember.RoleAdmin).SaveX(ctx)

	req := model.UpdateGroupMemberRoleRequest{Role: "member"}
	msg, err := service.UpdateMemberRole(ctx, owner.ID, group.ID, admin.ID, req)
	require.NoError(t, err)
	require.NotNil(t, msg)
	require.Equal(t, string(message.TypeSystemDemote), msg.Type)
}

func TestGroupChatServiceUpdateMemberRoleRejectsNonOwner(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("role group").SetInviteCode("role1235").SaveX(ctx)
	admin := client.User.Create().SetUsername("admin_role3").SetFullName("Admin Role").SaveX(ctx)
	member := client.User.Create().SetUsername("member_role3").SetFullName("Member Role").SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(admin.ID).SetRole(groupmember.RoleAdmin).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	req := model.UpdateGroupMemberRoleRequest{Role: "admin"}
	_, err := service.UpdateMemberRole(ctx, admin.ID, group.ID, member.ID, req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "owner")
}

func TestGroupChatServiceUpdateMemberRoleRejectsSelf(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("role group").SetInviteCode("role1236").SaveX(ctx)
	owner := client.User.Create().SetUsername("owner_role4").SetFullName("Owner Role").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	req := model.UpdateGroupMemberRoleRequest{Role: "admin"}
	_, err := service.UpdateMemberRole(ctx, owner.ID, group.ID, owner.ID, req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Cannot change your own role")
}

func TestGroupChatServiceUpdateMemberRoleRejectsSameRole(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("role group").SetInviteCode("role1237").SaveX(ctx)
	owner := client.User.Create().SetUsername("owner_role5").SetFullName("Owner Role").SaveX(ctx)
	admin := client.User.Create().SetUsername("admin_role5").SetFullName("Admin Role").SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(admin.ID).SetRole(groupmember.RoleAdmin).SaveX(ctx)

	req := model.UpdateGroupMemberRoleRequest{Role: "admin"}
	_, err := service.UpdateMemberRole(ctx, owner.ID, group.ID, admin.ID, req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already has")
}

func TestGroupChatServiceUpdateMemberRoleRejectsBannedUser(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("role group").SetInviteCode("roleban").SaveX(ctx)
	owner := client.User.Create().SetUsername("owner_ban").SetFullName("Owner Ban").SaveX(ctx)
	banned := client.User.Create().SetUsername("banned_mem").SetFullName("Banned Mem").SetIsBanned(true).SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(banned.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	req := model.UpdateGroupMemberRoleRequest{Role: "admin"}
	_, err := service.UpdateMemberRole(ctx, owner.ID, group.ID, banned.ID, req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "suspended/banned")
}

func TestGroupChatServiceTransferOwnershipSuccess(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("trans group").SetInviteCode("trans123").SaveX(ctx)
	owner := client.User.Create().SetUsername("owner_trans").SetFullName("Owner Trans").SaveX(ctx)
	admin := client.User.Create().SetUsername("admin_trans").SetFullName("Admin Trans").SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(admin.ID).SetRole(groupmember.RoleAdmin).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	done := make(chan struct{}, 1)
	publisher.EXPECT().BroadcastToChat(group.ID, mock.MatchedBy(func(evt websocket.Event) bool {
		return evt.Type == websocket.EventMessageNew
	})).Run(func(_ uuid.UUID, _ websocket.Event) {
		done <- struct{}{}
	}).Once()
	service.wsHub = publisher

	req := model.TransferGroupOwnershipRequest{NewOwnerID: admin.ID}
	msg, err := service.TransferOwnership(ctx, owner.ID, group.ID, req)
	require.NoError(t, err)
	require.NotNil(t, msg)
	require.Equal(t, string(message.TypeSystemPromote), msg.Type)
	require.NotNil(t, msg.ActionData)
	require.Equal(t, "Admin Trans", msg.ActionData["target_name"])

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("expected websocket broadcast")
	}

	oldOwnerMember := client.GroupMember.Query().Where(groupmember.GroupChatID(groupEntity.ID), groupmember.UserID(owner.ID)).OnlyX(ctx)
	require.Equal(t, groupmember.RoleAdmin, oldOwnerMember.Role)

	newOwnerMember := client.GroupMember.Query().Where(groupmember.GroupChatID(groupEntity.ID), groupmember.UserID(admin.ID)).OnlyX(ctx)
	require.Equal(t, groupmember.RoleOwner, newOwnerMember.Role)
}

func TestGroupChatServiceTransferOwnershipRejectsSelf(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	ownerID := uuid.New()
	req := model.TransferGroupOwnershipRequest{NewOwnerID: ownerID}
	_, err := service.TransferOwnership(ctx, ownerID, uuid.New(), req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "yourself")
}

func TestGroupChatServiceTransferOwnershipRejectsNonOwner(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("trans group").SetInviteCode("trans1232").SaveX(ctx)
	admin := client.User.Create().SetUsername("admin_trans2").SetFullName("Admin Trans").SaveX(ctx)
	member := client.User.Create().SetUsername("member_trans2").SetFullName("Member Trans").SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(admin.ID).SetRole(groupmember.RoleAdmin).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	req := model.TransferGroupOwnershipRequest{NewOwnerID: member.ID}
	_, err := service.TransferOwnership(ctx, admin.ID, group.ID, req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "owner")
}

func TestGroupChatServiceTransferOwnershipRejectsNonMember(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("trans group").SetInviteCode("trans1233").SaveX(ctx)
	owner := client.User.Create().SetUsername("owner_trans3").SetFullName("Owner Trans").SaveX(ctx)
	nonMember := client.User.Create().SetUsername("non_member_trans3").SetFullName("Non Member").SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	req := model.TransferGroupOwnershipRequest{NewOwnerID: nonMember.ID}
	_, err := service.TransferOwnership(ctx, owner.ID, group.ID, req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a member")
}

func TestGroupChatServiceTransferOwnershipRejectsBannedUser(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("trans group").SetInviteCode("transban").SaveX(ctx)
	owner := client.User.Create().SetUsername("owner_tb").SetFullName("Owner TB").SaveX(ctx)
	banned := client.User.Create().SetUsername("banned_tb").SetFullName("Banned TB").SetIsBanned(true).SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(banned.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	req := model.TransferGroupOwnershipRequest{NewOwnerID: banned.ID}
	_, err := service.TransferOwnership(ctx, owner.ID, group.ID, req)
	require.Error(t, err)
	require.Contains(t, err.Error(), "suspended/banned")
}

func TestGroupChatServiceRoleCancelledContext(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := service.UpdateMemberRole(ctx, uuid.New(), uuid.New(), uuid.New(), model.UpdateGroupMemberRoleRequest{Role: "admin"})
	require.Error(t, err)

	_, err = service.TransferOwnership(ctx, uuid.New(), uuid.New(), model.TransferGroupOwnershipRequest{NewOwnerID: uuid.New()})
	require.Error(t, err)
}

func TestGroupChatServiceRoleValidationAndNotFoundBranches(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	_, err := service.UpdateMemberRole(ctx, uuid.New(), uuid.New(), uuid.New(), model.UpdateGroupMemberRoleRequest{Role: "invalid"})
	require.Error(t, err)

	_, err = service.TransferOwnership(ctx, uuid.New(), uuid.New(), model.TransferGroupOwnershipRequest{NewOwnerID: uuid.Nil})
	require.Error(t, err)

	user1 := client.User.Create().SetUsername("u_rnf1").SaveX(ctx)
	user2 := client.User.Create().SetUsername("u_rnf2").SaveX(ctx)

	_, err = service.UpdateMemberRole(ctx, user1.ID, uuid.New(), user2.ID, model.UpdateGroupMemberRoleRequest{Role: "admin"})
	require.Error(t, err)

	_, err = service.TransferOwnership(ctx, user1.ID, uuid.New(), model.TransferGroupOwnershipRequest{NewOwnerID: user2.ID})
	require.Error(t, err)

	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("rnf group").SetInviteCode("rnf123").SaveX(ctx)

	_, err = service.UpdateMemberRole(ctx, user1.ID, group.ID, user2.ID, model.UpdateGroupMemberRoleRequest{Role: "admin"})
	require.Error(t, err)

	_, err = service.TransferOwnership(ctx, user1.ID, group.ID, model.TransferGroupOwnershipRequest{NewOwnerID: user2.ID})
	require.Error(t, err)

	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(user1.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	_, err = service.UpdateMemberRole(ctx, user1.ID, group.ID, user2.ID, model.UpdateGroupMemberRoleRequest{Role: "admin"})
	require.Error(t, err)

	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(user2.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	_, err = service.TransferOwnership(ctx, user1.ID, group.ID, model.TransferGroupOwnershipRequest{NewOwnerID: user2.ID})
	require.Error(t, err)
}

func TestGroupChatServiceRoleWithoutFullName(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("nf group").SetInviteCode("nf123").SaveX(ctx)
	owner := client.User.Create().SetUsername("owner_nf").SaveX(ctx)
	member := client.User.Create().SetUsername("member_nf").SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	msg, err := service.UpdateMemberRole(ctx, owner.ID, group.ID, member.ID, model.UpdateGroupMemberRoleRequest{Role: "admin"})
	require.NoError(t, err)
	require.NotNil(t, msg)

	msg, err = service.TransferOwnership(ctx, owner.ID, group.ID, model.TransferGroupOwnershipRequest{NewOwnerID: member.ID})
	require.NoError(t, err)
	require.NotNil(t, msg)
}
