package config

import (
	"context"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestQueryIndexStatementsCoverSearchAndCleanupQueries(t *testing.T) {
	statements := strings.Join(queryIndexStatements(), "\n")

	requiredFragments := []string{
		"CREATE EXTENSION IF NOT EXISTS pg_trgm",
		"users_lower_full_name_prefix_idx",
		"users_lower_username_prefix_idx",
		"users_username_trgm_idx",
		"users_email_trgm_idx",
		"users_full_name_trgm_idx",
		"users_username_ilike_trgm_idx",
		"users_email_ilike_trgm_idx",
		"users_full_name_ilike_trgm_idx",
		"users_active_id_idx",
		"users_active_role_id_idx",
		"users_active_full_name_id_idx",
		"group_chats_public_lower_name_prefix_idx",
		"group_chats_public_name_id_idx",
		"group_chats_name_trgm_idx",
		"group_chats_description_trgm_idx",
		"group_chats_name_ilike_trgm_idx",
		"group_chats_description_ilike_trgm_idx",
		"group_members_group_joined_idx",
		"group_members_user_group_idx",
		"user_blocks_blocked_blocker_idx",
		"chats_active_last_message_idx",
		"messages_chat_id_id_idx",
		"private_chats_user1_chat_idx",
		"private_chats_user2_chat_idx",
		"message_outboxes_claim_idx",
		"message_outboxes_locked_idx",
		"users_deleted_at_id_idx",
		"chats_deleted_at_id_idx",
		"private_chats_abandoned_idx",
		"messages_sender_id_idx",
		"media_pending_expired_idx",
		"media_completed_orphan_idx",
		"media_uploader_status_category_idx",
		"reports_status_id_idx",
		"reports_reason_trgm_idx",
		"reports_reason_ilike_trgm_idx",
	}

	for _, fragment := range requiredFragments {
		if !strings.Contains(statements, fragment) {
			t.Fatalf("query index statements missing %q", fragment)
		}
	}
}

func TestInitEntFatalOnInvalidConnection(t *testing.T) {
	cmd := exec.Command(os.Args[0], "-test.run=^TestInitEntProcessHelper$")
	cmd.Env = append(os.Environ(), "INIT_ENT_SUBPROCESS=1")
	if err := cmd.Run(); err == nil {
		t.Fatal("expected subprocess to exit with failure on invalid connection")
	}
}

func TestInitEntProcessHelper(t *testing.T) {
	if os.Getenv("INIT_ENT_SUBPROCESS") != "1" {
		return
	}
	cfg := &AppConfig{
		DBHost:     "127.0.0.1",
		DBPort:     "1",
		DBUser:     "invalid",
		DBPassword: "invalid",
		DBName:     "invalid",
		DBSSLMode:  "disable",
		DBMigrate:  true,
	}
	_ = InitEnt(cfg)
}

func TestOpenInstrumentedDBConfiguresPoolAndMetrics(t *testing.T) {
	cfg := &AppConfig{
		DBHost:            "localhost",
		DBPort:            "5432",
		DBMaxOpenConns:    7,
		DBMaxIdleConns:    3,
		DBConnMaxLifetime: 2 * time.Minute,
		DBConnMaxIdleTime: 30 * time.Second,
	}

	db, registration, err := openInstrumentedDB(context.Background(), cfg)
	if err != nil {
		t.Fatalf("open instrumented database: %v", err)
	}
	defer func() {
		if err := registration.Unregister(); err != nil {
			t.Fatalf("unregister database metrics: %v", err)
		}
		_ = db.Close()
	}()

	stats := db.Stats()
	if stats.MaxOpenConnections != 7 {
		t.Fatalf("expected max open connections 7, got %d", stats.MaxOpenConnections)
	}
	if stats.MaxIdleClosed != 0 {
		t.Fatalf("expected no closed idle connections, got %d", stats.MaxIdleClosed)
	}
}
