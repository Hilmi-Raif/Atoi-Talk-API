package service

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/privatechat"
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

func newChatServiceTest(t *testing.T) (*ChatService, *ent.Client, *repositorymocks.MockChatReader, *repositorymocks.MockGroupMemberReader, *adaptermocks.MockRedisOnlineStore) {
	t.Helper()
	client := enttest.Open(t, dialect.SQLite, "file:chat-service-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	chatRepo := repositorymocks.NewMockChatReader(t)
	groupMemberRepo := repositorymocks.NewMockGroupMemberReader(t)
	redisOnline := adaptermocks.NewMockRedisOnlineStore(t)
	service := NewChatService(
		client,
		chatRepo,
		groupMemberRepo,
		&config.AppConfig{},
		config.NewValidator(),
		nil,
		adaptermocks.NewMockURLGenerator(t),
		redisOnline,
	)
	return service, client, chatRepo, groupMemberRepo, redisOnline
}

func loadPrivateChatForServiceTest(ctx context.Context, client *ent.Client, chatID uuid.UUID) *ent.Chat {
	return client.Chat.Query().
		Where(chat.ID(chatID)).
		WithPrivateChat(func(query *ent.PrivateChatQuery) {
			query.WithUser1().WithUser2()
		}).
		OnlyX(ctx)
}

func TestChatServiceGetChatsMapsPrivateChatAndUsesBatchPresence(t *testing.T) {
	service, client, chatRepo, _, redisOnline := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SetFullName("Current").SaveX(ctx)
	other := client.User.Create().SetUsername("other").SetFullName("Other").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(current.ID).SetUser2ID(other.ID).SaveX(ctx)
	loaded := loadPrivateChatForServiceTest(ctx, client, chatEntity.ID)

	chatRepo.EXPECT().GetChats(mock.Anything, current.ID, "", "", 20).Return([]*ent.Chat{loaded}, "next", true, nil)
	redisOnline.EXPECT().ExistsMany(mock.Anything, []string{"online:" + other.ID.String()}).Return(map[string]bool{
		"online:" + other.ID.String(): true,
	}, nil)

	result, nextCursor, hasNext, err := service.GetChats(ctx, current.ID, model.GetChatsRequest{})

	require.NoError(t, err)
	require.Equal(t, "next", nextCursor)
	require.True(t, hasNext)
	require.Len(t, result, 1)
	require.Equal(t, other.ID, *result[0].OtherUserID)
	require.Equal(t, "Other", result[0].Name)
	require.True(t, result[0].IsOnline)
}

func TestChatServiceGetChatsMapsBlockWhenPresenceBatchFails(t *testing.T) {
	service, client, chatRepo, _, redisOnline := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SetFullName("Current").SaveX(ctx)
	other := client.User.Create().SetUsername("other").SetFullName("Other").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(current.ID).SetUser2ID(other.ID).SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(other.ID).SetBlockedID(current.ID).SaveX(ctx)
	loaded := loadPrivateChatForServiceTest(ctx, client, chatEntity.ID)
	chatRepo.EXPECT().GetChats(mock.Anything, current.ID, "", "", 20).Return([]*ent.Chat{loaded}, "", false, nil)
	redisOnline.EXPECT().ExistsMany(mock.Anything, []string{"online:" + other.ID.String()}).Return(nil, errors.New("presence unavailable"))

	result, _, _, err := service.GetChats(ctx, current.ID, model.GetChatsRequest{})

	require.NoError(t, err)
	require.Len(t, result, 1)
	require.False(t, result[0].IsOnline)
}

func TestChatServiceGetChatsRejectsInvalidRequestBeforeRepository(t *testing.T) {
	service, client, chatRepo, _, _ := newChatServiceTest(t)
	defer client.Close()

	result, nextCursor, hasNext, err := service.GetChats(context.Background(), uuid.New(), model.GetChatsRequest{Limit: 51})

	require.Nil(t, result)
	require.Empty(t, nextCursor)
	require.False(t, hasNext)
	require.Error(t, err)
	chatRepo.AssertNotCalled(t, "GetChats", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestChatServiceGetChatsMapsRepositoryError(t *testing.T) {
	service, client, chatRepo, _, _ := newChatServiceTest(t)
	defer client.Close()
	chatRepo.EXPECT().GetChats(mock.Anything, mock.Anything, "", "", 20).Return(nil, "", false, errors.New("database unavailable"))

	result, nextCursor, hasNext, err := service.GetChats(context.Background(), uuid.New(), model.GetChatsRequest{})

	require.Nil(t, result)
	require.Empty(t, nextCursor)
	require.False(t, hasNext)
	require.Error(t, err)
}

func TestChatServiceGetChatByIDMapsPrivateChatAndPresence(t *testing.T) {
	service, client, chatRepo, _, redisOnline := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SetFullName("Current").SaveX(ctx)
	other := client.User.Create().SetUsername("other").SetFullName("Other").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(current.ID).SetUser2ID(other.ID).SaveX(ctx)
	loaded := loadPrivateChatForServiceTest(ctx, client, chatEntity.ID)
	chatRepo.EXPECT().GetChatByID(mock.Anything, current.ID, chatEntity.ID).Return(loaded, nil)
	redisOnline.EXPECT().Exists(mock.Anything, "online:"+other.ID.String()).Return(true, nil)

	result, err := service.GetChatByID(ctx, current.ID, chatEntity.ID)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, other.ID, *result.OtherUserID)
	require.Equal(t, "Other", result.Name)
	require.True(t, result.IsOnline)
}

func TestChatServiceGetChatByIDMapsBlockAndPresenceFailure(t *testing.T) {
	service, client, chatRepo, _, redisOnline := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SetFullName("Current").SaveX(ctx)
	other := client.User.Create().SetUsername("other").SetFullName("Other").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(current.ID).SetUser2ID(other.ID).SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(current.ID).SetBlockedID(other.ID).SaveX(ctx)
	loaded := loadPrivateChatForServiceTest(ctx, client, chatEntity.ID)
	chatRepo.EXPECT().GetChatByID(mock.Anything, current.ID, chatEntity.ID).Return(loaded, nil)
	redisOnline.EXPECT().Exists(mock.Anything, "online:"+other.ID.String()).Return(false, errors.New("presence unavailable"))

	result, err := service.GetChatByID(ctx, current.ID, chatEntity.ID)

	require.NoError(t, err)
	require.True(t, result.IsBlockedByMe)
	require.False(t, result.IsOnline)
}

func TestChatServiceGetChatByIDMapsGroupAndActionNames(t *testing.T) {
	service, client, chatRepo, _, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SetFullName("Current").SaveX(ctx)
	actor := client.User.Create().SetUsername("actor").SetFullName("Actor").SaveX(ctx)
	target := client.User.Create().SetUsername("target").SetFullName("Target").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().
		SetChatID(chatEntity.ID).
		SetName("group").
		SetDescription("description").
		SetInviteCode("group-invite").
		SetIsPublic(true).
		SaveX(ctx)
	client.GroupMember.Create().
		SetGroupChatID(group.ID).
		SetUserID(current.ID).
		SetRole(groupmember.RoleOwner).
		SetUnreadCount(2).
		SaveX(ctx)
	lastMessage := client.Message.Create().
		SetChatID(chatEntity.ID).
		SetSenderID(actor.ID).
		SetType(message.TypeSystemAdd).
		SetActionData(map[string]interface{}{
			"target_id": target.ID.String(),
			"actor_id":  actor.ID.String(),
		}).
		SaveX(ctx)
	client.Chat.UpdateOneID(chatEntity.ID).
		SetLastMessageID(lastMessage.ID).
		SetLastMessageAt(lastMessage.CreatedAt).
		SaveX(ctx)
	loaded := client.Chat.Query().
		Where(chat.ID(chatEntity.ID)).
		WithGroupChat(func(query *ent.GroupChatQuery) {
			query.WithMembers()
		}).
		WithLastMessage(func(query *ent.MessageQuery) {
			query.WithSender()
		}).
		OnlyX(ctx)
	chatRepo.EXPECT().GetChatByID(mock.Anything, current.ID, chatEntity.ID).Return(loaded, nil)

	result, err := service.GetChatByID(ctx, current.ID, chatEntity.ID)

	require.NoError(t, err)
	require.Equal(t, "group", result.Name)
	require.Equal(t, "description", *result.Description)
	require.True(t, *result.IsPublic)
	require.Equal(t, group.InviteCode, *result.InviteCode)
	require.Equal(t, "owner", *result.MyRole)
	require.Equal(t, 1, result.MemberCount)
	require.Equal(t, target.ID.String(), result.LastMessage.ActionData["target_id"])
	require.Equal(t, "Target", result.LastMessage.ActionData["target_name"])
	require.Equal(t, "Actor", result.LastMessage.ActionData["actor_name"])
	require.Equal(t, lastMessage.ID, result.LastMessage.ID)
}

func TestChatServiceGetChatByIDHidesActionIDsForDeletedUsers(t *testing.T) {
	service, client, chatRepo, _, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SaveX(ctx)
	actor := client.User.Create().SetUsername("actor").SaveX(ctx)
	target := client.User.Create().SetUsername("target").SaveX(ctx)
	client.User.UpdateOneID(actor.ID).SetDeletedAt(time.Now().UTC()).SaveX(ctx)
	client.User.UpdateOneID(target.ID).SetDeletedAt(time.Now().UTC()).SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("group").SetInviteCode("deleted-users").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(current.ID).SetRole(groupmember.RoleMember).SaveX(ctx)
	lastMessage := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(actor.ID).SetType(message.TypeSystemAdd).SetActionData(map[string]interface{}{
		"target_id": target.ID.String(),
		"actor_id":  actor.ID.String(),
	}).SaveX(ctx)
	client.Chat.UpdateOneID(chatEntity.ID).SetLastMessageID(lastMessage.ID).SetLastMessageAt(lastMessage.CreatedAt).SaveX(ctx)
	loaded := client.Chat.Query().
		Where(chat.ID(chatEntity.ID)).
		WithGroupChat(func(query *ent.GroupChatQuery) { query.WithMembers() }).
		WithLastMessage(func(query *ent.MessageQuery) { query.WithSender() }).
		OnlyX(ctx)
	chatRepo.EXPECT().GetChatByID(mock.Anything, current.ID, chatEntity.ID).Return(loaded, nil)

	result, err := service.GetChatByID(ctx, current.ID, chatEntity.ID)

	require.NoError(t, err)
	require.Equal(t, "Deleted User", result.LastMessage.ActionData["target_name"])
	require.Equal(t, "Deleted User", result.LastMessage.ActionData["actor_name"])
	require.NotContains(t, result.LastMessage.ActionData, "target_id")
	require.NotContains(t, result.LastMessage.ActionData, "actor_id")
}

func TestChatServiceGetChatByIDHidesLastMessageForEmptyPublicGroup(t *testing.T) {
	service, client, chatRepo, _, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("public").SetInviteCode("empty-public").SetIsPublic(true).SaveX(ctx)
	lastMessage := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(current.ID).SetType(message.TypeRegular).SetContent("hidden").SaveX(ctx)
	client.Chat.UpdateOneID(chatEntity.ID).SetLastMessageID(lastMessage.ID).SetLastMessageAt(lastMessage.CreatedAt).SaveX(ctx)
	loaded := client.Chat.Query().
		Where(chat.ID(chatEntity.ID)).
		WithGroupChat(func(query *ent.GroupChatQuery) { query.WithMembers() }).
		WithLastMessage(func(query *ent.MessageQuery) { query.WithSender() }).
		OnlyX(ctx)
	chatRepo.EXPECT().GetChatByID(mock.Anything, current.ID, chatEntity.ID).Return(loaded, nil)

	result, err := service.GetChatByID(ctx, current.ID, chatEntity.ID)

	require.NoError(t, err)
	require.Nil(t, result.LastMessage)
}

func TestChatServiceGetChatsMapsGroupMemberCountAndActionNames(t *testing.T) {
	service, client, chatRepo, groupMemberRepo, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SetFullName("Current").SaveX(ctx)
	actor := client.User.Create().SetUsername("actor").SetFullName("Actor").SaveX(ctx)
	target := client.User.Create().SetUsername("target").SetFullName("Target").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("group").SetInviteCode("group-list").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(current.ID).SetRole(groupmember.RoleAdmin).SaveX(ctx)
	messageEntity := client.Message.Create().SetChatID(chatEntity.ID).SetSenderID(actor.ID).SetType(message.TypeSystemAdd).SetActionData(map[string]interface{}{
		"target_id": target.ID.String(),
		"actor_id":  actor.ID.String(),
	}).SaveX(ctx)
	client.Chat.UpdateOneID(chatEntity.ID).
		SetLastMessageID(messageEntity.ID).
		SetLastMessageAt(messageEntity.CreatedAt).
		SaveX(ctx)
	loaded := client.Chat.Query().
		Where(chat.ID(chatEntity.ID)).
		WithGroupChat(func(query *ent.GroupChatQuery) {
			query.WithMembers()
		}).
		WithLastMessage(func(query *ent.MessageQuery) {
			query.WithSender()
		}).
		OnlyX(ctx)
	chatRepo.EXPECT().GetChats(mock.Anything, current.ID, "", "", 20).Return([]*ent.Chat{loaded}, "", false, nil)
	groupMemberRepo.EXPECT().CountActiveMembersByGroupIDs(mock.Anything, group.ID).Return(map[uuid.UUID]int{group.ID: 3}, nil)

	result, _, _, err := service.GetChats(ctx, current.ID, model.GetChatsRequest{})

	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Equal(t, 3, result[0].MemberCount)
	require.Equal(t, "Target", result[0].LastMessage.ActionData["target_name"])
	require.Equal(t, "Actor", result[0].LastMessage.ActionData["actor_name"])
	require.Equal(t, messageEntity.ID, result[0].LastMessage.ID)
}

func TestChatServiceGetChatsContinuesWhenMemberCountFails(t *testing.T) {
	service, client, chatRepo, groupMemberRepo, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("group").SetInviteCode("count-error").SaveX(ctx)
	client.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(current.ID).SetRole(groupmember.RoleMember).SaveX(ctx)
	loaded := client.Chat.Query().
		Where(chat.ID(chatEntity.ID)).
		WithGroupChat(func(query *ent.GroupChatQuery) { query.WithMembers() }).
		OnlyX(ctx)
	chatRepo.EXPECT().GetChats(mock.Anything, current.ID, "", "", 20).Return([]*ent.Chat{loaded}, "", false, nil)
	groupMemberRepo.EXPECT().CountActiveMembersByGroupIDs(mock.Anything, group.ID).Return(nil, errors.New("count failed"))

	result, _, _, err := service.GetChats(ctx, current.ID, model.GetChatsRequest{})

	require.NoError(t, err)
	require.Len(t, result, 1)
	require.Zero(t, result[0].MemberCount)
}

func TestChatServiceGetChatByIDMapsNotFound(t *testing.T) {
	service, client, chatRepo, _, _ := newChatServiceTest(t)
	defer client.Close()
	chatRepo.EXPECT().GetChatByID(mock.Anything, mock.Anything, mock.Anything).Return(nil, &ent.NotFoundError{})

	result, err := service.GetChatByID(context.Background(), uuid.New(), uuid.New())

	require.Nil(t, result)
	require.Error(t, err)
}

func TestChatServiceHideChatUpdatesPrivateChatAndBroadcasts(t *testing.T) {
	service, client, _, _, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SaveX(ctx)
	other := client.User.Create().SetUsername("other").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	privateChat := client.PrivateChat.Create().
		SetChatID(chatEntity.ID).
		SetUser1ID(current.ID).
		SetUser2ID(other.ID).
		SetUser1UnreadCount(2).
		SaveX(ctx)

	done := make(chan struct{})
	publisher := websocketmocks.NewMockPublisher(t)
	publisher.EXPECT().BroadcastToUser(current.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventChatHide && event.Meta != nil && event.Meta.ChatID == chatEntity.ID
	})).Run(func(uuid.UUID, websocket.Event) {
		close(done)
	}).Once()
	service.wsHub = publisher

	require.NoError(t, service.HideChat(ctx, current.ID, chatEntity.ID))

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for hide broadcast")
	}

	updated := client.PrivateChat.GetX(ctx, privateChat.ID)
	require.NotNil(t, updated.User1HiddenAt)
	require.Zero(t, updated.User1UnreadCount)
}

