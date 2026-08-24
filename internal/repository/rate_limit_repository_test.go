package repository

import (
	"AtoiTalkAPI/internal/adapter"
	"AtoiTalkAPI/internal/config"
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestRateLimitRepositoryAllowsWithinWindowAndRejectsAfterLimit(t *testing.T) {
	server := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{
		RedisHost: server.Host(),
		RedisPort: server.Port(),
	})
	if err != nil {
		t.Fatalf("create Redis adapter: %v", err)
	}
	defer func() { _ = redisAdapter.Client().Close() }()

	repo := NewRateLimitRepository(redisAdapter)
	ctx := context.Background()
	allowed, ttl, err := repo.Allow(ctx, "rate-limit", 2, time.Minute)
	if err != nil || !allowed || ttl <= 0 {
		t.Fatalf("expected first request allowed with ttl, got allowed=%t ttl=%s err=%v", allowed, ttl, err)
	}
	allowed, _, err = repo.Allow(ctx, "rate-limit", 2, time.Minute)
	if err != nil || !allowed {
		t.Fatalf("expected second request allowed, got allowed=%t err=%v", allowed, err)
	}
	allowed, ttl, err = repo.Allow(ctx, "rate-limit", 2, time.Minute)
	if err != nil || allowed || ttl <= 0 {
		t.Fatalf("expected third request rejected with ttl, got allowed=%t ttl=%s err=%v", allowed, ttl, err)
	}
}

func TestRateLimitRepositoryRefreshesMissingExpiry(t *testing.T) {
	server := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{
		RedisHost: server.Host(),
		RedisPort: server.Port(),
	})
	if err != nil {
		t.Fatalf("create Redis adapter: %v", err)
	}
	defer func() { _ = redisAdapter.Client().Close() }()

	ctx := context.Background()
	redisAdapter.Client().Set(ctx, "rate-limit", 5, 0)
	repo := NewRateLimitRepository(redisAdapter)
	allowed, ttl, err := repo.Allow(ctx, "rate-limit", 10, time.Minute)
	if err != nil || !allowed || ttl <= 0 {
		t.Fatalf("expected missing expiry to be restored, got allowed=%t ttl=%s err=%v", allowed, ttl, err)
	}
}

func TestRateLimitRepositoryReturnsErrorOnClosedServer(t *testing.T) {
	server := miniredis.RunT(t)
	redisAdapter, err := adapter.NewRedisAdapter(&config.AppConfig{
		RedisHost: server.Host(),
		RedisPort: server.Port(),
	})
	if err != nil {
		t.Fatalf("create Redis adapter: %v", err)
	}
	server.Close()
	repo := NewRateLimitRepository(redisAdapter)
	_, _, err = repo.Allow(context.Background(), "rate-limit", 10, time.Minute)
	if err == nil {
		t.Fatal("expected error on closed redis server")
	}
}
