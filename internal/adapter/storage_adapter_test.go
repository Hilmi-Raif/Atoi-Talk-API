package adapter

import (
	"AtoiTalkAPI/internal/config"
	"bytes"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestStorageAdapterDownloadRejectsBodyAboveLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("a", maxExternalDownloadBytes+1)))
	}))
	defer server.Close()

	adapter := &StorageAdapter{
		httpClient: server.Client(),
	}

	_, _, err := adapter.Download(server.URL)
	if err == nil {
		t.Fatal("expected oversized response to be rejected")
	}
	if !strings.Contains(err.Error(), "response body exceeds") {
		t.Fatalf("expected response size error, got %v", err)
	}
}

func TestStorageAdapterDownloadAcceptsBodyAtLimit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(strings.Repeat("a", maxExternalDownloadBytes)))
	}))
	defer server.Close()

	adapter := &StorageAdapter{
		httpClient: server.Client(),
	}

	data, _, err := adapter.Download(server.URL)
	if err != nil {
		t.Fatalf("expected response at limit to succeed: %v", err)
	}
	if len(data) != maxExternalDownloadBytes {
		t.Fatalf("expected %d bytes, got %d", maxExternalDownloadBytes, len(data))
	}
}

func TestStorageAdapterDownloadRejectsNonSuccessStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	}))
	defer server.Close()

	adapter := &StorageAdapter{httpClient: server.Client()}
	_, _, err := adapter.Download(server.URL)
	if err == nil || !strings.Contains(err.Error(), "status code: 404") {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestStorageAdapterDownloadReturnsRetryableStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "upstream unavailable", http.StatusBadGateway)
	}))
	defer server.Close()

	adapter := &StorageAdapter{httpClient: server.Client()}
	_, _, err := adapter.Download(server.URL)
	if err == nil || !strings.Contains(err.Error(), "status code: 502") {
		t.Fatalf("expected retryable status error, got %v", err)
	}
}

func TestStorageAdapterStoreFromReaderRejectsMissingClient(t *testing.T) {
	adapter := &StorageAdapter{}
	if err := adapter.StoreFromReader(strings.NewReader("payload"), "text/plain", "file.txt", false); err == nil {
		t.Fatal("expected missing S3 client error")
	}
}

func TestStorageAdapterStoreFromReaderUsesPrivateBucketAndDefaultContentType(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "/private/") {
			t.Errorf("expected private bucket path, got %q", r.URL.Path)
		}
		if got := r.Header.Get("Content-Type"); got != "application/octet-stream" {
			t.Errorf("expected default content type, got %q", got)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	adapter := newStorageAdapterForTest(t, server)
	if err := adapter.StoreFromReader(strings.NewReader("payload"), "", "file.txt", false); err != nil {
		t.Fatalf("store failed: %v", err)
	}
}

func TestStorageAdapterStoreDeleteAndHead(t *testing.T) {
	var mu sync.Mutex
	var methods []string
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		methods = append(methods, r.Method)
		mu.Unlock()

		if r.Method == http.MethodPut {
			body, _ = io.ReadAll(r.Body)
			if got := r.Header.Get("Content-Type"); got != "text/plain" {
				t.Errorf("expected content type text/plain, got %q", got)
			}
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodHead {
			w.Header().Set("Content-Length", "12")
			w.Header().Set("Content-Type", "image/png")
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
	}))
	defer server.Close()

	adapter := newStorageAdapterForTest(t, server)
	if err := adapter.StoreFromReader(strings.NewReader("payload"), "text/plain", "folder\\file.txt", true); err != nil {
		t.Fatalf("store failed: %v", err)
	}
	if string(body) != "payload" {
		t.Fatalf("unexpected stored body: %q", body)
	}
	if err := adapter.Delete("folder\\file.txt", true); err != nil {
		t.Fatalf("delete failed: %v", err)
	}
	info, err := adapter.Head("folder\\file.txt", true)
	if err != nil {
		t.Fatalf("head failed: %v", err)
	}
	if info.Size != 12 || info.ContentType != "image/png" {
		t.Fatalf("unexpected object info: %+v", info)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(methods) != 3 || methods[0] != http.MethodPut || methods[1] != http.MethodDelete || methods[2] != http.MethodHead {
		t.Fatalf("unexpected S3 methods: %v", methods)
	}
}

