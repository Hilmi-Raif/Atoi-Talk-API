package test

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/ent/userblock"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func createTestImage(t *testing.T, width, height int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	for x := 0; x < width; x++ {
		for y := 0; y < height; y++ {
			img.Set(x, y, color.RGBA{255, 0, 0, 255})
		}
	}

	var buf bytes.Buffer
	err := jpeg.Encode(&buf, img, nil)
	assert.NoError(t, err)
	return buf.Bytes()
}

func newProfileJSONRequest(token string, payload interface{}) *http.Request {
	body, _ := json.Marshal(payload)
	req, _ := http.NewRequest("PUT", "/api/user/profile", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return req
}

func uploadCompletedUserAvatar(t *testing.T, token, originalName string, content []byte) uuid.UUID {
	t.Helper()

	uploadReq := newUploadMediaRequest("user_avatar", originalName, len(content), "image/jpeg", "dummy-token")
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

func TestGetCurrentUser(t *testing.T) {
	validEmail := "current@example.com"
	validUsername := "currentuser"
	validName := "Current User"
	validBio := "I am current user"

	t.Run("Success", func(t *testing.T) {
		clearDatabase(context.Background())

		u, err := testClient.User.Create().
			SetEmail(validEmail).
			SetUsername(validUsername).
			SetFullName(validName).
			SetBio(validBio).
			Save(context.Background())
		assert.NoError(t, err)

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u.ID)

		req, _ := http.NewRequest("GET", "/api/user/current", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}
		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)

		dataMap, ok := resp.Data.(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, validEmail, dataMap["email"])
		assert.Equal(t, validUsername, dataMap["username"])
		assert.Equal(t, validName, dataMap["full_name"])
		assert.Equal(t, validBio, dataMap["bio"])
	})

	t.Run("Unauthorized", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/user/current", nil)

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusUnauthorized, rr.Code) {
			printBody(t, rr)
		}
	})
}

