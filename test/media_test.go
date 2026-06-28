package test

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/groupmember"
	"AtoiTalkAPI/ent/message"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"AtoiTalkAPI/internal/helper"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
)

func TestUploadMedia(t *testing.T) {
	clearDatabase(context.Background())

	u := createTestUser(t, "uploader")
	token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u.ID)

	t.Run("Success - Upload Image", func(t *testing.T) {
		imgData := createTestImage(t, 100, 100)
		req := newUploadMediaRequest("message_attachment", "test_image.jpg", len(imgData), "image/jpeg", "dummy-token")
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)

		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		mediaMap := dataMap["media"].(map[string]interface{})

		assert.NotEmpty(t, mediaMap["id"])
		assert.Equal(t, "test_image.jpg", mediaMap["original_name"])
		assert.Equal(t, "image/jpeg", mediaMap["mime_type"])
		assert.Equal(t, "pending", mediaMap["upload_status"])
		assert.NotEmpty(t, dataMap["upload_url"])
		uploadHeaders := dataMap["upload_headers"].(map[string]interface{})
		assert.Equal(t, "image/jpeg", uploadHeaders["Content-Type"])
		assert.NotContains(t, uploadHeaders, "Content-Length")
		assert.Contains(t, strings.ToLower(dataMap["upload_url"].(string)), "content-length")

		fileName := mediaMap["file_name"].(string)
		_, err := s3Client.PutObject(context.Background(), &s3.PutObjectInput{
			Bucket:      aws.String(testConfig.S3BucketPrivate),
			Key:         aws.String(fileName),
			Body:        bytes.NewReader(imgData),
			ContentType: aws.String("image/jpeg"),
		})
		assert.NoError(t, err)

		completeReq, _ := http.NewRequest("POST", fmt.Sprintf("/api/media/%s/complete", mediaMap["id"]), nil)
		completeReq.Header.Set("Authorization", "Bearer "+token)
		completeRR := executeRequest(completeReq)
		if !assert.Equal(t, http.StatusOK, completeRR.Code) {
			printBody(t, completeRR)
		}

		var completeResp helper.ResponseSuccess
		json.Unmarshal(completeRR.Body.Bytes(), &completeResp)
		completeMap := completeResp.Data.(map[string]interface{})
		assert.Equal(t, "completed", completeMap["upload_status"])
		assert.NotEmpty(t, completeMap["url"])
	})

	t.Run("Fail - Invalid JSON", func(t *testing.T) {
		req, _ := http.NewRequest("POST", "/api/media/upload", bytes.NewBufferString("{"))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Success - Message Attachment Allows Any MIME", func(t *testing.T) {
		req := newUploadMediaRequest("message_attachment", "test.exe", 100, "application/x-msdownload", "dummy-token")
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)
		if !assert.Equal(t, http.StatusOK, rr.Code) {
			printBody(t, rr)
		}
	})

	t.Run("Fail - Missing Captcha", func(t *testing.T) {
		req := newUploadMediaRequest("message_attachment", "test_image.jpg", 100, "image/jpeg", "")
		req.Header.Set("Authorization", "Bearer "+token)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Fail - Unauthorized", func(t *testing.T) {
		req := newUploadMediaRequest("message_attachment", "test_image.jpg", 100, "image/jpeg", "dummy-token")
		rr := executeRequest(req)
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})
}

func newUploadMediaRequest(usage, originalName string, fileSize int, mimeType, captchaToken string) *http.Request {
	body, _ := json.Marshal(map[string]interface{}{
		"usage":         usage,
		"original_name": originalName,
		"file_size":     fileSize,
		"mime_type":     mimeType,
		"captcha_token": captchaToken,
	})
	req, _ := http.NewRequest("POST", "/api/media/upload", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")
	return req
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
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/media/%s/url", mediaPrivate.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		assert.NotEmpty(t, dataMap["url"])
	})

	t.Run("Success - Refresh URL (Group Chat)", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/media/%s/url", mediaGroup.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusOK, rr.Code)

		var resp helper.ResponseSuccess
		json.Unmarshal(rr.Body.Bytes(), &resp)
		dataMap := resp.Data.(map[string]interface{})
		assert.NotEmpty(t, dataMap["url"])
	})

	t.Run("Fail - Not Member (Private Chat)", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/media/%s/url", mediaPrivate.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token3)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Not Member (Group Chat)", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/media/%s/url", mediaGroup.ID), nil)
		req.Header.Set("Authorization", "Bearer "+token3)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("Fail - Media Not Found", func(t *testing.T) {
		req, _ := http.NewRequest("GET", fmt.Sprintf("/api/media/%s/url", "00000000-0000-0000-0000-000000000000"), nil)
		req.Header.Set("Authorization", "Bearer "+token1)

		rr := executeRequest(req)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}