func TestStorageAdapterStoreReadsMultipartFile(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPut {
			http.Error(w, "unexpected method", http.StatusMethodNotAllowed)
			return
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) != "payload" {
			t.Errorf("unexpected uploaded payload: %q", body)
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	var form bytes.Buffer
	writer := multipart.NewWriter(&form)
	part, err := writer.CreateFormFile("file", "file.txt")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	_, _ = part.Write([]byte("payload"))
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer failed: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/upload", &form)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	parsed, err := multipart.NewReader(request.Body, mustBoundary(t, writer.FormDataContentType())).ReadForm(1024)
	if err != nil {
		t.Fatalf("parse multipart form failed: %v", err)
	}
	defer func() { _ = parsed.RemoveAll() }()

	adapter := newStorageAdapterForTest(t, server)
	file := parsed.File["file"][0]
	if err := adapter.Store(file, "uploads\\file.txt", false); err != nil {
		t.Fatalf("store failed: %v", err)
	}
}

func TestStorageAdapterStoreOpensFileError(t *testing.T) {
	adapter := &StorageAdapter{}
	if err := adapter.Store(&multipart.FileHeader{}, "file.txt", false); err == nil {
		t.Fatal("expected file open error")
	}
}

func TestStorageAdapterURLsAndPresignedRequests(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	adapter := newStorageAdapterForTest(t, server)
	if got := adapter.GetPublicURL("folder\\avatar.png"); got != "http://cdn.example/folder/avatar.png" {
		t.Fatalf("unexpected public URL: %q", got)
	}
	adapter.publicDomain = ""
	if got := adapter.GetPublicURL("folder\\avatar.png"); got != "https://public.s3.us-east-1.amazonaws.com/folder/avatar.png" {
		t.Fatalf("unexpected fallback public URL: %q", got)
	}

	getURL, err := adapter.GetPresignedURL("private\\file.txt", time.Minute)
	if err != nil || getURL == "" {
		t.Fatalf("expected presigned GET URL, got %q, %v", getURL, err)
	}
	putURL, headers, err := adapter.GetPresignedPutURL("private\\file.txt", "", 12, false, time.Minute)
	if err != nil || putURL == "" || headers["Content-Type"] != "application/octet-stream" {
		t.Fatalf("unexpected presigned PUT result: %q, %v, %#v", putURL, err, headers)
	}
	publicPutURL, headers, err := adapter.GetPresignedPutURL("public/file.txt", "text/plain", 12, true, time.Minute)
	if err != nil || publicPutURL == "" || headers["Content-Type"] != "text/plain" {
		t.Fatalf("unexpected public presigned PUT result: %q, %v, %#v", publicPutURL, err, headers)
	}
}

func TestStorageAdapterPresignedRequestsRejectMissingClient(t *testing.T) {
	adapter := &StorageAdapter{}
	if _, err := adapter.GetPresignedURL("file.txt", time.Minute); err == nil {
		t.Fatal("expected missing presign client error")
	}
	if _, _, err := adapter.GetPresignedPutURL("file.txt", "text/plain", 1, false, time.Minute); err == nil {
		t.Fatal("expected missing presign client error")
	}
}

func newStorageAdapterForTest(t *testing.T, server *httptest.Server) *StorageAdapter {
	t.Helper()
	client := config.NewS3Client(&config.AppConfig{
		S3Region:    "us-east-1",
		S3AccessKey: "access",
		S3SecretKey: "secret",
		S3Endpoint:  server.URL,
	})
	if client == nil {
		t.Fatal("expected S3 client")
	}
	return NewStorageAdapter(&config.AppConfig{
		S3BucketPublic:  "public",
		S3BucketPrivate: "private",
		S3Region:        "us-east-1",
		S3PublicDomain:  "http://cdn.example",
	}, client, server.Client())
}

func mustBoundary(t *testing.T, contentType string) string {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(contentType)
	if err != nil || mediaType != "multipart/form-data" {
		t.Fatalf("invalid multipart content type: %q", contentType)
	}
	return params["boundary"]
}
