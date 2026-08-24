package repository

import (
	"AtoiTalkAPI/internal/adapter"
	"AtoiTalkAPI/internal/config"
	"AtoiTalkAPI/internal/helper"
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

func newSessionRepositoryTest(t *testing.T, jwtExp int) (*SessionRepository, *adapter.RedisAdapter) {
	t.Helper()
	server := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{
		RedisHost: server.Host(),
		RedisPort: server.Port(),
	})
	if err != nil {
		t.Fatalf("create Redis adapter: %v", err)
	}
	t.Cleanup(func() { _ = redisAdapter.Client().Close() })
	return NewSessionRepository(redisAdapter, &config.AppConfig{JWTExp: jwtExp}), redisAdapter
}

func TestSessionRepositoryRevokeAndSnapshotRoundTrip(t *testing.T) {
	repo, redisAdapter := newSessionRepositoryTest(t, 60)
	ctx := context.Background()
	userID := uuid.New()
	if err := repo.RevokeAllSessions(ctx, userID); err != nil {
		t.Fatalf("revoke sessions wrapper: %v", err)
	}

	marker, err := repo.RevokeAllSessionsAt(ctx, userID, 1_700_000_000_000)
	if err != nil || marker == "" {
		t.Fatalf("revoke sessions: marker=%q err=%v", marker, err)
	}
	snapshot, err := repo.SnapshotUserRevoke(ctx, userID)
	if err != nil || !snapshot.Exists || snapshot.Value != marker || snapshot.TTL <= 0 {
		t.Fatalf("unexpected revoke snapshot: %+v err=%v", snapshot, err)
	}

	revoked, err := repo.IsUserRevoked(ctx, userID, 1_699_999_999_000)
	if err != nil || !revoked {
		t.Fatalf("expected old token revoked, got revoked=%t err=%v", revoked, err)
	}
	revoked, err = repo.IsUserRevoked(ctx, userID, 1_700_000_001_000)
	if err != nil || revoked {
		t.Fatalf("expected newer token allowed, got revoked=%t err=%v", revoked, err)
	}

	if err := redisAdapter.Client().Expire(ctx, "revoked_user:"+userID.String(), 0).Err(); err != nil {
		t.Fatalf("remove revoke key: %v", err)
	}
}

func TestSessionRepositoryHandlesMissingAndInvalidRevokeValues(t *testing.T) {
	repo, redisAdapter := newSessionRepositoryTest(t, 60)
	ctx := context.Background()
	userID := uuid.New()

	snapshot, err := repo.SnapshotUserRevoke(ctx, userID)
	if err != nil || snapshot.Exists {
		t.Fatalf("expected empty snapshot, got %+v err=%v", snapshot, err)
	}
	revoked, err := repo.IsUserRevoked(ctx, userID, 10)
	if err != nil || revoked {
		t.Fatalf("expected missing revoke key to allow token, got revoked=%t err=%v", revoked, err)
	}

	key := "revoked_user:" + userID.String()
	if err := redisAdapter.Set(ctx, key, "invalid-marker", time.Minute); err != nil {
		t.Fatalf("set invalid marker: %v", err)
	}
	if _, err := repo.IsUserRevoked(ctx, userID, 10); err == nil {
		t.Fatal("expected invalid revoke marker error")
	}

	if err := redisAdapter.Set(ctx, key, "1700000000:marker", time.Minute); err != nil {
		t.Fatalf("set seconds marker: %v", err)
	}
	revoked, err = repo.IsUserRevoked(ctx, userID, 1_700_000_000_000)
	if err != nil || !revoked {
		t.Fatalf("expected seconds marker to be normalized, got revoked=%t err=%v", revoked, err)
	}
}

func TestSessionRepositoryRollbackRestoresOrDeletesOnlyExpectedValue(t *testing.T) {
	repo, redisAdapter := newSessionRepositoryTest(t, 60)
	ctx := context.Background()
	userID := uuid.New()
	key := "revoked_user:" + userID.String()

	if err := repo.RollbackUserRevoke(ctx, userID, "", helper.SessionRevokeSnapshot{}); err != nil {
		t.Fatalf("empty rollback marker: %v", err)
	}
	if err := redisAdapter.Set(ctx, key, "new-value", time.Minute); err != nil {
		t.Fatalf("set current value: %v", err)
	}
	if err := repo.RollbackUserRevoke(ctx, userID, "old-value", helper.SessionRevokeSnapshot{}); err != nil {
		t.Fatalf("mismatched rollback: %v", err)
	}
	value, _ := redisAdapter.Get(ctx, key)
	if value != "new-value" {
		t.Fatalf("mismatched rollback changed value to %q", value)
	}

	snapshot := helper.SessionRevokeSnapshot{Exists: true, Value: "previous", TTL: time.Minute}
	if err := redisAdapter.Set(ctx, key, "expected", time.Minute); err != nil {
		t.Fatalf("set expected value: %v", err)
	}
	if err := repo.RollbackUserRevoke(ctx, userID, "expected", snapshot); err != nil {
		t.Fatalf("restore rollback: %v", err)
	}
	value, _ = redisAdapter.Get(ctx, key)
	if value != "previous" {
		t.Fatalf("expected previous value, got %q", value)
	}

	if err := redisAdapter.Set(ctx, key, "expected", time.Minute); err != nil {
		t.Fatalf("set expected delete value: %v", err)
	}
	if err := repo.RollbackUserRevoke(ctx, userID, "expected", helper.SessionRevokeSnapshot{}); err != nil {
		t.Fatalf("delete rollback: %v", err)
	}
	if _, err := redisAdapter.Get(ctx, key); err != redis.Nil {
		t.Fatalf("expected revoke key deleted, got err=%v", err)
	}
}

func TestSessionRepositoryBlacklistToken(t *testing.T) {
	repo, _ := newSessionRepositoryTest(t, 60)
	ctx := context.Background()

	blacklisted, err := repo.IsTokenBlacklisted(ctx, "token")
	if err != nil || blacklisted {
		t.Fatalf("expected token not blacklisted, got blacklisted=%t err=%v", blacklisted, err)
	}
	if err := repo.BlacklistToken(ctx, "token", time.Minute); err != nil {
		t.Fatalf("blacklist token: %v", err)
	}
	blacklisted, err = repo.IsTokenBlacklisted(ctx, "token")
	if err != nil || !blacklisted {
		t.Fatalf("expected token blacklisted, got blacklisted=%t err=%v", blacklisted, err)
	}
}

func TestSessionRepositoryHandlesRedisErrors(t *testing.T) {
	server := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{
		RedisHost: server.Host(),
		RedisPort: server.Port(),
	})
	if err != nil {
		t.Fatalf("create Redis adapter: %v", err)
	}
	server.Close()
	repo := NewSessionRepository(redisAdapter, &config.AppConfig{JWTExp: 60})
	ctx := context.Background()
	userID := uuid.New()

	if _, err := repo.RevokeAllSessionsAt(ctx, userID, 100); err == nil {
		t.Fatal("expected error on RevokeAllSessionsAt with closed redis")
	}
	if _, err := repo.SnapshotUserRevoke(ctx, userID); err == nil {
		t.Fatal("expected error on SnapshotUserRevoke with closed redis")
	}
	if err := repo.RollbackUserRevoke(ctx, userID, "expected", helper.SessionRevokeSnapshot{}); err == nil {
		t.Fatal("expected error on RollbackUserRevoke with closed redis")
	}
}
