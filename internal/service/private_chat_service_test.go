package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/media"
	adaptermocks "AtoiTalkAPI/internal/adapter/mocks"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/model"
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

func newPrivateChatService(t *testing.T) (*PrivateChatService, func()) {
	t.Helper()
	client := enttest.Open(t, dialect.SQLite, "file:private-chat-service-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	cache := adaptermocks.NewMockRedisCache(t)
	cache.EXPECT().Del(mock.Anything, mock.Anything).Return(nil).Maybe()

	service := NewPrivateChatService(client, &config.AppConfig{}, config.NewValidator(), nil, cache, adaptermocks.NewMockPublicURLGenerator(t))
	return service, func() {
		client.Close()
	}
}

func TestPrivateChatServiceRejectsInvalidTargets(t *testing.T) {
	service, cleanup := newPrivateChatService(t)
	defer cleanup()
	ctx := context.Background()
	userID := uuid.New()

	response, err := service.CreatePrivateChat(ctx, userID, model.CreatePrivateChatRequest{TargetUserID: userID})
	require.Nil(t, response)
	require.Error(t, err)

	response, err = service.CreatePrivateChat(ctx, userID, model.CreatePrivateChatRequest{TargetUserID: uuid.New()})
	require.Nil(t, response)
	require.Error(t, err)
}

func TestPrivateChatServiceRejectsBannedAndBlockedUsers(t *testing.T) {
	service, cleanup := newPrivateChatService(t)
	defer cleanup()
	ctx := context.Background()
	client := service.client
	creator := client.User.Create().SetUsername("creator").SaveX(ctx)
	bannedUntil := time.Now().UTC().Add(time.Hour)
	banned := client.User.Create().SetUsername("banned").SetIsBanned(true).SetBannedUntil(bannedUntil).SaveX(ctx)

	response, err := service.CreatePrivateChat(ctx, creator.ID, model.CreatePrivateChatRequest{TargetUserID: banned.ID})
	require.Nil(t, response)
	require.Error(t, err)

	blocked := client.User.Create().SetUsername("blocked").SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(creator.ID).SetBlockedID(blocked.ID).SaveX(ctx)
	response, err = service.CreatePrivateChat(ctx, creator.ID, model.CreatePrivateChatRequest{TargetUserID: blocked.ID})
	require.Nil(t, response)
	require.Error(t, err)
}

func TestPrivateChatServiceCreatesAndReusesPrivateChat(t *testing.T) {
	service, cleanup := newPrivateChatService(t)
	defer cleanup()
	ctx := context.Background()
	creator := service.client.User.Create().SetUsername("creator").SetFullName("Creator").SaveX(ctx)
	target := service.client.User.Create().SetUsername("target").SetFullName("Target").SaveX(ctx)

	first, err := service.CreatePrivateChat(ctx, creator.ID, model.CreatePrivateChatRequest{TargetUserID: target.ID})
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, "private", first.Type)

	second, err := service.CreatePrivateChat(ctx, target.ID, model.CreatePrivateChatRequest{TargetUserID: creator.ID})
	require.NoError(t, err)
	require.Equal(t, first.ID, second.ID)

	count := service.client.Chat.Query().CountX(ctx)
	require.Equal(t, 1, count)
}

func TestPrivateChatServicePublishesNewChatToBothUsers(t *testing.T) {
	service, cleanup := newPrivateChatService(t)
	defer cleanup()
	ctx := context.Background()
	creator := service.client.User.Create().SetUsername("creator").SetFullName("Creator").SaveX(ctx)
	target := service.client.User.Create().SetUsername("target").SetFullName("Target").SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher
	events := make(chan uuid.UUID, 2)
	publisher.EXPECT().BroadcastToUser(target.ID, mock.Anything).Run(func(id uuid.UUID, _ websocket.Event) {
		events <- id
	}).Once()
	publisher.EXPECT().BroadcastToUser(creator.ID, mock.Anything).Run(func(id uuid.UUID, _ websocket.Event) {
		events <- id
	}).Once()
	cache := service.redisAdapter.(*adaptermocks.MockRedisCache)
	cache.EXPECT().Exists(mock.Anything, "online:"+creator.ID.String()).Return(true, nil).Once()
	cache.EXPECT().Exists(mock.Anything, "online:"+target.ID.String()).Return(false, nil).Once()

	response, err := service.CreatePrivateChat(ctx, creator.ID, model.CreatePrivateChatRequest{TargetUserID: target.ID})

	require.NoError(t, err)
	require.NotNil(t, response)
	require.Equal(t, "private", response.Type)
	seen := map[uuid.UUID]bool{}
	for range 2 {
		select {
		case id := <-events:
			seen[id] = true
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for private chat broadcasts")
		}
	}
	require.True(t, seen[creator.ID])
	require.True(t, seen[target.ID])
}