func TestChatServiceHideChatRejectsGroupChat(t *testing.T) {
	service, client, _, _, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	user := client.User.Create().SetUsername("user").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)

	err := service.HideChat(ctx, user.ID, chatEntity.ID)

	require.Error(t, err)
}

func TestChatServiceHideChatRejectsMissingAndUnauthorizedChats(t *testing.T) {
	service, client, _, _, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	current := client.User.Create().SetUsername("current").SaveX(ctx)
	other := client.User.Create().SetUsername("other").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(current.ID).SetUser2ID(other.ID).SaveX(ctx)

	require.Error(t, service.HideChat(ctx, uuid.New(), chatEntity.ID))
	require.Error(t, service.HideChat(ctx, current.ID, uuid.New()))
}

func TestChatServiceMarkAsReadRejectsMissingChat(t *testing.T) {
	service, client, _, _, _ := newChatServiceTest(t)
	defer client.Close()

	err := service.MarkAsRead(context.Background(), uuid.New(), uuid.New())
	require.Error(t, err)
}

func TestChatServiceMarkAsReadPrivateChatUser1MarksReadAndBroadcasts(t *testing.T) {
	service, client, _, _, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	user1 := client.User.Create().SetUsername("u1").SaveX(ctx)
	user2 := client.User.Create().SetUsername("u2").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().
		SetChatID(chatEntity.ID).
		SetUser1ID(user1.ID).
		SetUser2ID(user2.ID).
		SetUser1UnreadCount(3).
		SetUser2UnreadCount(0).
		SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher
	readCh := make(chan struct{}, 1)
	publisher.EXPECT().BroadcastToChat(chatEntity.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventChatRead
	})).Run(func(uuid.UUID, websocket.Event) {
		select {
		case readCh <- struct{}{}:
		default:
		}
	}).Once()

	err := service.MarkAsRead(ctx, user1.ID, chatEntity.ID)
	require.NoError(t, err)

	select {
	case <-readCh:
	case <-time.After(time.Second):
		t.Fatal("read event timeout")
	}

	pc := client.PrivateChat.Query().Where(privatechat.ChatID(chatEntity.ID)).OnlyX(ctx)
	require.Zero(t, pc.User1UnreadCount)
	require.NotNil(t, pc.User1LastReadAt)
}

