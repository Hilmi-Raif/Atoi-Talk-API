package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/ent/user"
	adaptermocks "AtoiTalkAPI/internal/adapter/mocks"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/model"
	repositorymocks "AtoiTalkAPI/internal/repository/mocks"
	"AtoiTalkAPI/internal/websocket"
	websocketmocks "AtoiTalkAPI/internal/websocket/mocks"
	"context"
	"errors"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func newMockUserService(t *testing.T, reader *repositorymocks.MockUserReader) *UserService {
	t.Helper()
	return NewUserService(
		nil,
		reader,
		&config.AppConfig{},
		config.NewValidator(),
		adaptermocks.NewMockPublicURLGenerator(t),
		nil,
		adaptermocks.NewMockRedisPresence(t),
	)
}

func newEntUserService(t *testing.T) (*UserService, *ent.Client, *adaptermocks.MockPublicURLGenerator, *adaptermocks.MockRedisPresence) {
	t.Helper()
	client := enttest.Open(t, dialect.SQLite, "file:user-service-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	storage := adaptermocks.NewMockPublicURLGenerator(t)
	presence := adaptermocks.NewMockRedisPresence(t)
	service := NewUserService(client, nil, &config.AppConfig{}, config.NewValidator(), storage, nil, presence)
	return service, client, storage, presence
}

func TestUserServiceGetCurrentUserMapsProfileAndPresence(t *testing.T) {
	service, client, _, presence := newEntUserService(t)
	defer client.Close()
	ctx := context.Background()
	username := "current"
	fullName := "Current User"
	email := "current@example.com"
	bio := "about me"
	password := "hash"
	userEntity := client.User.Create().
		SetUsername(username).
		SetFullName(fullName).
		SetEmail(email).
		SetBio(bio).
		SetPasswordHash(password).
		SaveX(ctx)
	presence.EXPECT().Exists(mock.Anything, "online:"+userEntity.ID.String()).Return(true, nil)

	result, err := service.GetCurrentUser(ctx, userEntity.ID)

	require.NoError(t, err)
	require.Equal(t, userEntity.ID, result.ID)
	require.Equal(t, username, result.Username)
	require.Equal(t, fullName, result.FullName)
	require.Equal(t, email, result.Email)
	require.Equal(t, bio, result.Bio)
	require.True(t, result.HasPassword)
	require.NotNil(t, result.IsOnline)
	require.True(t, *result.IsOnline)
}

func TestUserServiceGetCurrentUserReturnsNotFound(t *testing.T) {
	service, client, _, _ := newEntUserService(t)
	defer client.Close()

	result, err := service.GetCurrentUser(context.Background(), uuid.New())

	require.Nil(t, result)
	require.Error(t, err)
}

func TestUserServiceGetUserProfileHidesPresenceWhenBlocked(t *testing.T) {
	service, client, storage, presence := newEntUserService(t)
	defer client.Close()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SaveX(ctx)
	lastSeen := time.Now().UTC().Add(-time.Minute)
	target := client.User.Create().SetUsername("target").SetFullName("Target").SetLastSeenAt(lastSeen).SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(current.ID).SetBlockedID(target.ID).SaveX(ctx)
	presence.EXPECT().Exists(mock.Anything, "online:"+target.ID.String()).Return(true, nil)

	result, err := service.GetUserProfile(ctx, current.ID, target.ID)

	require.NoError(t, err)
	require.Equal(t, target.ID, result.ID)
	require.NotNil(t, result.IsBlockedByMe)
	require.True(t, *result.IsBlockedByMe)
	require.NotNil(t, result.IsBlockedByOther)
	require.False(t, *result.IsBlockedByOther)
	require.NotNil(t, result.IsOnline)
	require.False(t, *result.IsOnline)
	require.Nil(t, result.LastSeenAt)
	storage.AssertNotCalled(t, "GetPublicURL", mock.Anything)
}

func TestUserServiceGetUserProfileReturnsNotFound(t *testing.T) {
	service, client, _, _ := newEntUserService(t)
	defer client.Close()

	result, err := service.GetUserProfile(context.Background(), uuid.New(), uuid.New())

	require.Nil(t, result)
	require.Error(t, err)
}

func TestUserServiceGetUserProfileMapsVisibleUserDetails(t *testing.T) {
	service, client, storage, presence := newEntUserService(t)
	defer client.Close()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SaveX(ctx)
	lastSeen := time.Now().UTC().Add(-time.Minute)
	target := client.User.Create().
		SetUsername("target").
		SetFullName("Target").
		SetBio("profile").
		SetLastSeenAt(lastSeen).
		SaveX(ctx)
	avatar := client.Media.Create().
		SetFileName("avatars/target.png").
		SetOriginalName("target.png").
		SetFileSize(10).
		SetMimeType("image/png").
		SetUploaderID(target.ID).
		SaveX(ctx)
	target = client.User.UpdateOne(target).SetAvatar(avatar).SaveX(ctx)
	storage.EXPECT().GetPublicURL("avatars/target.png").Return("https://cdn.test/target.png")
	presence.EXPECT().Exists(mock.Anything, "online:"+target.ID.String()).Return(true, nil)

	result, err := service.GetUserProfile(ctx, current.ID, target.ID)

	require.NoError(t, err)
	require.Equal(t, "target", result.Username)
	require.Equal(t, "Target", result.FullName)
	require.Equal(t, "profile", result.Bio)
	require.Equal(t, "https://cdn.test/target.png", result.Avatar)
	require.NotNil(t, result.IsOnline)
	require.True(t, *result.IsOnline)
	require.NotNil(t, result.LastSeenAt)
}

func TestUserServiceSearchUsersMapsResults(t *testing.T) {
	reader := repositorymocks.NewMockUserReader(t)
	storage := adaptermocks.NewMockPublicURLGenerator(t)
	presence := adaptermocks.NewMockRedisPresence(t)
	currentUserID := uuid.New()
	userID := uuid.New()
	username := "target"
	fullName := "Target User"
	bio := "hello"
	avatar := &ent.Media{FileName: "avatars/target.png"}
	reader.EXPECT().SearchUsers(mock.Anything, currentUserID, "target", "", 10, (*uuid.UUID)(nil)).Return([]*ent.User{{
		ID:       userID,
		Username: &username,
		FullName: &fullName,
		Bio:      &bio,
		Edges:    ent.UserEdges{Avatar: avatar},
	}}, "next", true, nil)
	storage.EXPECT().GetPublicURL("avatars/target.png").Return("https://cdn.test/target.png")

	service := NewUserService(nil, reader, &config.AppConfig{}, config.NewValidator(), storage, nil, presence)
	users, cursor, hasNext, err := service.SearchUsers(context.Background(), currentUserID, model.SearchUserRequest{Query: "target", Limit: 10})

	require.NoError(t, err)
	require.Equal(t, "next", cursor)
	require.True(t, hasNext)
	require.Len(t, users, 1)
	require.Equal(t, userID, users[0].ID)
	require.Equal(t, "target", users[0].Username)
	require.Equal(t, "https://cdn.test/target.png", users[0].Avatar)
}

func TestUserServiceSearchUsersIncludesExistingPrivateChat(t *testing.T) {
	reader := repositorymocks.NewMockUserReader(t)
	storage := adaptermocks.NewMockPublicURLGenerator(t)
	presence := adaptermocks.NewMockRedisPresence(t)
	service, client, _, _ := newEntUserService(t)
	defer client.Close()
	service.userRepo = reader
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SaveX(ctx)
	target := client.User.Create().SetUsername("target").SaveX(ctx)
	privateChat := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(privateChat.ID).SetUser1ID(current.ID).SetUser2ID(target.ID).SaveX(ctx)
	reader.EXPECT().SearchUsers(mock.Anything, current.ID, "target", "", 10, (*uuid.UUID)(nil)).Return([]*ent.User{target}, "", false, nil)

	result, _, _, err := service.SearchUsers(ctx, current.ID, model.SearchUserRequest{Query: "target", Limit: 10, IncludeChatID: true})

	require.NoError(t, err)
	require.Len(t, result, 1)
	require.NotNil(t, result[0].PrivateChatID)
	require.Equal(t, privateChat.ID, *result[0].PrivateChatID)
	storage.AssertNotCalled(t, "GetPublicURL", mock.Anything)
	presence.AssertNotCalled(t, "Exists", mock.Anything, mock.Anything)
}

func TestUserServiceSearchUsersRejectsInvalidCursorAndRepositoryErrors(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "invalid cursor", err: errors.New("invalid cursor format")},
		{name: "repository failure", err: errors.New("database unavailable")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := repositorymocks.NewMockUserReader(t)
			reader.EXPECT().SearchUsers(mock.Anything, mock.Anything, "", "bad", 10, (*uuid.UUID)(nil)).Return(nil, "", false, tt.err)
			service := newMockUserService(t, reader)

			users, cursor, hasNext, err := service.SearchUsers(context.Background(), uuid.New(), model.SearchUserRequest{Cursor: "bad", Limit: 10})

			require.Nil(t, users)
			require.Empty(t, cursor)
			require.False(t, hasNext)
			require.Error(t, err)
		})
	}
}

