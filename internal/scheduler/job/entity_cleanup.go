package job

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/chat"
	"AtoiTalkAPI/ent/user"
	"AtoiTalkAPI/internal/config"
	"context"
	"log/slog"
	"time"
)

const cleanupBatchSize = 100

func RunEntityCleanup(ctx context.Context, client *ent.Client, cfg *config.AppConfig) error {
	retentionDays := cfg.SoftDeleteRetentionDays
	if retentionDays < 0 {
		retentionDays = 30
	}
	cutoff := time.Now().UTC().AddDate(0, 0, -retentionDays)

	slog.Info("Running Entity Cleanup", "cutoff", cutoff)

	for {
		usersToDelete, err := client.User.Query().
			Where(user.DeletedAtLT(cutoff)).
			Order(ent.Asc(user.FieldID)).
			Select(user.FieldID).
			Limit(cleanupBatchSize).
			All(ctx)
		if err != nil {
			slog.Error("Failed to query users for cleanup", "error", err)
			return err
		}
		if len(usersToDelete) == 0 {
			break
		}

		deleted := 0
		for _, u := range usersToDelete {
			if err := client.User.DeleteOneID(u.ID).Exec(ctx); err != nil {
				slog.Error("Failed to delete user", "userID", u.ID, "error", err)
				continue
			}
			deleted++
		}
		if deleted == 0 {
			slog.Warn("Stopping user cleanup because the current batch made no progress")
			break
		}
	}

	for {
		chatsToDelete, err := client.Chat.Query().
			Where(chat.DeletedAtLT(cutoff)).
			Order(ent.Asc(chat.FieldID)).
			Select(chat.FieldID).
			Limit(cleanupBatchSize).
			All(ctx)
		if err != nil {
			slog.Error("Failed to query chats for cleanup", "error", err)
			return err
		}
		if len(chatsToDelete) == 0 {
			break
		}

		deleted := 0
		for _, c := range chatsToDelete {
			if err := client.Chat.DeleteOneID(c.ID).Exec(ctx); err != nil {
				slog.Error("Failed to delete chat", "chatID", c.ID, "error", err)
				continue
			}
			deleted++
		}
		if deleted == 0 {
			slog.Warn("Stopping chat cleanup because the current batch made no progress")
			break
		}
	}

	return nil
}
