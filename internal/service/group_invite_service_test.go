package service

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/media"
	adaptermocks "AtoiTalkAPI/internal/adapter/mocks"
	"AtoiTalkAPI/internal/websocket"
	websocketmocks "AtoiTalkAPI/internal/websocket/mocks"
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGroupChatServiceGetGroupByInviteCodeMapsPreview(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("invite group").SetInviteCode("invite123").SaveX(ctx)
	user := client.User.Create().SetUsername("owner_inv").SetFullName("Owner Inv").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(user.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	resp, err := service.GetGroupByInviteCode(ctx, "invite123")
	require.NoError(t, err)
	require.Equal(t, group.ID, resp.ID)
	require.Equal(t, "invite group", resp.Name)
	require.Equal(t, 1, resp.MemberCount)
}

func TestGroupChatServiceGetGroupByInviteCodeRejectsExpiredInvite(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	expired := time.Now().UTC().Add(-time.Hour)
	client.GroupChat.Create().SetChatID(group.ID).SetName("expired group").SetInviteCode("expired123").SetInviteExpiresAt(expired).SaveX(ctx)

	_, err := service.GetGroupByInviteCode(ctx, "expired123")
	require.Error(t, err)
	require.Contains(t, err.Error(), "expired")
}

func TestGroupChatServiceJoinGroupByInviteCreatesMemberAndMessage(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	client.GroupChat.Create().SetChatID(group.ID).SetName("invite group").SetInviteCode("join123").SaveX(ctx)
	user := client.User.Create().SetUsername("joiner_inv").SetFullName("Joiner Inv").SaveX(ctx)
	redisCache := service.redisAdapter.(*adaptermocks.MockRedisCache)
	redisCache.EXPECT().Del(mock.Anything, "chat_members:"+group.ID.String()).Return(nil)

	resp, err := service.JoinGroupByInvite(ctx, user.ID, "join123")
	require.NoError(t, err)
	require.Equal(t, group.ID, resp.ID)
	require.NotNil(t, resp.LastMessage)
}

func TestGroupChatServiceJoinGroupByInviteRejectsDuplicateMember(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("invite group").SetInviteCode("joindup").SaveX(ctx)
	user := client.User.Create().SetUsername("dup_user").SetFullName("Dup User").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(user.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	_, err := service.JoinGroupByInvite(ctx, user.ID, "joindup")
	require.Error(t, err)
	require.Contains(t, err.Error(), "already a member")
}

func TestGroupChatServiceJoinGroupByInviteBroadcasts(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	client.GroupChat.Create().SetChatID(group.ID).SetName("invite group").SetInviteCode("joinbroad").SaveX(ctx)
	user := client.User.Create().SetUsername("joiner_broad").SetFullName("Joiner Broad").SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	chatDone := make(chan struct{}, 1)
	userDone := make(chan struct{}, 1)

	publisher.EXPECT().BroadcastToUser(user.ID, mock.MatchedBy(func(evt websocket.Event) bool {
		return evt.Type == websocket.EventChatNew
	})).Run(func(_ uuid.UUID, _ websocket.Event) {
		userDone <- struct{}{}
	}).Once()

	publisher.EXPECT().BroadcastToChat(group.ID, mock.MatchedBy(func(evt websocket.Event) bool {
		return evt.Type == websocket.EventMessageNew
	})).Run(func(_ uuid.UUID, _ websocket.Event) {
		chatDone <- struct{}{}
	}).Once()

	service.wsHub = publisher

	_, err := service.JoinGroupByInvite(ctx, user.ID, "joinbroad")
	require.NoError(t, err)

	select {
	case <-chatDone:
	case <-time.After(2 * time.Second):
		t.Fatal("expected websocket broadcast to chat")
	}

	select {
	case <-userDone:
	case <-time.After(2 * time.Second):
		t.Fatal("expected websocket broadcast to user")
	}
}

func TestGroupChatServiceResetInviteCodeSuccess(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("reset group").SetInviteCode("oldcode123").SetIsPublic(true).SaveX(ctx)
	owner := client.User.Create().SetUsername("owner_reset").SetFullName("Owner Reset").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	resp, err := service.ResetInviteCode(ctx, owner.ID, group.ID)
	require.NoError(t, err)
	require.NotEqual(t, "oldcode123", resp.InviteCode)
}

func TestGroupChatServiceResetInviteCodeRejectsMember(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("reset group").SetInviteCode("oldcode123").SaveX(ctx)
	member := client.User.Create().SetUsername("member_reset").SetFullName("Member Reset").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	_, err := service.ResetInviteCode(ctx, member.ID, group.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "Only admins")
}

func TestGroupChatServiceResetInviteCodeRejectsNonMember(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	client.GroupChat.Create().SetChatID(group.ID).SetName("reset group").SetInviteCode("oldcode123").SaveX(ctx)
	nonMember := client.User.Create().SetUsername("non_member_reset").SetFullName("Non Member").SaveX(ctx)

	_, err := service.ResetInviteCode(ctx, nonMember.ID, group.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not a member")
}

func TestGroupChatServiceResetInviteCodeNotFound(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	user := client.User.Create().SetUsername("user_reset").SetFullName("User").SaveX(ctx)

	_, err := service.ResetInviteCode(ctx, user.ID, uuid.New())
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found")
}

func TestGroupChatServiceResetInviteCodePrivateBroadcastsOnlyToAdmins(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	group := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(group.ID).SetName("private reset").SetInviteCode("oldpriv").SetIsPublic(false).SaveX(ctx)
	owner := client.User.Create().SetUsername("owner_priv").SetFullName("Owner Priv").SaveX(ctx)
	member := client.User.Create().SetUsername("member_priv").SetFullName("Member Priv").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	done := make(chan struct{}, 1)
	publisher.EXPECT().BroadcastToUser(owner.ID, mock.Anything).Run(func(_ uuid.UUID, _ websocket.Event) {
		done <- struct{}{}
	}).Once()
	service.wsHub = publisher

	_, err := service.ResetInviteCode(ctx, owner.ID, group.ID)
	require.NoError(t, err)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("expected websocket broadcast to owner")
	}
}

func TestGroupChatServiceGetGroupByInviteCodeNotFound(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	_, err := service.GetGroupByInviteCode(ctx, "nonexistent")
	require.Error(t, err)
}

func TestGroupChatServiceJoinGroupByInviteNotFound(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	user := client.User.Create().SetUsername("user_join_nf2").SetFullName("User").SaveX(ctx)

	_, err := service.JoinGroupByInvite(ctx, user.ID, "nonexistent")
	require.Error(t, err)
}

func TestGroupChatServiceInviteCancelledContext(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := service.GetGroupByInviteCode(ctx, "any")
	require.Error(t, err)

	_, err = service.JoinGroupByInvite(ctx, uuid.New(), "any")
	require.Error(t, err)

	_, err = service.ResetInviteCode(ctx, uuid.New(), uuid.New())
	require.Error(t, err)
}

func TestGroupChatServiceGetGroupByInviteCodeWithAvatarAndDescription(t *testing.T) {
	service, client, _, _, storage := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner_inv_av@test.com").SetUsername("owner_inv_av").SetFullName("Owner Inv Av").SaveX(ctx)

	av := client.Media.Create().
		SetUploaderID(owner.ID).
		SetFileName("avatars/inv_group.png").
		SetOriginalName("inv_group.png").
		SetCategory(media.CategoryGroupAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetMimeType("image/png").
		SetFileSize(1024).
		SaveX(ctx)

	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parent.ID).
		SetName("Invite Group With Avatar").
		SetDescription("Group with description and avatar").
		SetInviteCode("inv_av12345").
		SetIsPublic(true).
		SetAvatarID(av.ID).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().
		SetGroupChatID(group.ID).
		SetUserID(owner.ID).
		SetRole(groupmember.RoleOwner).
		SaveX(ctx)

	storage.EXPECT().GetPublicURL("avatars/inv_group.png").Return("https://cdn.example.com/avatars/inv_group.png").Maybe()

	preview, err := service.GetGroupByInviteCode(ctx, "inv_av12345")
	require.NoError(t, err)
	require.NotNil(t, preview)
	require.Equal(t, "Invite Group With Avatar", preview.Name)
	require.Equal(t, "Group with description and avatar", preview.Description)
	require.Equal(t, "https://cdn.test/avatar.png", preview.Avatar)
	require.Equal(t, 1, preview.MemberCount)
}

func TestGroupChatServiceJoinGroupByInviteWithAvatarAndPrivateGroup(t *testing.T) {
	service, client, _, _, storage := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner_priv_join@test.com").SetUsername("owner_priv_join").SetFullName("Owner Priv Join").SaveX(ctx)
	user := client.User.Create().SetEmail("user_priv_join@test.com").SetUsername("user_priv_join").SetFullName("User Priv Join").SaveX(ctx)

	av := client.Media.Create().
		SetUploaderID(owner.ID).
		SetFileName("avatars/priv_join.png").
		SetOriginalName("priv_join.png").
		SetCategory(media.CategoryGroupAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetMimeType("image/png").
		SetFileSize(1024).
		SaveX(ctx)

	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parent.ID).
		SetName("Private Group Join").
		SetInviteCode("priv_join123").
		SetIsPublic(false).
		SetAvatarID(av.ID).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().
		SetGroupChatID(group.ID).
		SetUserID(owner.ID).
		SetRole(groupmember.RoleOwner).
		SaveX(ctx)

	storage.EXPECT().GetPublicURL("avatars/priv_join.png").Return("https://cdn.test/avatar.png").Maybe()

	publisher := websocketmocks.NewMockPublisher(t)
	userDone := make(chan struct{}, 1)
	chatDone := make(chan struct{}, 1)

	publisher.EXPECT().BroadcastToUser(user.ID, mock.Anything).Run(func(_ uuid.UUID, _ websocket.Event) {
		userDone <- struct{}{}
	}).Once()

	publisher.EXPECT().BroadcastToChat(parent.ID, mock.Anything).Run(func(_ uuid.UUID, _ websocket.Event) {
		chatDone <- struct{}{}
	}).Once()

	service.wsHub = publisher

	res, err := service.JoinGroupByInvite(ctx, user.ID, "priv_join123")
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "https://cdn.test/avatar.png", res.Avatar)
	require.Nil(t, res.InviteCode)

	select {
	case <-userDone:
	case <-time.After(2 * time.Second):
		t.Fatal("expected user broadcast")
	}

	select {
	case <-chatDone:
	case <-time.After(2 * time.Second):
		t.Fatal("expected chat broadcast")
	}
}

func TestGroupInviteServiceJoinGroupByInvitePublicWithAvatarAndBroadcasts(t *testing.T) {
	service, client, _, _, storage := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner_pub_join@test.com").SetUsername("owner_pub_join").SetFullName("Owner Pub Join").SaveX(ctx)
	user := client.User.Create().SetEmail("user_pub_join@test.com").SetUsername("user_pub_join").SetFullName("User Pub Join").SaveX(ctx)

	av := client.Media.Create().
		SetUploaderID(owner.ID).
		SetFileName("avatars/pub_join.png").
		SetOriginalName("pub_join.png").
		SetCategory(media.CategoryGroupAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetMimeType("image/png").
		SetFileSize(1024).
		SaveX(ctx)

	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parent.ID).
		SetName("Public Group Join").
		SetInviteCode("pub_join123").
		SetIsPublic(true).
		SetAvatarID(av.ID).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().
		SetGroupChatID(group.ID).
		SetUserID(owner.ID).
		SetRole(groupmember.RoleOwner).
		SaveX(ctx)

	storage.EXPECT().GetPublicURL("avatars/pub_join.png").Return("https://cdn.test/avatar.png").Maybe()

	publisher := websocketmocks.NewMockPublisher(t)
	userDone := make(chan struct{}, 1)
	chatDone := make(chan struct{}, 1)

	publisher.EXPECT().BroadcastToUser(user.ID, mock.Anything).Run(func(_ uuid.UUID, _ websocket.Event) {
		userDone <- struct{}{}
	}).Once()

	publisher.EXPECT().BroadcastToChat(parent.ID, mock.Anything).Run(func(_ uuid.UUID, _ websocket.Event) {
		chatDone <- struct{}{}
	}).Once()

	service.wsHub = publisher

	res, err := service.JoinGroupByInvite(ctx, user.ID, "pub_join123")
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "https://cdn.test/avatar.png", res.Avatar)
	require.NotNil(t, res.InviteCode)
	require.Equal(t, "pub_join123", *res.InviteCode)

	select {
	case <-userDone:
	case <-time.After(2 * time.Second):
		t.Fatal("expected user broadcast")
	}

	select {
	case <-chatDone:
	case <-time.After(2 * time.Second):
		t.Fatal("expected chat broadcast")
	}
}

