//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupchat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"AtoiTalkAPI/internal/websocket"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	ws "github.com/gorilla/websocket"
	"github.com/stretchr/testify/assert"
)

func uploadCompletedGroupAvatar(t *testing.T, token, originalName string, content []byte) uuid.UUID {
	t.Helper()

	uploadReq := model.UploadMediaRequest{
		Usage:        "group_avatar",
		OriginalName: originalName,
		FileSize:     int64(len(content)),
		MimeType:     "image/jpeg",
		CaptchaToken: dummyTurnstileToken,
	}
	uploadRR := makeRequest("POST", "/api/media/upload", uploadReq, token)
	assert.Equal(t, http.StatusOK, uploadRR.Code)

	uploadData := parseResponse[model.UploadMediaResponse](t, uploadRR)
	mediaID := uploadData.Media.ID
	fileName := uploadData.Media.FileName

	_, err := s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(testConfig.S3BucketPublic),
		Key:         aws.String(fileName),
		Body:        bytes.NewReader(content),
		ContentType: aws.String("image/jpeg"),
	})
	assert.NoError(t, err)

	completeRR := makeRequest("POST", fmt.Sprintf("/api/media/%s/complete", mediaID), nil, token)
	assert.Equal(t, http.StatusOK, completeRR.Code)

	return mediaID
}

