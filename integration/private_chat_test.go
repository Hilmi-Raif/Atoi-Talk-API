//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/userblock"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

func TestCreatePrivateChat(t *testing.T) {
	clearDatabase(context.Background())

	u1 := createTestUser(t, "user1")
	u2 := createTestUser(t, "user2")

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)

	t.Run("Success", func(t *testing.T) {
		reqBody := model.CreatePrivateChatRequest{
			TargetUserID: u2.ID,
		}
		rr := makeRequest("POST", "/api/chats/private", reqBody, token1)
		assert.Equal(t, http.StatusOK, rr.Code)

		dataMap := parseResponse[model.ChatResponse](t, rr)
		assert.Equal(t, "private", dataMap.Type)
		assert.NotEqual(t, uuid.Nil, dataMap.ID)
	})

	t.Run("Fail if Blocked", func(t *testing.T) {
		testClient.UserBlock.Create().SetBlockerID(u1.ID).SetBlockedID(u2.ID).SaveX(context.Background())

		reqBody := model.CreatePrivateChatRequest{TargetUserID: u2.ID}
		rr := makeRequest("POST", "/api/chats/private", reqBody, token1)
		assert.Equal(t, http.StatusForbidden, rr.Code)

		testClient.UserBlock.Delete().Where(userblock.BlockerID(u1.ID), userblock.BlockedID(u2.ID)).ExecX(context.Background())
	})

	t.Run("Chat Already Exists", func(t *testing.T) {
		reqBody := model.CreatePrivateChatRequest{TargetUserID: u2.ID}
		rr1 := makeRequest("POST", "/api/chats/private", reqBody, token1)
		assert.Equal(t, http.StatusOK, rr1.Code)

		dataMap1 := parseResponse[model.ChatResponse](t, rr1)
		chatID1 := dataMap1.ID

		rr2 := makeRequest("POST", "/api/chats/private", reqBody, token1)
		assert.Equal(t, http.StatusOK, rr2.Code)

		dataMap2 := parseResponse[model.ChatResponse](t, rr2)
		chatID2 := dataMap2.ID

		assert.Equal(t, chatID1, chatID2)
	})

	t.Run("Target User Not Found", func(t *testing.T) {
		reqBody := model.CreatePrivateChatRequest{
			TargetUserID: uuid.New(),
		}
		rr := makeRequest("POST", "/api/chats/private", reqBody, token1)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})

	t.Run("Chat With Self", func(t *testing.T) {
		reqBody := model.CreatePrivateChatRequest{
			TargetUserID: u1.ID,
		}
		rr := makeRequest("POST", "/api/chats/private", reqBody, token1)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Unauthorized", func(t *testing.T) {
		reqBody := model.CreatePrivateChatRequest{TargetUserID: uuid.New()}
		rr := makeRequest("POST", "/api/chats/private", reqBody, "")
		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("Fail - Create Chat with Deleted User", func(t *testing.T) {
		deletedUser := testClient.User.Create().
			SetEmail("deleted@test.com").
			SetUsername("deleted").
			SetFullName("Deleted User").
			SetDeletedAt(time.Now().UTC()).
			SaveX(context.Background())

		reqBody := model.CreatePrivateChatRequest{TargetUserID: deletedUser.ID}
		rr := makeRequest("POST", "/api/chats/private", reqBody, token1)
		assert.Equal(t, http.StatusNotFound, rr.Code)
	})
}

func TestCreatePrivateChat_ReverseOrder(t *testing.T) {
	clearDatabase(context.Background())

	u1 := createTestUser(t, "user1")
	u2 := createTestUser(t, "user2")

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	reqBody1 := model.CreatePrivateChatRequest{TargetUserID: u2.ID}
	rr1 := makeRequest("POST", "/api/chats/private", reqBody1, token1)
	assert.Equal(t, http.StatusOK, rr1.Code)
	data1 := parseResponse[model.ChatResponse](t, rr1)

	reqBody2 := model.CreatePrivateChatRequest{TargetUserID: u1.ID}
	rr2 := makeRequest("POST", "/api/chats/private", reqBody2, token2)
	assert.Equal(t, http.StatusOK, rr2.Code)
	data2 := parseResponse[model.ChatResponse](t, rr2)

	assert.Equal(t, data1.ID, data2.ID)
}

func TestCreatePrivateChat_Validation(t *testing.T) {
	clearDatabase(context.Background())
	u1 := createTestUser(t, "user1")
	token, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)

	t.Run("Missing TargetUserID", func(t *testing.T) {
		rr := makeRequest("POST", "/api/chats/private", map[string]interface{}{}, token)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("Invalid JSON", func(t *testing.T) {
		rr := makeRequest("POST", "/api/chats/private", "invalid-json", token)
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}