func TestChatServiceMarkAsReadPrivateChatUser2MarksReadWhenBlocked(t *testing.T) {
	service, client, _, _, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	user1 := client.User.Create().SetUsername("u1b").SaveX(ctx)
	user2 := client.User.Create().SetUsername("u2b").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().
		SetChatID(chatEntity.ID).
		SetUser1ID(user1.ID).
		SetUser2ID(user2.ID).
		SetUser1UnreadCount(0).
		SetUser2UnreadCount(5).
		SaveX(ctx)
	client.UserBlock.Create().SetBlockerID(user1.ID).SetBlockedID(user2.ID).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher

	err := service.MarkAsRead(ctx, user2.ID, chatEntity.ID)
	require.NoError(t, err)

	pc := client.PrivateChat.Query().Where(privatechat.ChatID(chatEntity.ID)).OnlyX(ctx)
	require.Zero(t, pc.User2UnreadCount)
}

func TestChatServiceMarkAsReadPrivateChatReturnsEarlyWhenUnreadIsZero(t *testing.T) {
	service, client, _, _, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	user1 := client.User.Create().SetUsername("u1z").SaveX(ctx)
	user2 := client.User.Create().SetUsername("u2z").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().
		SetChatID(chatEntity.ID).
		SetUser1ID(user1.ID).
		SetUser2ID(user2.ID).
		SetUser1UnreadCount(0).
		SetUser2UnreadCount(0).
		SaveX(ctx)

	require.NoError(t, service.MarkAsRead(ctx, user1.ID, chatEntity.ID))
	require.NoError(t, service.MarkAsRead(ctx, user2.ID, chatEntity.ID))
}