func TestCreateGroupChat(t *testing.T) {
	clearDatabase(context.Background())

	password := "Password123!"
	hashedPassword, _ := helper.HashPassword(password)
	u1, _ := testClient.User.Create().SetEmail("u1@test.com").SetUsername("user1").SetFullName("User 1").SetPasswordHash(hashedPassword).Save(context.Background())
	u2, _ := testClient.User.Create().SetEmail("u2@test.com").SetUsername("user2").SetFullName("User 2").SetPasswordHash(hashedPassword).Save(context.Background())
	u3, _ := testClient.User.Create().SetEmail("u3@test.com").SetUsername("user3").SetFullName("User 3").SetPasswordHash(hashedPassword).Save(context.Background())

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)

	t.Run("Success - Create Group with Text Only", func(t *testing.T) {
		rr := makeRequest("POST", "/api/chats/group", model.CreateGroupChatRequest{
			Name:        "Test Group 1",
			Description: "A group for testing",
			MemberIDs:   []uuid.UUID{u2.ID, u3.ID},
		}, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.ChatListResponse](t, rr)
		assert.Equal(t, "Test Group 1", dataMap.Name)
		assert.NotNil(t, dataMap.Description)
		assert.Equal(t, "A group for testing", *dataMap.Description)
		assert.Equal(t, "group", dataMap.Type)
		assert.NotNil(t, dataMap.MyRole)
		assert.Equal(t, "owner", *dataMap.MyRole)
		assert.Equal(t, 3, dataMap.MemberCount)
		assert.NotNil(t, dataMap.InviteCode)
		assert.NotEmpty(t, *dataMap.InviteCode)
		assert.NotNil(t, dataMap.InviteExpiresAt)

		assert.NotNil(t, dataMap.LastMessage)
		assert.Equal(t, "system_create", dataMap.LastMessage.Type)
		assert.Equal(t, "Test Group 1", dataMap.LastMessage.ActionData["initial_name"])

		gc, err := testClient.GroupChat.Query().Where(groupchat.Name("Test Group 1")).WithChat().Only(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, u1.ID, *gc.CreatedBy)
		assert.NotEmpty(t, gc.InviteCode)
		assert.NotNil(t, gc.InviteExpiresAt)

		members, err := gc.QueryMembers().All(context.Background())
		assert.NoError(t, err)
		assert.Len(t, members, 3)
	})

	t.Run("Success - Create Public Group", func(t *testing.T) {
		rr := makeRequest("POST", "/api/chats/group", model.CreateGroupChatRequest{
			Name:      "Public Group",
			MemberIDs: []uuid.UUID{u2.ID},
			IsPublic:  true,
		}, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.ChatListResponse](t, rr)
		assert.NotNil(t, dataMap.IsPublic)
		assert.True(t, *dataMap.IsPublic)
		assert.NotNil(t, dataMap.InviteCode)
		assert.NotEmpty(t, *dataMap.InviteCode)
		assert.Nil(t, dataMap.InviteExpiresAt)

		gc, err := testClient.GroupChat.Query().Where(groupchat.Name("Public Group")).Only(context.Background())
		assert.NoError(t, err)
		assert.True(t, gc.IsPublic)
		assert.Nil(t, gc.InviteExpiresAt)
	})

	t.Run("Success - Create Group with Whitespace", func(t *testing.T) {
		rr := makeRequest("POST", "/api/chats/group", model.CreateGroupChatRequest{
			Name:        "  Spaced Group  ",
			Description: "  Spaced Desc  ",
			MemberIDs:   []uuid.UUID{u2.ID},
		}, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		gc, err := testClient.GroupChat.Query().Where(groupchat.Name("Spaced Group")).Only(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "Spaced Group", gc.Name)
		assert.Equal(t, "Spaced Desc", *gc.Description)
	})

	t.Run("Success - Create Group with Avatar", func(t *testing.T) {
		fileContent := createTestImage(t, 100, 100)
		avatarMediaID := uploadCompletedGroupAvatar(t, token1, "test_avatar.jpg", fileContent)

		rr := makeRequest("POST", "/api/chats/group", model.CreateGroupChatRequest{
			Name:          "Group With Avatar",
			MemberIDs:     []uuid.UUID{u2.ID},
			AvatarMediaID: &avatarMediaID,
		}, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.ChatListResponse](t, rr)
		assert.Contains(t, dataMap.Avatar, testConfig.S3PublicDomain)

		gc, err := testClient.GroupChat.Query().
			Where(groupchat.Name("Group With Avatar")).
			WithAvatar().
			Only(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, gc.Edges.Avatar)

		_, err = s3Client.HeadObject(context.Background(), &s3.HeadObjectInput{
			Bucket: aws.String(testConfig.S3BucketPublic),
			Key:    aws.String(gc.Edges.Avatar.FileName),
		})
		assert.NoError(t, err)
	})

	t.Run("Fail - No Members", func(t *testing.T) {
		rr := makeRequest("POST", "/api/chats/group", model.CreateGroupChatRequest{Name: "Empty Group"}, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Add Self", func(t *testing.T) {
		rr := makeRequest("POST", "/api/chats/group", model.CreateGroupChatRequest{
			Name:      "Self Group",
			MemberIDs: []uuid.UUID{u1.ID},
		}, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Invalid Member ID", func(t *testing.T) {
		rr := makeRequest("POST", "/api/chats/group", model.CreateGroupChatRequest{
			Name:      "Ghost Group",
			MemberIDs: []uuid.UUID{uuid.New()},
		}, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Blocked Member", func(t *testing.T) {
		testClient.UserBlock.Create().SetBlockerID(u1.ID).SetBlockedID(u2.ID).Exec(context.Background())

		rr := makeRequest("POST", "/api/chats/group", model.CreateGroupChatRequest{
			Name:      "Blocked Group",
			MemberIDs: []uuid.UUID{u2.ID},
		}, token1)
		assert.Equal(t, http.StatusForbidden, rr.Code)

		testClient.UserBlock.Delete().Exec(context.Background())
	})

	t.Run("Fail - No Name", func(t *testing.T) {
		rr := makeRequest("POST", "/api/chats/group", model.CreateGroupChatRequest{MemberIDs: []uuid.UUID{u2.ID}}, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Success - Group Survives Creator Deletion", func(t *testing.T) {
		creator, _ := testClient.User.Create().SetEmail("creator@test.com").SetUsername("creator").SetFullName("Creator").Save(context.Background())
		member, _ := testClient.User.Create().SetEmail("member@test.com").SetUsername("member").SetFullName("Member").Save(context.Background())

		chatEntity := testClient.Chat.Create().SetType("group").SaveX(context.Background())
		gc := testClient.GroupChat.Create().SetChat(chatEntity).SetCreator(creator).SetName("Survivor Group").SetInviteCode("survivor").SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(creator).SetRole(groupmember.RoleOwner).SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(member).SetRole(groupmember.RoleMember).SaveX(context.Background())

		err := testClient.User.DeleteOneID(creator.ID).Exec(context.Background())
		assert.NoError(t, err)

		gcReload, err := testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).Only(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "Survivor Group", gcReload.Name)
		assert.Nil(t, gcReload.CreatedBy)

		exists, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(member.ID)).Exist(context.Background())
		assert.True(t, exists)
	})

	t.Run("Fail - Create Group with Deleted User", func(t *testing.T) {
		deletedUser, _ := testClient.User.Create().
			SetEmail("deleted@test.com").
			SetUsername("deleted").
			SetFullName("Deleted User").
			SetDeletedAt(time.Now().UTC()).
			Save(context.Background())

		rr := makeRequest("POST", "/api/chats/group", model.CreateGroupChatRequest{
			Name:      "Group with Deleted User",
			MemberIDs: []uuid.UUID{deletedUser.ID},
		}, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestUpdateGroupChat(t *testing.T) {
	clearDatabase(context.Background())

	u1 := testClient.User.Create().SetEmail("u1@test.com").SetUsername("owner").SetFullName("Owner").SaveX(context.Background())
	u2 := testClient.User.Create().SetEmail("u2@test.com").SetUsername("admin").SetFullName("Admin").SaveX(context.Background())
	u3 := testClient.User.Create().SetEmail("u3@test.com").SetUsername("member").SetFullName("Member").SaveX(context.Background())
	u4 := testClient.User.Create().SetEmail("u4@test.com").SetUsername("outsider").SetFullName("Outsider").SaveX(context.Background())

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token3, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u3.ID)
	token4, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u4.ID)

	chatEntity := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc := testClient.GroupChat.Create().SetChat(chatEntity).SetCreator(u1).SetName("Original Name").SetDescription("Original Desc").SetInviteCode("original").SetIsPublic(false).SetInviteExpiresAt(time.Now().Add(7 * 24 * time.Hour)).SaveX(context.Background())

	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u2).SetRole(groupmember.RoleAdmin).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).SetRole(groupmember.RoleMember).SaveX(context.Background())

	t.Run("Success - Rename Group", func(t *testing.T) {
		newName := "New Group Name"
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), model.UpdateGroupChatRequest{Name: &newName}, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		gcReload, _ := testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).Only(context.Background())
		assert.Equal(t, "New Group Name", gcReload.Name)

		lastMsg, err := testClient.Chat.Query().Where(chat.ID(gc.ChatID)).QueryLastMessage().Only(context.Background())
		if assert.NoError(t, err) {
			assert.Equal(t, message.TypeSystemRename, lastMsg.Type)
			assert.Equal(t, "New Group Name", lastMsg.ActionData["new_name"])
		}

		dataMap := parseResponse[model.ChatListResponse](t, rr)
		assert.NotNil(t, dataMap.MyRole)
		assert.Equal(t, "owner", *dataMap.MyRole)
	})

	t.Run("Success - Update Description", func(t *testing.T) {
		newDescription := "New Description"
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), model.UpdateGroupChatRequest{Description: &newDescription}, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.ChatListResponse](t, rr)
		assert.NotNil(t, dataMap.Description)
		assert.Equal(t, "New Description", *dataMap.Description)

		gcReload, _ := testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).Only(context.Background())
		assert.Equal(t, "New Description", *gcReload.Description)

		lastMsg, err := testClient.Chat.Query().Where(chat.ID(gc.ChatID)).QueryLastMessage().Only(context.Background())
		if assert.NoError(t, err) {
			assert.Equal(t, message.TypeSystemDescription, lastMsg.Type)
			assert.Equal(t, "New Description", lastMsg.ActionData["new_description"])
		}
	})

	t.Run("Success - Update IsPublic to True", func(t *testing.T) {
		isPublic := true
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), model.UpdateGroupChatRequest{IsPublic: &isPublic}, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.ChatListResponse](t, rr)
		assert.NotNil(t, dataMap.IsPublic)
		assert.True(t, *dataMap.IsPublic)
		assert.NotNil(t, dataMap.InviteCode)
		assert.Nil(t, dataMap.InviteExpiresAt)

		gcReload, _ := testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).Only(context.Background())
		assert.True(t, gcReload.IsPublic)
		assert.Nil(t, gcReload.InviteExpiresAt)

		lastMsg, err := testClient.Chat.Query().Where(chat.ID(gc.ChatID)).QueryLastMessage().Only(context.Background())
		if assert.NoError(t, err) {
			assert.Equal(t, "system_visibility", string(lastMsg.Type))
			assert.Equal(t, "public", lastMsg.ActionData["new_visibility"])
		}
	})

	t.Run("Success - Update IsPublic to False", func(t *testing.T) {
		gcBefore, _ := testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).Only(context.Background())
		oldCode := gcBefore.InviteCode

		isPublic := false
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), model.UpdateGroupChatRequest{IsPublic: &isPublic}, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.ChatListResponse](t, rr)
		assert.NotNil(t, dataMap.IsPublic)
		assert.False(t, *dataMap.IsPublic)
		assert.NotNil(t, dataMap.InviteCode)
		assert.NotEqual(t, oldCode, *dataMap.InviteCode)
		assert.NotNil(t, dataMap.InviteExpiresAt)

		gcReload, _ := testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).Only(context.Background())
		assert.False(t, gcReload.IsPublic)
		assert.NotNil(t, gcReload.InviteExpiresAt)
		assert.NotEqual(t, oldCode, gcReload.InviteCode)

		lastMsg, err := testClient.Chat.Query().Where(chat.ID(gc.ChatID)).QueryLastMessage().Only(context.Background())
		if assert.NoError(t, err) {
			assert.Equal(t, "system_visibility", string(lastMsg.Type))
			assert.Equal(t, "private", lastMsg.ActionData["new_visibility"])
		}
	})

	t.Run("Success - Update Avatar", func(t *testing.T) {
		fileContent := createTestImage(t, 100, 100)
		avatarMediaID := uploadCompletedGroupAvatar(t, token1, "new_avatar.jpg", fileContent)

		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), model.UpdateGroupChatRequest{AvatarMediaID: &avatarMediaID}, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		gcReload, _ := testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).WithAvatar().Only(context.Background())
		assert.NotNil(t, gcReload.Edges.Avatar)

		_, err := s3Client.HeadObject(context.Background(), &s3.HeadObjectInput{
			Bucket: aws.String(testConfig.S3BucketPublic),
			Key:    aws.String(gcReload.Edges.Avatar.FileName),
		})
		assert.NoError(t, err)

		dataMap := parseResponse[model.ChatListResponse](t, rr)
		assert.Contains(t, dataMap.Avatar, testConfig.S3PublicDomain)

		lastMsg, _ := testClient.Chat.Query().Where(chat.ID(gc.ChatID)).QueryLastMessage().Only(context.Background())
		assert.Equal(t, message.TypeSystemAvatar, lastMsg.Type)
	})

	t.Run("Success - Delete Avatar", func(t *testing.T) {
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), model.UpdateGroupChatRequest{DeleteAvatar: true}, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.ChatListResponse](t, rr)
		assert.Empty(t, dataMap.Avatar)

		gcReload, _ := testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).WithAvatar().Only(context.Background())
		assert.Nil(t, gcReload.Edges.Avatar)

		lastMsg, _ := testClient.Chat.Query().Where(chat.ID(gc.ChatID)).QueryLastMessage().Only(context.Background())
		assert.Equal(t, message.TypeSystemAvatar, lastMsg.Type)
		assert.Equal(t, "removed", lastMsg.ActionData["action"])
	})

	t.Run("Fail - Member", func(t *testing.T) {
		newName := "Hacked Name"
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), model.UpdateGroupChatRequest{Name: &newName}, token3)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Not Member", func(t *testing.T) {
		newName := "Hacked Name"
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), model.UpdateGroupChatRequest{Name: &newName}, token4)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Name Too Short", func(t *testing.T) {
		newName := "Hi"
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), model.UpdateGroupChatRequest{Name: &newName}, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Update Deleted Group", func(t *testing.T) {
		chatEntity.Update().SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		newName := "Zombie Group"
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), model.UpdateGroupChatRequest{Name: &newName}, token1)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestSearchGroupMembers(t *testing.T) {
	clearDatabase(context.Background())
	u1 := testClient.User.Create().SetEmail("u1@test.com").SetUsername("alpha").SetFullName("Alpha User").SaveX(context.Background())
	u2 := testClient.User.Create().SetEmail("u2@test.com").SetUsername("beta").SetFullName("Beta User").SaveX(context.Background())
	u3 := testClient.User.Create().SetEmail("u3@test.com").SetUsername("gamma").SetFullName("Gamma User").SaveX(context.Background())
	u4 := testClient.User.Create().SetEmail("u4@test.com").SetUsername("delta").SetFullName("Delta User").SaveX(context.Background())

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token4, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u4.ID)

	chatEntity := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc := testClient.GroupChat.Create().SetChat(chatEntity).SetCreator(u1).SetName("Search Test Group").SetInviteCode("search").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u2).SetRole(groupmember.RoleMember).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).SetRole(groupmember.RoleMember).SaveX(context.Background())

	t.Run("Success - Get All Members", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := parseResponse[[]model.GroupMemberDTO](t, rr)
		assert.Len(t, dataList, 3)
		assert.False(t, resp.Meta.HasNext)
	})

	t.Run("Success - Search by Username", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/chats/group/%s/members?query=beta", chatEntity.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.GroupMemberDTO](t, rr)
		assert.Len(t, dataList, 1)
		assert.Equal(t, "Beta User", dataList[0].FullName)
	})

	t.Run("Success - Search by Full Name", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/chats/group/%s/members?query=Gamma", chatEntity.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.GroupMemberDTO](t, rr)
		assert.Len(t, dataList, 1)
		assert.Equal(t, "Gamma User", dataList[0].FullName)
	})

	t.Run("Success - Pagination", func(t *testing.T) {
		rr1 := makeRequest("GET", fmt.Sprintf("/api/chats/group/%s/members?limit=2", chatEntity.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr1.Code)

		var resp1 helper.ResponseWithPagination
		json.Unmarshal(rr1.Body.Bytes(), &resp1)
		dataList1 := parseResponse[[]model.GroupMemberDTO](t, rr1)
		assert.Len(t, dataList1, 2)
		assert.True(t, resp1.Meta.HasNext)
		assert.NotEmpty(t, resp1.Meta.NextCursor)

		rr2 := makeRequest("GET", fmt.Sprintf("/api/chats/group/%s/members?limit=2&cursor=%s", chatEntity.ID, resp1.Meta.NextCursor), nil, token1)
		assert.Equal(t, http.StatusOK, rr2.Code)

		var resp2 helper.ResponseWithPagination
		json.Unmarshal(rr2.Body.Bytes(), &resp2)
		dataList2 := parseResponse[[]model.GroupMemberDTO](t, rr2)
		assert.Len(t, dataList2, 1)
		assert.False(t, resp2.Meta.HasNext)
	})

	t.Run("Fail - Not a Member", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), nil, token4)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Group Not Found", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats/group/99999/members", nil, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Search Members in Deleted Group", func(t *testing.T) {
		chatEntity.Update().SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), nil, token1)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestAddGroupMember(t *testing.T) {
	clearDatabase(context.Background())
	u1 := testClient.User.Create().SetEmail("u1@test.com").SetUsername("owner").SetFullName("Owner User").SaveX(context.Background())
	u2 := testClient.User.Create().SetEmail("u2@test.com").SetUsername("admin").SetFullName("Admin User").SaveX(context.Background())
	u3 := testClient.User.Create().SetEmail("u3@test.com").SetUsername("member").SetFullName("Member User").SaveX(context.Background())
	u4 := testClient.User.Create().SetEmail("u4@test.com").SetUsername("newbie").SetFullName("Newbie User").SaveX(context.Background())
	u5 := testClient.User.Create().SetEmail("u5@test.com").SetUsername("newbie2").SetFullName("Newbie User 2").SaveX(context.Background())

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token3, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u3.ID)

	chatEntity := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc := testClient.GroupChat.Create().SetChat(chatEntity).SetCreator(u1).SetName("Add Member Test").SetInviteCode("add").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u2).SetRole(groupmember.RoleAdmin).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).SetRole(groupmember.RoleMember).SaveX(context.Background())

	t.Run("Success - Owner Adds Multiple Members", func(t *testing.T) {
		reqBody := model.AddGroupMemberRequest{UserIDs: []uuid.UUID{u4.ID, u5.ID}}
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), reqBody, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.MessageResponse](t, rr)
		assert.Len(t, dataList, 2)
		assert.Equal(t, "system_add", dataList[0].Type)
		assert.Equal(t, u1.ID.String(), dataList[0].ActionData["actor_id"])

		isMember4, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u4.ID)).Exist(context.Background())
		assert.True(t, isMember4)
		isMember5, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u5.ID)).Exist(context.Background())
		assert.True(t, isMember5)
	})

	t.Run("Fail - Member Tries to Add Member", func(t *testing.T) {
		u6 := testClient.User.Create().SetEmail("u6@test.com").SetUsername("another").SetFullName("Another User").SaveX(context.Background())
		reqBody := model.AddGroupMemberRequest{UserIDs: []uuid.UUID{u6.ID}}
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), reqBody, token3)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Add Existing Member", func(t *testing.T) {
		reqBody := model.AddGroupMemberRequest{UserIDs: []uuid.UUID{u2.ID}}
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), reqBody, token1)
		assert.Equal(t, http.StatusConflict, rr.Code)
	})

	t.Run("Fail - Add Non-Existent User", func(t *testing.T) {
		reqBody := model.AddGroupMemberRequest{UserIDs: []uuid.UUID{uuid.New()}}
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), reqBody, token1)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Add Member to Deleted Group", func(t *testing.T) {
		chatEntity.Update().SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		u7 := testClient.User.Create().SetEmail("u7@test.com").SetUsername("another7").SetFullName("Another User 7").SaveX(context.Background())
		reqBody := model.AddGroupMemberRequest{UserIDs: []uuid.UUID{u7.ID}}
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), reqBody, token1)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Add Deleted User to Group", func(t *testing.T) {
		chatEntity.Update().ClearDeletedAt().ExecX(context.Background())

		deletedUser, _ := testClient.User.Create().
			SetEmail("deleted2@test.com").
			SetUsername("deleted2").
			SetFullName("Deleted User 2").
			SetDeletedAt(time.Now().UTC()).
			Save(context.Background())

		reqBody := model.AddGroupMemberRequest{UserIDs: []uuid.UUID{deletedUser.ID}}
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), reqBody, token1)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestLeaveGroup(t *testing.T) {
	clearDatabase(context.Background())
	u1 := testClient.User.Create().SetEmail("u1@test.com").SetUsername("owner").SetFullName("Owner").SaveX(context.Background())
	u2 := testClient.User.Create().SetEmail("u2@test.com").SetUsername("member").SetFullName("Member").SaveX(context.Background())
	u3 := testClient.User.Create().SetEmail("u3@test.com").SetUsername("outsider").SetFullName("Outsider").SaveX(context.Background())

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)
	token3, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u3.ID)

	chatEntity := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc := testClient.GroupChat.Create().SetChat(chatEntity).SetCreator(u1).SetName("Leave Test").SetInviteCode("leave").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u2).SetRole(groupmember.RoleMember).SaveX(context.Background())

	t.Run("Success - Member Leaves", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/leave", gc.ChatID), nil, token2)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.MessageResponse](t, rr)
		assert.Equal(t, "system_leave", dataMap.Type)
		assert.NotNil(t, dataMap.SenderID)
		assert.Equal(t, u2.ID, *dataMap.SenderID)

		exists, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u2.ID)).Exist(context.Background())
		assert.False(t, exists)
	})

	t.Run("Fail - Owner Leaves", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/leave", gc.ChatID), nil, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Not Member", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/leave", gc.ChatID), nil, token3)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestKickMember(t *testing.T) {
	clearDatabase(context.Background())
	u1 := testClient.User.Create().SetEmail("u1@test.com").SetUsername("owner").SetFullName("Owner").SaveX(context.Background())
	u2 := testClient.User.Create().SetEmail("u2@test.com").SetUsername("admin").SetFullName("Admin").SaveX(context.Background())
	u3 := testClient.User.Create().SetEmail("u3@test.com").SetUsername("member").SetFullName("Member").SaveX(context.Background())
	u4 := testClient.User.Create().SetEmail("u4@test.com").SetUsername("outsider").SetFullName("Outsider").SaveX(context.Background())

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)
	token3, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u3.ID)
	token4, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u4.ID)

	chatEntity := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc := testClient.GroupChat.Create().SetChat(chatEntity).SetCreator(u1).SetName("Kick Test").SetInviteCode("kick").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u2).SetRole(groupmember.RoleAdmin).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).SetRole(groupmember.RoleMember).SaveX(context.Background())

	t.Run("Success - Owner Kicks Member", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u3.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.MessageResponse](t, rr)
		assert.Equal(t, "system_kick", dataMap.Type)
		assert.Equal(t, u3.ID.String(), dataMap.ActionData["target_id"])

		exists, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u3.ID)).Exist(context.Background())
		assert.False(t, exists)
	})

	t.Run("Success - Admin Kicks Member", func(t *testing.T) {
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).SetRole(groupmember.RoleMember).SaveX(context.Background())

		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u3.ID), nil, token2)
		assert.Equal(t, http.StatusOK, rr.Code)

		exists, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u3.ID)).Exist(context.Background())
		assert.False(t, exists)
	})

	t.Run("Success - Kick Deleted Member", func(t *testing.T) {
		defer testClient.User.UpdateOne(u3).ClearDeletedAt().ExecX(context.Background())

		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).SetRole(groupmember.RoleMember).SaveX(context.Background())
		testClient.User.UpdateOne(u3).SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u3.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.MessageResponse](t, rr)
		assert.Equal(t, u3.ID.String(), dataMap.ActionData["target_id"])
		assert.NotEmpty(t, dataMap.ActionData["target_name"])

		exists, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u3.ID)).Exist(context.Background())
		assert.False(t, exists)
	})

	t.Run("Fail - Admin Kicks Admin", func(t *testing.T) {
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).SetRole(groupmember.RoleAdmin).SaveX(context.Background())

		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u3.ID), nil, token2)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Admin Kicks Owner", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u1.ID), nil, token2)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Member Kicks Member", func(t *testing.T) {
		testClient.GroupMember.Update().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u3.ID)).SetRole(groupmember.RoleMember).ExecX(context.Background())

		exists, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u4.ID)).Exist(context.Background())
		if !exists {
			testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u4).SetRole(groupmember.RoleMember).SaveX(context.Background())
		}

		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u4.ID), nil, token3)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Kick Self", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u1.ID), nil, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Not Member", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u2.ID), nil, token4)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})
}

