package repository

import (
	redisinfra "AtoiTalkAPI/internal/infrastructure/redis"
	"context"
	"fmt"
	"time"
)

type RateLimitRepository struct {
	redisAdapter *redisinfra.RedisAdapter
}

func NewRateLimitRepository(redisAdapter *redisinfra.RedisAdapter) *RateLimitRepository {
	return &RateLimitRepository{
		redisAdapter: redisAdapter,
	}
}

func (r *RateLimitRepository) Allow(ctx context.Context, key string, limit int, window time.Duration) (bool, time.Duration, error) {
	client := r.redisAdapter.Client()
	pipe := client.Pipeline()
	incr := pipe.Incr(ctx, key)
	ttlCmd := pipe.TTL(ctx, key)
	_, err := pipe.Exec(ctx)

	if err != nil {
		return false, 0, err
	}

	count := incr.Val()
	ttl := ttlCmd.Val()

	if count == 1 || ttl <= 0 {
		expireSet, err := client.Expire(ctx, key, window).Result()
		if err != nil {
			return false, 0, fmt.Errorf("failed to set rate limit expiry: %w", err)
		}
		if !expireSet {
			return false, 0, fmt.Errorf("failed to set rate limit expiry: key missing")
		}
		ttl = window
	}

	if count > int64(limit) {
		return false, ttl, nil
	}

	return true, ttl, nil
}
