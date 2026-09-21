package redis

import (
	"context"
	"time"
)

type Store interface {
	Set(context.Context, string, interface{}, time.Duration) error
	Get(context.Context, string) (string, error)
	Del(context.Context, string) error
}

type OAuthStore interface {
	Set(context.Context, string, interface{}, time.Duration) error
	GetDel(context.Context, string) (string, error)
}

type Presence interface {
	Exists(context.Context, string) (bool, error)
}

type OnlineStore interface {
	Presence
	ExistsMany(context.Context, []string) (map[string]bool, error)
}

type Cache interface {
	Del(context.Context, string) error
	Exists(context.Context, string) (bool, error)
}