func TestUserServiceSearchUsersRejectsInvalidRequest(t *testing.T) {
	reader := repositorymocks.NewMockUserReader(t)
	service := newMockUserService(t, reader)

	users, cursor, hasNext, err := service.SearchUsers(context.Background(), uuid.New(), model.SearchUserRequest{Limit: 51})

	require.Nil(t, users)
	require.Empty(t, cursor)
	require.False(t, hasNext)
	require.Error(t, err)
}

func TestUserServiceGetBlockedUsersMapsResults(t *testing.T) {
	reader := repositorymocks.NewMockUserReader(t)
	storage := adaptermocks.NewMockPublicURLGenerator(t)
	presence := adaptermocks.NewMockRedisPresence(t)
	currentUserID := uuid.New()
	blockedID := uuid.New()
	username := "blocked"
	fullName := "Blocked User"
	reader.EXPECT().GetBlockedUsers(mock.Anything, currentUserID, "", "", 10).Return([]*ent.User{{
		ID:       blockedID,
		Username: &username,
		FullName: &fullName,
	}}, "next", true, nil)

	service := NewUserService(nil, reader, &config.AppConfig{}, config.NewValidator(), storage, nil, presence)
	users, cursor, hasNext, err := service.GetBlockedUsers(context.Background(), currentUserID, model.GetBlockedUsersRequest{Limit: 10})

	require.NoError(t, err)
	require.Equal(t, "next", cursor)
	require.True(t, hasNext)
	require.Len(t, users, 1)
	require.Equal(t, blockedID, users[0].ID)
	require.NotNil(t, users[0].IsBlockedByMe)
	require.True(t, *users[0].IsBlockedByMe)
}