func TestUpdateMemberRole(t *testing.T) {
	clearDatabase(context.Background())
	u1 := testClient.User.Create().SetEmail("u1@test.com").SetUsername("owner").SetFullName("Owner").SaveX(context.Background())
	u2 := testClient.User.Create().SetEmail("u2@test.com").SetUsername("member").SetFullName("Member").SaveX(context.Background())
	u3 := testClient.User.Create().SetEmail("u3@test.com").SetUsername("admin").SetFullName("Admin").SaveX(context.Background())

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	chatEntity := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc := testClient.GroupChat.Create().SetChat(chatEntity).SetCreator(u1).SetName("Role Test").SetInviteCode("role").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u2).SetRole(groupmember.RoleMember).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).SetRole(groupmember.RoleAdmin).SaveX(context.Background())

	t.Run("Success - Promote to Admin", func(t *testing.T) {
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", gc.ChatID, u2.ID), model.UpdateGroupMemberRoleRequest{Role: "admin"}, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.MessageResponse](t, rr)
		assert.Equal(t, "system_promote", dataMap.Type)
		assert.Equal(t, "admin", dataMap.ActionData["new_role"])

		member, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u2.ID)).Only(context.Background())
		assert.Equal(t, groupmember.RoleAdmin, member.Role)
	})

	t.Run("Success - Demote to Member", func(t *testing.T) {
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", gc.ChatID, u3.ID), model.UpdateGroupMemberRoleRequest{Role: "member"}, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		member, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u3.ID)).Only(context.Background())
		assert.Equal(t, groupmember.RoleMember, member.Role)
	})

	t.Run("Fail - Promote Deleted User", func(t *testing.T) {
		testClient.User.UpdateOne(u2).SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", gc.ChatID, u2.ID), model.UpdateGroupMemberRoleRequest{Role: "admin"}, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)

		testClient.User.UpdateOne(u2).ClearDeletedAt().ExecX(context.Background())
	})

	t.Run("Fail - Admin Promotes", func(t *testing.T) {
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", gc.ChatID, u3.ID), model.UpdateGroupMemberRoleRequest{Role: "admin"}, token2)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Promote Self", func(t *testing.T) {
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", gc.ChatID, u1.ID), model.UpdateGroupMemberRoleRequest{Role: "member"}, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestTransferOwnership(t *testing.T) {
	clearDatabase(context.Background())
	u1 := testClient.User.Create().SetEmail("u1@test.com").SetUsername("owner").SetFullName("Owner").SaveX(context.Background())
	u2 := testClient.User.Create().SetEmail("u2@test.com").SetUsername("admin").SetFullName("Admin").SaveX(context.Background())
	u3 := testClient.User.Create().SetEmail("u3@test.com").SetUsername("member").SetFullName("Member").SaveX(context.Background())

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	chatEntity := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc := testClient.GroupChat.Create().SetChat(chatEntity).SetCreator(u1).SetName("Transfer Test").SetInviteCode("transfer").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u2).SetRole(groupmember.RoleAdmin).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).SetRole(groupmember.RoleMember).SaveX(context.Background())

	t.Run("Success - Transfer to Admin", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/transfer", gc.ChatID), model.TransferGroupOwnershipRequest{NewOwnerID: u2.ID}, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.MessageResponse](t, rr)
		assert.Equal(t, "system_promote", dataMap.Type)
		assert.Equal(t, "ownership_transferred", dataMap.ActionData["action"])

		oldOwner, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u1.ID)).Only(context.Background())
		newOwner, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u2.ID)).Only(context.Background())

		assert.Equal(t, groupmember.RoleAdmin, oldOwner.Role)
		assert.Equal(t, groupmember.RoleOwner, newOwner.Role)
	})

	t.Run("Fail - Transfer to Deleted User", func(t *testing.T) {
		testClient.User.UpdateOne(u3).SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/transfer", gc.ChatID), model.TransferGroupOwnershipRequest{NewOwnerID: u3.ID}, token2)
		assert.Equal(t, http.StatusBadRequest, rr.Code)

		testClient.User.UpdateOne(u3).ClearDeletedAt().ExecX(context.Background())
	})

	t.Run("Fail - Not Owner", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/transfer", gc.ChatID), model.TransferGroupOwnershipRequest{NewOwnerID: u3.ID}, token1)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Transfer to Self", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/transfer", gc.ChatID), model.TransferGroupOwnershipRequest{NewOwnerID: u2.ID}, token2)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestDeleteGroup(t *testing.T) {
	clearDatabase(context.Background())
	u1 := testClient.User.Create().SetEmail("u1@test.com").SetUsername("owner").SetFullName("Owner").SaveX(context.Background())
	u2 := testClient.User.Create().SetEmail("u2@test.com").SetUsername("member").SetFullName("Member").SaveX(context.Background())

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	chatEntity := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc := testClient.GroupChat.Create().SetChat(chatEntity).SetCreator(u1).SetName("Delete Test").SetInviteCode("delete").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u2).SetRole(groupmember.RoleMember).SaveX(context.Background())

	t.Run("Success - Owner Deletes Group", func(t *testing.T) {
		server := httptest.NewServer(testRouter)
		defer server.Close()
		wsURL := "ws" + strings.TrimPrefix(server.URL, "http") + "/ws?token=" + token2
		conn, _, _ := ws.DefaultDialer.Dial(wsURL, nil)
		defer conn.Close()
		time.Sleep(100 * time.Millisecond)

		rr := makeRequest("DELETE", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		c, _ := testClient.Chat.Query().Where(chat.ID(gc.ChatID)).Only(context.Background())
		assert.NotNil(t, c.DeletedAt)

		conn.SetReadDeadline(time.Now().UTC().Add(2 * time.Second))
		_, msg, err := conn.ReadMessage()
		assert.NoError(t, err)
		var event websocket.Event
		json.Unmarshal(msg, &event)
		assert.Equal(t, websocket.EventChatDelete, event.Type)
		assert.Equal(t, gc.ChatID, event.Meta.ChatID)
	})

	t.Run("Fail - Member Deletes Group", func(t *testing.T) {
		chatEntity2 := testClient.Chat.Create().SetType("group").SaveX(context.Background())
		gc2 := testClient.GroupChat.Create().SetChat(chatEntity2).SetCreator(u1).SetName("Delete Test 2").SetInviteCode("delete2").SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc2).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc2).SetUser(u2).SetRole(groupmember.RoleMember).SaveX(context.Background())

		rr := makeRequest("DELETE", fmt.Sprintf("/api/chats/group/%s", gc2.ChatID), nil, token2)
		assert.Equal(t, http.StatusForbidden, rr.Code)

		c, _ := testClient.Chat.Query().Where(chat.ID(gc2.ChatID)).Only(context.Background())
		assert.Nil(t, c.DeletedAt)
	})

	t.Run("Fail - Already Deleted", func(t *testing.T) {
		rr := makeRequest("DELETE", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), nil, token1)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Success - Member Deletes Account", func(t *testing.T) {
		chatEntity3 := testClient.Chat.Create().SetType("group").SaveX(context.Background())
		gc3 := testClient.GroupChat.Create().SetChat(chatEntity3).SetCreator(u1).SetName("Delete Account Test").SetInviteCode("delete3").SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc3).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())

		u3 := testClient.User.Create().SetEmail("u3@test.com").SetUsername("member3").SetFullName("Member 3").SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc3).SetUser(u3).SetRole(groupmember.RoleMember).SaveX(context.Background())

		testClient.User.UpdateOne(u3).SetDeletedAt(time.Now().UTC()).SetFullName("Deleted Account").ExecX(context.Background())

		exists, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc3.ID), groupmember.UserID(u3.ID)).Exist(context.Background())
		assert.True(t, exists)

		rr := makeRequest("GET", fmt.Sprintf("/api/chats/group/%s/members", chatEntity3.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.GroupMemberDTO](t, rr)
		assert.Len(t, dataList, 1)
		assert.Equal(t, u1.ID, dataList[0].UserID)
	})
}