func TestChatServiceMarkAsReadPrivateChatRejectsForbiddenParticipant(t *testing.T) {
	service, client, _, _, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	user1 := client.User.Create().SetUsername("u1f").SaveX(ctx)
	user2 := client.User.Create().SetUsername("u2f").SaveX(ctx)
	stranger := client.User.Create().SetUsername("stranger").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	client.PrivateChat.Create().
		SetChatID(chatEntity.ID).
		SetUser1ID(user1.ID).
		SetUser2ID(user2.ID).
		SetUser1UnreadCount(2).
		SaveX(ctx)

	require.Error(t, service.MarkAsRead(ctx, stranger.ID, chatEntity.ID))
}

func TestChatServiceMarkAsReadGroupChatMarksReadAndBroadcasts(t *testing.T) {
	service, client, _, _, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	user := client.User.Create().SetUsername("gm1").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Group").SetInviteCode("group-read").SaveX(ctx)
	client.GroupMember.Create().
		SetGroupChatID(group.ID).
		SetUserID(user.ID).
		SetRole(groupmember.RoleMember).
		SetUnreadCount(4).
		SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher
	readCh := make(chan struct{}, 1)
	publisher.EXPECT().BroadcastToChat(chatEntity.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventChatRead
	})).Run(func(uuid.UUID, websocket.Event) {
		select {
		case readCh <- struct{}{}:
		default:
		}
	}).Once()

	err := service.MarkAsRead(ctx, user.ID, chatEntity.ID)
	require.NoError(t, err)

	select {
	case <-readCh:
	case <-time.After(time.Second):
		t.Fatal("read event timeout")
	}

	gm := client.GroupMember.Query().Where(groupmember.GroupChatID(group.ID), groupmember.UserID(user.ID)).OnlyX(ctx)
	require.Zero(t, gm.UnreadCount)
	require.NotNil(t, gm.LastReadAt)
}

