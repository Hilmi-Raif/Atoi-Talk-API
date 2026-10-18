package pushworker

import (
	"AtoiTalkAPI/internal/infrastructure/observability"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go.opentelemetry.io/otel/trace"
	"log/slog"
	"testing"
	"time"

	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/pushdelivery"
	"AtoiTalkAPI/ent/pushdevice"
	"AtoiTalkAPI/internal/infrastructure/fcm"
	"AtoiTalkAPI/internal/infrastructure/mocks"
	"entgo.io/ent/dialect"
	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	_ "github.com/mattn/go-sqlite3"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func pushFixture(t *testing.T) (*ent.Client, *ent.Message, *ent.PushDevice, *redis.Client) {
	t.Helper()
	ctx := context.Background()
	c := enttest.Open(t, dialect.SQLite, "file:push-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	t.Cleanup(func() { _ = c.Close() })
	sender := c.User.Create().SaveX(ctx)
	recipient := c.User.Create().SaveX(ctx)
	chat := c.Chat.Create().SetType("private").SaveX(ctx)
	c.PrivateChat.Create().SetChatID(chat.ID).SetUser1ID(sender.ID).SetUser2ID(recipient.ID).SaveX(ctx)
	msg := c.Message.Create().SetChatID(chat.ID).SetSenderID(sender.ID).SetContent("hello").SaveX(ctx)
	d := c.PushDevice.Create().SetBindingID(uuid.New()).SetInstallationID(uuid.New()).SetUserID(recipient.ID).SetToken("token").SetPlatform(pushdevice.PlatformAndroid).SetSessionHash("session").SetSecretHash("secret").SetIssuedAt(time.Now().UTC().Add(-time.Minute).UnixMilli()).SetExpiresAt(time.Now().UTC().Add(time.Hour)).SaveX(ctx)
	r := redis.NewClient(&redis.Options{Addr: miniredis.RunT(t).Addr()})
	t.Cleanup(func() { _ = r.Close() })
	return c, msg, d, r
}

func enqueue(t *testing.T, c *ent.Client, msg *ent.Message, event uuid.UUID) {
	t.Helper()
	tx, err := c.Tx(context.Background())
	require.NoError(t, err)
	require.NoError(t, Enqueue(context.Background(), tx, msg, event))
	require.NoError(t, tx.Commit())
}

func TestEnqueueIsTransactionalAndIdempotent(t *testing.T) {
	ctx := context.Background()
	c, msg, _, _ := pushFixture(t)
	event := uuid.New()
	tx, err := c.Tx(ctx)
	require.NoError(t, err)
	require.NoError(t, Enqueue(ctx, tx, msg, event))
	require.NoError(t, tx.Rollback())
	require.Zero(t, c.PushDelivery.Query().CountX(ctx))
	enqueue(t, c, msg, event)
	enqueue(t, c, msg, event)
	require.Equal(t, 1, c.PushDelivery.Query().CountX(ctx))
}

func TestWorkerRetriesWithoutRepeatingSuccessfulDelivery(t *testing.T) {
	ctx := context.Background()
	c, msg, _, r := pushFixture(t)
	enqueue(t, c, msg, uuid.New())
	sender := mocks.NewMockFCMSender(t)
	sender.On("Send", mock.Anything, "token", mock.Anything).Return(&fcm.SendError{Status: 429, Retry: true, After: time.Minute}).Once()
	sender.On("Send", mock.Anything, "token", mock.Anything).Return(nil).Once()
	w := Worker{Store: &Store{Client: c, Dialect: dialect.SQLite}, Sender: sender, Redis: r}
	worked, err := w.ProcessOne(ctx)
	require.True(t, worked)
	require.NoError(t, err)
	pending := c.PushDelivery.Query().OnlyX(ctx)
	require.Nil(t, pending.FinishedAt)
	require.Greater(t, pending.AvailableAt.Sub(time.Now().UTC()), 50*time.Second)
	c.PushDelivery.UpdateOne(pending).SetAvailableAt(time.Now().UTC().Add(-time.Second)).ExecX(ctx)
	_, err = w.ProcessOne(ctx)
	require.NoError(t, err)
	require.Equal(t, "sent", c.PushDelivery.Query().OnlyX(ctx).Outcome)
	worked, err = w.ProcessOne(ctx)
	require.NoError(t, err)
	require.False(t, worked)
	sender.AssertNumberOfCalls(t, "Send", 2)
}

func TestWorkerSuppressesInvalidBindingsAndSessions(t *testing.T) {
	for _, reason := range []string{"logout", "revoked", "binding", "expired", "deleted", "blocked"} {
		t.Run(reason, func(t *testing.T) {
			ctx := context.Background()
			c, msg, d, r := pushFixture(t)
			enqueue(t, c, msg, uuid.New())
			switch reason {
			case "logout":
				require.NoError(t, r.Set(ctx, "push_logout:"+d.SessionHash, "1", time.Hour).Err())
			case "revoked":
				require.NoError(t, r.Set(ctx, "revoked_user:"+d.UserID.String(), time.Now().UTC().UnixMilli(), time.Hour).Err())
			case "binding":
				c.PushDevice.UpdateOne(d).SetBindingID(uuid.New()).ExecX(ctx)
			case "expired":
				c.PushDevice.UpdateOne(d).SetExpiresAt(time.Now().UTC().Add(-time.Second)).ExecX(ctx)
			case "deleted":
				c.Message.UpdateOne(msg).SetDeletedAt(time.Now().UTC()).ExecX(ctx)
			case "blocked":
				c.UserBlock.Create().SetBlockerID(d.UserID).SetBlockedID(*msg.SenderID).ExecX(ctx)
			}
			sender := mocks.NewMockFCMSender(t)
			w := Worker{Store: &Store{Client: c, Dialect: dialect.SQLite}, Sender: sender, Redis: r}
			_, err := w.ProcessOne(ctx)
			require.NoError(t, err)
			sender.AssertNotCalled(t, "Send", mock.Anything, mock.Anything, mock.Anything)
			require.Equal(t, "skipped", c.PushDelivery.Query().OnlyX(ctx).Outcome)
		})
	}
}

func TestWorkerInvalidTokenAndLeaseFencing(t *testing.T) {
	ctx := context.Background()
	c, msg, d, r := pushFixture(t)
	enqueue(t, c, msg, uuid.New())
	sender := mocks.NewMockFCMSender(t)
	sender.On("Send", mock.Anything, "token", mock.Anything).Return(&fcm.SendError{Status: 404, Invalid: true}).Once()
	store := &Store{Client: c, Dialect: dialect.SQLite}
	w := Worker{Store: store, Sender: sender, Redis: r}
	_, err := w.ProcessOne(ctx)
	require.NoError(t, err)
	require.NotNil(t, c.PushDevice.GetX(ctx, d.ID).DisabledAt)
	require.Equal(t, "invalid_token", c.PushDelivery.Query().OnlyX(ctx).Outcome)
	c.PushDelivery.Update().ClearFinishedAt().SetAvailableAt(time.Now().UTC().Add(-time.Second)).ExecX(ctx)
	old, err := store.Claim(ctx)
	require.NoError(t, err)
	c.PushDelivery.UpdateOne(old).SetLockedAt(time.Now().UTC().Add(-2 * time.Minute)).ExecX(ctx)
	current, err := store.Claim(ctx)
	require.NoError(t, err)
	require.ErrorIs(t, store.Finish(ctx, old, "sent", nil, 0), ErrLeaseLost)
	require.NoError(t, store.Finish(ctx, current, "skipped", nil, 0))
	require.True(t, c.PushDelivery.Query().Where(pushdelivery.FinishedAtNotNil()).ExistX(ctx))
}

func TestExpiredTemporaryBanDoesNotSuppressPush(t *testing.T) {
	ctx := context.Background()
	c, msg, d, _ := pushFixture(t)
	c.User.UpdateOneID(d.UserID).SetIsBanned(true).SetBannedUntil(time.Now().UTC().Add(-time.Hour)).ExecX(ctx)
	allowed, err := Eligible(ctx, c, msg, d.UserID)
	require.NoError(t, err)
	require.True(t, allowed)

}

func TestWorkerRunDrainsQueueAndStops(t *testing.T) {
	c, msg, _, r := pushFixture(t)
	enqueue(t, c, msg, uuid.New())
	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	sender := mocks.NewMockFCMSender(t)
	sender.On("Send", mock.Anything, "token", mock.Anything).Return(nil).Once()
	w := Worker{Store: &Store{Client: c, Dialect: dialect.SQLite}, Sender: sender, Redis: r}
	require.NoError(t, w.Run(ctx))
	sender.AssertNumberOfCalls(t, "Send", 1)
	require.Equal(t, "sent", c.PushDelivery.Query().OnlyX(context.Background()).Outcome)
}
func TestWorkerPermanentFailureAndRetryLimit(t *testing.T) {
	for _, permanent := range []bool{true, false} {
		t.Run(fmt.Sprint(permanent), func(t *testing.T) {
			ctx := context.Background()
			c, msg, _, r := pushFixture(t)
			enqueue(t, c, msg, uuid.New())
			if !permanent {
				c.PushDelivery.Update().SetAttemptCount(9).ExecX(ctx)
			}
			sender := mocks.NewMockFCMSender(t)
			sender.On("Send", mock.Anything, "token", mock.Anything).Return(&fcm.SendError{Status: 403, Retry: !permanent}).Once()
			w := Worker{Store: &Store{Client: c, Dialect: dialect.SQLite}, Sender: sender, Redis: r}
			_, err := w.ProcessOne(ctx)
			require.NoError(t, err)
			require.Equal(t, "dead_letter", c.PushDelivery.Query().OnlyX(ctx).Outcome)
		})
	}
}

func TestGroupPushRespectsMembershipHistory(t *testing.T) {
	ctx := context.Background()
	c, msg, d, _ := pushFixture(t)
	conversation := c.Chat.Create().SetType("group").SaveX(ctx)
	group := c.GroupChat.Create().SetChatID(conversation.ID).SetCreatorID(*msg.SenderID).SetName("Push group").SetInviteCode("push-invite").SaveX(ctx)
	msg = c.Message.Create().SetChatID(conversation.ID).SetSenderID(*msg.SenderID).SetContent("group message").SaveX(ctx)
	membership := c.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(d.UserID).SetJoinedAt(msg.CreatedAt.Add(-time.Minute)).SaveX(ctx)
	allowed, err := Eligible(ctx, c, msg, d.UserID)
	require.NoError(t, err)
	require.True(t, allowed)
	enqueue(t, c, msg, uuid.New())
	require.Equal(t, 1, c.PushDelivery.Query().CountX(ctx))
	c.GroupMember.DeleteOne(membership).ExecX(ctx)
	membership = c.GroupMember.Create().SetGroupChatID(group.ID).SetUserID(d.UserID).SetJoinedAt(msg.CreatedAt.Add(time.Minute)).SaveX(ctx)
	allowed, err = Eligible(ctx, c, msg, d.UserID)
	require.NoError(t, err)
	require.False(t, allowed)
	c.GroupMember.DeleteOne(membership).ExecX(ctx)
	allowed, err = Eligible(ctx, c, msg, d.UserID)
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestWorkerRetryLogCorrelatesOriginWithoutToken(t *testing.T) {
	ctx := context.Background()
	c, msg, _, r := pushFixture(t)
	sc := trace.NewSpanContext(trace.SpanContextConfig{TraceID: trace.TraceID{7}, SpanID: trace.SpanID{8}})
	parent := observability.TraceParent(trace.ContextWithSpanContext(ctx, sc))
	origin := c.MessageOutbox.Create().SetEventType("message.new").SetMessageID(msg.ID).SetChatID(msg.ChatID).SetSenderID(*msg.SenderID).SetPayload(map[string]interface{}{observability.TraceParentKey: parent}).SaveX(ctx)
	enqueue(t, c, msg, origin.ID)
	var output bytes.Buffer
	oldLogger := slog.Default()
	slog.SetDefault(observability.NewLogger(&output, "production", "push-worker", "test"))
	t.Cleanup(func() { slog.SetDefault(oldLogger) })
	sender := mocks.NewMockFCMSender(t)
	sender.On("Send", mock.Anything, "token", mock.Anything).Return(&fcm.SendError{Status: 429, Retry: true}).Once()
	w := Worker{Store: &Store{Client: c, Dialect: dialect.SQLite}, Sender: sender, Redis: r}
	_, err := w.ProcessOne(ctx)
	require.NoError(t, err)
	var log map[string]interface{}
	require.NoError(t, json.Unmarshal(output.Bytes(), &log))
	require.Equal(t, origin.ID.String(), log["event_id"])
	require.Equal(t, sc.TraceID().String(), log["trace_id"])
	require.Equal(t, "pending", log["outcome"])
	require.Equal(t, float64(429), log["provider_status"])
	require.NotContains(t, output.String(), `"token"`)
	require.Contains(t, log["source"].(map[string]interface{})["function"], "finish")
}
