package adapter

import (
	"AtoiTalkAPI/internal/config"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/redis/go-redis/v9"
)

type RedisAdapter struct {
	client *redis.Client
}

func NewRedisAdapter(cfg *config.AppConfig) (*RedisAdapter, error) {
	addr := fmt.Sprintf("%s:%s", cfg.RedisHost, cfg.RedisPort)
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: cfg.RedisPassword,
		DB:       cfg.RedisDB,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := client.Ping(ctx).Err(); err != nil {
		slog.Error("Failed to connect to Redis", "error", err, "addr", addr)
		return nil, err
	}

	slog.Info("Connected to Redis", "addr", addr)

	return &RedisAdapter{
		client: client,
	}, nil
}

func (r *RedisAdapter) Set(ctx context.Context, key string, value interface{}, expiration time.Duration) error {
	return r.client.Set(ctx, key, value, expiration).Err()
}

func (r *RedisAdapter) Get(ctx context.Context, key string) (string, error) {
	return r.client.Get(ctx, key).Result()
}

func (r *RedisAdapter) Del(ctx context.Context, key string) error {
	return r.client.Del(ctx, key).Err()
}

func (r *RedisAdapter) Exists(ctx context.Context, key string) (bool, error) {
	count, err := r.client.Exists(ctx, key).Result()
	return count > 0, err
}

func (r *RedisAdapter) ExistsMany(ctx context.Context, keys []string) (map[string]bool, error) {
	if len(keys) == 0 {
		return map[string]bool{}, nil
	}

	results, err := r.client.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for _, key := range keys {
			pipe.Exists(ctx, key)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	statuses := make(map[string]bool, len(keys))
	for i, result := range results {
		if command, ok := result.(*redis.IntCmd); ok && i < len(keys) {
			statuses[keys[i]] = command.Val() > 0
		}
	}
	return statuses, nil
}

func (r *RedisAdapter) GetDel(ctx context.Context, key string) (string, error) {
	return r.client.GetDel(ctx, key).Result()
}

func (r *RedisAdapter) Client() *redis.Client {
	return r.client
}
