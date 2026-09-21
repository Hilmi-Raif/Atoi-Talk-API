package repository

import (
	"AtoiTalkAPI/internal/domain/helper"
	"AtoiTalkAPI/internal/infrastructure/config"
	redisinfra "AtoiTalkAPI/internal/infrastructure/redis"
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/redis/go-redis/v9"
)

type SessionRepository struct {
	redisAdapter *redisinfra.RedisAdapter
	cfg          *config.AppConfig
}

func NewSessionRepository(redisAdapter *redisinfra.RedisAdapter, cfg *config.AppConfig) *SessionRepository {
	return &SessionRepository{
		redisAdapter: redisAdapter,
		cfg:          cfg,
	}
}

func (r *SessionRepository) RevokeAllSessions(ctx context.Context, userID uuid.UUID) error {
	_, err := r.RevokeAllSessionsAt(ctx, userID, time.Now().UTC().UnixMilli())
	return err
}

func (r *SessionRepository) RevokeAllSessionsAt(ctx context.Context, userID uuid.UUID, revokedAt int64) (string, error) {
	ttl := time.Duration(r.cfg.JWTExp) * time.Second
	key := fmt.Sprintf("revoked_user:%s", userID)
	marker := fmt.Sprintf("%d:%s", revokedAt, uuid.NewString())

	err := r.redisAdapter.Set(ctx, key, marker, ttl)
	if err != nil {
		slog.Error("Failed to revoke all sessions in repository", "error", err, "userID", userID)
		return "", fmt.Errorf("failed to revoke sessions: %w", err)
	}
	return marker, nil
}

func (r *SessionRepository) SnapshotUserRevoke(ctx context.Context, userID uuid.UUID) (helper.SessionRevokeSnapshot, error) {
	key := fmt.Sprintf("revoked_user:%s", userID)

	val, err := r.redisAdapter.Get(ctx, key)
	if err != nil {
		if err == redis.Nil {
			return helper.SessionRevokeSnapshot{}, nil
		}
		return helper.SessionRevokeSnapshot{}, fmt.Errorf("failed to snapshot revoke key: %w", err)
	}

	ttl, err := r.redisAdapter.Client().TTL(ctx, key).Result()
	if err != nil {
		return helper.SessionRevokeSnapshot{}, fmt.Errorf("failed to read revoke key ttl: %w", err)
	}

	if ttl < 0 {
		ttl = 0
	}

	return helper.SessionRevokeSnapshot{
		Exists: true,
		Value:  val,
		TTL:    ttl,
	}, nil
}

func (r *SessionRepository) RollbackUserRevoke(ctx context.Context, userID uuid.UUID, expectedValue string, snapshot helper.SessionRevokeSnapshot) error {
	if expectedValue == "" {
		return nil
	}

	key := fmt.Sprintf("revoked_user:%s", userID)

	currentValue, err := r.redisAdapter.Get(ctx, key)
	if err != nil {
		if err == redis.Nil {
			return nil
		}
		return fmt.Errorf("failed to read revoke key for rollback: %w", err)
	}

	if currentValue != expectedValue {
		return nil
	}

	if snapshot.Exists {
		if err := r.redisAdapter.Set(ctx, key, snapshot.Value, snapshot.TTL); err != nil {
			return fmt.Errorf("failed to restore previous revoke key: %w", err)
		}
		return nil
	}

	if err := r.redisAdapter.Del(ctx, key); err != nil {
		return fmt.Errorf("failed to delete revoke key during rollback: %w", err)
	}

	return nil
}

func (r *SessionRepository) BlacklistToken(ctx context.Context, tokenString string, ttl time.Duration) error {
	key := fmt.Sprintf("blacklist:%s", tokenString)
	return r.redisAdapter.Set(ctx, key, "revoked", ttl)
}

func (r *SessionRepository) IsTokenBlacklisted(ctx context.Context, tokenString string) (bool, error) {
	key := fmt.Sprintf("blacklist:%s", tokenString)
	val, err := r.redisAdapter.Get(ctx, key)
	if err != nil {
		if err == redis.Nil {
			return false, nil
		}
		return false, err
	}
	return val != "", nil
}

func (r *SessionRepository) IsUserRevoked(ctx context.Context, userID uuid.UUID, tokenIssuedAt int64) (bool, error) {
	key := fmt.Sprintf("revoked_user:%s", userID)
	revokedAtStr, err := r.redisAdapter.Get(ctx, key)

	if err != nil {
		if err == redis.Nil {
			return false, nil
		}
		return false, err
	}

	if revokedAtStr == "" {
		return false, nil
	}

	revokedAtValue := revokedAtStr
	if sep := strings.IndexByte(revokedAtStr, ':'); sep > 0 {
		revokedAtValue = revokedAtStr[:sep]
	}

	revokedAt, err := strconv.ParseInt(revokedAtValue, 10, 64)
	if err != nil {
		return false, fmt.Errorf("invalid revoked timestamp for user %s: %w", userID, err)
	}

	if revokedAt < 1_000_000_000_000 {
		revokedAt *= 1000
	}

	return tokenIssuedAt <= revokedAt, nil
}