func TestPrivateChatToResponseMapsPrivateChat(t *testing.T) {
	createdAt := time.Date(2026, time.August, 23, 12, 30, 0, 0, time.UTC)
	chatEntity := &ent.Chat{ID: uuid.New(), Type: chat.TypePrivate, CreatedAt: createdAt}
	privateChat := &ent.PrivateChat{Edges: ent.PrivateChatEdges{Chat: chatEntity}}

	response := privateChatToResponse(privateChat)

	require.NotNil(t, response)
	require.Equal(t, chatEntity.ID, response.ID)
	require.Equal(t, "private", response.Type)
	require.Equal(t, createdAt.Format(time.RFC3339), response.CreatedAt)
}

func TestPrivateChatToResponseRejectsMissingEntities(t *testing.T) {
	require.Nil(t, privateChatToResponse(nil))
	require.Nil(t, privateChatToResponse(&ent.PrivateChat{}))
}

func TestPrivateChatServiceRejectsInvalidRequest(t *testing.T) {
	service, cleanup := newPrivateChatService(t)
	defer cleanup()

	response, err := service.CreatePrivateChat(context.Background(), uuid.New(), model.CreatePrivateChatRequest{})
	require.Nil(t, response)
	require.Error(t, err)
}

func TestPrivateChatServicePublishesWithAvatars(t *testing.T) {
	service, cleanup := newPrivateChatService(t)
	defer cleanup()
	ctx := context.Background()
	client := service.client

	creator := client.User.Create().SetUsername("creator-av").SetFullName("Creator Av").SaveX(ctx)
	target := client.User.Create().SetUsername("target-av").SetFullName("Target Av").SaveX(ctx)

	creatorAv := client.Media.Create().SetFileName("c.png").SetOriginalName("c.png").SetFileSize(10).SetMimeType("image/png").SetCategory(media.CategoryUserAvatar).SetUploader(creator).SaveX(ctx)
	targetAv := client.Media.Create().SetFileName("t.png").SetOriginalName("t.png").SetFileSize(10).SetMimeType("image/png").SetCategory(media.CategoryUserAvatar).SetUploader(target).SaveX(ctx)

	client.User.UpdateOneID(creator.ID).SetAvatar(creatorAv).SaveX(ctx)
	client.User.UpdateOneID(target.ID).SetAvatar(targetAv).SaveX(ctx)

	storage := service.storageAdapter.(*adaptermocks.MockPublicURLGenerator)
	storage.EXPECT().GetPublicURL("c.png").Return("https://cdn.example.com/c.png").Maybe()
	storage.EXPECT().GetPublicURL("t.png").Return("https://cdn.example.com/t.png").Maybe()

	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher
	events := make(chan uuid.UUID, 2)
	publisher.EXPECT().BroadcastToUser(target.ID, mock.Anything).Run(func(id uuid.UUID, _ websocket.Event) {
		events <- id
	}).Once()
	publisher.EXPECT().BroadcastToUser(creator.ID, mock.Anything).Run(func(id uuid.UUID, _ websocket.Event) {
		events <- id
	}).Once()

	cache := service.redisAdapter.(*adaptermocks.MockRedisCache)
	cache.EXPECT().Exists(mock.Anything, "online:"+creator.ID.String()).Return(true, nil).Once()
	cache.EXPECT().Exists(mock.Anything, "online:"+target.ID.String()).Return(true, nil).Once()

	response, err := service.CreatePrivateChat(ctx, creator.ID, model.CreatePrivateChatRequest{TargetUserID: target.ID})

	require.NoError(t, err)
	require.NotNil(t, response)

	for range 2 {
		select {
		case <-events:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for broadcasts")
		}
	}
}

func TestPrivateChatServiceCreatePrivateChatAllowsUnbannedUser(t *testing.T) {
	service, cleanup := newPrivateChatService(t)
	defer cleanup()
	ctx := context.Background()
	client := service.client

	creator := client.User.Create().SetUsername("creator-unban").SaveX(ctx)
	past := time.Now().UTC().Add(-1 * time.Hour)
	target := client.User.Create().SetUsername("target-unban").SetIsBanned(true).SetBannedUntil(past).SaveX(ctx)

	cache := service.redisAdapter.(*adaptermocks.MockRedisCache)
	cache.EXPECT().Exists(mock.Anything, mock.Anything).Return(false, nil).Maybe()

	response, err := service.CreatePrivateChat(ctx, creator.ID, model.CreatePrivateChatRequest{TargetUserID: target.ID})
	require.NoError(t, err)
	require.NotNil(t, response)

	permBanned := client.User.Create().SetUsername("perm-banned").SetIsBanned(true).SaveX(ctx)
	_, err = service.CreatePrivateChat(ctx, creator.ID, model.CreatePrivateChatRequest{TargetUserID: permBanned.ID})
	require.Error(t, err)
}

