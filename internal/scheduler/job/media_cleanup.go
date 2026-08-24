package job

import (
	"AtoiTalkAPI/ent"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/internal/adapter"
	"AtoiTalkAPI/internal/config"
	"context"
	"log/slog"
	"time"
)

func RunMediaCleanup(ctx context.Context, client *ent.Client, storage *adapter.StorageAdapter, cfg *config.AppConfig) error {
	retentionDays := cfg.MediaRetentionDays
	if retentionDays < 0 {
		retentionDays = 7.0
	}

	duration := time.Duration(retentionDays * 24 * float64(time.Hour))
	cutoff := time.Now().UTC().Add(-duration)

	slog.Info("Running Media Cleanup", "retentionDays", retentionDays, "cutoff", cutoff, "now_utc", time.Now().UTC())

	now := time.Now().UTC()
	for {
		orphans, err := client.Media.Query().
			Where(
				media.Or(
					media.And(
						media.UploadStatusEQ(media.UploadStatusPending),
						media.UploadExpiresAtNotNil(),
						media.UploadExpiresAtLT(now),
					),
					media.And(
						media.UploadStatusEQ(media.UploadStatusCompleted),
						media.CreatedAtLT(cutoff),
					),
				),
				media.MessageIDIsNil(),
				media.Not(media.HasUserAvatar()),
				media.Not(media.HasGroupAvatar()),
				media.Not(media.HasReports()),
			).
			Order(ent.Asc(media.FieldID)).
			Select(media.FieldID, media.FieldFileName, media.FieldCategory).
			Limit(cleanupBatchSize).
			All(ctx)
		if err != nil {
			slog.Error("Failed to query orphan media", "error", err)
			return err
		}
		if len(orphans) == 0 {
			break
		}

		deleted := 0
		for _, m := range orphans {
			isPublic := m.Category == media.CategoryUserAvatar || m.Category == media.CategoryGroupAvatar
			if err := storage.Delete(m.FileName, isPublic); err != nil {
				if fallbackErr := storage.Delete(m.FileName, !isPublic); fallbackErr != nil {
					slog.Error("Failed to delete S3 file", "mediaID", m.ID, "key", m.FileName, "error", err, "fallback_error", fallbackErr)
					continue
				}
				slog.Warn("Deleted S3 file using fallback bucket", "mediaID", m.ID, "key", m.FileName)
			}

			if err := client.Media.DeleteOneID(m.ID).Exec(ctx); err != nil {
				slog.Error("Failed to delete media row", "mediaID", m.ID, "error", err)
				continue
			}
			deleted++
		}
		if deleted == 0 {
			slog.Warn("Stopping media cleanup because the current batch made no progress")
			break
		}
	}

	return nil
}
