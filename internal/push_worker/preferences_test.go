package pushworker

import (
	"AtoiTalkAPI/internal/infrastructure/mocks"
	"context"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestPreferencesSuppressQueueAndPendingDelivery(t *testing.T) {
	for _, mode := range []string{"global", "forever", "temporary", "expired", "unmuted"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			c, msg, d, r := pushFixture(t)
			enqueue(t, c, msg, uuid.New())
			switch mode {
			case "global":
				c.User.UpdateOneID(d.UserID).SetMessageNotificationsEnabled(false).ExecX(ctx)
			case "forever":
				c.ChatNotificationPreference.Create().SetUserID(d.UserID).SetChatID(msg.ChatID).SetMutedForever(true).ExecX(ctx)
			case "temporary":
				c.ChatNotificationPreference.Create().SetUserID(d.UserID).SetChatID(msg.ChatID).SetMutedUntil(time.Now().UTC().Add(time.Hour)).ExecX(ctx)
			case "expired":
				c.ChatNotificationPreference.Create().SetUserID(d.UserID).SetChatID(msg.ChatID).SetMutedUntil(time.Now().UTC().Add(-time.Hour)).ExecX(ctx)
			}
			sender := mocks.NewMockFCMSender(t)
			if mode == "expired" || mode == "unmuted" {
				sender.On("Send", mock.Anything, "token", mock.Anything).Return(nil).Once()
			}
			w := Worker{Store: &Store{Client: c, Dialect: "sqlite3"}, Sender: sender, Redis: r}
			_, err := w.ProcessOne(ctx)
			require.NoError(t, err)
			expected := 0
			if mode == "expired" || mode == "unmuted" {
				expected = 1
			}
			sender.AssertNumberOfCalls(t, "Send", expected)
			enqueue(t, c, msg, uuid.New())
			require.Equal(t, 1+expected, c.PushDelivery.Query().CountX(ctx))
			if expected == 0 {
				c.User.UpdateOneID(d.UserID).SetMessageNotificationsEnabled(true).ExecX(ctx)
				c.ChatNotificationPreference.Delete().ExecX(ctx)
				worked, retryErr := w.ProcessOne(ctx)
				require.NoError(t, retryErr)
				require.False(t, worked)
				sender.AssertNotCalled(t, "Send", mock.Anything, mock.Anything, mock.Anything)
			}
		})
	}
}