func TestUserServiceGetBlockedUsersHandlesRepositoryErrors(t *testing.T) {
	reader := repositorymocks.NewMockUserReader(t)
	reader.EXPECT().GetBlockedUsers(mock.Anything, mock.Anything, "", "bad", 10).Return(nil, "", false, errors.New("invalid cursor format"))
	service := newMockUserService(t, reader)

	users, cursor, hasNext, err := service.GetBlockedUsers(context.Background(), uuid.New(), model.GetBlockedUsersRequest{Cursor: "bad", Limit: 10})

	require.Nil(t, users)
	require.Empty(t, cursor)
	require.False(t, hasNext)
	require.Error(t, err)
}

func TestUserServiceGetBlockedUsersMapsAvatar(t *testing.T) {
	reader := repositorymocks.NewMockUserReader(t)
	storage := adaptermocks.NewMockPublicURLGenerator(t)
	presence := adaptermocks.NewMockRedisPresence(t)
	blockedID := uuid.New()
	username := "blocked"
	avatar := &ent.Media{FileName: "avatars/blocked.png"}
	reader.EXPECT().GetBlockedUsers(mock.Anything, mock.Anything, "", "", 10).Return([]*ent.User{{
		ID:       blockedID,
		Username: &username,
		Edges:    ent.UserEdges{Avatar: avatar},
	}}, "", false, nil)
	storage.EXPECT().GetPublicURL("avatars/blocked.png").Return("https://cdn.test/blocked.png")
	service := NewUserService(nil, reader, &config.AppConfig{}, config.NewValidator(), storage, nil, presence)

	users, _, _, err := service.GetBlockedUsers(context.Background(), uuid.New(), model.GetBlockedUsersRequest{Limit: 10})

	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Equal(t, "https://cdn.test/blocked.png", users[0].Avatar)
}