func TestGetUserProfile(t *testing.T) {
	validEmail := "other@example.com"
	validUsername := "otheruser"
	validName := "Other User"
	validBio := "I am another user"

	t.Run("Success", func(t *testing.T) {
		clearDatabase(context.Background())

		targetUser, err := testClient.User.Create().
			SetEmail(validEmail).
			SetUsername(validUsername).
			SetFullName(validName).
			SetBio(validBio).
			Save(context.Background())
		assert.NoError(t, err)

		requestingUser, err := testClient.User.Create().
			SetEmail("requester@example.com").
			SetUsername("requester").
			SetFullName("Requester").
			Save(context.Background())
		assert.NoError(t, err)

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, requestingUser.ID)

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/users/%s", targetUser.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
			return
		}
		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)

		dataMap, ok := resp.Data.(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, targetUser.ID.String(), dataMap["id"])
		assert.Nil(t, dataMap["email"])
		assert.Equal(t, validUsername, dataMap["username"])
		assert.Equal(t, validName, dataMap["full_name"])
		assert.Equal(t, validBio, dataMap["bio"])
		assert.False(t, dataMap["is_blocked_by_me"].(bool))
		assert.False(t, dataMap["is_blocked_by_other"].(bool))
	})

	t.Run("Blocked By Me (Should Return OK with flags)", func(t *testing.T) {
		clearDatabase(context.Background())

		targetUser, _ := testClient.User.Create().SetEmail("target@test.com").SetUsername("target").SetFullName("Target").SetBio("Target Bio").Save(context.Background())
		blockerUser, _ := testClient.User.Create().SetEmail("blocker@test.com").SetUsername("blocker").SetFullName("Blocker").Save(context.Background())

		testClient.UserBlock.Create().SetBlockerID(blockerUser.ID).SetBlockedID(targetUser.ID).Save(context.Background())

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, blockerUser.ID)

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/users/%s", targetUser.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap, ok := resp.Data.(map[string]interface{})
		assert.True(t, ok)
		assert.True(t, dataMap["is_blocked_by_me"].(bool))
		assert.False(t, dataMap["is_blocked_by_other"].(bool))

		assert.Equal(t, "target", dataMap["username"])
		assert.Equal(t, "Target Bio", dataMap["bio"])

		assert.Nil(t, dataMap["last_seen_at"])
		assert.False(t, dataMap["is_online"].(bool))
	})

	t.Run("Blocked By Other (Should Return OK with flags)", func(t *testing.T) {
		clearDatabase(context.Background())

		targetUser, _ := testClient.User.Create().SetEmail("target@test.com").SetUsername("target").SetFullName("Target").SetBio("Target Bio").Save(context.Background())
		blockerUser, _ := testClient.User.Create().SetEmail("blocker@test.com").SetUsername("blocker").SetFullName("Blocker").Save(context.Background())

		testClient.UserBlock.Create().SetBlockerID(targetUser.ID).SetBlockedID(blockerUser.ID).Save(context.Background())

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, blockerUser.ID)

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/users/%s", targetUser.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap, ok := resp.Data.(map[string]interface{})
		assert.True(t, ok)
		assert.False(t, dataMap["is_blocked_by_me"].(bool))
		assert.True(t, dataMap["is_blocked_by_other"].(bool))

		assert.Equal(t, "target", dataMap["username"])
		assert.Equal(t, "Target Bio", dataMap["bio"])

		assert.Nil(t, dataMap["last_seen_at"])
		assert.False(t, dataMap["is_online"].(bool))
	})

	t.Run("User Not Found", func(t *testing.T) {
		clearDatabase(context.Background())

		requestingUser, err := testClient.User.Create().
			SetEmail("requester@example.com").
			SetUsername("requester").
			SetFullName("Requester").
			Save(context.Background())
		assert.NoError(t, err)

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, requestingUser.ID)

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/users/%s", "00000000-0000-0000-0000-000000000000"), nil)
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusNotFound, rr.Code) {
			printBody(t, rr)
		}
	})

	t.Run("Fail - Get Deleted User Profile", func(t *testing.T) {
		clearDatabase(context.Background())

		deletedUser, _ := testClient.User.Create().
			SetEmail("deleted@test.com").
			SetUsername("deleted").
			SetFullName("Deleted User").
			SetDeletedAt(time.Now().UTC()).
			Save(context.Background())

		requestingUser, _ := testClient.User.Create().
			SetEmail("requester@test.com").
			SetUsername("requester").
			SetFullName("Requester").
			Save(context.Background())

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, requestingUser.ID)

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/users/%s", deletedUser.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Invalid ID Format", func(t *testing.T) {
		clearDatabase(context.Background())

		requestingUser, err := testClient.User.Create().
			SetEmail("requester@example.com").
			SetUsername("requester").
			SetFullName("Requester").
			Save(context.Background())
		assert.NoError(t, err)

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, requestingUser.ID)

		req, _ := http.NewRequest("GET", "/api/users/invalid-uuid", nil)
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusBadRequest, rr.Code) {
			printBody(t, rr)
		}
	})

	t.Run("Unauthorized", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/users/00000000-0000-0000-0000-000000000000", nil)

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusUnauthorized, rr.Code) {
			printBody(t, rr)
		}
	})
}

