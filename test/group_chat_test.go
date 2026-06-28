package test

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

func newGroupJSONRequest(method, path, token string, payload interface{}) *http.Request {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest(method, path, bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)
	return req
}

func uploadCompletedGroupAvatar(t *testing.T, token, originalName string, content []byte) uuid.UUID {
	t.Helper()

	uploadReq := newUploadMediaRequest("group_avatar", originalName, len(content), "image/jpeg", "dummy-token")
	uploadReq.Header.Set("Authorization", "Bearer "+token)
	uploadRR := executeRequest(uploadReq)
	if !assert.Equal(t, http.StatusOK, uploadRR.Code) {
		printBody(t, uploadRR)
	}

	var uploadResp helper.ResponseSuccess
	json.Unmarshal(uploadRR.Body.Bytes(), &uploadResp)
	uploadData := uploadResp.Data.(map[string]interface{})
	mediaMap := uploadData["media"].(map[string]interface{})
	mediaID := mediaMap["id"].(string)
	fileName := mediaMap["file_name"].(string)

	_, err := s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket:      aws.String(testConfig.S3BucketPublic),
		Key:         aws.String(fileName),
		Body:        bytes.NewReader(content),
		ContentType: aws.String("image/jpeg"),
	})
	assert.NoError(t, err)

	completeReq, _ := http.NewRequest("POST", fmt.Sprintf("/api/media/%s/complete", mediaID), nil)
	completeReq.Header.Set("Authorization", "Bearer "+token)
	completeRR := executeRequest(completeReq)
	if !assert.Equal(t, http.StatusOK, completeRR.Code) {
		printBody(t, completeRR)
	}

	parsedMediaID, err := uuid.Parse(mediaID)
	assert.NoError(t, err)
	return parsedMediaID
}