func TestUserServiceUpdateProfileReturnsCurrentProfileWithoutChanges(t *testing.T) {
	service, client, storage, presence := newEntUserService(t)
	defer client.Close()
	ctx := context.Background()
	lastSeen := time.Now().UTC().Add(-time.Minute)
	userEntity := client.User.Create().
		SetUsername("current").
		SetFullName("Current User").
		SetEmail("current@example.com").
		SetBio("bio").
		SetPasswordHash("hash").
		SetLastSeenAt(lastSeen).
		SaveX(ctx)
	presence.EXPECT().Exists(mock.Anything, "online:"+userEntity.ID.String()).Return(true, nil)

	result, err := service.UpdateProfile(ctx, userEntity.ID, model.UpdateProfileRequest{FullName: "Current User", Bio: "bio"})

	require.NoError(t, err)
	require.Equal(t, userEntity.ID, result.ID)
	require.Equal(t, "current", result.Username)
	require.Equal(t, "Current User", result.FullName)
	require.Equal(t, "bio", result.Bio)
	require.True(t, result.HasPassword)
	require.NotNil(t, result.LastSeenAt)
	storage.AssertNotCalled(t, "GetPublicURL", mock.Anything)
}

func TestUserServiceUpdateProfileUpdatesTextFields(t *testing.T) {
	service, client, storage, presence := newEntUserService(t)
	defer client.Close()
	ctx := context.Background()
	userEntity := client.User.Create().
		SetUsername("oldname").
		SetFullName("Old Name").
		SetBio("old bio").
		SaveX(ctx)
	presence.EXPECT().Exists(mock.Anything, "online:"+userEntity.ID.String()).Return(false, nil)

	result, err := service.UpdateProfile(ctx, userEntity.ID, model.UpdateProfileRequest{
		Username: "newname",
		FullName: " New Name ",
		Bio:      " new bio ",
	})

	require.NoError(t, err)
	require.Equal(t, "newname", result.Username)
	require.Equal(t, "New Name", result.FullName)
	require.Equal(t, "new bio", result.Bio)
	storage.AssertNotCalled(t, "GetPublicURL", mock.Anything)

	updated := client.User.GetX(ctx, userEntity.ID)
	require.Equal(t, "newname", *updated.Username)
	require.Equal(t, "New Name", *updated.FullName)
	require.Equal(t, "new bio", *updated.Bio)
}

func TestUserServiceUpdateProfileRejectsInvalidAvatarAndConflictingAvatarOptions(t *testing.T) {
	service, client, _, presence := newEntUserService(t)
	defer client.Close()
	ctx := context.Background()
	userEntity := client.User.Create().SetUsername("current").SetFullName("Current User").SaveX(ctx)
	avatarID := uuid.New()
	presence.EXPECT().Exists(mock.Anything, "online:"+userEntity.ID.String()).Return(false, nil)

	result, err := service.UpdateProfile(ctx, userEntity.ID, model.UpdateProfileRequest{
		FullName:      "Current User",
		AvatarMediaID: &avatarID,
	})
	require.Nil(t, result)
	require.Error(t, err)

	result, err = service.UpdateProfile(ctx, userEntity.ID, model.UpdateProfileRequest{
		FullName:      "Current User",
		AvatarMediaID: &avatarID,
		DeleteAvatar:  true,
	})
	require.Nil(t, result)
	require.Error(t, err)
}

func TestUserServiceUpdateProfileRejectsInvalidRequestBeforeDatabaseWork(t *testing.T) {
	service, client, _, presence := newEntUserService(t)
	defer client.Close()

	result, err := service.UpdateProfile(context.Background(), uuid.New(), model.UpdateProfileRequest{
		FullName:     "ab",
		DeleteAvatar: true,
		AvatarMediaID: func() *uuid.UUID {
			id := uuid.New()
			return &id
		}(),
	})

	require.Nil(t, result)
	require.Error(t, err)
	presence.AssertNotCalled(t, "Exists", mock.Anything, mock.Anything)
}

