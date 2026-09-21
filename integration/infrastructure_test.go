//go:build integration

package integration

import (
	"AtoiTalkAPI/internal/infrastructure/config"
	"AtoiTalkAPI/internal/infrastructure/database/repository"
	objectstorage "AtoiTalkAPI/internal/infrastructure/object_storage"
	"AtoiTalkAPI/internal/infrastructure/observability"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestOperationalHealthEndpoints(t *testing.T) {
	for _, test := range []struct {
		path   string
		status string
	}{
		{path: "/healthz", status: "healthy"},
		{path: "/livez", status: "alive"},
		{path: "/readyz", status: "ready"},
	} {
		t.Run(test.path, func(t *testing.T) {
			rr := makeRequest(http.MethodGet, test.path, nil, "")
			assert.Equal(t, http.StatusOK, rr.Code)

			var response observability.HealthResponse
			require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
			assert.Equal(t, test.status, response.Status)
			assert.NotEmpty(t, response.Timestamp)
		})
	}
}

func TestOperationalReadinessReportsDependencyFailure(t *testing.T) {
	handler := observability.NewHealthHandler(
		func(context.Context) error { return errors.New("database unavailable") },
		func(context.Context) error { return nil },
	)
	rr := httptest.NewRecorder()
	handler.Ready(rr, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	assert.Equal(t, http.StatusServiceUnavailable, rr.Code)
	var response observability.HealthResponse
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
	assert.Equal(t, "not_ready", response.Status)
	assert.Equal(t, "database unavailable", response.Checks["database"])
	assert.Equal(t, "ok", response.Checks["redis"])
}

func TestStorageAdapterLifecycle(t *testing.T) {
	ctx := context.Background()
	privatePath := "integration/storage/private.txt"
	publicPath := "integration/storage/public.txt"

	require.NoError(t, testStorageAdapter.StoreFromReader(bytes.NewReader([]byte("private data")), "text/plain", privatePath, false))
	require.NoError(t, testStorageAdapter.StoreFromReaderContext(ctx, bytes.NewReader([]byte("public data")), "", publicPath, true))

	privateInfo, err := testStorageAdapter.Head(privatePath, false)
	require.NoError(t, err)
	assert.EqualValues(t, len("private data"), privateInfo.Size)
	assert.Equal(t, "text/plain", privateInfo.ContentType)

	publicInfo, err := testStorageAdapter.HeadContext(ctx, publicPath, true)
	require.NoError(t, err)
	assert.EqualValues(t, len("public data"), publicInfo.Size)
	assert.Equal(t, "application/octet-stream", publicInfo.ContentType)
	assert.Contains(t, testStorageAdapter.GetPublicURL(publicPath), publicPath)

	downloadServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		_, _ = io.WriteString(w, "downloaded")
	}))
	defer downloadServer.Close()

	data, contentType, err := testStorageAdapter.Download(downloadServer.URL)
	require.NoError(t, err)
	assert.Equal(t, []byte("downloaded"), data)
	assert.Equal(t, "text/plain; charset=utf-8", contentType)

	require.NoError(t, testStorageAdapter.Delete(privatePath, false))
	require.NoError(t, testStorageAdapter.DeleteContext(ctx, publicPath, true))
	_, err = testStorageAdapter.Head(privatePath, false)
	assert.Error(t, err)
}

func TestStorageAdapterExternalDownloadGuards(t *testing.T) {
	t.Run("retries transient upstream failures", func(t *testing.T) {
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			if attempts.Add(1) < 3 {
				http.Error(w, "temporary failure", http.StatusBadGateway)
				return
			}
			_, _ = io.WriteString(w, "recovered")
		}))
		defer server.Close()

		adapter := objectstorage.NewStorageAdapter(testConfig, nil, server.Client())
		data, _, err := adapter.Download(server.URL)
		require.NoError(t, err)
		assert.Equal(t, []byte("recovered"), data)
		assert.EqualValues(t, 3, attempts.Load())
	})

	t.Run("rejects oversized response", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Length", "5242881")
			_, _ = io.WriteString(w, strings.Repeat("x", 1024))
		}))
		defer server.Close()

		adapter := objectstorage.NewStorageAdapter(testConfig, nil, server.Client())
		_, _, err := adapter.Download(server.URL)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "response body exceeds")
	})
}

func TestStorageAdapterPresignedURLs(t *testing.T) {
	cfg := *testConfig
	cfg.S3PresignEndpoint = "http://presign.example:9090"
	client := config.NewS3Client(&cfg)
	require.NotNil(t, client)
	adapter := objectstorage.NewStorageAdapter(&cfg, client, http.DefaultClient)

	getURL, err := adapter.GetPresignedURL("private/file.txt", time.Minute)
	require.NoError(t, err)
	assert.Contains(t, getURL, "presign.example:9090")
	assert.Contains(t, getURL, cfg.S3BucketPrivate+"/private/file.txt")

	putURL, headers, err := adapter.GetPresignedPutURL("public/file.txt", "", 12, true, time.Minute)
	require.NoError(t, err)
	assert.Contains(t, putURL, "presign.example:9090")
	assert.Contains(t, putURL, cfg.S3BucketPublic+"/public/file.txt")
	assert.Equal(t, "application/octet-stream", headers["Content-Type"])
}

func TestSessionRepositoryRevokeAndRollback(t *testing.T) {
	ctx := context.Background()
	sessionRepo := repository.NewSessionRepository(redisAdapter, testConfig)
	userID := uuid.New()
	key := "revoked_user:" + userID.String()

	initialSnapshot, err := sessionRepo.SnapshotUserRevoke(ctx, userID)
	require.NoError(t, err)
	assert.False(t, initialSnapshot.Exists)

	marker, err := sessionRepo.RevokeAllSessionsAt(ctx, userID, 1_700_000_000_000)
	require.NoError(t, err)
	assert.NotEmpty(t, marker)

	revoked, err := sessionRepo.IsUserRevoked(ctx, userID, 1_700_000_000_000)
	require.NoError(t, err)
	assert.True(t, revoked)

	require.NoError(t, sessionRepo.RollbackUserRevoke(ctx, userID, marker, initialSnapshot))
	exists, err := redisAdapter.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists)

	require.NoError(t, redisAdapter.Set(ctx, key, "old-marker", time.Minute))
	previousSnapshot, err := sessionRepo.SnapshotUserRevoke(ctx, userID)
	require.NoError(t, err)
	assert.True(t, previousSnapshot.Exists)

	newMarker, err := sessionRepo.RevokeAllSessionsAt(ctx, userID, 1_700_000_000_100)
	require.NoError(t, err)
	require.NoError(t, sessionRepo.RollbackUserRevoke(ctx, userID, newMarker, previousSnapshot))

	value, err := redisAdapter.Get(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, "old-marker", value)
	require.NoError(t, redisAdapter.Del(ctx, key))

	require.NoError(t, sessionRepo.RevokeAllSessions(ctx, userID))
	require.NoError(t, redisAdapter.Del(ctx, key))
}

func TestJobMetricsRunsSuccessFailureAndTimeout(t *testing.T) {
	metrics := observability.NewJobMetrics()

	assert.NoError(t, metrics.Run(context.Background(), "success", func(context.Context) error {
		return nil
	}))

	expectedErr := errors.New("job failed")
	assert.ErrorIs(t, metrics.Run(context.Background(), "failure", func(context.Context) error {
		return expectedErr
	}), expectedErr)

	err := metrics.RunWithTimeout(context.Background(), "timeout", time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	assert.ErrorIs(t, err, context.DeadlineExceeded)
}
