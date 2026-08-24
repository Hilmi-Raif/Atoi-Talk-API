//go:build integration

package integration

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

func uploadCompletedUserAvatar(t *testing.T, token, originalName string, content []byte) uuid.UUID {
	t.Helper()

	uploadReq := model.UploadMediaRequest{
		Usage:        "user_avatar",
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

		rr := makeRequest("GET", "/api/user/current", nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.UserDTO](t, rr)
		assert.Equal(t, validEmail, data.Email)
		assert.Equal(t, validUsername, data.Username)
		assert.Equal(t, validName, data.FullName)
		assert.Equal(t, validBio, data.Bio)
	})

	t.Run("Unauthorized", func(t *testing.T) {
		rr := makeRequest("GET", "/api/user/current", nil, "")
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
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

		rr := makeRequest("GET", fmt.Sprintf("/api/users/%s", targetUser.ID), nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.UserDTO](t, rr)
		assert.Equal(t, targetUser.ID, data.ID)
		assert.Empty(t, data.Email)
		assert.Equal(t, validUsername, data.Username)
		assert.Equal(t, validName, data.FullName)
		assert.Equal(t, validBio, data.Bio)
		assert.NotNil(t, data.IsBlockedByMe)
		assert.False(t, *data.IsBlockedByMe)
		assert.NotNil(t, data.IsBlockedByOther)
		assert.False(t, *data.IsBlockedByOther)
	})

	t.Run("Blocked By Me", func(t *testing.T) {
		clearDatabase(context.Background())

		targetUser, _ := testClient.User.Create().SetEmail("target@test.com").SetUsername("target").SetFullName("Target").SetBio("Target Bio").Save(context.Background())
		blockerUser, _ := testClient.User.Create().SetEmail("blocker@test.com").SetUsername("blocker").SetFullName("Blocker").Save(context.Background())

		testClient.UserBlock.Create().SetBlockerID(blockerUser.ID).SetBlockedID(targetUser.ID).Save(context.Background())

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, blockerUser.ID)

		rr := makeRequest("GET", fmt.Sprintf("/api/users/%s", targetUser.ID), nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.UserDTO](t, rr)
		assert.NotNil(t, data.IsBlockedByMe)
		assert.True(t, *data.IsBlockedByMe)
		assert.NotNil(t, data.IsBlockedByOther)
		assert.False(t, *data.IsBlockedByOther)
		assert.Equal(t, "target", data.Username)
		assert.Equal(t, "Target Bio", data.Bio)
		assert.Nil(t, data.LastSeenAt)
		assert.NotNil(t, data.IsOnline)
		assert.False(t, *data.IsOnline)
	})

	t.Run("Blocked By Other", func(t *testing.T) {
		clearDatabase(context.Background())

		targetUser, _ := testClient.User.Create().SetEmail("target@test.com").SetUsername("target").SetFullName("Target").SetBio("Target Bio").Save(context.Background())
		blockerUser, _ := testClient.User.Create().SetEmail("blocker@test.com").SetUsername("blocker").SetFullName("Blocker").Save(context.Background())

		testClient.UserBlock.Create().SetBlockerID(targetUser.ID).SetBlockedID(blockerUser.ID).Save(context.Background())

		token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, blockerUser.ID)

		rr := makeRequest("GET", fmt.Sprintf("/api/users/%s", targetUser.ID), nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.UserDTO](t, rr)
		assert.NotNil(t, data.IsBlockedByMe)
		assert.False(t, *data.IsBlockedByMe)
		assert.NotNil(t, data.IsBlockedByOther)
		assert.True(t, *data.IsBlockedByOther)
		assert.Equal(t, "target", data.Username)
		assert.Equal(t, "Target Bio", data.Bio)
		assert.Nil(t, data.LastSeenAt)
		assert.NotNil(t, data.IsOnline)
		assert.False(t, *data.IsOnline)
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

		rr := makeRequest("GET", fmt.Sprintf("/api/users/%s", uuid.Nil), nil, token)
		assert.Equal(t, http.StatusNotFound, rr.Code)
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

		rr := makeRequest("GET", fmt.Sprintf("/api/users/%s", deletedUser.ID), nil, token)
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

		rr := makeRequest("GET", "/api/users/invalid-uuid", nil, token)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Unauthorized", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/users/%s", uuid.Nil), nil, "")
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
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

		rr := makeRequest("PUT", "/api/user/profile", model.UpdateProfileRequest{
			FullName: "New Name",
			Bio:      "New Bio",
			Username: "newusername",
		}, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.UserDTO](t, rr)
		assert.Equal(t, "New Name", data.FullName)
		assert.Equal(t, "newusername", data.Username)

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

		rr := makeRequest("PUT", "/api/user/profile", model.UpdateProfileRequest{
			FullName: "  New Name  ",
			Bio:      "  New Bio  ",
		}, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.UserDTO](t, rr)
		assert.Equal(t, "New Name", data.FullName)
		assert.Equal(t, "New Bio", data.Bio)

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

		rr := makeRequest("PUT", "/api/user/profile", model.UpdateProfileRequest{
			FullName: "User 1",
			Username: "user2",
		}, token)
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

		rr := makeRequest("PUT", "/api/user/profile", model.UpdateProfileRequest{
			FullName:      "New Name",
			AvatarMediaID: &avatarMediaID,
		}, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		data := parseResponse[model.UserDTO](t, rr)
		assert.NotEmpty(t, data.Avatar)
		assert.Contains(t, data.Avatar, testConfig.S3PublicDomain)

		updatedUser, err := testClient.User.Query().Where(user.ID(u.ID)).WithAvatar().Only(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, updatedUser.Edges.Avatar)

		_, err = s3Client.HeadObject(context.Background(), &s3.HeadObjectInput{
			Bucket: aws.String(testConfig.S3BucketPublic),
			Key:    aws.String(updatedUser.Edges.Avatar.FileName),
		})
		assert.NoError(t, err)
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

		rr := makeRequest("PUT", "/api/user/profile", model.UpdateProfileRequest{
			FullName:     "User With Avatar",
			DeleteAvatar: true,
		}, token)
		assert.Equal(t, http.StatusOK, rr.Code)

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

		uploadReq := model.UploadMediaRequest{
			Usage:        "user_avatar",
			OriginalName: "avatar.txt",
			FileSize:     int64(len("This is not an image")),
			MimeType:     "text/plain",
			CaptchaToken: "dummy-token",
		}
		rr := makeRequest("POST", "/api/media/upload", uploadReq, token)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
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

		uploadReq := model.UploadMediaRequest{
			Usage:        "user_avatar",
			OriginalName: "large.jpg",
			FileSize:     4 * 1024 * 1024,
			MimeType:     "image/jpeg",
			CaptchaToken: "dummy-token",
		}
		rr := makeRequest("POST", "/api/media/upload", uploadReq, token)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
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
		uploadReq := model.UploadMediaRequest{
			Usage:        "user_avatar",
			OriginalName: "large_dim.jpg",
			FileSize:     int64(len(imgData)),
			MimeType:     "image/jpeg",
			CaptchaToken: "dummy-token",
		}
		rr := makeRequest("POST", "/api/media/upload", uploadReq, token)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("Unauthorized", func(t *testing.T) {
		rr := makeRequest("PUT", "/api/user/profile", model.UpdateProfileRequest{FullName: "New Name"}, "")
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("Success - Delete User Keeps Messages", func(t *testing.T) {
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
		assert.Nil(t, survivingMsg.SenderID)
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

	t.Run("Success - List All Pagination", func(t *testing.T) {
		rr := makeRequest("GET", "/api/users?query=User&limit=2", nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := parseResponse[[]model.UserDTO](t, rr)
		assert.Len(t, dataList, 2)
		assert.True(t, resp.Meta.HasNext)
		assert.NotEmpty(t, resp.Meta.NextCursor)

		assert.Equal(t, "User Alice", dataList[0].FullName)
		assert.Equal(t, "User Bob", dataList[1].FullName)

		cursor := resp.Meta.NextCursor
		rr2 := makeRequest("GET", fmt.Sprintf("/api/users?query=User&limit=2&cursor=%s", cursor), nil, token)
		assert.Equal(t, http.StatusOK, rr2.Code)

		dataList2 := parseResponse[[]model.UserDTO](t, rr2)
		assert.Len(t, dataList2, 2)
		assert.Equal(t, "User Charlie", dataList2[0].FullName)
		assert.Equal(t, "User David", dataList2[1].FullName)
	})

	t.Run("Success - Search with Private Chat ID include_chat_id=true", func(t *testing.T) {
		rr := makeRequest("GET", "/api/users?query=User%20Alice&include_chat_id=true", nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.UserDTO](t, rr)
		assert.Len(t, dataList, 1)
		assert.Equal(t, "User Alice", dataList[0].FullName)
		assert.NotNil(t, dataList[0].PrivateChatID)
		assert.Equal(t, chatEntity.ID, *dataList[0].PrivateChatID)
	})

	t.Run("Success - Search with Private Chat ID include_chat_id=false", func(t *testing.T) {
		rr := makeRequest("GET", "/api/users?query=User%20Alice&include_chat_id=false", nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.UserDTO](t, rr)
		assert.Len(t, dataList, 1)
		assert.Equal(t, "User Alice", dataList[0].FullName)
		assert.Nil(t, dataList[0].PrivateChatID)
	})

	t.Run("Success - Search without Private Chat ID", func(t *testing.T) {
		rr := makeRequest("GET", "/api/users?query=User%20Bob", nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.UserDTO](t, rr)
		assert.Len(t, dataList, 1)
		assert.Equal(t, "User Bob", dataList[0].FullName)
		assert.Nil(t, dataList[0].PrivateChatID)
	})

	t.Run("Success - Search by Username", func(t *testing.T) {
		rr := makeRequest("GET", "/api/users?query=useralice", nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.UserDTO](t, rr)
		assert.Len(t, dataList, 1)
		assert.Equal(t, "User Alice", dataList[0].FullName)
	})

	t.Run("Success - Search by Email Exact Match", func(t *testing.T) {
		rr := makeRequest("GET", "/api/users?query=useralice@test.com", nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.UserDTO](t, rr)
		assert.NotEmpty(t, dataList)
		assert.Equal(t, "User Alice", dataList[0].FullName)
	})

	t.Run("Fail - Search by Partial Email", func(t *testing.T) {
		rr := makeRequest("GET", "/api/users?query=useralice@test", nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.UserDTO](t, rr)
		assert.Len(t, dataList, 0)
	})

	t.Run("Success - Exclude Blocked Users", func(t *testing.T) {
		testClient.UserBlock.Create().SetBlockerID(searcher.ID).SetBlockedID(users["User Bob"].ID).Save(context.Background())
		defer testClient.UserBlock.Delete().Where(userblock.BlockerID(searcher.ID), userblock.BlockedID(users["User Bob"].ID)).ExecX(context.Background())

		rr := makeRequest("GET", "/api/users?query=User%20Bob", nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.UserDTO](t, rr)
		assert.Len(t, dataList, 0)
	})

	t.Run("Success - Mutual Block", func(t *testing.T) {
		testClient.UserBlock.Create().SetBlockerID(users["User Eve"].ID).SetBlockedID(searcher.ID).Save(context.Background())
		testClient.UserBlock.Create().SetBlockerID(searcher.ID).SetBlockedID(users["User Eve"].ID).Save(context.Background())
		defer testClient.UserBlock.Delete().Where(userblock.BlockerID(users["User Eve"].ID), userblock.BlockedID(searcher.ID)).ExecX(context.Background())
		defer testClient.UserBlock.Delete().Where(userblock.BlockerID(searcher.ID), userblock.BlockedID(users["User Eve"].ID)).ExecX(context.Background())

		rr := makeRequest("GET", "/api/users?query=User%20Eve", nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.UserDTO](t, rr)
		assert.Len(t, dataList, 0)
	})

	t.Run("Success - Exclude Deleted Users", func(t *testing.T) {
		testClient.User.UpdateOne(users["User Charlie"]).SetDeletedAt(time.Now().UTC()).ExecX(context.Background())
		defer testClient.User.UpdateOne(users["User Charlie"]).ClearDeletedAt().ExecX(context.Background())

		rr := makeRequest("GET", "/api/users?query=User%20Charlie", nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.UserDTO](t, rr)
		assert.Len(t, dataList, 0)
	})

	t.Run("Success - Exclude Chat Members", func(t *testing.T) {
		chatGroup := testClient.Chat.Create().SetType(chat.TypeGroup).SaveX(context.Background())
		gc := testClient.GroupChat.Create().SetChat(chatGroup).SetCreator(searcher).SetName("Exclude Test Group").SetInviteCode("exclude").SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(searcher).SetRole(groupmember.RoleOwner).SaveX(context.Background())
		testClient.GroupMember.Create().SetGroupChat(gc).SetUser(users["User Alice"]).SetRole(groupmember.RoleMember).SaveX(context.Background())

		rr := makeRequest("GET", fmt.Sprintf("/api/users?query=User&exclude_chat_id=%s", gc.ChatID), nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.UserDTO](t, rr)
		foundAlice := false
		foundBob := false
		for _, u := range dataList {
			if u.FullName == "User Alice" {
				foundAlice = true
			}
			if u.FullName == "User Bob" {
				foundBob = true
			}
		}

		assert.False(t, foundAlice)
		assert.True(t, foundBob)
	})

	t.Run("Fail - Invalid Exclude Chat ID", func(t *testing.T) {
		rr := makeRequest("GET", "/api/users?query=User&exclude_chat_id=invalid-uuid", nil, token)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Empty Result", func(t *testing.T) {
		rr := makeRequest("GET", "/api/users?query=zoro", nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseWithPagination
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataList := parseResponse[[]model.UserDTO](t, rr)
		assert.Len(t, dataList, 0)
		assert.False(t, resp.Meta.HasNext)
	})

	t.Run("Invalid Cursor", func(t *testing.T) {
		rr := makeRequest("GET", "/api/users?cursor=invalid-base64-string&query=User", nil, token)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Unauthorized", func(t *testing.T) {
		rr := makeRequest("GET", "/api/users", nil, "")
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
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
		rr := makeRequest("GET", "/api/users/blocked", nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.UserDTO](t, rr)
		assert.Len(t, dataList, 2)

		names := make(map[string]bool)
		for _, u := range dataList {
			names[u.Username] = true
			assert.NotNil(t, u.IsBlockedByMe)
			assert.True(t, *u.IsBlockedByMe)
		}
		assert.True(t, names["blocked1"])
		assert.True(t, names["blocked2"])
		assert.False(t, names["unblocked"])
	})

	t.Run("Success - Search Blocked User", func(t *testing.T) {
		rr := makeRequest("GET", "/api/users/blocked?query=Blocked%20One", nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.UserDTO](t, rr)
		assert.Len(t, dataList, 1)
		assert.Equal(t, "Blocked One", dataList[0].FullName)
	})

	t.Run("Success - Empty List", func(t *testing.T) {
		cleanUser, _ := testClient.User.Create().SetEmail("clean@test.com").SetUsername("clean").SetFullName("Clean").Save(context.Background())
		cleanToken, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, cleanUser.ID)

		rr := makeRequest("GET", "/api/users/blocked", nil, cleanToken)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataList := parseResponse[[]model.UserDTO](t, rr)
		assert.Empty(t, dataList)
	})
}

func TestBlockUser(t *testing.T) {
	clearDatabase(context.Background())

	u1, _ := testClient.User.Create().SetEmail("u1@test.com").SetUsername("u1").SetFullName("User 1").Save(context.Background())
	u2, _ := testClient.User.Create().SetEmail("u2@test.com").SetUsername("u2").SetFullName("User 2").Save(context.Background())

	token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)

	t.Run("Success Block", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/users/%s/block", u2.ID), nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		exists, _ := testClient.UserBlock.Query().Where(userblock.BlockerID(u1.ID), userblock.BlockedID(u2.ID)).Exist(context.Background())
		assert.True(t, exists)
	})

	t.Run("Success Unblock", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/users/%s/unblock", u2.ID), nil, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		exists, _ := testClient.UserBlock.Query().Where(userblock.BlockerID(u1.ID), userblock.BlockedID(u2.ID)).Exist(context.Background())
		assert.False(t, exists)
	})

	t.Run("Block Self", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/users/%s/block", u1.ID), nil, token)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Block Non-Existent User", func(t *testing.T) {
		rr := makeRequest("POST", fmt.Sprintf("/api/users/%s/block", uuid.Nil), nil, token)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Fail - Block Deleted User", func(t *testing.T) {
		deletedUser := testClient.User.Create().
			SetEmail("deleted@test.com").
			SetUsername("deleted").
			SetFullName("Deleted User").
			SetDeletedAt(time.Now().UTC()).
			SaveX(context.Background())

		rr := makeRequest("POST", fmt.Sprintf("/api/users/%s/block", deletedUser.ID), nil, token)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}