func TestCreateGroupChat(t *testing.T) {
	clearDatabase(context.Background())

	password := "Password123!"
	hashedPassword, _ := helper.HashPassword(password)
	u1, _ := testClient.User.Create().SetEmail("u1@test.com").SetUsername("user1").SetFullName("User 1").SetPasswordHash(hashedPassword).Save(context.Background())
	u2, _ := testClient.User.Create().SetEmail("u2@test.com").SetUsername("user2").SetFullName("User 2").SetPasswordHash(hashedPassword).Save(context.Background())
	u3, _ := testClient.User.Create().SetEmail("u3@test.com").SetUsername("user3").SetFullName("User 3").SetPasswordHash(hashedPassword).Save(context.Background())

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)

	t.Run("Success - Create Group with Text Only (Private Default)", func(t *testing.T) {
		req := newGroupJSONRequest("POST", "/api/chats/group", token1, model.CreateGroupChatRequest{
			Name:        "Test Group 1",
			Description: "A group for testing",
			MemberIDs:   []uuid.UUID{u2.ID, u3.ID},
		})

		rr := executeRequest(req)
		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})

		assert.Equal(t, "Test Group 1", dataMap["name"])
		assert.Equal(t, "A group for testing", dataMap["description"])
		assert.Equal(t, "group", dataMap["type"])
		assert.Equal(t, "owner", dataMap["my_role"])
		assert.Equal(t, float64(3), dataMap["member_count"])
		assert.NotEmpty(t, dataMap["invite_code"], "Invite code should be returned in response")
		assert.NotNil(t, dataMap["invite_expires_at"], "Invite expiration should be returned for private group")

		lastMsg, ok := dataMap["last_message"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "system_create", lastMsg["type"])
		actionData := lastMsg["action_data"].(map[string]interface{})
		assert.Equal(t, "Test Group 1", actionData["initial_name"])

		gc, err := testClient.GroupChat.Query().Where(groupchat.Name("Test Group 1")).WithChat().Only(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, u1.ID, *gc.CreatedBy)
		assert.NotEmpty(t, gc.InviteCode, "Invite code should be generated automatically")
		assert.NotNil(t, gc.InviteExpiresAt, "Private group should have invite expiration")

		members, err := gc.QueryMembers().All(context.Background())
		assert.NoError(t, err)
		assert.Len(t, members, 3)
	})

	t.Run("Success - Create Public Group", func(t *testing.T) {
		req := newGroupJSONRequest("POST", "/api/chats/group", token1, model.CreateGroupChatRequest{
			Name:      "Public Group",
			MemberIDs: []uuid.UUID{u2.ID},
			IsPublic:  true,
		})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		assert.True(t, dataMap["is_public"].(bool))
		assert.NotEmpty(t, dataMap["invite_code"], "Invite code should be returned in response")
		assert.Nil(t, dataMap["invite_expires_at"], "Invite expiration should be nil for public group")

		gc, err := testClient.GroupChat.Query().Where(groupchat.Name("Public Group")).Only(context.Background())
		assert.NoError(t, err)
		assert.True(t, gc.IsPublic)
		assert.Nil(t, gc.InviteExpiresAt, "Public group should NOT have invite expiration")
	})

	t.Run("Success - Create Group with Whitespace", func(t *testing.T) {
		req := newGroupJSONRequest("POST", "/api/chats/group", token1, model.CreateGroupChatRequest{
			Name:        "  Spaced Group  ",
			Description: "  Spaced Desc  ",
			MemberIDs:   []uuid.UUID{u2.ID},
		})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		gc, err := testClient.GroupChat.Query().Where(groupchat.Name("Spaced Group")).Only(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "Spaced Group", gc.Name)
		assert.Equal(t, "Spaced Desc", *gc.Description)
	})

	t.Run("Success - Create Group with Avatar", func(t *testing.T) {
		fileContent := createTestImage(t, 100, 100)
		avatarMediaID := uploadCompletedGroupAvatar(t, token1, "test_avatar.jpg", fileContent)

		req := newGroupJSONRequest("POST", "/api/chats/group", token1, model.CreateGroupChatRequest{
			Name:          "Group With Avatar",
			MemberIDs:     []uuid.UUID{u2.ID},
			AvatarMediaID: &avatarMediaID,
		})

		rr := executeRequest(req)
		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
			return
		}

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		avatarURL := dataMap["avatar"].(string)
		assert.Contains(t, avatarURL, testConfig.S3PublicDomain, "Group avatar URL should contain public domain")

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
		assert.NoError(t, err, "Avatar file should exist in S3")
	})

	t.Run("Fail - No Members", func(t *testing.T) {
		req := newGroupJSONRequest("POST", "/api/chats/group", token1, model.CreateGroupChatRequest{Name: "Empty Group"})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Add Self", func(t *testing.T) {
		req := newGroupJSONRequest("POST", "/api/chats/group", token1, model.CreateGroupChatRequest{
			Name:      "Self Group",
			MemberIDs: []uuid.UUID{u1.ID},
		})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Invalid Member ID", func(t *testing.T) {
		req := newGroupJSONRequest("POST", "/api/chats/group", token1, model.CreateGroupChatRequest{
			Name:      "Ghost Group",
			MemberIDs: []uuid.UUID{uuid.New()},
		})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Blocked Member", func(t *testing.T) {

		testClient.UserBlock.Create().SetBlockerID(u1.ID).SetBlockedID(u2.ID).Exec(context.Background())

		req := newGroupJSONRequest("POST", "/api/chats/group", token1, model.CreateGroupChatRequest{
			Name:      "Blocked Group",
			MemberIDs: []uuid.UUID{u2.ID},
		})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)

		testClient.UserBlock.Delete().Exec(context.Background())
	})

	t.Run("Fail - No Name", func(t *testing.T) {
		req := newGroupJSONRequest("POST", "/api/chats/group", token1, model.CreateGroupChatRequest{MemberIDs: []uuid.UUID{u2.ID}})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Invalid JSON", func(t *testing.T) {
		req, _ := http.NewRequest("POST", "/api/chats/group", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Success - Group Survives Creator Deletion (SetNull)", func(t *testing.T) {

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
		assert.Nil(t, gcReload.CreatedBy, "CreatedBy should be NULL")

		exists, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(member.ID)).Exist(context.Background())
		assert.True(t, exists, "Other members should remain in the group")
	})

	t.Run("Fail - Create Group with Deleted User", func(t *testing.T) {
		deletedUser, _ := testClient.User.Create().
			SetEmail("deleted@test.com").
			SetUsername("deleted").
			SetFullName("Deleted User").
			SetDeletedAt(time.Now().UTC()).
			Save(context.Background())

		req := newGroupJSONRequest("POST", "/api/chats/group", token1, model.CreateGroupChatRequest{
			Name:      "Group with Deleted User",
			MemberIDs: []uuid.UUID{deletedUser.ID},
		})

		rr := executeRequest(req)
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

	t.Run("Success - Rename Group (Owner)", func(t *testing.T) {
		newName := "New Group Name"
		req := newGroupJSONRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), token1, model.UpdateGroupChatRequest{Name: &newName})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		gcReload, _ := testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).Only(context.Background())
		assert.Equal(t, "New Group Name", gcReload.Name)

		lastMsg, err := testClient.Chat.Query().Where(chat.ID(gc.ChatID)).QueryLastMessage().Only(context.Background())
		if assert.NoError(t, err) {
			assert.Equal(t, message.TypeSystemRename, lastMsg.Type)
			assert.Equal(t, "New Group Name", lastMsg.ActionData["new_name"])
		}

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		assert.Equal(t, "owner", dataMap["my_role"])
	})

	t.Run("Success - Update Description (Owner)", func(t *testing.T) {
		newDescription := "New Description"
		req := newGroupJSONRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), token1, model.UpdateGroupChatRequest{Description: &newDescription})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		assert.Equal(t, "New Description", dataMap["description"])

		gcReload, _ := testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).Only(context.Background())
		assert.Equal(t, "New Description", *gcReload.Description)

		lastMsg, err := testClient.Chat.Query().Where(chat.ID(gc.ChatID)).QueryLastMessage().Only(context.Background())
		if assert.NoError(t, err) {
			assert.Equal(t, message.TypeSystemDescription, lastMsg.Type)
			assert.Equal(t, "New Description", lastMsg.ActionData["new_description"])
		}
	})

	t.Run("Success - Update IsPublic to True (Should Remove Expiry)", func(t *testing.T) {
		isPublic := true
		req := newGroupJSONRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), token1, model.UpdateGroupChatRequest{IsPublic: &isPublic})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		assert.True(t, dataMap["is_public"].(bool))
		assert.NotNil(t, dataMap["invite_code"])
		assert.Nil(t, dataMap["invite_expires_at"])

		gcReload, _ := testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).Only(context.Background())
		assert.True(t, gcReload.IsPublic)
		assert.Nil(t, gcReload.InviteExpiresAt, "Public group should have nil InviteExpiresAt")

		lastMsg, err := testClient.Chat.Query().Where(chat.ID(gc.ChatID)).QueryLastMessage().Only(context.Background())
		if assert.NoError(t, err) {
			assert.Equal(t, "system_visibility", string(lastMsg.Type))
			assert.Equal(t, "public", lastMsg.ActionData["new_visibility"])
		}
	})

	t.Run("Success - Update IsPublic to False (Should Add Expiry and Reset Code)", func(t *testing.T) {

		gcBefore, _ := testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).Only(context.Background())
		oldCode := gcBefore.InviteCode

		isPublic := false
		req := newGroupJSONRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), token1, model.UpdateGroupChatRequest{IsPublic: &isPublic})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		assert.False(t, dataMap["is_public"].(bool))
		newCode := dataMap["invite_code"].(string)
		assert.NotEmpty(t, newCode)
		assert.NotEqual(t, oldCode, newCode)
		assert.NotNil(t, dataMap["invite_expires_at"])

		gcReload, _ := testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).Only(context.Background())
		assert.False(t, gcReload.IsPublic)
		assert.NotNil(t, gcReload.InviteExpiresAt, "Private group should have InviteExpiresAt")
		assert.NotEqual(t, oldCode, gcReload.InviteCode, "Invite code should be reset when changing to private")

		lastMsg, err := testClient.Chat.Query().Where(chat.ID(gc.ChatID)).QueryLastMessage().Only(context.Background())
		if assert.NoError(t, err) {
			assert.Equal(t, "system_visibility", string(lastMsg.Type))
			assert.Equal(t, "private", lastMsg.ActionData["new_visibility"])
		}
	})

	t.Run("Success - Update Avatar (Owner)", func(t *testing.T) {

		fileContent := createTestImage(t, 100, 100)
		avatarMediaID := uploadCompletedGroupAvatar(t, token1, "new_avatar.jpg", fileContent)

		req := newGroupJSONRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), token1, model.UpdateGroupChatRequest{AvatarMediaID: &avatarMediaID})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		gcReload, _ := testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).WithAvatar().Only(context.Background())
		assert.NotNil(t, gcReload.Edges.Avatar)

		_, err := s3Client.HeadObject(context.Background(), &s3.HeadObjectInput{
			Bucket: aws.String(testConfig.S3BucketPublic),
			Key:    aws.String(gcReload.Edges.Avatar.FileName),
		})
		assert.NoError(t, err, "Avatar file should exist in S3")

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		avatarURL := dataMap["avatar"].(string)
		assert.Contains(t, avatarURL, testConfig.S3PublicDomain, "Group avatar URL should contain public domain")

		lastMsg, _ := testClient.Chat.Query().Where(chat.ID(gc.ChatID)).QueryLastMessage().Only(context.Background())
		assert.Equal(t, message.TypeSystemAvatar, lastMsg.Type)
	})

	t.Run("Success - Delete Avatar (Owner)", func(t *testing.T) {

		gcReload, _ := testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).WithAvatar().Only(context.Background())
		assert.NotNil(t, gcReload.Edges.Avatar)

		req := newGroupJSONRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), token1, model.UpdateGroupChatRequest{DeleteAvatar: true})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		assert.Empty(t, dataMap["avatar"])

		gcReload, _ = testClient.GroupChat.Query().Where(groupchat.ID(gc.ID)).WithAvatar().Only(context.Background())
		assert.Nil(t, gcReload.Edges.Avatar)

		lastMsg, _ := testClient.Chat.Query().Where(chat.ID(gc.ChatID)).QueryLastMessage().Only(context.Background())
		assert.Equal(t, message.TypeSystemAvatar, lastMsg.Type)
		assert.Equal(t, "removed", lastMsg.ActionData["action"])
	})

	t.Run("Fail - Member (Not Admin/Owner)", func(t *testing.T) {
		newName := "Hacked Name"
		req := newGroupJSONRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), token3, model.UpdateGroupChatRequest{Name: &newName})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Not Member", func(t *testing.T) {
		newName := "Hacked Name"
		req := newGroupJSONRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), token4, model.UpdateGroupChatRequest{Name: &newName})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Invalid Data (Name too short)", func(t *testing.T) {
		newName := "Hi"
		req := newGroupJSONRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), token1, model.UpdateGroupChatRequest{Name: &newName})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Update Deleted Group", func(t *testing.T) {

		chatEntity.Update().SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		newName := "Zombie Group"
		req := newGroupJSONRequest("PUT", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), token1, model.UpdateGroupChatRequest{Name: &newName})

		rr := executeRequest(req)
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
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		assert.Len(t, dataList, 3)
		assert.False(t, resp.Meta.HasNext)
	})

	t.Run("Success - Search by Username", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/group/%s/members?query=beta", chatEntity.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		assert.Len(t, dataList, 1)
		member := dataList[0].(map[string]interface{})

		assert.Equal(t, "Beta User", member["full_name"])
	})

	t.Run("Success - Search by Full Name", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/group/%s/members?query=Gamma", chatEntity.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		assert.Len(t, dataList, 1)
		member := dataList[0].(map[string]interface{})
		assert.Equal(t, "Gamma User", member["full_name"])
	})

	t.Run("Success - Pagination", func(t *testing.T) {

		req1, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/group/%s/members?limit=2", chatEntity.ID), nil)
		req1.Header.Set("Authorization", "Bearer "+token1)
		rr1 := executeRequest(req1)
		assert.Equal(t, http.StatusOK, rr1.Code)

		var resp1 helper.ResponseWithPagination
		json.Unmarshal(rr1.Body.Bytes(), &resp1)
		dataList1 := resp1.Data.([]interface{})
		assert.Len(t, dataList1, 2)
		assert.True(t, resp1.Meta.HasNext)
		assert.NotEmpty(t, resp1.Meta.NextCursor)

		req2, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/group/%s/members?limit=2&cursor=%s", chatEntity.ID, resp1.Meta.NextCursor), nil)
		req2.Header.Set("Authorization", "Bearer "+token1)
		rr2 := executeRequest(req2)
		assert.Equal(t, http.StatusOK, rr2.Code)

		var resp2 helper.ResponseWithPagination
		json.Unmarshal(rr2.Body.Bytes(), &resp2)
		dataList2 := resp2.Data.([]interface{})
		assert.Len(t, dataList2, 1)
		assert.False(t, resp2.Meta.HasNext)
	})

	t.Run("Fail - Not a Member", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token4)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Group Not Found", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/chats/group/99999/members", nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Search Members in Deleted Group", func(t *testing.T) {

		chatEntity.Update().SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
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
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 2)

		msg1 := dataList[0].(map[string]interface{})
		assert.Equal(t, "system_add", msg1["type"])
		actionData := msg1["action_data"].(map[string]interface{})
		assert.Equal(t, u1.ID.String(), actionData["actor_id"])
		assert.Equal(t, float64(5), msg1["member_count"])

		isMember4, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u4.ID)).Exist(context.Background())
		assert.True(t, isMember4)
		isMember5, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u5.ID)).Exist(context.Background())
		assert.True(t, isMember5)
	})

	t.Run("Fail - Member Tries to Add Member", func(t *testing.T) {
		u6 := testClient.User.Create().SetEmail("u6@test.com").SetUsername("another").SetFullName("Another User").SaveX(context.Background())
		reqBody := model.AddGroupMemberRequest{UserIDs: []uuid.UUID{u6.ID}}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token3)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Add Existing Member", func(t *testing.T) {
		reqBody := model.AddGroupMemberRequest{UserIDs: []uuid.UUID{u2.ID}}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusConflict, rr.Code)
	})

	t.Run("Fail - Add Non-Existent User", func(t *testing.T) {
		reqBody := model.AddGroupMemberRequest{UserIDs: []uuid.UUID{uuid.New()}}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Add Member to Deleted Group", func(t *testing.T) {

		chatEntity.Update().SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		u7 := testClient.User.Create().SetEmail("u7@test.com").SetUsername("another7").SetFullName("Another User 7").SaveX(context.Background())
		reqBody := model.AddGroupMemberRequest{UserIDs: []uuid.UUID{u7.ID}}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
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
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members", chatEntity.ID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
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
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/leave", gc.ChatID), nil)
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		assert.Equal(t, "system_leave", dataMap["type"])
		assert.Equal(t, u2.ID.String(), dataMap["sender_id"])
		assert.Equal(t, float64(1), dataMap["member_count"])

		exists, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u2.ID)).Exist(context.Background())
		assert.False(t, exists)
	})

	t.Run("Fail - Owner Leaves", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/leave", gc.ChatID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Not Member", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/leave", gc.ChatID), nil)
		req.Header.Set("Authorization", "Bearer "+token3)

		rr := executeRequest(req)
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
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u3.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		assert.Equal(t, "system_kick", dataMap["type"])
		actionData := dataMap["action_data"].(map[string]interface{})
		assert.Equal(t, u3.ID.String(), actionData["target_id"])
		assert.Equal(t, float64(2), dataMap["member_count"])

		exists, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u3.ID)).Exist(context.Background())
		assert.False(t, exists)
	})

	t.Run("Success - Admin Kicks Member", func(t *testing.T) {

		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).SetRole(groupmember.RoleMember).SaveX(context.Background())

		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u3.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		exists, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u3.ID)).Exist(context.Background())
		assert.False(t, exists)
	})

	t.Run("Success - Kick Deleted Member (Cleanup)", func(t *testing.T) {
		defer testClient.User.UpdateOne(u3).ClearDeletedAt().ExecX(context.Background())

		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).SetRole(groupmember.RoleMember).SaveX(context.Background())
		testClient.User.UpdateOne(u3).SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u3.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		actionData := dataMap["action_data"].(map[string]interface{})

		assert.Equal(t, u3.ID.String(), actionData["target_id"])
		assert.NotEmpty(t, actionData["target_name"])

		exists, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u3.ID)).Exist(context.Background())
		assert.False(t, exists)
	})

	t.Run("Fail - Admin Kicks Admin", func(t *testing.T) {

		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u3).SetRole(groupmember.RoleAdmin).SaveX(context.Background())

		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u3.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Admin Kicks Owner", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u1.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Member Kicks Member", func(t *testing.T) {

		testClient.GroupMember.Update().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u3.ID)).SetRole(groupmember.RoleMember).ExecX(context.Background())

		exists, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u4.ID)).Exist(context.Background())
		if !exists {
			testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u4).SetRole(groupmember.RoleMember).SaveX(context.Background())
		}

		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u4.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token3)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Kick Self", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u1.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Not Member", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/members/%s/kick", gc.ChatID, u2.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token4)

		rr := executeRequest(req)
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
		reqBody := model.UpdateGroupMemberRoleRequest{Role: "admin"}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", gc.ChatID, u2.ID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		assert.Equal(t, "system_promote", dataMap["type"])
		actionData := dataMap["action_data"].(map[string]interface{})
		assert.Equal(t, "admin", actionData["new_role"])

		member, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u2.ID)).Only(context.Background())
		assert.Equal(t, groupmember.RoleAdmin, member.Role)
	})

	t.Run("Success - Demote to Member", func(t *testing.T) {
		reqBody := model.UpdateGroupMemberRoleRequest{Role: "member"}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", gc.ChatID, u3.ID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		member, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u3.ID)).Only(context.Background())
		assert.Equal(t, groupmember.RoleMember, member.Role)
	})

	t.Run("Fail - Promote Deleted User", func(t *testing.T) {
		testClient.User.UpdateOne(u2).SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		reqBody := model.UpdateGroupMemberRoleRequest{Role: "admin"}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", gc.ChatID, u2.ID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)

		testClient.User.UpdateOne(u2).ClearDeletedAt().ExecX(context.Background())
	})

	t.Run("Fail - Admin Promotes", func(t *testing.T) {

		reqBody := model.UpdateGroupMemberRoleRequest{Role: "admin"}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", gc.ChatID, u3.ID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")

		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Promote Self", func(t *testing.T) {
		reqBody := model.UpdateGroupMemberRoleRequest{Role: "member"}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("PUT", fmt.Sprintf("/api/chats/group/%s/members/%s/role", gc.ChatID, u1.ID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
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
		reqBody := model.TransferGroupOwnershipRequest{NewOwnerID: u2.ID}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/transfer", gc.ChatID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		assert.Equal(t, "system_promote", dataMap["type"])
		actionData := dataMap["action_data"].(map[string]interface{})
		assert.Equal(t, "ownership_transferred", actionData["action"])

		oldOwner, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u1.ID)).Only(context.Background())
		newOwner, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc.ID), groupmember.UserID(u2.ID)).Only(context.Background())

		assert.Equal(t, groupmember.RoleAdmin, oldOwner.Role)
		assert.Equal(t, groupmember.RoleOwner, newOwner.Role)
	})

	t.Run("Fail - Transfer to Deleted User", func(t *testing.T) {
		testClient.User.UpdateOne(u3).SetDeletedAt(time.Now().UTC()).ExecX(context.Background())

		reqBody := model.TransferGroupOwnershipRequest{NewOwnerID: u3.ID}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/transfer", gc.ChatID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)

		testClient.User.UpdateOne(u3).ClearDeletedAt().ExecX(context.Background())
	})

	t.Run("Fail - Not Owner", func(t *testing.T) {

		reqBody := model.TransferGroupOwnershipRequest{NewOwnerID: u3.ID}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/transfer", gc.ChatID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Transfer to Self", func(t *testing.T) {

		reqBody := model.TransferGroupOwnershipRequest{NewOwnerID: u2.ID}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/transfer", gc.ChatID), bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
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

		req, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
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

		req, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/chats/group/%s", gc2.ChatID), nil)
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)

		c, _ := testClient.Chat.Query().Where(chat.ID(gc2.ChatID)).Only(context.Background())
		assert.Nil(t, c.DeletedAt)
	})

	t.Run("Fail - Already Deleted", func(t *testing.T) {

		req, _ := http.NewRequest("DELETE", fmt.Sprintf("/api/chats/group/%s", gc.ChatID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Success - Member Deletes Account (Remains in Group)", func(t *testing.T) {

		chatEntity3 := testClient.Chat.Create().SetType("group").SaveX(context.Background())
		gc3 := testClient.GroupChat.Create().SetChat(chatEntity3).SetCreator(u1).SetName("Delete Account Test").SetInviteCode("delete3").SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc3).SetUser(u1).SetRole(groupmember.RoleOwner).SaveX(context.Background())

		u3 := testClient.User.Create().SetEmail("u3@test.com").SetUsername("member3").SetFullName("Member 3").SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc3).SetUser(u3).SetRole(groupmember.RoleMember).SaveX(context.Background())

		testClient.User.UpdateOne(u3).SetDeletedAt(time.Now().UTC()).SetFullName("Deleted Account").ExecX(context.Background())

		exists, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc3.ID), groupmember.UserID(u3.ID)).Exist(context.Background())
		assert.True(t, exists, "Deleted user should remain in the group")

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/group/%s/members", chatEntity3.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		assert.Len(t, dataList, 1)
		member := dataList[0].(map[string]interface{})
		assert.Equal(t, u1.ID.String(), member["user_id"])
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
		req, _ := http.NewRequest("GET", "/api/chats/group/public", nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		assert.Len(t, dataList, 3)
		names := make(map[string]bool)
		for _, item := range dataList {
			g := item.(map[string]interface{})
			names[g["name"].(string)] = true
		}
		assert.True(t, names["Public Group 1"])
		assert.True(t, names["Public Group 2"])
		assert.True(t, names["Public Group 3"])
		assert.False(t, names["Private Group"])
	})

	t.Run("Success - Search Public Groups", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/chats/group/public?query=Public%20Group%201", nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)

		if resp.Data == nil {
			assert.Fail(t, "Response data is nil")
			return
		}

		dataList, ok := resp.Data.([]interface{})
		if !ok {
			assert.Fail(t, "Response data is not a list")
			return
		}

		assert.Len(t, dataList, 1)
		if len(dataList) > 0 {
			g := dataList[0].(map[string]interface{})
			assert.Equal(t, "Public Group 1", g["name"])
		}
	})

	t.Run("Success - Sort by Member Count", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/chats/group/public?sort_by=member_count", nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		assert.Len(t, dataList, 3)

		g1 := dataList[0].(map[string]interface{})
		g2 := dataList[1].(map[string]interface{})
		g3 := dataList[2].(map[string]interface{})

		assert.Equal(t, "Public Group 1", g1["name"])
		assert.Equal(t, float64(3), g1["member_count"])

		assert.Equal(t, "Public Group 3", g2["name"])
		assert.Equal(t, float64(2), g2["member_count"])

		assert.Equal(t, "Public Group 2", g3["name"])
		assert.Equal(t, float64(1), g3["member_count"])
	})

	t.Run("Success - Sort by Member Count with Pagination", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/chats/group/public?sort_by=member_count&limit=2", nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		assert.Len(t, dataList, 2)
		assert.True(t, resp.Meta.HasNext)
		assert.NotEmpty(t, resp.Meta.NextCursor)

		g1 := dataList[0].(map[string]interface{})
		g2 := dataList[1].(map[string]interface{})
		assert.Equal(t, "Public Group 1", g1["name"])
		assert.Equal(t, "Public Group 3", g2["name"])

		req2, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/group/public?sort_by=member_count&limit=2&cursor=%s", resp.Meta.NextCursor), nil)
		req2.Header.Set("Authorization", "Bearer "+token1)

		rr2 := executeRequest(req2)
		assert.Equal(t, http.StatusOK, rr2.Code)

		var resp2 helper.ResponseWithPagination
		json.Unmarshal(rr2.Body.Bytes(), &resp2)
		dataList2 := resp2.Data.([]interface{})

		assert.Len(t, dataList2, 1)
		assert.False(t, resp2.Meta.HasNext)

		g3 := dataList2[0].(map[string]interface{})
		assert.Equal(t, "Public Group 2", g3["name"])
		assert.Equal(t, float64(1), g3["member_count"])
	})

	t.Run("Fail - Invalid Sort By", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/chats/group/public?sort_by=invalid", nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
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
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/join", gc1.ChatID), nil)
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})

		assert.Equal(t, "Public Group", dataMap["name"])
		assert.Equal(t, "group", dataMap["type"])
		assert.True(t, dataMap["is_public"].(bool))

		lastMsg, ok := dataMap["last_message"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "system_join", lastMsg["type"])
		assert.Equal(t, u2.ID.String(), lastMsg["sender_id"])

		isMember, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc1.ID), groupmember.UserID(u2.ID)).Exist(context.Background())
		assert.True(t, isMember)
	})

	t.Run("Fail - Join Private Group", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/join", gc2.ChatID), nil)
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Already Member", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/join", gc1.ChatID), nil)
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusConflict, rr.Code)
	})

	t.Run("Fail - Group Not Found", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/chats/group/%s/join", uuid.New()), nil)
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
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
		reqBody := model.JoinGroupByInviteRequest{InviteCode: "validcode"}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/chats/group/join/invite", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		assert.Equal(t, "Private Group", dataMap["name"])
		assert.Equal(t, "group", dataMap["type"])
		lastMsg, ok := dataMap["last_message"].(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "system_join", lastMsg["type"])

		isMember, _ := testClient.GroupMember.Query().Where(groupmember.GroupChatID(gc1.ID), groupmember.UserID(u2.ID)).Exist(context.Background())
		assert.True(t, isMember)
	})

	t.Run("Fail - Expired Invite Code", func(t *testing.T) {
		reqBody := model.JoinGroupByInviteRequest{InviteCode: "expiredcode"}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/chats/group/join/invite", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Invalid Invite Code", func(t *testing.T) {
		reqBody := model.JoinGroupByInviteRequest{InviteCode: "invalidcode"}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/chats/group/join/invite", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Already Member", func(t *testing.T) {
		reqBody := model.JoinGroupByInviteRequest{InviteCode: "validcode"}
		body, _ := json.Marshal(reqBody)
		req, _ := http.NewRequest("POST", "/api/chats/group/join/invite", bytes.NewBuffer(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)

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
		req, _ := http.NewRequest("GET", "/api/chats/group/invite/previewcode", nil)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		data := resp.Data.(map[string]interface{})
		assert.Equal(t, "Preview Group", data["name"])
		assert.Equal(t, chat1.ID.String(), data["id"], "Should return ChatID, not GroupChatID")
	})

	t.Run("Fail - Expired Code", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/chats/group/invite/expiredprev", nil)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Invalid Code", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/chats/group/invite/invalid", nil)
		rr := executeRequest(req)
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
		req, _ := http.NewRequest("PUT", fmt.Sprintf("/api/chats/group/%s/invite", gc.ChatID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		data := resp.Data.(map[string]interface{})
		newCode := data["invite_code"].(string)
		expiresAt := data["expires_at"].(string)

		assert.NotEqual(t, "oldcode", newCode)
		assert.NotEmpty(t, newCode)
		assert.NotEmpty(t, expiresAt, "Private group reset should have expiration")

		reqOld, _ := http.NewRequest("GET", "/api/chats/group/invite/oldcode", nil)
		rrOld := executeRequest(reqOld)
		assert.Equal(t, http.StatusNotFound, rrOld.Code)

		reqNew, _ := http.NewRequest("GET", fmt.Sprintf("/api/chats/group/invite/%s", newCode), nil)
		rrNew := executeRequest(reqNew)
		assert.Equal(t, http.StatusOK, rrNew.Code)
	})

	t.Run("Success - Reset Code (Public Group)", func(t *testing.T) {
		req, _ := http.NewRequest("PUT", fmt.Sprintf("/api/chats/group/%s/invite", gcPub.ChatID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		data := resp.Data.(map[string]interface{})
		newCode := data["invite_code"].(string)
		expiresAt := data["expires_at"]

		assert.NotEqual(t, "oldpub", newCode)
		assert.NotEmpty(t, newCode)
		assert.Nil(t, expiresAt, "Public group reset should NOT have expiration")
	})

	t.Run("Fail - Non-Admin", func(t *testing.T) {
		req, _ := http.NewRequest("PUT", fmt.Sprintf("/api/chats/group/%s/invite", gc.ChatID), nil)
		req.Header.Set("Authorization", "Bearer "+token2)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})
}
