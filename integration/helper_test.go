//go:build integration

package integration

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type APIResponse[T any] struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
	Data    T      `json:"data"`
}

type APIErrorResponse struct {
	Success bool   `json:"success"`
	Error   string `json:"error"`
}

func makeRequest(method, url string, body any, token string) *httptest.ResponseRecorder {
	var bodyReader io.Reader
	if body != nil {
		switch b := body.(type) {
		case []byte:
			bodyReader = bytes.NewBuffer(b)
		case string:
			bodyReader = bytes.NewBufferString(b)
		case io.Reader:
			bodyReader = b
		default:
			marshaled, _ := json.Marshal(body)
			bodyReader = bytes.NewBuffer(marshaled)
		}
	}
	req, _ := http.NewRequest(method, url, bodyReader)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	return executeRequest(req)
}

func parseResponse[T any](t *testing.T, rr *httptest.ResponseRecorder) T {
	t.Helper()
	var resp APIResponse[T]
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v, body: %s", err, rr.Body.String())
	}
	return resp.Data
}

func parseErrorResponse(t *testing.T, rr *httptest.ResponseRecorder) string {
	t.Helper()
	var resp APIErrorResponse
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	return resp.Error
}

func generateUniqueEmail(prefix string) string {
	return fmt.Sprintf("%s_%d@example.com", prefix, time.Now().UnixNano())
}