func TestUserServiceUpdateProfileSetsAndDeletesAvatar(t *testing.T) {
	service, client, storage, presence := newEntUserService(t)
	defer client.Close()
	ctx := context.Background()
	userEntity := client.User.Create().SetUsername("avatar-user").SetFullName("Avatar User").SaveX(ctx)
	avatar := client.Media.Create().
		SetFileName("avatars/new.png").
		SetOriginalName("new.png").
		SetFileSize(10).
		SetMimeType("image/png").
		SetCategory(media.CategoryUserAvatar).
		SetUploadStatus(media.UploadStatusCompleted).
		SetUploaderID(userEntity.ID).
		SaveX(ctx)
	presence.EXPECT().Exists(mock.Anything, "online:"+userEntity.ID.String()).Return(false, nil).Twice()
	storage.EXPECT().GetPublicURL("avatars/new.png").Return("https://cdn.test/new.png")

	result, err := service.UpdateProfile(ctx, userEntity.ID, model.UpdateProfileRequest{FullName: "Avatar User", AvatarMediaID: &avatar.ID})
	require.NoError(t, err)
	require.Equal(t, "https://cdn.test/new.png", result.Avatar)

	result, err = service.UpdateProfile(ctx, userEntity.ID, model.UpdateProfileRequest{FullName: "Avatar User", DeleteAvatar: true})
	require.NoError(t, err)
	require.Empty(t, result.Avatar)
	updated := client.User.GetX(ctx, userEntity.ID)
	require.Nil(t, updated.AvatarID)
}

func TestUserServiceUpdateProfileRejectsConflictingUsername(t *testing.T) {
	service, client, _, _ := newEntUserService(t)
	defer client.Close()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SetFullName("Current User").SaveX(ctx)
	client.User.Create().SetUsername("taken").SetFullName("Taken User").SaveX(ctx)

	result, err := service.UpdateProfile(ctx, current.ID, model.UpdateProfileRequest{Username: "taken", FullName: "Current User"})

	require.Nil(t, result)
	require.Error(t, err)
}

func TestUserServiceBlockAndUnblockUserLifecycle(t *testing.T) {
	service, client, _, _ := newEntUserService(t)
	defer client.Close()
	ctx := context.Background()
	blocker := client.User.Create().SetUsername("blocker").SaveX(ctx)
	target := client.User.Create().SetUsername("target").SaveX(ctx)

	require.NoError(t, service.BlockUser(ctx, blocker.ID, target.ID))
	require.True(t, client.UserBlock.Query().Where().ExistX(ctx))
	require.NoError(t, service.BlockUser(ctx, blocker.ID, target.ID))
	require.NoError(t, service.UnblockUser(ctx, blocker.ID, target.ID))
	require.False(t, client.UserBlock.Query().Where().ExistX(ctx))
}

func TestUserServiceBlockUserPublishesEvents(t *testing.T) {
	service, client, _, _ := newEntUserService(t)
	defer client.Close()
	ctx := context.Background()
	blocker := client.User.Create().SetUsername("blocker").SaveX(ctx)
	target := client.User.Create().SetUsername("target").SaveX(ctx)
	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher
	first := make(chan struct{})
	second := make(chan struct{})
	publisher.EXPECT().BroadcastToUser(target.ID, mock.MatchedBy(func(event websocket.Event) bool { return event.Type == websocket.EventUserBlock })).Run(func(uuid.UUID, websocket.Event) { close(first) }).Return()
	publisher.EXPECT().BroadcastToUser(blocker.ID, mock.MatchedBy(func(event websocket.Event) bool { return event.Type == websocket.EventUserBlock })).Run(func(uuid.UUID, websocket.Event) { close(second) }).Return()

	require.NoError(t, service.BlockUser(ctx, blocker.ID, target.ID))
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("target block event was not published")
	}
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("blocker block event was not published")
	}
}

