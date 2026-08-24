package adapter

import (
	"AtoiTalkAPI/internal/config"
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
)

func TestRedisAdapterRoundTrip(t *testing.T) {
	server := miniredis.RunT(t)
	adapter, err := NewRedisAdapter(&config.AppConfig{
		RedisHost: server.Host(),
		RedisPort: server.Port(),
	})
	if err != nil {
		t.Fatalf("expected Redis adapter, got %v", err)
	}

	ctx := context.Background()
	if err := adapter.Set(ctx, "key", "value", time.Minute); err != nil {
		t.Fatalf("set failed: %v", err)
	}
	value, err := adapter.Get(ctx, "key")
	if err != nil || value != "value" {
		t.Fatalf("unexpected get result: %q, %v", value, err)
	}
	if err := adapter.Del(ctx, "key"); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	if _, err := adapter.Get(ctx, "key"); err == nil {
		t.Fatal("expected missing key error")
	}
	if adapter.Client() == nil {
		t.Fatal("expected underlying Redis client")
	}
}

func TestRedisAdapterExists(t *testing.T) {
	server := miniredis.RunT(t)
	redisAdapter, err := NewRedisAdapter(&config.AppConfig{RedisHost: server.Host(), RedisPort: server.Port()})
	if err != nil {
		t.Fatalf("expected Redis adapter, got %v", err)
	}

	ctx := context.Background()
	exists, err := redisAdapter.Exists(ctx, "online:user")
	if err != nil || exists {
		t.Fatalf("expected missing key, got exists=%t err=%v", exists, err)
	}
	if err := redisAdapter.Set(ctx, "online:user", "1", time.Minute); err != nil {
		t.Fatalf("set failed: %v", err)
	}
	exists, err = redisAdapter.Exists(ctx, "online:user")
	if err != nil || !exists {
		t.Fatalf("expected existing key, got exists=%t err=%v", exists, err)
	}
}

func TestRedisAdapterExistsMany(t *testing.T) {
	server := miniredis.RunT(t)
	redisAdapter, err := NewRedisAdapter(&config.AppConfig{RedisHost: server.Host(), RedisPort: server.Port()})
	if err != nil {
		t.Fatalf("expected Redis adapter, got %v", err)
	}

	ctx := context.Background()
	if err := redisAdapter.Set(ctx, "online:user-1", "1", time.Minute); err != nil {
		t.Fatalf("set failed: %v", err)
	}

	statuses, err := redisAdapter.ExistsMany(ctx, []string{"online:user-1", "online:user-2"})
	if err != nil {
		t.Fatalf("exists many failed: %v", err)
	}
	if !statuses["online:user-1"] || statuses["online:user-2"] {
		t.Fatalf("unexpected statuses: %#v", statuses)
	}
}

func TestRedisAdapterExistsManyWithNoKeys(t *testing.T) {
	server := miniredis.RunT(t)
	redisAdapter, err := NewRedisAdapter(&config.AppConfig{RedisHost: server.Host(), RedisPort: server.Port()})
	if err != nil {
		t.Fatalf("expected Redis adapter, got %v", err)
	}

	statuses, err := redisAdapter.ExistsMany(context.Background(), nil)
	if err != nil {
		t.Fatalf("exists many failed: %v", err)
	}
	if len(statuses) != 0 {
		t.Fatalf("expected empty status map, got %#v", statuses)
	}
}

func TestRedisAdapterGetDelReturnsAndDeletesValue(t *testing.T) {
	server := miniredis.RunT(t)
	redisAdapter, err := NewRedisAdapter(&config.AppConfig{RedisHost: server.Host(), RedisPort: server.Port()})
	if err != nil {
		t.Fatalf("expected Redis adapter, got %v", err)
	}

	ctx := context.Background()
	if err := redisAdapter.Set(ctx, "oauth-state", "state-value", time.Minute); err != nil {
		t.Fatalf("set failed: %v", err)
	}
	value, err := redisAdapter.GetDel(ctx, "oauth-state")
	if err != nil || value != "state-value" {
		t.Fatalf("unexpected getdel result: %q, %v", value, err)
	}
	if _, err := redisAdapter.Get(ctx, "oauth-state"); err == nil {
		t.Fatal("expected getdel to remove the key")
	}
}

func TestNewRedisAdapterReturnsConnectionError(t *testing.T) {
	_, err := NewRedisAdapter(&config.AppConfig{
		RedisHost: "127.0.0.1",
		RedisPort: "1",
	})
	if err == nil {
		t.Fatal("expected Redis connection error")
	}
}