func TestChatServiceMarkAsReadGroupChatReturnsEarlyWhenUnreadIsZero(t *testing.T) {
	service, client, _, _, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	user := client.User.Create().SetUsername("gmz").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	group := client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Group").SetInviteCode("group-read-z").SaveX(ctx)
	client.GroupMember.Create().
		SetGroupChatID(group.ID).
		SetUserID(user.ID).
		SetRole(groupmember.RoleMember).
		SetUnreadCount(0).
		SaveX(ctx)

	require.NoError(t, service.MarkAsRead(ctx, user.ID, chatEntity.ID))
}

func TestChatServiceMarkAsReadGroupChatRejectsNonMember(t *testing.T) {
	service, client, _, _, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()
	user := client.User.Create().SetUsername("gmnon").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypeGroup).SaveX(ctx)
	client.GroupChat.Create().SetChatID(chatEntity.ID).SetName("Group").SetInviteCode("group-read-non").SaveX(ctx)

	require.Error(t, service.MarkAsRead(ctx, user.ID, chatEntity.ID))
}

func TestChatServiceHideChatUser2AndBroadcasts(t *testing.T) {
	service, client, _, _, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	u1 := client.User.Create().SetUsername("u1-hide").SaveX(ctx)
	u2 := client.User.Create().SetUsername("u2-hide").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	pc := client.PrivateChat.Create().SetChatID(chatEntity.ID).SetUser1ID(u1.ID).SetUser2ID(u2.ID).SetUser2UnreadCount(3).SaveX(ctx)

	publisher := websocketmocks.NewMockPublisher(t)
	service.wsHub = publisher
	hidden := make(chan struct{}, 1)
	publisher.EXPECT().BroadcastToUser(u2.ID, mock.MatchedBy(func(event websocket.Event) bool {
		return event.Type == websocket.EventChatHide
	})).Run(func(uuid.UUID, websocket.Event) {
		hidden <- struct{}{}
	}).Once()

	err := service.HideChat(ctx, u2.ID, chatEntity.ID)
	require.NoError(t, err)

	select {
	case <-hidden:
	case <-time.After(time.Second):
		t.Fatal("expected chat hide broadcast to user2")
	}

	updatedPC := client.PrivateChat.GetX(ctx, pc.ID)
	require.NotNil(t, updatedPC.User2HiddenAt)
	require.Equal(t, 0, updatedPC.User2UnreadCount)
}

func TestChatServiceHideChatRejectsWhenPrivateChatEdgeMissing(t *testing.T) {
	service, client, _, _, _ := newChatServiceTest(t)
	defer client.Close()
	ctx := context.Background()

	u := client.User.Create().SetUsername("u-missing-pc").SaveX(ctx)
	chatEntity := client.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)

	err := service.HideChat(ctx, u.ID, chatEntity.ID)
	require.Error(t, err)
}