func TestUserServiceUnblockUserPublishesEvents(t *testing.T) {
	service, client, _, _ := newEntUserService(t)
	defer client.Close()
	ctx := context.Background()
	blocker := client.User.Create().SetUsername("blocker").SaveX(ctx)
	target := client.User.Create().SetUsername("target").SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(blocker.ID).SetBlockedID(target.ID).SaveX(ctx)
	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher
	first := make(chan struct{})
	second := make(chan struct{})
	publisher.EXPECT().BroadcastToUser(target.ID, mock.MatchedBy(func(event websocket.Event) bool { return event.Type == websocket.EventUserUnblock })).Run(func(uuid.UUID, websocket.Event) { close(first) }).Return()
	publisher.EXPECT().BroadcastToUser(blocker.ID, mock.MatchedBy(func(event websocket.Event) bool { return event.Type == websocket.EventUserUnblock })).Run(func(uuid.UUID, websocket.Event) { close(second) }).Return()

	require.NoError(t, service.UnblockUser(ctx, blocker.ID, target.ID))
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatal("target unblock event was not published")
	}
	select {
	case <-second:
	case <-time.After(time.Second):
		t.Fatal("blocker unblock event was not published")
	}
}

func TestUserServiceBlockUserRejectsSelfAndMissingTarget(t *testing.T) {
	service, client, _, _ := newEntUserService(t)
	defer client.Close()
	ctx := context.Background()
	blocker := client.User.Create().SetUsername("blocker").SaveX(ctx)

	require.Error(t, service.BlockUser(ctx, blocker.ID, blocker.ID))
	require.Error(t, service.BlockUser(ctx, blocker.ID, uuid.New()))
}

func TestUserServiceUpdateProfileRejectsInvalidAvatarMedia(t *testing.T) {
	service, client, _, presence := newEntUserService(t)
	defer client.Close()
	ctx := context.Background()
	userEntity := client.User.Create().SetUsername("u-inv-av").SaveX(ctx)
	fakeMediaID := uuid.New()

	presence.EXPECT().Exists(mock.Anything, "online:"+userEntity.ID.String()).Return(true, nil)

	result, err := service.UpdateProfile(ctx, userEntity.ID, model.UpdateProfileRequest{
		FullName:      "Valid User Name",
		AvatarMediaID: &fakeMediaID,
	})
	require.Nil(t, result)
	require.Error(t, err)
}

func TestUserServiceUpdateProfileBroadcastsUserUpdate(t *testing.T) {
	service, client, _, presence := newEntUserService(t)
	defer client.Close()
	ctx := context.Background()
	userEntity := client.User.Create().SetUsername("u-broadcast").SetFullName("Old Name").SaveX(ctx)

	presence.EXPECT().Exists(mock.Anything, "online:"+userEntity.ID.String()).Return(true, nil)

	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher

	toUser := make(chan struct{})
	toContacts := make(chan struct{})

	publisher.EXPECT().BroadcastToUser(userEntity.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventUserUpdate
	})).Run(func(uuid.UUID, websocket.Event) { close(toUser) }).Return().Once()

	publisher.EXPECT().BroadcastToContacts(userEntity.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventUserUpdate
	})).Run(func(uuid.UUID, websocket.Event) { close(toContacts) }).Return().Once()

	result, err := service.UpdateProfile(ctx, userEntity.ID, model.UpdateProfileRequest{
		FullName: "New Full Name",
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, "New Full Name", result.FullName)

	select {
	case <-toUser:
	case <-time.After(time.Second):
		t.Fatal("user update event not broadcasted to user")
	}

	select {
	case <-toContacts:
	case <-time.After(time.Second):
		t.Fatal("user update event not broadcasted to contacts")
	}
}

