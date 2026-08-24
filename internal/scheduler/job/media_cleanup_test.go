package job

import (
	"AtoiTalkAPI/ent/enttest"
	"AtoiTalkAPI/ent/media"
	"AtoiTalkAPI/internal/adapter"
	"AtoiTalkAPI/internal/config"
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	_ "github.com/mattn/go-sqlite3"
	"github.com/stretchr/testify/require"
)

func TestRunMediaCleanupDeletesExpiredMediaAfterStorageCleanup(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:media-cleanup-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	var mu sync.Mutex
	var deletePaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		deletePaths = append(deletePaths, r.URL.Path)
		mu.Unlock()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	storage := newMediaCleanupStorage(t, server)
	ctx := context.Background()
	old := time.Now().UTC().Add(-8 * 24 * time.Hour)
	expired := time.Now().UTC().Add(-time.Minute)

	pending := client.Media.Create().
		SetFileName("pending.bin").
		SetOriginalName("pending.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusPending).
		SetUploadExpiresAt(expired).
		SaveX(ctx)
	completed := client.Media.Create().
		SetFileName("completed.bin").
		SetOriginalName("completed.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusCompleted).
		SetCreatedAt(old).
		SaveX(ctx)
	kept := client.Media.Create().
		SetFileName("kept.bin").
		SetOriginalName("kept.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusCompleted).
		SaveX(ctx)

	err := RunMediaCleanup(ctx, client, storage, &config.AppConfig{MediaRetentionDays: 7})

	require.NoError(t, err)
	_, err = client.Media.Get(ctx, pending.ID)
	require.Error(t, err)
	_, err = client.Media.Get(ctx, completed.ID)
	require.Error(t, err)
	_, err = client.Media.Get(ctx, kept.ID)
	require.NoError(t, err)

	mu.Lock()
	defer mu.Unlock()
	require.ElementsMatch(t, []string{"/private/pending.bin", "/private/completed.bin"}, deletePaths)
}

func TestRunMediaCleanupUsesFallbackBucketWhenPrimaryDeleteFails(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:media-cleanup-fallback-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	var mu sync.Mutex
	var deletePaths []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		mu.Lock()
		deletePaths = append(deletePaths, r.URL.Path)
		attempt := len(deletePaths)
		mu.Unlock()
		if attempt == 1 {
			http.Error(w, "primary bucket unavailable", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	storage := newMediaCleanupStorage(t, server)
	ctx := context.Background()
	mediaEntity := client.Media.Create().
		SetFileName("fallback.bin").
		SetOriginalName("fallback.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusPending).
		SetUploadExpiresAt(time.Now().UTC().Add(-time.Minute)).
		SaveX(ctx)

	err := RunMediaCleanup(ctx, client, storage, &config.AppConfig{MediaRetentionDays: 7})

	require.NoError(t, err)
	_, err = client.Media.Get(ctx, mediaEntity.ID)
	require.Error(t, err)
	mu.Lock()
	defer mu.Unlock()
	require.Len(t, deletePaths, 2)
}

func TestRunMediaCleanupKeepsMediaWhenBothStorageDeletesFail(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:media-cleanup-fail-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "s3 failed", http.StatusInternalServerError)
	}))
	defer server.Close()

	storage := newMediaCleanupStorage(t, server)
	ctx := context.Background()
	mediaEntity := client.Media.Create().
		SetFileName("failed.bin").
		SetOriginalName("failed.bin").
		SetFileSize(10).
		SetMimeType("application/octet-stream").
		SetUploadStatus(media.UploadStatusPending).
		SetUploadExpiresAt(time.Now().UTC().Add(-time.Minute)).
		SaveX(ctx)

	err := RunMediaCleanup(ctx, client, storage, &config.AppConfig{MediaRetentionDays: -1})
	require.NoError(t, err)
	_, err = client.Media.Get(ctx, mediaEntity.ID)
	require.NoError(t, err)
}

func TestRunMediaCleanupReturnsQueryErrorOnCancelledContext(t *testing.T) {
	client := enttest.Open(t, dialect.SQLite, "file:media-cleanup-cancel-"+t.Name()+"?mode=memory&cache=shared&_fk=1")
	defer client.Close()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	storage := newMediaCleanupStorage(t, server)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := RunMediaCleanup(ctx, client, storage, &config.AppConfig{MediaRetentionDays: 7})
	require.Error(t, err)
}

func newMediaCleanupStorage(t *testing.T, server *httptest.Server) *adapter.StorageAdapter {
	t.Helper()
	client := config.NewS3Client(&config.AppConfig{
		S3Region:    "us-east-1",
		S3AccessKey: "access",
		S3SecretKey: "secret",
		S3Endpoint:  server.URL,
	})
	return adapter.NewStorageAdapter(&config.AppConfig{
		S3BucketPublic:  "public",
		S3BucketPrivate: "private",
		S3Region:        "us-east-1",
	}, client, server.Client())
}
