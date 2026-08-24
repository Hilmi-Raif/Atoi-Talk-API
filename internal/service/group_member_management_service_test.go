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

func TestGroupMemberMgtServiceAddMemberSuccessAndBroadcasts(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner User").SaveX(ctx)
	target1 := client.User.Create().SetEmail("target1@test.com").SetUsername("target1").SetFullName("Target One").SaveX(ctx)
	target2 := client.User.Create().SetEmail("target2@test.com").SetUsername("target2").SetFullName("Target Two").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team").
		SetInviteCode("team_inv1").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().
		SetGroupChatID(group.ID).
		SetUserID(owner.ID).
		SetRole(groupmember.RoleOwner).
		SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	chatBroadcast := make(chan websocket.Event, 1)
	user1Broadcast := make(chan websocket.Event, 1)
	user2Broadcast := make(chan websocket.Event, 1)

	publisher.EXPECT().BroadcastToChat(parentChat.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		chatBroadcast <- evt
	}).Maybe()

	publisher.EXPECT().BroadcastToUser(target1.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		user1Broadcast <- evt
	}).Maybe()

	publisher.EXPECT().BroadcastToUser(target2.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		user2Broadcast <- evt
	}).Maybe()

	service.wsHub = publisher

	res, err := service.AddMember(ctx, owner.ID, parentChat.ID, model.AddGroupMemberRequest{
		UserIDs: []uuid.UUID{target1.ID, target2.ID},
	})
	require.NoError(t, err)
	require.Len(t, res, 2)

	select {
	case evt := <-chatBroadcast:
		require.Equal(t, websocket.EventMessageNew, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected chat broadcast")
	}

	select {
	case evt := <-user1Broadcast:
		require.Equal(t, websocket.EventChatNew, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected user1 broadcast")
	}

	select {
	case evt := <-user2Broadcast:
		require.Equal(t, websocket.EventChatNew, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected user2 broadcast")
	}
}

func TestGroupMemberMgtServiceAddMemberErrorBranches(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner User").SaveX(ctx)
	member := client.User.Create().SetEmail("member@test.com").SetUsername("member").SetFullName("Member User").SaveX(ctx)
	banned := client.User.Create().SetEmail("banned@test.com").SetUsername("banned").SetFullName("Banned User").SetIsBanned(true).SaveX(ctx)
	target := client.User.Create().SetEmail("target@test.com").SetUsername("target").SetFullName("Target User").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team").
		SetInviteCode("team_inv2").
		SetIsPublic(false).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().
		SetGroupChatID(group.ID).
		SetUserID(owner.ID).
		SetRole(groupmember.RoleOwner).
		SaveX(ctx)

	client.GroupMember.Create().
		SetGroupChatID(group.ID).
		SetUserID(member.ID).
		SetRole(groupmember.RoleMember).
		SaveX(ctx)

	_, err := service.AddMember(ctx, owner.ID, parentChat.ID, model.AddGroupMemberRequest{UserIDs: []uuid.UUID{}})
	require.Error(t, err)

	_, err = service.AddMember(ctx, owner.ID, uuid.New(), model.AddGroupMemberRequest{UserIDs: []uuid.UUID{target.ID}})
	require.Error(t, err)

	stranger := client.User.Create().SetEmail("stranger@test.com").SetUsername("stranger").SetFullName("Stranger").SaveX(ctx)
	_, err = service.AddMember(ctx, stranger.ID, parentChat.ID, model.AddGroupMemberRequest{UserIDs: []uuid.UUID{target.ID}})
	require.Error(t, err)

	_, err = service.AddMember(ctx, member.ID, parentChat.ID, model.AddGroupMemberRequest{UserIDs: []uuid.UUID{target.ID}})
	require.Error(t, err)

	_, err = service.AddMember(ctx, owner.ID, parentChat.ID, model.AddGroupMemberRequest{UserIDs: []uuid.UUID{uuid.New()}})
	require.Error(t, err)

	_, err = service.AddMember(ctx, owner.ID, parentChat.ID, model.AddGroupMemberRequest{UserIDs: []uuid.UUID{banned.ID}})
	require.Error(t, err)

	_, err = service.AddMember(ctx, owner.ID, parentChat.ID, model.AddGroupMemberRequest{UserIDs: []uuid.UUID{member.ID}})
	require.Error(t, err)
}

func TestGroupMemberMgtServiceAddMemberPartialExistingAndBroadcasts(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	existing := client.User.Create().SetEmail("existing@test.com").SetUsername("existing").SetFullName("Existing").SaveX(ctx)
	newMember := client.User.Create().SetEmail("new@test.com").SetUsername("new").SetFullName("New Member").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team").
		SetInviteCode("team_inv3").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(existing.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	publisher.EXPECT().BroadcastToChat(mock.Anything, mock.Anything).Maybe()
	publisher.EXPECT().BroadcastToUser(mock.Anything, mock.Anything).Maybe()
	service.wsHub = publisher

	res, err := service.AddMember(ctx, owner.ID, parentChat.ID, model.AddGroupMemberRequest{
		UserIDs: []uuid.UUID{existing.ID, newMember.ID},
	})
	require.NoError(t, err)
	require.Len(t, res, 1)
}

func TestGroupMemberMgtServiceAddMemberAllowsExpiredBanUser(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	expired := time.Now().UTC().Add(-1 * time.Hour)
	target := client.User.Create().
		SetEmail("target@test.com").
		SetUsername("target").
		SetFullName("Target").
		SetIsBanned(true).
		SetBannedUntil(expired).
		SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team").
		SetInviteCode("team_inv4").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	publisher.EXPECT().BroadcastToChat(mock.Anything, mock.Anything).Maybe()
	publisher.EXPECT().BroadcastToUser(mock.Anything, mock.Anything).Maybe()
	service.wsHub = publisher

	res, err := service.AddMember(ctx, owner.ID, parentChat.ID, model.AddGroupMemberRequest{
		UserIDs: []uuid.UUID{target.ID},
	})
	require.NoError(t, err)
	require.Len(t, res, 1)
}

func TestGroupMemberMgtServiceLeaveGroupSuccessAndBroadcasts(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	member := client.User.Create().SetEmail("member@test.com").SetUsername("member").SetFullName("Member").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team").
		SetInviteCode("team_inv5").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	chatBroadcast := make(chan websocket.Event, 1)

	publisher.EXPECT().BroadcastToChat(parentChat.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		chatBroadcast <- evt
	}).Maybe()

	service.wsHub = publisher

	msg, err := service.LeaveGroup(ctx, member.ID, parentChat.ID)
	require.NoError(t, err)
	require.NotNil(t, msg)
	require.Equal(t, string(message.TypeSystemLeave), msg.Type)

	select {
	case evt := <-chatBroadcast:
		require.Equal(t, websocket.EventMessageNew, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected leave broadcast")
	}
}

func TestGroupMemberMgtServiceLeaveGroupErrorBranches(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	stranger := client.User.Create().SetEmail("stranger@test.com").SetUsername("stranger").SetFullName("Stranger").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team").
		SetInviteCode("team_inv6").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	_, err := service.LeaveGroup(ctx, owner.ID, uuid.New())
	require.Error(t, err)

	_, err = service.LeaveGroup(ctx, stranger.ID, parentChat.ID)
	require.Error(t, err)

	_, err = service.LeaveGroup(ctx, owner.ID, parentChat.ID)
	require.Error(t, err)
}

func TestGroupMemberMgtServiceKickMemberSuccessAndBroadcasts(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	admin := client.User.Create().SetEmail("admin@test.com").SetUsername("admin").SetFullName("Admin").SaveX(ctx)
	member := client.User.Create().SetEmail("member@test.com").SetUsername("member").SetFullName("Member").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team").
		SetInviteCode("team_inv7").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(admin.ID).SetRole(groupmember.RoleAdmin).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	chatBroadcast := make(chan websocket.Event, 1)
	userBroadcast := make(chan websocket.Event, 1)

	publisher.EXPECT().BroadcastToChat(parentChat.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		chatBroadcast <- evt
	}).Maybe()

	publisher.EXPECT().BroadcastToUser(member.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		userBroadcast <- evt
	}).Maybe()

	service.wsHub = publisher

	msg, err := service.KickMember(ctx, admin.ID, parentChat.ID, member.ID)
	require.NoError(t, err)
	require.NotNil(t, msg)
	require.Equal(t, string(message.TypeSystemKick), msg.Type)

	select {
	case evt := <-chatBroadcast:
		require.Equal(t, websocket.EventMessageNew, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected kick chat broadcast")
	}

	select {
	case evt := <-userBroadcast:
		require.Equal(t, websocket.EventChatDelete, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected kick user broadcast")
	}
}

func TestGroupMemberMgtServiceKickMemberErrorBranches(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	admin := client.User.Create().SetEmail("admin@test.com").SetUsername("admin").SetFullName("Admin").SaveX(ctx)
	member := client.User.Create().SetEmail("member@test.com").SetUsername("member").SetFullName("Member").SaveX(ctx)
	stranger := client.User.Create().SetEmail("stranger@test.com").SetUsername("stranger").SetFullName("Stranger").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team").
		SetInviteCode("team_inv8").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(admin.ID).SetRole(groupmember.RoleAdmin).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	_, err := service.KickMember(ctx, owner.ID, uuid.New(), member.ID)
	require.Error(t, err)

	_, err = service.KickMember(ctx, stranger.ID, parentChat.ID, member.ID)
	require.Error(t, err)

	_, err = service.KickMember(ctx, member.ID, parentChat.ID, admin.ID)
	require.Error(t, err)

	_, err = service.KickMember(ctx, owner.ID, parentChat.ID, owner.ID)
	require.Error(t, err)

	_, err = service.KickMember(ctx, admin.ID, parentChat.ID, owner.ID)
	require.Error(t, err)

	_, err = service.KickMember(ctx, owner.ID, parentChat.ID, stranger.ID)
	require.Error(t, err)
}

func TestGroupMemberMgtServiceAddMemberCardinalityAndDuplicates(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	target := client.User.Create().SetEmail("target@test.com").SetUsername("target").SetFullName("Target").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team").
		SetInviteCode("team_inv9").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	_, err := service.AddMember(ctx, owner.ID, parentChat.ID, model.AddGroupMemberRequest{
		UserIDs: []uuid.UUID{target.ID, target.ID},
	})
	require.Error(t, err)

	tooMany := make([]uuid.UUID, 101)
	for i := range tooMany {
		tooMany[i] = uuid.New()
	}
	_, err = service.AddMember(ctx, owner.ID, parentChat.ID, model.AddGroupMemberRequest{
		UserIDs: tooMany,
	})
	require.Error(t, err)
}

func TestGroupMemberMgtServiceCancelledContextBranches(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := service.AddMember(ctx, uuid.New(), uuid.New(), model.AddGroupMemberRequest{UserIDs: []uuid.UUID{uuid.New()}})
	require.Error(t, err)

	_, err = service.LeaveGroup(ctx, uuid.New(), uuid.New())
	require.Error(t, err)

	_, err = service.KickMember(ctx, uuid.New(), uuid.New(), uuid.New())
	require.Error(t, err)
}

func TestGroupMemberMgtServiceKickMemberWithAndWithoutFullNameAndBroadcasts(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	targetWithFullName := client.User.Create().SetEmail("target1@test.com").SetUsername("target1").SetFullName("Target One").SaveX(ctx)
	targetWithoutFullName := client.User.Create().SetEmail("target2@test.com").SetUsername("target2").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team").
		SetInviteCode("team_kick").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(targetWithFullName.ID).SetRole(groupmember.RoleMember).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(targetWithoutFullName.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	chatBroadcast := make(chan websocket.Event, 2)
	userBroadcast := make(chan websocket.Event, 2)

	publisher.EXPECT().BroadcastToChat(parentChat.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		chatBroadcast <- evt
	}).Maybe()

	publisher.EXPECT().BroadcastToUser(mock.Anything, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		userBroadcast <- evt
	}).Maybe()

	service.wsHub = publisher

	res, err := service.KickMember(ctx, owner.ID, parentChat.ID, targetWithFullName.ID)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, message.TypeSystemKick.String(), res.Type)

	res2, err := service.KickMember(ctx, owner.ID, parentChat.ID, targetWithoutFullName.ID)
	require.NoError(t, err)
	require.NotNil(t, res2)
	require.Equal(t, message.TypeSystemKick.String(), res2.Type)
}

func TestGroupMemberMgtServiceAddMemberWithoutFullNameAndBroadcasts(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner_add_nofn@test.com").SetUsername("owner_add_nofn").SaveX(ctx)
	target := client.User.Create().SetEmail("target_add_nofn@test.com").SetUsername("target_add_nofn").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team Add No FN").
		SetInviteCode("team_nofn").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	chatBroadcast := make(chan websocket.Event, 1)
	userBroadcast := make(chan websocket.Event, 1)

	publisher.EXPECT().BroadcastToChat(parentChat.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		chatBroadcast <- evt
	}).Maybe()

	publisher.EXPECT().BroadcastToUser(target.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		userBroadcast <- evt
	}).Maybe()

	service.wsHub = publisher

	res, err := service.AddMember(ctx, owner.ID, parentChat.ID, model.AddGroupMemberRequest{
		UserIDs: []uuid.UUID{target.ID},
	})
	require.NoError(t, err)
	require.Len(t, res, 1)

	select {
	case evt := <-chatBroadcast:
		require.Equal(t, websocket.EventMessageNew, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected chat broadcast")
	}

	select {
	case evt := <-userBroadcast:
		require.Equal(t, websocket.EventChatNew, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected user broadcast")
	}
}

func TestGroupMemberMgtServiceLeaveGroupWithBroadcasts(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner_leave_bc@test.com").SetUsername("owner_leave_bc").SaveX(ctx)
	member := client.User.Create().SetEmail("member_leave_bc@test.com").SetUsername("member_leave_bc").SetFullName("Member Leaving").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team Leave BC").
		SetInviteCode("team_leave_bc").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	chatBroadcast := make(chan websocket.Event, 1)

	publisher.EXPECT().BroadcastToChat(parentChat.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		chatBroadcast <- evt
	}).Maybe()

	service.wsHub = publisher

	res, err := service.LeaveGroup(ctx, member.ID, parentChat.ID)
	require.NoError(t, err)
	require.NotNil(t, res)

	select {
	case evt := <-chatBroadcast:
		require.Equal(t, websocket.EventMessageNew, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected chat broadcast on leave")
	}
}

func TestGroupMemberMgtServiceKickMemberWithBroadcasts(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	admin := client.User.Create().SetEmail("admin_kick_bc@test.com").SetUsername("admin_kick_bc").SaveX(ctx)
	target := client.User.Create().SetEmail("target_kick_bc@test.com").SetUsername("target_kick_bc").SetFullName("Target Kicked").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team Kick BC").
		SetInviteCode("team_kick_bc").
		SetIsPublic(true).
		SetCreatorID(admin.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(admin.ID).SetRole(groupmember.RoleAdmin).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(target.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	chatBroadcast := make(chan websocket.Event, 1)
	userBroadcast := make(chan websocket.Event, 1)

	publisher.EXPECT().BroadcastToChat(parentChat.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		chatBroadcast <- evt
	}).Maybe()

	publisher.EXPECT().BroadcastToUser(target.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		userBroadcast <- evt
	}).Maybe()

	service.wsHub = publisher

	res, err := service.KickMember(ctx, admin.ID, parentChat.ID, target.ID)
	require.NoError(t, err)
	require.NotNil(t, res)

	select {
	case evt := <-chatBroadcast:
		require.Equal(t, websocket.EventMessageNew, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected chat broadcast on kick")
	}

	select {
	case evt := <-userBroadcast:
		require.Equal(t, websocket.EventChatDelete, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected user broadcast on kick")
	}
}

func TestGroupMemberMgtServiceKickMemberWithTargetFullNameEnrichment(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	admin := client.User.Create().SetEmail("kicker_admin@test.com").SetUsername("kicker_admin").SetFullName("Admin User").SaveX(ctx)
	target := client.User.Create().SetEmail("kicked_target@test.com").SetUsername("kicked_target").SetFullName("Target Person").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Enrich Kick Team").
		SetInviteCode("enrich_kick").
		SetIsPublic(true).
		SetCreatorID(admin.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(admin.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(target.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	res, err := service.KickMember(ctx, admin.ID, parentChat.ID, target.ID)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "Target Person", res.ActionData["target_name"])
}