func TestUserServiceBlockAndUnblockUserEarlyReturns(t *testing.T) {
	service, client, _, _ := newEntUserService(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	sameID := uuid.New()
	err := service.BlockUser(ctx, sameID, sameID)
	require.Error(t, err)

	user1 := client.User.Create().SetUsername("block-u1").SaveX(ctx)
	missingID := uuid.New()
	err = service.BlockUser(ctx, user1.ID, missingID)
	require.Error(t, err)

	user2 := client.User.Create().SetUsername("block-u2").SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(user1.ID).SetBlockedID(user2.ID).SaveX(ctx)
	err = service.BlockUser(ctx, user1.ID, user2.ID)
	require.NoError(t, err)
}

func TestUserServiceUpdateProfileNoChangesReturnsDirectly(t *testing.T) {
	service, client, storage, presence := newEntUserService(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	now := time.Now().UTC()
	avatar := client.Media.Create().
		SetFileName("avatars/current.png").
		SetOriginalName("current.png").
		SetFileSize(100).
		SetMimeType("image/png").
		SetCategory("user_avatar").
		SaveX(ctx)

	userEntity := client.User.Create().
		SetUsername("nochangeuser").
		SetEmail("nochange@example.com").
		SetFullName("No Change").
		SetBio("Bio text").
		SetAvatar(avatar).
		SetLastSeenAt(now).
		SaveX(ctx)

	storage.EXPECT().GetPublicURL("avatars/current.png").Return("https://cdn.example.com/avatars/current.png").Once()
	presence.EXPECT().Exists(mock.Anything, "online:"+userEntity.ID.String()).Return(true, nil).Once()

	res, err := service.UpdateProfile(ctx, userEntity.ID, model.UpdateProfileRequest{
		FullName: "No Change",
		Bio:      "Bio text",
		Username: "nochangeuser",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "No Change", res.FullName)
	require.Equal(t, "Bio text", res.Bio)
	require.Equal(t, "https://cdn.example.com/avatars/current.png", res.Avatar)
	require.NotNil(t, res.LastSeenAt)
}

func TestUserServiceGetBlockedUsersWithFullUserFields(t *testing.T) {
	reader := repositorymocks.NewMockUserReader(t)
	service := newMockUserService(t, reader)
	storage := service.storageAdapter.(*adaptermocks.MockPublicURLGenerator)
	ctx := context.Background()

	uID := uuid.New()
	targetID := uuid.New()
	avatarName := "avatars/blocked.png"
	bio := "Blocked user bio"
	uname := "blockeduser"
	fname := "Blocked FullName"

	entUser := &ent.User{
		ID:       targetID,
		Username: &uname,
		FullName: &fname,
		Bio:      &bio,
		Role:     user.RoleUser,
		Edges: ent.UserEdges{
			Avatar: &ent.Media{
				FileName: avatarName,
			},
		},
	}

	reader.EXPECT().GetBlockedUsers(ctx, uID, "", "", 10).Return([]*ent.User{entUser}, "next-cur", true, nil).Once()
	storage.EXPECT().GetPublicURL(avatarName).Return("https://cdn.example.com/" + avatarName).Once()

	res, nextCursor, hasNext, err := service.GetBlockedUsers(ctx, uID, model.GetBlockedUsersRequest{Limit: 10})
	require.NoError(t, err)
	require.True(t, hasNext)
	require.Equal(t, "next-cur", nextCursor)
	require.Len(t, res, 1)
	require.Equal(t, "blockeduser", res[0].Username)
	require.Equal(t, "Blocked FullName", res[0].FullName)
	require.Equal(t, "Blocked user bio", res[0].Bio)
	require.Equal(t, "https://cdn.example.com/avatars/blocked.png", res[0].Avatar)
	require.NotNil(t, res[0].IsBlockedByMe)
	require.True(t, *res[0].IsBlockedByMe)
}

func TestUserServiceGetUserProfileBlockedByOtherAndBanned(t *testing.T) {
	service, client, _, presence := newEntUserService(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	me := client.User.Create().SetUsername("me-uprof-test").SaveX(ctx)
	other := client.User.Create().SetUsername("other-uprof-test").SetFullName("Other Person").SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(other.ID).SetBlockedID(me.ID).SaveX(ctx)

	presence.EXPECT().Exists(mock.Anything, "online:"+other.ID.String()).Return(true, nil).Once()

	res, err := service.GetUserProfile(ctx, me.ID, other.ID)
	require.NoError(t, err)
	require.NotNil(t, res)
	require.True(t, *res.IsBlockedByOther)
	require.False(t, *res.IsBlockedByMe)
	require.False(t, *res.IsOnline)
	require.Nil(t, res.LastSeenAt)

	bannedTarget := client.User.Create().
		SetUsername("banned-uprof-tgt").
		SetIsBanned(true).
		SaveX(ctx)

	presence.EXPECT().Exists(mock.Anything, "online:"+bannedTarget.ID.String()).Return(true, nil).Once()

	resBanned, err := service.GetUserProfile(ctx, me.ID, bannedTarget.ID)
	require.NoError(t, err)
	require.NotNil(t, resBanned)
	require.False(t, *resBanned.IsOnline)
	require.Nil(t, resBanned.LastSeenAt)
}

func TestUserServiceUpdateProfileSetsInitialFullName(t *testing.T) {
	service, client, _, presence := newEntUserService(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	userEntity := client.User.Create().SetUsername("u-set-init-fn").SaveX(ctx)

	presence.EXPECT().Exists(mock.Anything, "online:"+userEntity.ID.String()).Return(true, nil).Once()

	res, err := service.UpdateProfile(ctx, userEntity.ID, model.UpdateProfileRequest{
		FullName: "Initial Full Name",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "Initial Full Name", res.FullName)
}

func TestUserServiceUpdateProfileClearsBio(t *testing.T) {
	service, client, _, presence := newEntUserService(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	userEntity := client.User.Create().SetUsername("u-clear-bio").SetFullName("Valid Clear Bio").SetBio("Old Bio Value").SaveX(ctx)

	presence.EXPECT().Exists(mock.Anything, "online:"+userEntity.ID.String()).Return(true, nil).Once()

	res, err := service.UpdateProfile(ctx, userEntity.ID, model.UpdateProfileRequest{
		FullName: "Valid Clear Bio",
		Bio:      "",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "", res.Bio)

	updated, err := client.User.Get(ctx, userEntity.ID)
	require.NoError(t, err)
	require.Nil(t, updated.Bio)
}

func TestUserServiceUpdateProfilePreservesAvatarWhenUpdatingName(t *testing.T) {
	service, client, storage, presence := newEntUserService(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	avatar := client.Media.Create().
		SetFileName("avatars/user-pres.png").
		SetOriginalName("pres.png").
		SetFileSize(100).
		SetMimeType("image/png").
		SetCategory(media.CategoryUserAvatar).
		SaveX(ctx)

	userEntity := client.User.Create().
		SetUsername("u-pres-av").
		SetFullName("Old Name Pres").
		SetAvatar(avatar).
		SaveX(ctx)

	storage.EXPECT().GetPublicURL("avatars/user-pres.png").Return("https://cdn.example.com/avatars/user-pres.png").Once()
	presence.EXPECT().Exists(mock.Anything, "online:"+userEntity.ID.String()).Return(true, nil).Once()

	res, err := service.UpdateProfile(ctx, userEntity.ID, model.UpdateProfileRequest{
		FullName: "Brand New Name",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "Brand New Name", res.FullName)
	require.Equal(t, "https://cdn.example.com/avatars/user-pres.png", res.Avatar)
}

func TestUserServiceUpdateProfileUpdatesBio(t *testing.T) {
	service, client, _, presence := newEntUserService(t)
	defer func() { _ = client.Close() }()
	ctx := context.Background()

	userEntity := client.User.Create().
		SetUsername("u-bio-test").
		SetFullName("Bio User").
		SaveX(ctx)

	presence.EXPECT().Exists(mock.Anything, "online:"+userEntity.ID.String()).Return(true, nil).Once()

	res, err := service.UpdateProfile(ctx, userEntity.ID, model.UpdateProfileRequest{
		FullName: "Bio User",
		Bio:      "Brand new bio text",
	})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Equal(t, "Brand new bio text", res.Bio)

	updated, err := client.User.Get(ctx, userEntity.ID)
	require.NoError(t, err)
	require.NotNil(t, updated.Bio)
	require.Equal(t, "Brand new bio text", *updated.Bio)
}