func TestSearchPublicGroups(t *testing.T) {
	clearDatabase(context.Background())
	u1 := testClient.User.Create().SetEmail("u1@test.com").SetUsername("user1").SetFullName("User 1").SaveX(context.Background())
	u2 := testClient.User.Create().SetEmail("u2@test.com").SetUsername("user2").SetFullName("User 2").SaveX(context.Background())
	u3 := testClient.User.Create().SetEmail("u3@test.com").SetUsername("user3").SetFullName("User 3").SaveX(context.Background())
	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)

	chat1 := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc1 := testClient.GroupChat.Create().SetChat(chat1).SetCreator(u1).SetName("Public Group 1").SetIsPublic(true).SetInviteCode("pub1").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc1).SetUser(u1).SetRole("owner").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc1).SetUser(u2).SetRole("member").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc1).SetUser(u3).SetRole("member").SaveX(context.Background())

	chat2 := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	testClient.GroupChat.Create().SetChat(chat2).SetCreator(u1).SetName("Private Group").SetIsPublic(false).SetInviteCode("priv1").SaveX(context.Background())

	chat3 := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc3 := testClient.GroupChat.Create().SetChat(chat3).SetCreator(u1).SetName("Public Group 2").SetIsPublic(true).SetInviteCode("pub2").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc3).SetUser(u1).SetRole("owner").SaveX(context.Background())

	chat4 := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc4 := testClient.GroupChat.Create().SetChat(chat4).SetCreator(u1).SetName("Public Group 3").SetIsPublic(true).SetInviteCode("pub3").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc4).SetUser(u1).SetRole("owner").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc4).SetUser(u2).SetRole("member").SaveX(context.Background())

	t.Run("Success - List Public Groups", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats/group/public", nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.PublicGroupDTO](t, rr)
		assert.Len(t, dataList, 3)
		names := make(map[string]bool)
		for _, g := range dataList {
			names[g.Name] = true
		}
		assert.True(t, names["Public Group 1"])
		assert.True(t, names["Public Group 2"])
		assert.True(t, names["Public Group 3"])
		assert.False(t, names["Private Group"])
	})

	t.Run("Success - Search Public Groups", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats/group/public?query=Public%20Group%201", nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.PublicGroupDTO](t, rr)
		assert.Len(t, dataList, 1)
		assert.Equal(t, "Public Group 1", dataList[0].Name)
	})

	t.Run("Success - Sort by Member Count", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats/group/public?sort_by=member_count", nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.PublicGroupDTO](t, rr)
		assert.Len(t, dataList, 3)

		assert.Equal(t, "Public Group 1", dataList[0].Name)
		assert.Equal(t, 3, dataList[0].MemberCount)

		assert.Equal(t, "Public Group 3", dataList[1].Name)
		assert.Equal(t, 2, dataList[1].MemberCount)

		assert.Equal(t, "Public Group 2", dataList[2].Name)
		assert.Equal(t, 1, dataList[2].MemberCount)
	})

	t.Run("Success - Sort by Member Count with Pagination", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats/group/public?sort_by=member_count&limit=2", nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := parseResponse[[]model.PublicGroupDTO](t, rr)
		assert.Len(t, dataList, 2)
		assert.True(t, resp.Meta.HasNext)
		assert.NotEmpty(t, resp.Meta.NextCursor)

		assert.Equal(t, "Public Group 1", dataList[0].Name)
		assert.Equal(t, "Public Group 3", dataList[1].Name)

		rr2 := makeRequest("GET", fmt.Sprintf("/api/chats/group/public?sort_by=member_count&limit=2&cursor=%s", resp.Meta.NextCursor), nil, token1)
		assert.Equal(t, http.StatusOK, rr2.Code)

		var resp2 helper.ResponseWithPagination
		json.Unmarshal(rr2.Body.Bytes(), &resp2)
		dataList2 := parseResponse[[]model.PublicGroupDTO](t, rr2)
		assert.Len(t, dataList2, 1)
		assert.False(t, resp2.Meta.HasNext)

		assert.Equal(t, "Public Group 2", dataList2[0].Name)
		assert.Equal(t, 1, dataList2[0].MemberCount)
	})

	t.Run("Fail - Invalid Sort By", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats/group/public?sort_by=invalid", nil, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestJoinPublicGroup(t *testing.T) {
	clearDatabase(context.Background())
	u1 := testClient.User.Create().SetEmail("u1@test.com").SetUsername("user1").SetFullName("User 1").SaveX(context.Background())
	u2 := testClient.User.Create().SetEmail("u2@test.com").SetUsername("user2").SetFullName("User 2").SaveX(context.Background())
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	chat1 := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc1 := testClient.GroupChat.Create().SetChat(chat1).SetCreator(u1).SetName("Public Group").SetIsPublic(true).SetInviteCode("pubjoin").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc1).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())

	chat2 := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc2 := testClient.GroupChat.Create().SetChat(chat2).SetCreator(u1).SetName("Private Group").SetIsPublic(false).SetInviteCode("privjoin").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc2).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())

	t.Run("Success - Join Public Group", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/join", gc1.ChatID), nil, token2)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.ChatListResponse](t, rr)
		assert.Equal(t, "Public Group", dataMap.Name)
		assert.Equal(t, "group", dataMap.Type)
		assert.NotNil(t, dataMap.IsPublic)
		assert.True(t, *dataMap.IsPublic)

		assert.NotNil(t, dataMap.LastMessage)
		assert.Equal(t, "system_join", dataMap.LastMessage.Type)
		assert.NotNil(t, dataMap.LastMessage.SenderID)
		assert.Equal(t, u2.ID, *dataMap.LastMessage.SenderID)

		isMember, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc1.ID), groupmember.UserID(u2.ID)).Exist(context.Background())
		assert.True(t, isMember)
	})

	t.Run("Fail - Join Private Group", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/join", gc2.ChatID), nil, token2)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Already Member", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/join", gc1.ChatID), nil, token2)
		assert.Equal(t, http.StatusConflict, rr.Code)
	})

	t.Run("Fail - Group Not Found", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/chats/group/%s/join", uuid.New()), nil, token2)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestJoinGroupByInvite(t *testing.T) {
	clearDatabase(context.Background())
	u1 := testClient.User.Create().SetEmail("u1@test.com").SetUsername("user1").SetFullName("User 1").SaveX(context.Background())
	u2 := testClient.User.Create().SetEmail("u2@test.com").SetUsername("user2").SetFullName("User 2").SaveX(context.Background())
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	chat1 := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc1 := testClient.GroupChat.Create().SetChat(chat1).SetCreator(u1).SetName("Private Group").SetIsPublic(false).SetInviteCode("validcode").SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc1).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())

	chat2 := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	testClient.GroupChat.Create().SetChat(chat2).SetCreator(u1).SetName("Expired Group").SetIsPublic(false).SetInviteCode("expiredcode").SetInviteExpiresAt(time.Now().UTC().Add(-1 * time.Hour)).SaveX(context.Background())

	t.Run("Success - Join via Invite Code", func(t *testing.T) {
		rr := makeRequest("POST", "/api/chats/group/join/invite", model.JoinGroupByInviteRequest{InviteCode: "validcode"}, token2)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.ChatListResponse](t, rr)
		assert.Equal(t, "Private Group", dataMap.Name)
		assert.Equal(t, "group", dataMap.Type)
		assert.NotNil(t, dataMap.LastMessage)
		assert.Equal(t, "system_join", dataMap.LastMessage.Type)

		isMember, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc1.ID), groupmember.UserID(u2.ID)).Exist(context.Background())
		assert.True(t, isMember)
	})

	t.Run("Fail - Expired Invite Code", func(t *testing.T) {
		rr := makeRequest("POST", "/api/chats/group/join/invite", model.JoinGroupByInviteRequest{InviteCode: "expiredcode"}, token2)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Invalid Invite Code", func(t *testing.T) {
		rr := makeRequest("POST", "/api/chats/group/join/invite", model.JoinGroupByInviteRequest{InviteCode: "invalidcode"}, token2)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Already Member", func(t *testing.T) {
		rr := makeRequest("POST", "/api/chats/group/join/invite", model.JoinGroupByInviteRequest{InviteCode: "validcode"}, token2)
		assert.Equal(t, http.StatusConflict, rr.Code)
	})
}

