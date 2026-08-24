package adapter

import (
	"context"
	"io"
	"time"
)

// PublicURLGenerator provides public media URLs to application services.
type PublicURLGenerator interface {
	GetPublicURL(string) string
}

// URLGenerator is the storage URL surface used when mapping public and private media.
type URLGenerator interface {
	PublicURLGenerator
	GetPresignedURL(string, time.Duration) (string, error)
}

// AuthStorage contains the storage operations used by authentication flows.
type AuthStorage interface {
	PublicURLGenerator
	Download(string) ([]byte, string, error)
	StoreFromReader(io.Reader, string, string, bool) error
}

// MediaStorage contains the storage operations used by MediaService.
type MediaStorage interface {
	PublicURLGenerator
	GetPresignedPutURL(string, string, int64, bool, time.Duration) (string, map[string]string, error)
	Head(string, bool) (*StorageObjectInfo, error)
	GetPresignedURL(string, time.Duration) (string, error)
}

// CaptchaVerifier validates a captcha token.
type CaptchaVerifier interface {
	Verify(string, string) error
}

// EmailSender sends an email through the configured provider.
type EmailSender interface {
	Send([]string, string, string) error
}

// RedisStore contains basic key-value operations used by OTP and cache flows.
type RedisStore interface {
	Set(context.Context, string, interface{}, time.Duration) error
	Get(context.Context, string) (string, error)
	Del(context.Context, string) error
}

// RedisOAuthStore contains the one-time OAuth state operations.
type RedisOAuthStore interface {
	Set(context.Context, string, interface{}, time.Duration) error
	GetDel(context.Context, string) (string, error)
}

// RedisPresence checks whether a user is currently online.
type RedisPresence interface {
	Exists(context.Context, string) (bool, error)
}

// RedisOnlineStore supports both single-user and batched online presence checks.
type RedisOnlineStore interface {
	RedisPresence
	ExistsMany(context.Context, []string) (map[string]bool, error)
}

// RedisCache contains cache operations used by private chat flows.
type RedisCache interface {
	Del(context.Context, string) error
	Exists(context.Context, string) (bool, error)
}