func TestGroupInviteServiceResetInviteCodeByAdminInPublicGroupWithBroadcasts(t *testing.T) {
	service, client, _, _, storage := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	adminUser := client.User.Create().SetEmail("admin_reset_pub@test.com").SetUsername("admin_reset_pub").SaveX(ctx)
	member := client.User.Create().SetEmail("member_reset_pub@test.com").SetUsername("member_reset_pub").SaveX(ctx)

	av := client.Media.Create().
		SetUploaderID(adminUser.ID).
		SetFileName("avatars/pub_reset.png").
		SetOriginalName("pub_reset.png").
		SetCategory(media.CategoryGroupAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetMimeType("image/png").
		SetFileSize(1024).
		SaveX(ctx)

	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parent.ID).
		SetName("Public Group Reset").
		SetInviteCode("orig_pub_code").
		SetIsPublic(true).
		SetAvatarID(av.ID).
		SetCreatorID(adminUser.ID).
		SaveX(ctx)

	client.GroupMember.Create().
		SetGroupChatID(group.ID).
		SetUserID(adminUser.ID).
		SetRole(groupmember.RoleAdmin).
		SaveX(ctx)

	client.GroupMember.Create().
		SetGroupChatID(group.ID).
		SetUserID(member.ID).
		SetRole(groupmember.RoleMember).
		SaveX(ctx)

	storage.EXPECT().GetPublicURL("avatars/pub_reset.png").Return("https://cdn.test/avatar.png").Maybe()

	publisher := websocketmocks.NewMockPublisher(t)
	userBroadcast := make(chan websocket.Event, 2)

	publisher.EXPECT().BroadcastToUser(adminUser.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		userBroadcast <- evt
	}).Maybe()

	publisher.EXPECT().BroadcastToUser(member.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		userBroadcast <- evt
	}).Maybe()

	service.wsHub = publisher

	res, err := service.ResetInviteCode(ctx, adminUser.ID, parent.ID)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotEmpty(t, res.InviteCode)
	require.Nil(t, res.ExpiresAt)
	require.NotEqual(t, "orig_pub_code", res.InviteCode)

	select {
	case evt := <-userBroadcast:
		require.Equal(t, websocket.EventChatUpdate, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected invite reset broadcast")
	}
}