func TestUpdateProfile(t *testing.T) {
	validEmail := "profile@example.com"
	validUsername := "profileuser"
	validPassword := "Password123!"

	t.Run("Success Update Info Only", func(t *testing.T) {
		clearDatabase(context.Background())

		hashedPassword, _ := helper.HashPassword(validPassword)
		u, err := testClient.User.Create().
			SetEmail(validEmail).
			SetUsername(validUsername).
			SetFullName("Old Name").
			SetPasswordHash(hashedPassword).
			Save(context.Background())
		assert.NoError(t, err)

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u.ID)

		req := newProfileJSONRequest(token, model.UpdateProfileRequest{
			FullName: "New Name",
			Bio:      "New Bio",
			Username: "newusername",
		})

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}
		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)

		dataMap, ok := resp.Data.(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "New Name", dataMap["full_name"])
		assert.Equal(t, "newusername", dataMap["username"])

		updatedUser, err := testClient.User.Query().Where(user.ID(u.ID)).Only(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "New Name", *updatedUser.FullName)
		assert.Equal(t, "New Bio", *updatedUser.Bio)
		assert.Equal(t, "newusername", *updatedUser.Username)
	})

	t.Run("Success Update Info with Whitespace", func(t *testing.T) {
		clearDatabase(context.Background())

		hashedPassword, _ := helper.HashPassword(validPassword)
		u, err := testClient.User.Create().
			SetEmail(validEmail).
			SetUsername(validUsername).
			SetFullName("Old Name").
			SetPasswordHash(hashedPassword).
			Save(context.Background())
		assert.NoError(t, err)

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u.ID)

		req := newProfileJSONRequest(token, model.UpdateProfileRequest{
			FullName: "  New Name  ",
			Bio:      "  New Bio  ",
		})

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}
		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)

		dataMap, ok := resp.Data.(map[string]interface{})
		assert.True(t, ok)
		assert.Equal(t, "New Name", dataMap["full_name"])
		assert.Equal(t, "New Bio", dataMap["bio"])

		updatedUser, err := testClient.User.Query().Where(user.ID(u.ID)).Only(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "New Name", *updatedUser.FullName)
		assert.Equal(t, "New Bio", *updatedUser.Bio)
	})

	t.Run("Fail Update Username Taken", func(t *testing.T) {
		clearDatabase(context.Background())

		u1, _ := testClient.User.Create().SetEmail("u1@test.com").SetUsername("user1").SetFullName("User 1").Save(context.Background())
		testClient.User.Create().SetEmail("u2@test.com").SetUsername("user2").SetFullName("User 2").Save(context.Background())

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)

		req := newProfileJSONRequest(token, model.UpdateProfileRequest{
			FullName: "User 1",
			Username: "user2",
		})

		rr := executeRequest(req)
		assert.Equal(t, http.StatusConflict, rr.Code)
	})

	t.Run("Success Update Avatar", func(t *testing.T) {
		clearDatabase(context.Background())

		hashedPassword, _ := helper.HashPassword(validPassword)
		u, err := testClient.User.Create().
			SetEmail(validEmail).
			SetUsername(validUsername).
			SetFullName("Old Name").
			SetPasswordHash(hashedPassword).
			Save(context.Background())
		assert.NoError(t, err)

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u.ID)

		imgData := createTestImage(t, 400, 400)
		avatarMediaID := uploadCompletedUserAvatar(t, token, "avatar.jpg", imgData)

		req := newProfileJSONRequest(token, model.UpdateProfileRequest{
			FullName:      "New Name",
			AvatarMediaID: &avatarMediaID,
		})

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}
		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)

		dataMap, ok := resp.Data.(map[string]interface{})
		assert.True(t, ok)
		avatarURL, ok := dataMap["avatar"].(string)
		assert.True(t, ok)
		assert.NotEmpty(t, avatarURL)

		assert.Contains(t, avatarURL, testConfig.S3PublicDomain, "Avatar URL should contain the configured public domain")

		updatedUser, err := testClient.User.Query().Where(user.ID(u.ID)).WithAvatar().Only(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, updatedUser.Edges.Avatar)

		_, err = s3Client.HeadObject(context.Background(), &s3.HeadObjectInput{
			Bucket: aws.String(testConfig.S3BucketPublic),
			Key:    aws.String(updatedUser.Edges.Avatar.FileName),
		})
		assert.NoError(t, err, "Avatar file should exist in S3")
	})

	t.Run("Delete Avatar", func(t *testing.T) {
		clearDatabase(context.Background())

		u, err := testClient.User.Create().
			SetEmail(validEmail).SetUsername(validUsername).SetFullName("User With Avatar").
			Save(context.Background())
		assert.NoError(t, err)

		media, err := testClient.Media.Create().
			SetFileName("old_avatar.jpg").SetOriginalName("old.jpg").
			SetFileSize(1024).SetMimeType("image/jpeg").
			SetCategory("user_avatar").
			SetUploader(u).
			Save(context.Background())
		assert.NoError(t, err)

		u, err = testClient.User.UpdateOne(u).SetAvatar(media).Save(context.Background())
		assert.NoError(t, err)

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u.ID)

		req := newProfileJSONRequest(token, model.UpdateProfileRequest{
			FullName:     "User With Avatar",
			DeleteAvatar: true,
		})

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}

		updatedUser, _ := testClient.User.Query().Where(user.ID(u.ID)).WithAvatar().Only(context.Background())
		assert.Nil(t, updatedUser.Edges.Avatar)
	})

	t.Run("Fail Request Avatar Upload with Invalid Image MIME", func(t *testing.T) {
		clearDatabase(context.Background())

		hashedPassword, _ := helper.HashPassword(validPassword)
		u, err := testClient.User.Create().
			SetEmail(validEmail).
			SetUsername(validUsername).
			SetFullName("User").
			SetPasswordHash(hashedPassword).
			Save(context.Background())
		assert.NoError(t, err)

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u.ID)

		req := newUploadMediaRequest("user_avatar", "avatar.txt", len("This is not an image"), "text/plain", "dummy-token")
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusBadRequest, rr.Code) {
			printBody(t, rr)
		}
	})

	t.Run("Fail Request Avatar Upload Too Large", func(t *testing.T) {
		clearDatabase(context.Background())

		hashedPassword, _ := helper.HashPassword(validPassword)
		u, err := testClient.User.Create().
			SetEmail(validEmail).
			SetUsername(validUsername).
			SetFullName("User").
			SetPasswordHash(hashedPassword).
			Save(context.Background())
		assert.NoError(t, err)

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u.ID)

		req := newUploadMediaRequest("user_avatar", "large.jpg", 4*1024*1024, "image/jpeg", "dummy-token")
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusBadRequest, rr.Code) {
			printBody(t, rr)
		}
	})

	t.Run("Success Request Avatar Upload Ignores Dimensions", func(t *testing.T) {
		clearDatabase(context.Background())

		hashedPassword, _ := helper.HashPassword(validPassword)
		u, err := testClient.User.Create().
			SetEmail(validEmail).
			SetUsername(validUsername).
			SetFullName("User").
			SetPasswordHash(hashedPassword).
			Save(context.Background())
		assert.NoError(t, err)

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u.ID)

		imgData := createTestImage(t, 900, 900)
		req := newUploadMediaRequest("user_avatar", "large_dim.jpg", len(imgData), "image/jpeg", "dummy-token")
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}
	})

	t.Run("Unauthorized", func(t *testing.T) {
		req := newProfileJSONRequest("", model.UpdateProfileRequest{FullName: "New Name"})

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusUnauthorized, rr.Code) {
			printBody(t, rr)
		}
	})

	t.Run("Success - Delete User Keeps Messages (SetNull)", func(t *testing.T) {
		clearDatabase(context.Background())

		u1, _ := testClient.User.Create().SetEmail("u1@test.com").SetUsername("u1").SetFullName("U1").Save(context.Background())
		u2, _ := testClient.User.Create().SetEmail("u2@test.com").SetUsername("u2").SetFullName("U2").Save(context.Background())

		chatEntity, _ := testClient.Chat.Create().SetType(chat.TypePrivate).Save(context.Background())
		testClient.PrivateChat.Create().SetChat(chatEntity).SetUser1(u1).SetUser2(u2).Save(context.Background())

		msg, _ := testClient.Message.Create().
			SetChatID(chatEntity.ID).
			SetSenderID(u1.ID).
			SetType(message.TypeRegular).
			SetContent("I will survive").
			Save(context.Background())

		err := testClient.User.DeleteOneID(u1.ID).Exec(context.Background())
		assert.NoError(t, err)

		survivingMsg, err := testClient.Message.Query().Where(message.ID(msg.ID)).Only(context.Background())
		assert.NoError(t, err)
		assert.Equal(t, "I will survive", *survivingMsg.Content)
		assert.Nil(t, survivingMsg.SenderID, "SenderID should be NULL")
	})
}

