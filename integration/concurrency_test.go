//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestConcurrency_UnreadCount(t *testing.T) {
	clearDatabase(context.Background())

	u1 := createTestUser(t, "user1")
	u2 := createTestUser(t, "user2")

	token1, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u1.ID)
	token2, _ := helper.GenerateJWT(testConfig.JWTSecret, testConfig.JWTExp, u2.ID)

	chatEntity := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(context.Background())
	testClient.PrivateChat.Create().SetChat(chatEntity).SetUser1(u1).SetUser2(u2).SaveX(context.Background())

	var wg sync.WaitGroup
	messageCount := 20
	readCount := 5

	wg.Add(messageCount)
	for i := 0; i < messageCount; i++ {
		go func(idx int) {
			defer wg.Done()
			reqBody := model.SendMessageRequest{
				ChatID:  chatEntity.ID,
				Content: fmt.Sprintf("Message %d", idx),
			}
			makeRequest("POST", "/api/messages", reqBody, token1)
		}(i)
	}

	wg.Add(readCount)
	for i := 0; i < readCount; i++ {
		go func() {
			defer wg.Done()
			time.Sleep(time.Millisecond * 10)
			makeRequest("POST", fmt.Sprintf("/api/chats/%s/read", chatEntity.ID), nil, token2)
		}()
	}

	wg.Wait()

	pc, _ := testClient.PrivateChat.Query().Where(privatechat.ChatID(chatEntity.ID)).Only(context.Background())

	assert.GreaterOrEqual(t, pc.User2UnreadCount, 0)
	assert.LessOrEqual(t, pc.User2UnreadCount, messageCount)

	count, _ := testClient.Message.Query().Count(context.Background())
	assert.Equal(t, messageCount, count)
}