func TestGetGroupByInviteCode(t *testing.T) {
	clearDatabase(context.Background())
	u1 := testClient.User.Create().SetEmail("u1@test.com").SetUsername("user1").SetFullName("User 1").SaveX(context.Background())

	chat1 := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	testClient.GroupChat.Create().SetChat(chat1).SetCreator(u1).SetName("Preview Group").SetIsPublic(false).SetInviteCode("previewcode").SaveX(context.Background())

	chat2 := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	testClient.GroupChat.Create().SetChat(chat2).SetCreator(u1).SetName("Expired Preview").SetIsPublic(false).SetInviteCode("expiredprev").SetInviteExpiresAt(time.Now().UTC().Add(-1 * time.Hour)).SaveX(context.Background())

	t.Run("Success - Preview Group", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats/group/invite/previewcode", nil, "")
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.GroupPreviewDTO](t, rr)
		assert.Equal(t, "Preview Group", data.Name)
		assert.Equal(t, chat1.ID, data.ID)
	})

	t.Run("Fail - Expired Code", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats/group/invite/expiredprev", nil, "")
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Invalid Code", func(t *testing.T) {
		rr := makeRequest("GET", "/api/chats/group/invite/invalid", nil, "")
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestResetInviteCode(t *testing.T) {
	clearDatabase(context.Background())
	u1 := testClient.User.Create().SetEmail("u1@test.com").SetUsername("owner").SetFullName("Owner").SaveX(context.Background())
	u2 := testClient.User.Create().SetEmail("u2@test.com").SetUsername("member").SetFullName("Member").SaveX(context.Background())

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	chatEntity := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gc := testClient.GroupChat.Create().SetChat(chatEntity).SetCreator(u1).SetName("Reset Test").SetInviteCode("oldcode").SetIsPublic(false).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u2).SetRole(groupmember.RoleMember).SaveX(context.Background())

	chatEntityPub := testClient.Chat.Create().SetType("group").SaveX(context.Background())
	gcPub := testClient.GroupChat.Create().SetChat(chatEntityPub).SetCreator(u1).SetName("Reset Public").SetInviteCode("oldpub").SetIsPublic(true).SaveX(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gcPub).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())

	t.Run("Success - Reset Code (Private Group)", func(t *testing.T) {
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s/invite", gc.ChatID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.GroupInviteResponse](t, rr)
		newCode := data.InviteCode
		assert.NotEqual(t, "oldcode", newCode)
		assert.NotEmpty(t, newCode)
		assert.NotNil(t, data.ExpiresAt)

		rrOld := makeRequest("GET", "/api/chats/group/invite/oldcode", nil, "")
		assert.Equal(t, http.StatusNotFound, rrOld.Code)

		rrNew := makeRequest("GET", fmt.Sprintf("/api/chats/group/invite/%s", newCode), nil, "")
		assert.Equal(t, http.StatusOK, rrNew.Code)
	})

	t.Run("Success - Reset Code (Public Group)", func(t *testing.T) {
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s/invite", gcPub.ChatID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.GroupInviteResponse](t, rr)
		newCode := data.InviteCode
		assert.NotEqual(t, "oldpub", newCode)
		assert.NotEmpty(t, newCode)
		assert.Nil(t, data.ExpiresAt)
	})

	t.Run("Fail - Non-Admin", func(t *testing.T) {
		rr := makeRequest("PUT", fmt.Sprintf("/api/chats/group/%s/invite", gc.ChatID), nil, token2)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})
}