func TestSearchUsers(t *testing.T) {
	clearDatabase(context.Background())

	names := []string{"User David", "User Alice", "User Charlie", "User Bob", "User Eve"}
	users := make(map[string]*ent.User)
	for _, name := range names {

		username := strings.ToLower(strings.ReplaceAll(name, " ", ""))
		email := username + "@test.com"
		hashedPassword, _ := helper.HashPassword("Password123!")
		u, _ := testClient.User.Create().
			SetEmail(email).
			SetUsername(username).
			SetFullName(name).
			SetPasswordHash(hashedPassword).
			Save(context.Background())
		users[name] = u
	}

	searcher, _ := testClient.User.Create().
		SetEmail("searcher@test.com").
		SetUsername("searcher").
		SetFullName("Searcher").
		SetPasswordHash("hash").
		Save(context.Background())
	token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, searcher.ID)

	chatEntity, _ := testClient.Chat.Create().SetType(chat.TypePrivate).Save(context.Background())
	testClient.PrivateChat.Create().
		SetChat(chatEntity).
		SetUser1(searcher).
		SetUser2(users["User Alice"]).
		Save(context.Background())

	t.Run("Success - List All (Pagination)", func(t *testing.T) {

		req, _ := http.NewRequest("GET", "/api/users?query=User&limit=2", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)

		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 2)
		assert.True(t, resp.Meta.HasNext)
		assert.NotEmpty(t, resp.Meta.NextCursor)

		user1 := dataList[0].(map[string]interface{})
		user2 := dataList[1].(map[string]interface{})

		assert.Equal(t, "User Alice", user1["full_name"])
		assert.Equal(t, "User Bob", user2["full_name"])

		cursor := resp.Meta.NextCursor
		req2, _ := http.NewRequest("GET", fmt.Sprintf("/api/users?query=User&limit=2&cursor=%s", cursor), nil)
		req2.Header.Set("Authorization", "Bearer "+token)
		rr2 := executeRequest(req2)

		if !assert.Equal(t, http.StatusOK, rr2.Code) {
			printBody(t, rr2)
		}
		var resp2 helper.ResponseWithPagination
		json.Unmarshal(rr2.Body.Bytes(), &resp2)

		dataList2 := resp2.Data.([]interface{})
		assert.Len(t, dataList2, 2)

		user3 := dataList2[0].(map[string]interface{})
		user4 := dataList2[1].(map[string]interface{})
		assert.Equal(t, "User Charlie", user3["full_name"])
		assert.Equal(t, "User David", user4["full_name"])
	})

	t.Run("Success - Search with Private Chat ID (include_chat_id=true)", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/users?query=User%20Alice&include_chat_id=true", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}
		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)

		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 1)
		userAlice := dataList[0].(map[string]interface{})
		assert.Equal(t, "User Alice", userAlice["full_name"])
		assert.NotNil(t, userAlice["private_chat_id"])
		assert.Equal(t, chatEntity.ID.String(), userAlice["private_chat_id"])
	})

	t.Run("Success - Search with Private Chat ID (include_chat_id=false)", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/users?query=User%20Alice&include_chat_id=false", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}
		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)

		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 1)
		userAlice := dataList[0].(map[string]interface{})
		assert.Equal(t, "User Alice", userAlice["full_name"])
		assert.Nil(t, userAlice["private_chat_id"])
	})

	t.Run("Success - Search without Private Chat ID", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/users?query=User%20Bob", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}
		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)

		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 1)
		userBob := dataList[0].(map[string]interface{})
		assert.Equal(t, "User Bob", userBob["full_name"])
		assert.Nil(t, userBob["private_chat_id"])
	})

	t.Run("Success - Search by Username", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/users?query=useralice", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}
		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)

		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 1)
		userAlice := dataList[0].(map[string]interface{})
		assert.Equal(t, "User Alice", userAlice["full_name"])
	})

	t.Run("Success - Search by Email Exact Match", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/users?query=useralice@test.com", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}
		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)

		dataList := resp.Data.([]interface{})
		if assert.NotEmpty(t, dataList) {
			userAlice := dataList[0].(map[string]interface{})
			assert.Equal(t, "User Alice", userAlice["full_name"])
		}
	})

	t.Run("Fail - Search by Partial Email (Should be Empty)", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/users?query=useralice@test", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}
		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)

		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 0)
	})

	t.Run("Success - Exclude Blocked Users", func(t *testing.T) {

		testClient.UserBlock.Create().SetBlockerID(searcher.ID).SetBlockedID(users["User Bob"].ID).Save(context.Background())
		defer testClient.UserBlock.Delete().Where(userblock.BlockerID(searcher.ID), userblock.BlockedID(users["User Bob"].ID)).ExecX(context.Background())

		req, _ := http.NewRequest("GET", "/api/users?query=User%20Bob", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 0, "Blocked user should not appear in search")
	})

	t.Run("Success - Mutual Block (Both should not see each other)", func(t *testing.T) {

		testClient.UserBlock.Create().SetBlockerID(users["User Eve"].ID).SetBlockedID(searcher.ID).Save(context.Background())
		testClient.UserBlock.Create().SetBlockerID(searcher.ID).SetBlockedID(users["User Eve"].ID).Save(context.Background())
		defer testClient.UserBlock.Delete().Where(userblock.BlockerID(users["User Eve"].ID), userblock.BlockedID(searcher.ID)).ExecX(context.Background())
		defer testClient.UserBlock.Delete().Where(userblock.BlockerID(searcher.ID), userblock.BlockedID(users["User Eve"].ID)).ExecX(context.Background())

		req, _ := http.NewRequest("GET", "/api/users?query=User%20Eve", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 0, "Mutually blocked user should not appear in search")
	})

	t.Run("Success - Exclude Deleted Users", func(t *testing.T) {

		testClient.User.UpdateOne(users["User Charlie"]).SetDeletedAt(time.Now().UTC()).ExecX(context.Background())
		defer testClient.User.UpdateOne(users["User Charlie"]).ClearDeletedAt().ExecX(context.Background())

		req, _ := http.NewRequest("GET", "/api/users?query=User%20Charlie", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 0, "Deleted user should not appear in search")
	})

	t.Run("Success - Exclude Chat Members", func(t *testing.T) {

		chatGroup := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		gc := testClient.GroupChat.Create().SetChat(chatGroup).SetCreator(searcher).SetName("Exclude Test Group").SetInviteCode("exclude").SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(searcher).SetRole(groupmember.RoleOwner).SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(users["User Alice"]).SetRole(groupmember.RoleMember).SaveX(context.Background())

		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/users?query=User&exclude_chat_id=%s", gc.ChatID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})

		foundAlice := false
		foundBob := false
		for _, item := range dataList {
			u := item.(map[string]interface{})
			if u["full_name"] == "User Alice" {
				foundAlice = true
			}
			if u["full_name"] == "User Bob" {
				foundBob = true
			}
		}

		assert.False(t, foundAlice, "User Alice (member) should be excluded")
		assert.True(t, foundBob, "User Bob (non-member) should be included")
	})

	t.Run("Fail - Invalid Exclude Chat ID", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/users?query=User&exclude_chat_id=invalid-uuid", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Empty Result", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/users?query=zoro", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}
		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)

		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 0)
		assert.False(t, resp.Meta.HasNext)
	})

	t.Run("Invalid Cursor", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/users?cursor=invalid-base64-string&query=User", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusBadRequest, rr.Code) {
			printBody(t, rr)
		}
	})

	t.Run("Unauthorized", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/users", nil)
		rr := executeRequest(req)
		if !assert.Equal(t, http.StatusUnauthorized, rr.Code) {
			printBody(t, rr)
		}
	})
}

