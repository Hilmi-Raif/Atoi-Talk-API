//go:build integration

package integration

import (
	"AtoiTalkAPI/ent/pushdelivery"
	"AtoiTalkAPI/internal/infrastructure/fcm"
	"AtoiTalkAPI/internal/infrastructure/mocks"
	"context"
	"golang.org/x/oauth2"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/internal/api/application/push"
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/domain/model"
	messageworker "AtoiTalkAPI/internal/message_worker"
	"AtoiTalkAPI/internal/messaging/events"
	"AtoiTalkAPI/internal/push_worker"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestPushProjectionAtomicQueueAndLogout(t *testing.T) {
	ctx := context.Background()
	clearDatabase(ctx)
	previous := testConfig.PushEnabled
	testConfig.PushEnabled = true
	defer func() { testConfig.PushEnabled = previous }()
	sender := createTestUser(t, "push-sender")
	recipient := createTestUser(t, "push-recipient")
	token, err := helper.GenerateJWT(testConfig.JWTSecret, 3600, recipient.ID)
	require.NoError(t, err)
	installation := uuid.New()
	body := model.PushDeviceRequest{Token: "integration-fcm-token", Platform: "android", DeviceSecret: "01234567890123456789012345678901"}
	registration := makeRequest(http.MethodPut, "/api/push/devices/"+installation.String(), body, token)
	require.Equal(t, http.StatusOK, registration.Code, registration.Body.String())
	conversation := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	testClient.PrivateChat.Create().SetChat(conversation).SetUser1(sender).SetUser2(recipient).SaveX(ctx)
	msg := testClient.Message.Create().SetChat(conversation).SetSender(sender).SetContent("hello").SaveX(ctx)
	row := testClient.MessageOutbox.Create().SetChatID(conversation.ID).SetMessageID(msg.ID).SetSenderID(sender.ID).SetEventType(string(events.EventMessageNew)).SetAvailableAt(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
	lease := uuid.New()
	testClient.MessageOutbox.UpdateOne(row).SetLockToken(lease).SetLockedAt(time.Now().UTC()).ExecX(ctx)
	store := messageworker.NewEntOutboxStoreWithURLGenerator(testClient, testStorageAdapter)
	store.EnablePush(true)
	record := messageworker.OutboxRecord{ID: row.ID, LockToken: lease, EventType: row.EventType, ChatID: conversation.ID, MessageID: msg.ID, SenderID: sender.ID}
	require.NoError(t, store.Project(ctx, record))
	require.NoError(t, store.Project(ctx, record))
	require.Equal(t, 1, testClient.PushDelivery.Query().CountX(ctx))
	fake := mocks.NewMockFCMSender(t)
	worker := pushworker.Worker{Store: &pushworker.Store{Client: testClient, Dialect: "postgres"}, Sender: fake, Redis: redisAdapter.Client()}
	require.NoError(t, redisAdapter.Client().Set(ctx, "push_logout:"+push.Fingerprint(token), "1", time.Hour).Err())
	worked, err := worker.ProcessOne(ctx)
	require.NoError(t, err)
	require.True(t, worked)
	fake.AssertNotCalled(t, "Send", mock.Anything, mock.Anything, mock.Anything)
	require.Equal(t, "skipped", testClient.PushDelivery.Query().OnlyX(ctx).Outcome)
	response := makeRequest(http.MethodDelete, "/api/push/devices/"+installation.String(), nil, token)
	require.Equal(t, http.StatusOK, response.Code, response.Body.String())
}

func TestPushProviderRetryAndInvalidToken(t *testing.T) {
	ctx := context.Background()
	clearDatabase(ctx)
	sender := createTestUser(t, "push-provider-sender")
	recipient := createTestUser(t, "push-provider-recipient")
	conversation := testClient.Chat.Create().SetType(chat.TypePrivate).SaveX(ctx)
	testClient.PrivateChat.Create().SetChat(conversation).SetUser1(sender).SetUser2(recipient).SaveX(ctx)
	msg := testClient.Message.Create().SetChat(conversation).SetSender(sender).SetContent("hello").SaveX(ctx)
	device := testClient.PushDevice.Create().SetInstallationID(uuid.New()).SetBindingID(uuid.New()).SetUserID(recipient.ID).SetToken("provider-token").SetPlatform("web").SetSessionHash("provider-session").SetSecretHash("proof").SetIssuedAt(time.Now().UTC().UnixMilli()).SetExpiresAt(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
	tx, err := testClient.Tx(ctx)
	require.NoError(t, err)
	require.NoError(t, pushworker.Enqueue(ctx, tx, msg, uuid.New()))
	require.NoError(t, tx.Commit())
	var status atomic.Int32
	status.Store(http.StatusTooManyRequests)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "Bearer test-access", r.Header.Get("Authorization"))
		w.Header().Set("Retry-After", "60")
		current := int(status.Load())
		w.WriteHeader(current)
		if current == 404 {
			_, _ = w.Write([]byte(`{"error":{"details":[{"@type":"type.googleapis.com/google.firebase.fcm.v1.FcmError","errorCode":"UNREGISTERED"}]}}`))
		} else {
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	provider := &fcm.FCMAdapter{Client: server.Client(), Endpoint: server.URL, Tokens: oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "test-access"})}
	worker := pushworker.Worker{Store: &pushworker.Store{Client: testClient, Dialect: "postgres"}, Sender: provider, Redis: redisAdapter.Client()}
	worked, err := worker.ProcessOne(ctx)
	require.NoError(t, err)
	require.True(t, worked)
	row := testClient.PushDelivery.Query().OnlyX(ctx)
	require.Nil(t, row.FinishedAt)
	require.Greater(t, time.Until(row.AvailableAt), 50*time.Second)
	testClient.PushDelivery.UpdateOne(row).SetAvailableAt(time.Now().UTC().Add(-time.Second)).ExecX(ctx)
	status.Store(200)
	_, err = worker.ProcessOne(ctx)
	require.NoError(t, err)
	require.Equal(t, "sent", testClient.PushDelivery.Query().OnlyX(ctx).Outcome)
	tx, err = testClient.Tx(ctx)
	require.NoError(t, err)
	require.NoError(t, pushworker.Enqueue(ctx, tx, msg, uuid.New()))
	require.NoError(t, tx.Commit())
	status.Store(404)
	_, err = worker.ProcessOne(ctx)
	require.NoError(t, err)
	require.NotNil(t, testClient.PushDevice.GetX(ctx, device.ID).DisabledAt)
	require.Equal(t, 1, testClient.PushDelivery.Query().Where(pushdelivery.Outcome("invalid_token")).CountX(ctx))
}
