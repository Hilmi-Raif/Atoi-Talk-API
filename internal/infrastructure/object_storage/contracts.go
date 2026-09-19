package objectstorage

import (
	"io"
	"time"
)

type PublicURLGenerator interface {
	GetPublicURL(string) string
}

type URLGenerator interface {
	PublicURLGenerator
	GetPresignedURL(string, time.Duration) (string, error)
}

type AuthStorage interface {
	PublicURLGenerator
	Download(string) ([]byte, string, error)
	StoreFromReader(io.Reader, string, string, bool) error
}

type MediaStorage interface {
	PublicURLGenerator
	GetPresignedPutURL(string, string, int64, bool, time.Duration) (string, map[string]string, error)
	Head(string, bool) (*StorageObjectInfo, error)
	GetPresignedURL(string, time.Duration) (string, error)
}
