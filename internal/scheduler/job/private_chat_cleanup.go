package job

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/privatechat"
	"AtoiTalkAPI/internal/config"
	"context"
	"log/slog"
)

func RunPrivateChatCleanup(ctx context.Context, client *ent.Client, cfg *config.AppConfig) error {
	slog.Info("Running Private Chat Cleanup (Garbage Collection)")

	for {
		abandonedChats, err := client.PrivateChat.Query().
			Where(
				privatechat.User1IDIsNil(),
				privatechat.User2IDIsNil(),
			).
			Order(ent.Asc(privatechat.FieldID)).
			Select(privatechat.FieldID, privatechat.FieldChatID).
			Limit(cleanupBatchSize).
			All(ctx)
		if err != nil {
			slog.Error("Failed to query abandoned private chats", "error", err)
			return err
		}
		if len(abandonedChats) == 0 {
			break
		}

		deleted := 0
		for _, pc := range abandonedChats {
			if err := client.Chat.DeleteOneID(pc.ChatID).Exec(ctx); err != nil {
				slog.Error("Failed to delete abandoned chat", "chatID", pc.ChatID, "privateChatID", pc.ID, "error", err)
				continue
			}
			deleted++
		}
		if deleted == 0 {
			slog.Warn("Stopping private chat cleanup because the current batch made no progress")
			break
		}
	}

	return nil
}