func TestPrivateChatServiceReverseBlockAndCancelledContext(t *testing.T) {
	service, cleanup := newPrivateChatService(t)
	defer cleanup()
	ctx := context.Background()
	client := service.client

	creator := client.User.Create().SetUsername("rev-creator").SaveX(ctx)
	target := client.User.Create().SetUsername("rev-target").SaveX(ctx)

	client.UserBlock.Create().SetBlockerID(target.ID).SetBlockedID(creator.ID).SaveX(ctx)
	response, err := service.CreatePrivateChat(ctx, creator.ID, model.CreatePrivateChatRequest{TargetUserID: target.ID})
	require.Nil(t, response)
	require.Error(t, err)

	cancCtx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = service.CreatePrivateChat(cancCtx, creator.ID, model.CreatePrivateChatRequest{TargetUserID: target.ID})
	require.Error(t, err)
}

func TestPrivateChatServiceRejectsDeletedTargetUser(t *testing.T) {
	service, cleanup := newPrivateChatService(t)
	defer cleanup()
	ctx := context.Background()
	client := service.client

	creator := client.User.Create().SetUsername("del-creator").SaveX(ctx)
	target := client.User.Create().SetUsername("del-target").SetDeletedAt(time.Now().UTC()).SaveX(ctx)

	response, err := service.CreatePrivateChat(ctx, creator.ID, model.CreatePrivateChatRequest{TargetUserID: target.ID})
	require.Nil(t, response)
	require.Error(t, err)
}

func TestPrivateChatServiceCreatePrivateChatWithoutAvatarsAndWithFullNames(t *testing.T) {
	service, cleanup := newPrivateChatService(t)
	defer cleanup()
	ctx := context.Background()
	client := service.client
	redisCache := service.redisAdapter.(*adaptermocks.MockRedisCache)

	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher

	creator := client.User.Create().SetUsername("full-creator").SetFullName("Full Creator").SaveX(ctx)
	target := client.User.Create().SetUsername("full-target").SetFullName("Full Target").SaveX(ctx)

	redisCache.EXPECT().Exists(mock.Anything, "online:"+creator.ID.String()).Return(true, nil).Maybe()
	redisCache.EXPECT().Exists(mock.Anything, "online:"+target.ID.String()).Return(false, nil).Maybe()

	targetBroadcastDone := make(chan struct{})
	creatorBroadcastDone := make(chan struct{})

	publisher.EXPECT().BroadcastToUser(target.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventChatNew
	})).Run(func(uuid.UUID, websocket.Event) {
		close(targetBroadcastDone)
	}).Once()

	publisher.EXPECT().BroadcastToUser(creator.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventChatNew
	})).Run(func(uuid.UUID, websocket.Event) {
		close(creatorBroadcastDone)
	}).Once()

	response, err := service.CreatePrivateChat(ctx, creator.ID, model.CreatePrivateChatRequest{TargetUserID: target.ID})
	require.NoError(t, err)
	require.NotNil(t, response)
	require.Equal(t, string(chat.TypePrivate), response.Type)

	select {
	case <-targetBroadcastDone:
	case <-time.After(time.Second):
		t.Fatal("target broadcast not received")
	}

	select {
	case <-creatorBroadcastDone:
	case <-time.After(time.Second):
		t.Fatal("creator broadcast not received")
	}
}

func TestPrivateChatServiceCreatePrivateChatWithoutFullNames(t *testing.T) {
	service, cleanup := newPrivateChatService(t)
	defer cleanup()
	ctx := context.Background()
	client := service.client
	redisCache := service.redisAdapter.(*adaptermocks.MockRedisCache)

	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher

	creator := client.User.Create().SetUsername("nofn-creator").SaveX(ctx)
	target := client.User.Create().SetUsername("nofn-target").SaveX(ctx)

	redisCache.EXPECT().Exists(mock.Anything, "online:"+creator.ID.String()).Return(false, nil).Maybe()
	redisCache.EXPECT().Exists(mock.Anything, "online:"+target.ID.String()).Return(false, nil).Maybe()

	targetBroadcastDone := make(chan struct{})
	creatorBroadcastDone := make(chan struct{})

	publisher.EXPECT().BroadcastToUser(target.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventChatNew
	})).Run(func(uuid.UUID, websocket.Event) {
		close(targetBroadcastDone)
	}).Once()

	publisher.EXPECT().BroadcastToUser(creator.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventChatNew
	})).Run(func(uuid.UUID, websocket.Event) {
		close(creatorBroadcastDone)
	}).Once()

	response, err := service.CreatePrivateChat(ctx, creator.ID, model.CreatePrivateChatRequest{TargetUserID: target.ID})
	require.NoError(t, err)
	require.NotNil(t, response)

	select {
	case <-targetBroadcastDone:
	case <-time.After(time.Second):
		t.Fatal("target broadcast not received")
	}

	select {
	case <-creatorBroadcastDone:
	case <-time.After(time.Second):
		t.Fatal("creator broadcast not received")
	}
}
