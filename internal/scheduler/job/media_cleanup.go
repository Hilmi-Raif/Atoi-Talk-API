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
		All(ctx)

	if err != nil {
		slog.Error("Failed to query orphan media", "error", err)
		return err
	}

	slog.Info("Found orphan media candidates", "count", len(orphans))

	for _, m := range orphans {
		isPublic := m.Category == media.CategoryUserAvatar || m.Category == media.CategoryGroupAvatar
		if err := storage.Delete(m.FileName, isPublic); err != nil {
			if fallbackErr := storage.Delete(m.FileName, !isPublic); fallbackErr != nil {
				slog.Error("Failed to delete S3 file", "mediaID", m.ID, "key", m.FileName, "error", err, "fallback_error", fallbackErr)
				continue
			}
			slog.Warn("Deleted S3 file using fallback bucket", "mediaID", m.ID, "key", m.FileName)
		}

		err = client.Media.DeleteOneID(m.ID).Exec(ctx)
		if err != nil {
			slog.Error("Failed to delete media row", "mediaID", m.ID, "error", err)
		} else {
			slog.Info("Deleted orphan media", "mediaID", m.ID)
		}
	}

	return nil
}
