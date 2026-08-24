//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestUploadMedia(t *testing.T) {
	clearDatabase(context.Background())

	u := createTestUser(t, "uploader")
	token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u.ID)

	t.Run("Success - Upload Image", func(t *testing.T) {
		imgData := createTestImage(t, 100, 100)
		uploadReq := model.UploadMediaRequest{
			Usage:        "message_attachment",
			OriginalName: "test_image.jpg",
			FileSize:     int64(len(imgData)),
			MimeType:     "image/jpeg",
			CaptchaToken: "dummy-token",
		}
		rr := makeRequest("POST", "/api/media/upload", uploadReq, token)
		assert.Equal(t, http.StatusOK, rr.Code)

		uploadData := parseResponse[model.UploadMediaResponse](t, rr)
		assert.NotEqual(t, uuid.Nil, uploadData.Media.ID)
		assert.Equal(t, "test_image.jpg", uploadData.Media.OriginalName)
		assert.Equal(t, "image/jpeg", uploadData.Media.MimeType)
		assert.Equal(t, "pending", uploadData.Media.UploadStatus)
		assert.NotEmpty(t, uploadData.UploadURL)
		assert.Equal(t, "image/jpeg", uploadData.UploadHeaders["Content-Type"])
		assert.NotContains(t, uploadData.UploadHeaders, "Content-Length")
		assert.Contains(t, strings.ToLower(uploadData.UploadURL), "content-length")

		fileName := uploadData.Media.FileName
		_, err := s3Client.PutObject(context.Background(), &s3.PutObjectInput{
			Bucket:      aws.String(testConfig.S3BucketPrivate),
			Key:         aws.String(fileName),
			Body:        bytes.NewReader(imgData),
			ContentType: aws.String("image/jpeg"),
		})
		assert.NoError(t, err)

		completeRR := makeRequest("POST", fmt.Sprintf("/api/media/%s/complete", uploadData.Media.ID), nil, token)
		assert.Equal(t, http.StatusOK, completeRR.Code)

		completeData := parseResponse[model.MediaDTO](t, completeRR)
		assert.Equal(t, "completed", completeData.UploadStatus)
		assert.NotEmpty(t, completeData.URL)
	})

	t.Run("Success - Message Attachment Allows Any MIME", func(t *testing.T) {
		uploadReq := model.UploadMediaRequest{
			Usage:        "message_attachment",
			OriginalName: "test.exe",
			FileSize:     100,
			MimeType:     "application/x-msdownload",
			CaptchaToken: "dummy-token",
		}
		rr := makeRequest("POST", "/api/media/upload", uploadReq, token)
		assert.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("Fail - Missing Captcha", func(t *testing.T) {
		uploadReq := model.UploadMediaRequest{
			Usage:        "message_attachment",
			OriginalName: "test_image.jpg",
			FileSize:     100,
			MimeType:     "image/jpeg",
			CaptchaToken: "",
		}
		rr := makeRequest("POST", "/api/media/upload", uploadReq, token)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Unauthorized", func(t *testing.T) {
		uploadReq := model.UploadMediaRequest{
			Usage:        "message_attachment",
			OriginalName: "test_image.jpg",
			FileSize:     100,
			MimeType:     "image/jpeg",
			CaptchaToken: "dummy-token",
		}
		rr := makeRequest("POST", "/api/media/upload", uploadReq, "")
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})
}

func TestGetMediaURL(t *testing.T) {
	clearDatabase(context.Background())

	u1 := createTestUser(t, "user1")
	u2 := createTestUser(t, "user2")
	u3 := createTestUser(t, "user3")

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token3, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u3.ID)

	chatPrivate, _ := testClient.Chat.Create().SetType(chat.TypePrivate).Save(context.Background())
	testClient.PrivateChat.Create().SetChat(chatPrivate).SetUser1(u1).SetUser2(u2).Save(context.Background())

	chatGroup, _ := testClient.Chat.Create().SetType(chat.TypeGroup).Save(context.Background())
	gc, _ := testClient.GroupChat.Create().SetChat(chatGroup).SetCreator(u1).SetName("Test Group").SetInviteCode("test").Save(context.Background())
	testClient.GroupMember.Create().SetGroupChat(gc).SetUser(u1).SetRole(groupmember.RoleOwner).Save(context.Background())

	mediaPrivate, _ := testClient.Media.Create().
		SetFileName("private.jpg").SetOriginalName("private.jpg").SetFileSize(100).SetMimeType("image/jpeg").
		SetUploader(u1).Save(context.Background())
	testClient.Message.Create().SetChat(chatPrivate).SetSender(u1).SetType(message.TypeRegular).AddAttachments(mediaPrivate).Save(context.Background())

	mediaGroup, _ := testClient.Media.Create().
		SetFileName("group.jpg").SetOriginalName("group.jpg").SetFileSize(100).SetMimeType("image/jpeg").
		SetUploader(u1).Save(context.Background())
	testClient.Message.Create().SetChat(chatGroup).SetSender(u1).SetType(message.TypeRegular).AddAttachments(mediaGroup).Save(context.Background())

	s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(testConfig.S3BucketPrivate),
		Key:    aws.String("private.jpg"),
		Body:   bytes.NewReader([]byte("content")),
	})
	s3Client.PutObject(context.Background(), &s3.PutObjectInput{
		Bucket: aws.String(testConfig.S3BucketPrivate),
		Key:    aws.String("group.jpg"),
		Body:   bytes.NewReader([]byte("content")),
	})

	t.Run("Success - Refresh URL (Private Chat)", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/media/%s/url", mediaPrivate.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.MediaURLResponse](t, rr)
		assert.NotEmpty(t, dataMap.URL)
	})

	t.Run("Success - Refresh URL (Group Chat)", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/media/%s/url", mediaGroup.ID), nil, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.MediaURLResponse](t, rr)
		assert.NotEmpty(t, dataMap.URL)
	})

	t.Run("Fail - Not Member (Private Chat)", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/media/%s/url", mediaPrivate.ID), nil, token3)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Not Member (Group Chat)", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/media/%s/url", mediaGroup.ID), nil, token3)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Media Not Found", func(t *testing.T) {
		rr := makeRequest("GET", fmt.Sprintf("/api/media/%s/url", uuid.Nil), nil, token1)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}
