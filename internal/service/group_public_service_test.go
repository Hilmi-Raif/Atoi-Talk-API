package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/media"
	adaptermocks "AtoiTalkAPI/internal/adapter/mocks"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/websocket"
	websocketmocks "AtoiTalkAPI/internal/websocket/mocks"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestGroupChatServiceSearchPublicGroupsMapsResults(t *testing.T) {
	service, client, groupMemberRepo, groupChatRepo, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(parent.ID).SetName("public group").SetIsPublic(true).SetInviteCode("pub123").SaveX(ctx)
	user := client.User.Create().SetUsername("pub_user").SetFullName("Pub User").SaveX(ctx)

	groupChatRepo.EXPECT().SearchPublicGroups(mock.Anything, "public", "", 10, "name").Return([]*ent.GroupChat{group}, "next-cursor", true, nil)
	groupMemberRepo.EXPECT().CountActiveMembersByGroupIDs(mock.Anything, group.ID).Return(map[uuid.UUID]int{group.ID: 4}, nil)

	req := model.SearchPublicGroupsRequest{
		Query:  "public",
		Cursor: "",
		Limit:  10,
		SortBy: "name",
	}
	resp, nextCursor, hasMore, err := service.SearchPublicGroups(ctx, user.ID, req)
	require.NoError(t, err)
	require.Len(t, resp, 1)
	require.Equal(t, group.ID, resp[0].ID)
	require.Equal(t, group.ChatID, resp[0].ChatID)
	require.Equal(t, 4, resp[0].MemberCount)
	require.False(t, resp[0].IsMember)
	require.Equal(t, "next-cursor", nextCursor)
	require.True(t, hasMore)
}