func TestGetBlockedUsers(t *testing.T) {
	clearDatabase(context.Background())

	blocker, _ := testClient.User.Create().SetEmail("blocker@test.com").SetUsername("blocker").SetFullName("Blocker").Save(context.Background())
	blocked1, _ := testClient.User.Create().SetEmail("blocked1@test.com").SetUsername("blocked1").SetFullName("Blocked One").Save(context.Background())
	blocked2, _ := testClient.User.Create().SetEmail("blocked2@test.com").SetUsername("blocked2").SetFullName("Blocked Two").Save(context.Background())
	testClient.User.Create().SetEmail("unblocked@test.com").SetUsername("unblocked").SetFullName("Unblocked").Save(context.Background())

	testClient.UserBlock.Create().SetBlockerID(blocker.ID).SetBlockedID(blocked1.ID).Save(context.Background())
	testClient.UserBlock.Create().SetBlockerID(blocker.ID).SetBlockedID(blocked2.ID).Save(context.Background())

	token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, blocker.ID)

	t.Run("Success - List All Blocked Users", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/users/blocked", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 2)

		names := make(map[string]bool)
		for _, item := range dataList {
			u := item.(map[string]interface{})
			names[u["username"].(string)] = true
			assert.True(t, u["is_blocked_by_me"].(bool))
		}
		assert.True(t, names["blocked1"])
		assert.True(t, names["blocked2"])
		assert.False(t, names["unblocked"])
	})

	t.Run("Success - Search Blocked User", func(t *testing.T) {
		req, _ := http.NewRequest("GET", "/api/users/blocked?query=Blocked%20One", nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := resp.Data.([]interface{})
		assert.Len(t, dataList, 1)
		assert.Equal(t, "Blocked One", dataList[0].(map[string]interface{})["full_name"])
	})

	t.Run("Success - Empty List", func(t *testing.T) {

		cleanUser, _ := testClient.User.Create().SetEmail("clean@test.com").SetUsername("clean").SetFullName("Clean").Save(context.Background())
		cleanToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, cleanUser.ID)

		req, _ := http.NewRequest("GET", "/api/users/blocked", nil)
		req.Header.Set("Authorization", "Bearer "+cleanToken)
		rr := executeRequest(req)

		assert.Equal(t, http.StatusOK, rr.Code)
		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		assert.Empty(t, resp.Data)
	})
}