func TestGroupInviteServiceResetInviteCodeByAdminInPrivateGroupWithBroadcasts(t *testing.T) {
	service, client, _, _, storage := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner_priv_res@test.com").SetUsername("owner_priv_res").SaveX(ctx)
	adminUser := client.User.Create().SetEmail("admin_priv_res@test.com").SetUsername("admin_priv_res").SaveX(ctx)
	member := client.User.Create().SetEmail("member_priv_res@test.com").SetUsername("member_priv_res").SaveX(ctx)

	av := client.Media.Create().
		SetUploaderID(adminUser.ID).
		SetFileName("avatars/priv_reset.png").
		SetOriginalName("priv_reset.png").
		SetCategory(media.CategoryGroupAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetMimeType("image/png").
		SetFileSize(1024).
		SaveX(ctx)

	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parent.ID).
		SetName("Private Group Reset").
		SetInviteCode("orig_priv_code").
		SetIsPublic(false).
		SetAvatarID(av.ID).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(adminUser.ID).SetRole(groupmember.RoleAdmin).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	storage.EXPECT().GetPublicURL("avatars/priv_reset.png").Return("https://cdn.test/avatar.png").Maybe()

	publisher := websocketmocks.NewMockPublisher(t)
	adminBroadcast := make(chan websocket.Event, 1)
	ownerBroadcast := make(chan websocket.Event, 1)

	publisher.EXPECT().BroadcastToUser(adminUser.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		adminBroadcast <- evt
	}).Maybe()

	publisher.EXPECT().BroadcastToUser(owner.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		ownerBroadcast <- evt
	}).Maybe()

	service.wsHub = publisher

	res, err := service.ResetInviteCode(ctx, adminUser.ID, parent.ID)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotEmpty(t, res.InviteCode)
	require.NotNil(t, res.ExpiresAt)
	require.NotEqual(t, "orig_priv_code", res.InviteCode)

	select {
	case evt := <-adminBroadcast:
		require.Equal(t, websocket.EventChatUpdate, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected admin invite reset broadcast")
	}

	select {
	case evt := <-ownerBroadcast:
		require.Equal(t, websocket.EventChatUpdate, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected owner invite reset broadcast")
	}
}

func TestGroupInviteServiceOperationsWithWsHubNil(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	service.wsHub = nil

	owner := client.User.Create().SetEmail("owner_inv_nil@test.com").SetUsername("owner_inv_nil").SaveX(ctx)
	user := client.User.Create().SetEmail("joiner_inv_nil@test.com").SetUsername("joiner_inv_nil").SaveX(ctx)

	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parent.ID).
		SetName("nil ws invite group").
		SetInviteCode("nil_ws_code").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	joinRes, err := service.JoinGroupByInvite(ctx, user.ID, "nil_ws_code")
	require.NoError(t, err)
	require.NotNil(t, joinRes)

	resetRes, err := service.ResetInviteCode(ctx, owner.ID, parent.ID)
	require.NoError(t, err)
	require.NotNil(t, resetRes)
}