func TestGroupChatServiceSearchPublicGroupsMarksJoinedGroups(t *testing.T) {
	service, client, groupMemberRepo, groupChatRepo, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(parent.ID).SetName("public group").SetIsPublic(true).SetInviteCode("pub1234").SaveX(ctx)
	user := client.User.Create().SetUsername("joined_user").SetFullName("Joined User").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(user.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	groupChatRepo.EXPECT().SearchPublicGroups(mock.Anything, "", "", 20, "name").Return([]*ent.GroupChat{group}, "", false, nil)
	groupMemberRepo.EXPECT().CountActiveMembersByGroupIDs(mock.Anything, group.ID).Return(map[uuid.UUID]int{group.ID: 1}, nil)

	resp, _, _, err := service.SearchPublicGroups(ctx, user.ID, model.SearchPublicGroupsRequest{})
	require.NoError(t, err)
	require.Len(t, resp, 1)
	require.True(t, resp[0].IsMember)
}

func TestGroupChatServiceSearchPublicGroupsReturnsRepoError(t *testing.T) {
	service, client, _, groupChatRepo, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	user := client.User.Create().SetUsername("err_user").SetFullName("Err User").SaveX(ctx)

	groupChatRepo.EXPECT().SearchPublicGroups(mock.Anything, "", "", 20, "name").Return(nil, "", false, errors.New("db error"))

	_, _, _, err := service.SearchPublicGroups(ctx, user.ID, model.SearchPublicGroupsRequest{})
	require.Error(t, err)
}

func TestGroupChatServiceSearchPublicGroupsInvalidCursorAndCountFallback(t *testing.T) {
	service, client, groupMemberRepo, groupChatRepo, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	user := client.User.Create().SetUsername("user_cur").SetFullName("User Cur").SaveX(ctx)

	groupChatRepo.EXPECT().SearchPublicGroups(mock.Anything, "", "bad", 20, "name").Return(nil, "", false, errors.New("invalid cursor format"))

	_, _, _, err := service.SearchPublicGroups(ctx, user.ID, model.SearchPublicGroupsRequest{Cursor: "bad"})
	require.Error(t, err)

	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(parent.ID).SetName("public group").SetIsPublic(true).SetInviteCode("pubfall").SaveX(ctx)
	groupChatRepo.EXPECT().SearchPublicGroups(mock.Anything, "", "", 20, "name").Return([]*ent.GroupChat{group}, "", false, nil)
	groupMemberRepo.EXPECT().CountActiveMembersByGroupIDs(mock.Anything, group.ID).Return(nil, errors.New("count failed"))

	resp, _, _, err := service.SearchPublicGroups(ctx, user.ID, model.SearchPublicGroupsRequest{})
	require.NoError(t, err)
	require.Len(t, resp, 1)
	require.Equal(t, 0, resp[0].MemberCount)
}

func TestGroupChatServiceJoinPublicGroupSuccess(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	client.GroupChat.Create().SetChatID(parent.ID).SetName("pub join").SetIsPublic(true).SetInviteCode("joinpub").SaveX(ctx)
	user := client.User.Create().SetUsername("pub_joiner").SetFullName("Pub Joiner").SaveX(ctx)
	redisCache := service.redisAdapter.(*adaptermocks.MockRedisCache)
	redisCache.EXPECT().Del(mock.Anything, "chat_members:"+parent.ID.String()).Return(nil)

	resp, err := service.JoinPublicGroup(ctx, user.ID, parent.ID)
	require.NoError(t, err)
	require.Equal(t, parent.ID, resp.ID)
	require.NotNil(t, resp.LastMessage)
}

func TestGroupChatServiceJoinPublicGroupRejectsPrivateGroup(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	client.GroupChat.Create().SetChatID(parent.ID).SetName("priv group").SetIsPublic(false).SetInviteCode("priv123").SaveX(ctx)
	user := client.User.Create().SetUsername("priv_joiner").SetFullName("Priv Joiner").SaveX(ctx)

	_, err := service.JoinPublicGroup(ctx, user.ID, parent.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "private")
}

func TestGroupChatServiceJoinPublicGroupRejectsExistingMember(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	groupEntity := client.GroupChat.Create().SetChatID(parent.ID).SetName("exist group").SetIsPublic(true).SetInviteCode("exist123").SaveX(ctx)
	user := client.User.Create().SetUsername("exist_user").SetFullName("Exist User").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(groupEntity.ID).SetUserID(user.ID).SetRole(groupmember.RoleMember).SaveX(ctx)

	_, err := service.JoinPublicGroup(ctx, user.ID, parent.ID)
	require.Error(t, err)
	require.Contains(t, err.Error(), "already a member")
}

func TestGroupChatServiceJoinPublicGroupNotFound(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	user := client.User.Create().SetUsername("user_join_nf2").SetFullName("User").SaveX(ctx)

	_, err := service.JoinPublicGroup(ctx, user.ID, uuid.New())
	require.Error(t, err)
}

func TestGroupChatServiceJoinPublicGroupBroadcasts(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	client.GroupChat.Create().SetChatID(parent.ID).SetName("broadcast group").SetIsPublic(true).SetInviteCode("broad123").SaveX(ctx)
	user := client.User.Create().SetUsername("broad_user").SetFullName("Broad User").SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	chatDone := make(chan struct{}, 1)
	userDone := make(chan struct{}, 1)

	publisher.EXPECT().BroadcastToUser(user.ID, mock.MatchedBy(func(evt websocket.Event) bool {
		return evt.Type == websocket.EventChatNew
	})).Run(func(_ uuid.UUID, _ websocket.Event) {
		userDone <- struct{}{}
	}).Once()

	publisher.EXPECT().BroadcastToChat(parent.ID, mock.MatchedBy(func(evt websocket.Event) bool {
		return evt.Type == websocket.EventMessageNew
	})).Run(func(_ uuid.UUID, _ websocket.Event) {
		chatDone <- struct{}{}
	}).Once()

	service.wsHub = publisher

	_, err := service.JoinPublicGroup(ctx, user.ID, parent.ID)
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

func TestGroupChatServicePublicCancelledContext(t *testing.T) {
	service, client, _, groupChatRepo, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	groupChatRepo.EXPECT().SearchPublicGroups(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, "", false, context.Canceled).Maybe()

	_, _, _, err := service.SearchPublicGroups(ctx, uuid.New(), model.SearchPublicGroupsRequest{})
	require.Error(t, err)

	_, err = service.JoinPublicGroup(ctx, uuid.New(), uuid.New())
	require.Error(t, err)
}

func TestGroupChatServiceJoinPublicGroupWithAvatarAndBroadcasts(t *testing.T) {
	service, client, _, _, storage := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	owner := client.User.Create().SetEmail("owner_pub@test.com").SetUsername("owner_pub").SetFullName("Owner Pub").SaveX(ctx)
	user := client.User.Create().SetEmail("joiner_pub@test.com").SetUsername("joiner_pub").SetFullName("Joiner Pub").SaveX(ctx)

	av := client.Media.Create().
		SetUploaderID(owner.ID).
		SetFileName("avatars/pub_group.png").
		SetOriginalName("pub_group.png").
		SetCategory(media.CategoryGroupAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetMimeType("image/png").
		SetFileSize(1024).
		SaveX(ctx)

	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parent.ID).
		SetName("public group with avatar").
		SetIsPublic(true).
		SetInviteCode("pub_av123").
		SetAvatarID(av.ID).
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().
		SetGroupChatID(group.ID).
		SetUserID(owner.ID).
		SetRole(groupmember.RoleOwner).
		SaveX(ctx)

	storage.EXPECT().GetPublicURL("avatars/pub_group.png").Return("https://cdn.example.com/avatars/pub_group.png").Maybe()

	publisher := websocketmocks.NewMockPublisher(t)
	chatDone := make(chan struct{}, 1)
	userDone := make(chan struct{}, 1)

	publisher.EXPECT().BroadcastToUser(user.ID, mock.Anything).Run(func(_ uuid.UUID, _ websocket.Event) {
		userDone <- struct{}{}
	}).Once()

	publisher.EXPECT().BroadcastToChat(parent.ID, mock.Anything).Run(func(_ uuid.UUID, _ websocket.Event) {
		chatDone <- struct{}{}
	}).Once()

	service.wsHub = publisher

	res, err := service.JoinPublicGroup(ctx, user.ID, parent.ID)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "https://cdn.test/avatar.png", res.Avatar)

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

func TestGroupChatServiceJoinPublicGroupWithWsHubNil(t *testing.T) {
	service, client, _, _, _ := newGroupChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	service.wsHub = nil

	owner := client.User.Create().SetEmail("owner_wsnil@test.com").SetUsername("owner_wsnil").SetFullName("Owner Wsnil").SaveX(ctx)
	user := client.User.Create().SetEmail("joiner_wsnil@test.com").SetUsername("joiner_wsnil").SetFullName("Joiner Wsnil").SaveX(ctx)

	parent := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(parent.ID).
		SetName("public group wsnil").
		SetIsPublic(true).
		SetInviteCode("pub_wsnil123").
		SetCreatorID(owner.ID).
		SaveX(ctx)

	client.GroupMember.Create().
		SetGroupChatID(group.ID).
		SetUserID(owner.ID).
		SetRole(groupmember.RoleOwner).
		SaveX(ctx)

	res, err := service.JoinPublicGroup(ctx, user.ID, parent.ID)
	require.NoError(t, err)
	require.NotNil(t, res)
}
