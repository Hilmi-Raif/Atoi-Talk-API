package config

import (
	"strings"
	"testing"
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
		"user_blocks_blocked_blocker_idx",
		"chats_active_last_message_idx",
		"messages_chat_id_id_idx",
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
