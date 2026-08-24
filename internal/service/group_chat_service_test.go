package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/ent/message"
	adaptermocks "AtoiTalkAPI/internal/adapter/mocks"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/model"
	repositorymocks "AtoiTalkAPI/internal/repository/mocks"
	"AtoiTalkAPI/internal/websocket"
	websocketmocks "AtoiTalkAPI/internal/websocket/mocks"
	"context"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newGroupChatServiceTest(t *testing.T) (*GroupChatService, *ent.Client, *repositorymocks.MockGroupMemberReader, *repositorymocks.MockGroupChatReader, *adaptermocks.MockURLGenerator) {
	client := enttest.Open(t, dialect.SQLite, "file:group-chat-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	groupMemberRepo := &repositorymocks.MockGroupMemberReader{}
	groupChatRepo := &repositorymocks.MockGroupChatReader{}
	storage := &adaptermocks.MockURLGenerator{}
	cache := &adaptermocks.MockRedisCache{}
	cache.EXPECT().Del(mock.Anything, mock.Anything).Return(nil).Maybe()
	cache.EXPECT().Exists(mock.Anything, mock.Anything).Return(true, nil).Maybe()
	storage.EXPECT().GetPublicURL(mock.Anything).Return("https://cdn.test/avatar.png").Maybe()
	storage.EXPECT().GetPresignedURL(mock.Anything, mock.Anything).Return("https://cdn.test/presigned", nil).Maybe()

	cfg := &config.AppConfig{}
	validator := config.NewValidator()
	service := NewGroupChatService(client, groupMemberRepo, groupChatRepo, cfg, validator, nil, storage, cache)
	return service, client, groupMemberRepo, groupChatRepo, storage
}

func TestBuildGroupChatListResponseUsesProvidedMemberCount(t *testing.T) {
	service := &GroupChatService{}
	groupID := uuid.New()
	chatID := uuid.New()
	group := &ent.GroupChat{
		ID:     groupID,
		ChatID: chatID,
		Name:   "Alpha Team",
	}
	role := groupmember.RoleMember

	response := service.buildGroupChatListResponse(group, &role, nil, 7)

	require.Equal(t, chatID, response.ID)
	require.Equal(t, "Alpha Team", response.Name)
	require.Equal(t, 7, response.MemberCount)
	require.NotNil(t, response.MyRole)
	require.Equal(t, "member", *response.MyRole)
}

func TestGroupChatServiceGetGroupLastMessageResponse(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team").
		SetInviteCode("invlast123").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	resp := service.getGroupLastMessageResponse(ctx, parentChat.ID)
	require.Nil(t, resp)

	msg := client.Message.Create().
		SetChatID(parentChat.ID).
		SetSenderID(owner.ID).
		SetContent("hello last message").
		SetType(message.TypeRegular).
		SaveX(ctx)

	client.Chat.UpdateOne(parentChat).
		SetLastMessage(msg).
		SetLastMessageAt(msg.CreatedAt).
		ExecX(ctx)

	resp = service.getGroupLastMessageResponse(ctx, parentChat.ID)
	require.NotNil(t, resp)
	require.Equal(t, "hello last message", resp.Content)
}

func TestGroupChatServiceCreateGroupChatSuccessAndBroadcasts(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	member1 := client.User.Create().SetEmail("m1@test.com").SetUsername("m1").SetFullName("Member One").SaveX(ctx)
	member2 := client.User.Create().SetEmail("m2@test.com").SetUsername("m2").SetFullName("Member Two").SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	user1Broadcast := make(chan websocket.Event, 1)
	user2Broadcast := make(chan websocket.Event, 1)

	publisher.EXPECT().BroadcastToUser(owner.ID, mock.Anything).Maybe()

	publisher.EXPECT().BroadcastToUser(member1.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		user1Broadcast <- evt
	}).Maybe()

	publisher.EXPECT().BroadcastToUser(member2.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		user2Broadcast <- evt
	}).Maybe()

	service.wsHub = publisher

	res, err := service.CreateGroupChat(ctx, owner.ID, model.CreateGroupChatRequest{
		Name:      "New Group",
		IsPublic:  true,
		MemberIDs: []uuid.UUID{member1.ID, member2.ID},
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "New Group", res.Name)

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

func TestGroupChatServiceCreateGroupChatWithAvatarAndPrivateExpiry(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	member := client.User.Create().SetEmail("m@test.com").SetUsername("m").SetFullName("Member").SaveX(ctx)
	av := client.Media.Create().
		SetUploaderID(owner.ID).
		SetFileName("avatars/avatar.png").
		SetOriginalName("avatar.png").
		SetCategory(media.CategoryGroupAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetMimeType("image/png").
		SetFileSize(1024).
		SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	publisher.EXPECT().BroadcastToUser(mock.Anything, mock.Anything).Maybe()
	service.wsHub = publisher

	res, err := service.CreateGroupChat(ctx, owner.ID, model.CreateGroupChatRequest{
		Name:          "Private Team",
		IsPublic:      false,
		AvatarMediaID: &av.ID,
		MemberIDs:     []uuid.UUID{member.ID},
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.NotNil(t, res.IsPublic)
	require.False(t, *res.IsPublic)
}

func TestGroupChatServiceCreateGroupChatErrorBranches(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	banned := client.User.Create().SetEmail("banned@test.com").SetUsername("banned").SetFullName("Banned").SetIsBanned(true).SaveX(ctx)

	_, err := service.CreateGroupChat(ctx, owner.ID, model.CreateGroupChatRequest{Name: "", MemberIDs: []uuid.UUID{uuid.New()}})
	require.Error(t, err)

	_, err = service.CreateGroupChat(ctx, owner.ID, model.CreateGroupChatRequest{Name: "Team", MemberIDs: []uuid.UUID{owner.ID}})
	require.Error(t, err)

	_, err = service.CreateGroupChat(ctx, owner.ID, model.CreateGroupChatRequest{Name: "Team", MemberIDs: []uuid.UUID{uuid.New()}})
	require.Error(t, err)

	_, err = service.CreateGroupChat(ctx, owner.ID, model.CreateGroupChatRequest{Name: "Team", MemberIDs: []uuid.UUID{banned.ID}})
	require.Error(t, err)
}

func TestGroupChatServiceCreateGroupChatAllowsExpiredBanUser(t *testing.T) {
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

	publisher := websocketmocks.NewMockPublisher(t)
	publisher.EXPECT().BroadcastToUser(mock.Anything, mock.Anything).Maybe()
	service.wsHub = publisher

	res, err := service.CreateGroupChat(ctx, owner.ID, model.CreateGroupChatRequest{
		Name:      "Team",
		IsPublic:  true,
		MemberIDs: []uuid.UUID{target.ID},
	})
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestGroupChatServiceUpdateGroupChatSuccessAndBroadcasts(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	member := client.User.Create().SetEmail("member@test.com").SetUsername("member").SetFullName("Member").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Old Name").
		SetInviteCode("oldinv123").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
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

	publisher.EXPECT().BroadcastToUser(owner.ID, mock.Anything).Maybe()

	service.wsHub = publisher

	newName := "New Name"
	newDesc := "New Desc"
	res, err := service.UpdateGroupChat(ctx, owner.ID, parentChat.ID, model.UpdateGroupChatRequest{
		Name:        &newName,
		Description: &newDesc,
	}, false)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "New Name", res.Name)

	select {
	case evt := <-chatBroadcast:
		require.Equal(t, websocket.EventMessageNew, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected chat update broadcast")
	}

	select {
	case evt := <-userBroadcast:
		require.Equal(t, websocket.EventChatUpdate, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected user update broadcast")
	}
}

func TestGroupChatServiceUpdateGroupChatVisibilityAndAvatarChanges(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	av1 := client.Media.Create().
		SetUploaderID(owner.ID).
		SetFileName("avatars/av1.png").
		SetOriginalName("av1.png").
		SetCategory(media.CategoryGroupAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetMimeType("image/png").
		SetFileSize(1024).
		SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team").
		SetInviteCode("team_vis").
		SetIsPublic(true).
		SetAvatarID(av1.ID).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	isPriv := false
	res, err := service.UpdateGroupChat(ctx, owner.ID, parentChat.ID, model.UpdateGroupChatRequest{
		IsPublic: &isPriv,
	}, false)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.False(t, *res.IsPublic)

	isPub := true
	res, err = service.UpdateGroupChat(ctx, owner.ID, parentChat.ID, model.UpdateGroupChatRequest{
		IsPublic: &isPub,
	}, false)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, *res.IsPublic)

	res, err = service.UpdateGroupChat(ctx, owner.ID, parentChat.ID, model.UpdateGroupChatRequest{
		DeleteAvatar: true,
	}, false)
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestGroupChatServiceUpdateGroupChatErrorBranches(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	member := client.User.Create().SetEmail("member@test.com").SetUsername("member").SetFullName("Member").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team").
		SetInviteCode("team123").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	_, err := service.UpdateGroupChat(ctx, owner.ID, uuid.New(), model.UpdateGroupChatRequest{}, false)
	require.Error(t, err)

	stranger := client.User.Create().SetEmail("stranger@test.com").SetUsername("stranger").SetFullName("Stranger").SaveX(ctx)
	name := "New"
	_, err = service.UpdateGroupChat(ctx, stranger.ID, parentChat.ID, model.UpdateGroupChatRequest{Name: &name}, false)
	require.Error(t, err)

	_, err = service.UpdateGroupChat(ctx, member.ID, parentChat.ID, model.UpdateGroupChatRequest{Name: &name}, false)
	require.Error(t, err)

	res, err := service.UpdateGroupChat(ctx, owner.ID, parentChat.ID, model.UpdateGroupChatRequest{}, false)
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestGroupChatServiceDeleteGroupSuccessAndBroadcasts(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	member := client.User.Create().SetEmail("member@test.com").SetUsername("member").SetFullName("Member").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team").
		SetInviteCode("del123").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	userBroadcast := make(chan websocket.Event, 1)

	publisher.EXPECT().BroadcastToUser(member.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		userBroadcast <- evt
	}).Maybe()

	publisher.EXPECT().BroadcastToUser(owner.ID, mock.Anything).Maybe()

	service.wsHub = publisher

	err := service.DeleteGroup(ctx, owner.ID, parentChat.ID, false)
	require.NoError(t, err)

	select {
	case evt := <-userBroadcast:
		require.Equal(t, websocket.EventChatDelete, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected delete broadcast")
	}
}

func TestGroupChatServiceDeleteGroupErrorBranches(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	member := client.User.Create().SetEmail("member@test.com").SetUsername("member").SetFullName("Member").SaveX(ctx)
	stranger := client.User.Create().SetEmail("stranger@test.com").SetUsername("stranger").SetFullName("Stranger").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Team").
		SetInviteCode("delerr123").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	err := service.DeleteGroup(ctx, owner.ID, uuid.New(), false)
	require.Error(t, err)

	err = service.DeleteGroup(ctx, stranger.ID, parentChat.ID, false)
	require.Error(t, err)

	err = service.DeleteGroup(ctx, member.ID, parentChat.ID, false)
	require.Error(t, err)
}

func TestGroupChatServiceCreateGroupChatCardinalityAndDuplicates(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner@test.com").SetUsername("owner").SetFullName("Owner").SaveX(ctx)
	target := client.User.Create().SetEmail("target@test.com").SetUsername("target").SetFullName("Target").SaveX(ctx)

	_, err := service.CreateGroupChat(ctx, owner.ID, model.CreateGroupChatRequest{
		Name:      "Duplicate Team",
		MemberIDs: []uuid.UUID{target.ID, target.ID},
	})
	require.Error(t, err)

	tooMany := make([]uuid.UUID, 101)
	for i := range tooMany {
		tooMany[i] = uuid.New()
	}
	_, err = service.CreateGroupChat(ctx, owner.ID, model.CreateGroupChatRequest{
		Name:      "Too Many Team",
		MemberIDs: tooMany,
	})
	require.Error(t, err)
}

func TestGroupChatServiceCancelledContextBranches(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := service.CreateGroupChat(ctx, uuid.New(), model.CreateGroupChatRequest{Name: "Team", MemberIDs: []uuid.UUID{uuid.New()}})
	require.Error(t, err)

	_, err = service.UpdateGroupChat(ctx, uuid.New(), uuid.New(), model.UpdateGroupChatRequest{}, false)
	require.Error(t, err)

	err = service.DeleteGroup(ctx, uuid.New(), uuid.New(), false)
	require.Error(t, err)
}

func TestGroupChatServiceCreateGroupChatWithAvatarAndBroadcasts(t *testing.T) {
	service, client, _, _, storage := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner_cr_av@test.com").SetUsername("owner_cr_av").SetFullName("Owner Cr Av").SaveX(ctx)
	target := client.User.Create().SetEmail("target_cr_av@test.com").SetUsername("target_cr_av").SetFullName("Target Cr Av").SaveX(ctx)

	av := client.Media.Create().
		SetUploaderID(owner.ID).
		SetFileName("avatars/cr_group.png").
		SetOriginalName("cr_group.png").
		SetCategory(media.CategoryGroupAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetMimeType("image/png").
		SetFileSize(1024).
		SaveX(ctx)

	storage.EXPECT().GetPublicURL("avatars/cr_group.png").Return("https://cdn.example.com/avatars/cr_group.png").Maybe()

	publisher := websocketmocks.NewMockPublisher(t)
	ownerDone := make(chan struct{}, 1)
	targetDone := make(chan struct{}, 1)

	publisher.EXPECT().BroadcastToUser(owner.ID, mock.Anything).Run(func(_ uuid.UUID, _ websocket.Event) {
		ownerDone <- struct{}{}
	}).Once()

	publisher.EXPECT().BroadcastToUser(target.ID, mock.Anything).Run(func(_ uuid.UUID, _ websocket.Event) {
		targetDone <- struct{}{}
	}).Once()

	service.wsHub = publisher

	desc := "Team with avatar"
	res, err := service.CreateGroupChat(ctx, owner.ID, model.CreateGroupChatRequest{
		Name:          "Avatar Team",
		Description:   desc,
		AvatarMediaID: &av.ID,
		MemberIDs:     []uuid.UUID{target.ID},
		IsPublic:      true,
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "https://cdn.test/avatar.png", res.Avatar)

	select {
	case <-ownerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("expected broadcast to owner")
	}

	select {
	case <-targetDone:
	case <-time.After(2 * time.Second):
		t.Fatal("expected broadcast to target")
	}
}

func TestGroupChatServiceDeleteGroupByAdminWithBroadcasts(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	adminUser := client.User.Create().SetEmail("admin_del@test.com").SetUsername("admin_del").SetFullName("Admin Del").SaveX(ctx)
	owner := client.User.Create().SetEmail("owner_del_ad@test.com").SetUsername("owner_del_ad").SetFullName("Owner Del Ad").SaveX(ctx)
	member := client.User.Create().SetEmail("member_del_ad@test.com").SetUsername("member_del_ad").SetFullName("Member Del Ad").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Admin Delete Team").
		SetInviteCode("adm_del123").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(member.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	userBroadcast := make(chan websocket.Event, 2)

	publisher.EXPECT().BroadcastToUser(owner.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		userBroadcast <- evt
	}).Maybe()

	publisher.EXPECT().BroadcastToUser(member.ID, mock.Anything).Run(func(_ uuid.UUID, evt websocket.Event) {
		userBroadcast <- evt
	}).Maybe()

	service.wsHub = publisher

	err := service.DeleteGroup(ctx, adminUser.ID, parentChat.ID, true)
	require.NoError(t, err)

	select {
	case evt := <-userBroadcast:
		require.Equal(t, websocket.EventChatDelete, evt.Type)
	case <-time.After(2 * time.Second):
		t.Fatal("expected delete broadcast")
	}
}

func TestGroupChatServiceUpdateGroupChatSwitchPublicToPrivate(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("pub_to_priv@test.com").SetUsername("pub_to_priv").SaveX(ctx)
	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Pub to Priv Group").
		SetInviteCode("orig-pub-code").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	isPub := false
	res, err := service.UpdateGroupChat(ctx, owner.ID, parentChat.ID, model.UpdateGroupChatRequest{
		IsPublic: &isPub,
	}, false)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.False(t, *res.IsPublic)
	require.NotEmpty(t, res.InviteCode)
	require.NotNil(t, res.InviteExpiresAt)

	updated, err := client.GroupChat.Get(ctx, group.ID)
	require.NoError(t, err)
	require.False(t, updated.IsPublic)
	require.NotNil(t, updated.InviteExpiresAt)
	require.NotEqual(t, "orig-pub-code", updated.InviteCode)
}

func TestGroupChatServiceUpdateGroupChatAddAndChangeAvatar(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("avatar_owner@test.com").SetUsername("avatar_owner").SaveX(ctx)
	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Avatar Group").
		SetInviteCode("avatar_code").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	av1 := client.Media.Create().
		SetUploaderID(owner.ID).
		SetFileName("avatars/av1.png").
		SetOriginalName("av1.png").
		SetCategory(media.CategoryGroupAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetMimeType("image/png").
		SetFileSize(1024).
		SaveX(ctx)

	res, err := service.UpdateGroupChat(ctx, owner.ID, parentChat.ID, model.UpdateGroupChatRequest{
		AvatarMediaID: &av1.ID,
	}, false)
	require.NoError(t, err)
	require.NotNil(t, res)

	av2 := client.Media.Create().
		SetUploaderID(owner.ID).
		SetFileName("avatars/av2.png").
		SetOriginalName("av2.png").
		SetCategory(media.CategoryGroupAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetMimeType("image/png").
		SetFileSize(2048).
		SaveX(ctx)

	res, err = service.UpdateGroupChat(ctx, owner.ID, parentChat.ID, model.UpdateGroupChatRequest{
		AvatarMediaID: &av2.ID,
	}, false)
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestGroupChatServiceUpdateGroupChatDescriptionOnlyAndAdmin(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("desc_owner@test.com").SetUsername("desc_owner").SaveX(ctx)
	adminUser := client.User.Create().SetEmail("admin_user@test.com").SetUsername("admin_user").SaveX(ctx)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Desc Group").
		SetInviteCode("desc_code").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	newDesc := "Updated Description"
	res, err := service.UpdateGroupChat(ctx, adminUser.ID, parentChat.ID, model.UpdateGroupChatRequest{
		Description: &newDesc,
	}, true)
	require.NoError(t, err)
	require.NotNil(t, res)
}

func TestGroupChatServiceCreateAndUpdateGroupChatInvalidAvatarMedia(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("inv_av_owner@test.com").SetUsername("inv_av_owner").SaveX(ctx)
	member := client.User.Create().SetEmail("inv_av_mem@test.com").SetUsername("inv_av_mem").SaveX(ctx)
	invalidAvatarID := uuid.New()

	res, err := service.CreateGroupChat(ctx, owner.ID, model.CreateGroupChatRequest{
		Name:          "Inv Avatar Group",
		MemberIDs:     []uuid.UUID{member.ID},
		AvatarMediaID: &invalidAvatarID,
	})
	require.Nil(t, res)
	require.Error(t, err)

	parentChat := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parentChat.ID).
		SetName("Update Inv Av Group").
		SetInviteCode("upd_inv_code").
		SetIsPublic(true).
		SetCreatorID(owner.ID).
		SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(owner.ID).SetRole(groupmember.RoleOwner).SaveX(ctx)

	updRes, updErr := service.UpdateGroupChat(ctx, owner.ID, parentChat.ID, model.UpdateGroupChatRequest{
		AvatarMediaID: &invalidAvatarID,
	}, false)
	require.Nil(t, updRes)
	require.Error(t, updErr)
}