func TestBlockUser(t *testing.T) {
	clearDatabase(context.Background())

	u1, _ := testClient.User.Create().SetEmail("u1@test.com").SetUsername("u1").SetFullName("User 1").Save(context.Background())
	u2, _ := testClient.User.Create().SetEmail("u2@test.com").SetUsername("u2").SetFullName("User 2").Save(context.Background())

	token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)

	t.Run("Success Block", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/users/%s/block", u2.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		exists, _ := testClient.UserBlock.Query().Where(userblock.BlockerID(u1.ID), userblock.BlockedID(u2.ID)).Exist(context.Background())
		assert.True(t, exists)
	})

	t.Run("Success Unblock", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/users/%s/unblock", u2.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		exists, _ := testClient.UserBlock.Query().Where(userblock.BlockerID(u1.ID), userblock.BlockedID(u2.ID)).Exist(context.Background())
		assert.False(t, exists)
	})

	t.Run("Block Self", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/users/%s/block", u1.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Block Non-Existent User", func(t *testing.T) {
		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/users/%s/block", "00000000-0000-0000-0000-000000000000"), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Block Deleted User", func(t *testing.T) {
		deletedUser, _ := testClient.User.Create().
			SetEmail("deleted@test.com").
			SetUsername("deleted").
			SetFullName("Deleted User").
			SetDeletedAt(time.Now().UTC()).
			Save(context.Background())

		req, _ := http.NewRequest("POST", fmt.Sprintf("/api/users/%s/block", deletedUser.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token)
		rr := executeRequest(req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}
