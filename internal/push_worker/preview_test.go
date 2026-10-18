package pushworker

import (
	"context"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestNotificationPreviewUsesSenderAndGroup(t *testing.T) {
	ctx := context.Background()
	c, msg, _, _ := pushFixture(t)
	c.User.UpdateOneID(*msg.SenderID).SetFullName("Alya").ExecX(ctx)
	title, body, err := notificationPreview(ctx, c, msg)
	require.NoError(t, err)
	require.Equal(t, "Alya", title)
	require.Equal(t, "hello", body)
	c.GroupChat.Create().SetChatID(msg.ChatID).SetName("Tim Produk").SetInviteCode("preview-group").SaveX(ctx)
	msg = c.Message.UpdateOne(msg).SetContent("Halo\n  semua").SaveX(ctx)
	title, body, err = notificationPreview(ctx, c, msg)
	require.NoError(t, err)
	require.Equal(t, "Tim Produk", title)
	require.Equal(t, "Alya: Halo semua", body)
	msg = c.Message.UpdateOne(msg).SetContent(strings.Repeat("界", 300)).SaveX(ctx)
	_, body, err = notificationPreview(ctx, c, msg)
	require.NoError(t, err)
	require.LessOrEqual(t, len([]rune(body)), 160)
	msg = c.Message.UpdateOne(msg).ClearContent().SaveX(ctx)
	_, body, err = notificationPreview(ctx, c, msg)
	require.NoError(t, err)
	require.Equal(t, "Alya: Mengirim lampiran", body)
}
