package config

import (
	"AtoiTalkAPI/ent"
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"

	_ "github.com/lib/pq"
)

func InitEnt(cfg *AppConfig) *ent.Client {
	dsn := fmt.Sprintf("host=%s port=%s user=%s dbname=%s password=%s sslmode=%s",
		cfg.DBHost, cfg.DBPort, cfg.DBUser, cfg.DBName, cfg.DBPassword, cfg.DBSSLMode)

	client, err := ent.Open("postgres", dsn)
	if err != nil {
		slog.Error("failed opening connection to postgres", "error", err)
		os.Exit(1)
	}

	if cfg.DBMigrate {
		if err := client.Schema.Create(context.Background()); err != nil {
			slog.Error("failed creating schema resources", "error", err)
			os.Exit(1)
		}
		if err := ensureQueryIndexes(context.Background(), dsn); err != nil {
			slog.Error("failed creating query indexes", "error", err)
			os.Exit(1)
		}
		slog.Info("Database schema migrated successfully (Ent)")
	} else {
		slog.Info("Database migration skipped (DB_MIGRATE=false)")
	}

	slog.Info("Database connected successfully")
	return client
}

func ensureQueryIndexes(ctx context.Context, dsn string) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return err
	}
	defer db.Close()

	for _, statement := range queryIndexStatements() {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("execute query index statement: %w", err)
		}
	}

	return nil
}

func queryIndexStatements() []string {
	return []string{
		`CREATE EXTENSION IF NOT EXISTS pg_trgm`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS users_lower_full_name_prefix_idx ON users (lower(full_name) text_pattern_ops) WHERE deleted_at IS NULL AND full_name IS NOT NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS users_lower_username_prefix_idx ON users (lower(username) text_pattern_ops) WHERE deleted_at IS NULL AND username IS NOT NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS users_username_trgm_idx ON users USING gin (lower(username) gin_trgm_ops) WHERE deleted_at IS NULL AND username IS NOT NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS users_email_trgm_idx ON users USING gin (lower(email) gin_trgm_ops) WHERE deleted_at IS NULL AND email IS NOT NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS users_full_name_trgm_idx ON users USING gin (lower(full_name) gin_trgm_ops) WHERE deleted_at IS NULL AND full_name IS NOT NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS users_username_ilike_trgm_idx ON users USING gin (username gin_trgm_ops) WHERE deleted_at IS NULL AND username IS NOT NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS users_email_ilike_trgm_idx ON users USING gin (email gin_trgm_ops) WHERE deleted_at IS NULL AND email IS NOT NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS users_full_name_ilike_trgm_idx ON users USING gin (full_name gin_trgm_ops) WHERE deleted_at IS NULL AND full_name IS NOT NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS users_active_id_idx ON users (id DESC) WHERE deleted_at IS NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS users_active_role_id_idx ON users (role, id DESC) WHERE deleted_at IS NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS users_active_full_name_id_idx ON users (full_name ASC, id ASC) WHERE deleted_at IS NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS group_chats_public_lower_name_prefix_idx ON group_chats (lower(name) text_pattern_ops) WHERE is_public = true`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS group_chats_public_name_id_idx ON group_chats (name ASC, id ASC) WHERE is_public = true`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS group_chats_name_trgm_idx ON group_chats USING gin (lower(name) gin_trgm_ops)`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS group_chats_description_trgm_idx ON group_chats USING gin (lower(description) gin_trgm_ops) WHERE description IS NOT NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS group_chats_name_ilike_trgm_idx ON group_chats USING gin (name gin_trgm_ops)`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS group_chats_description_ilike_trgm_idx ON group_chats USING gin (description gin_trgm_ops) WHERE description IS NOT NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS group_members_group_joined_idx ON group_members (group_chat_id, joined_at ASC, id ASC)`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS user_blocks_blocked_blocker_idx ON user_blocks (blocked_id, blocker_id)`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS chats_active_last_message_idx ON chats (last_message_at DESC, id DESC) WHERE deleted_at IS NULL AND last_message_at IS NOT NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS messages_chat_id_id_idx ON messages (chat_id, id)`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS messages_sender_id_idx ON messages (sender_id) WHERE sender_id IS NOT NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS media_pending_expired_idx ON media (upload_expires_at) WHERE upload_status = 'pending' AND upload_expires_at IS NOT NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS media_completed_orphan_idx ON media (created_at) WHERE upload_status = 'completed' AND message_id IS NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS media_uploader_status_category_idx ON media (uploaded_by_id, upload_status, category) WHERE message_id IS NULL AND uploaded_by_id IS NOT NULL`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS reports_status_id_idx ON reports (status, id DESC)`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS reports_reason_trgm_idx ON reports USING gin (lower(reason) gin_trgm_ops)`,
		`CREATE INDEX CONCURRENTLY IF NOT EXISTS reports_reason_ilike_trgm_idx ON reports USING gin (reason gin_trgm_ops)`,
	}
}
