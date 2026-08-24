package helper

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSONAndErrors(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteSuccess(recorder, map[string]string{"ok": "yes"})
	if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("unexpected success response: code=%d headers=%v", recorder.Code, recorder.Header())
	}

	recorder = httptest.NewRecorder()
	WriteSuccessWithPaginationBidirectional(recorder, []string{"item"}, "next", true, "prev", true)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "next") || !strings.Contains(recorder.Body.String(), "prev") {
		t.Fatalf("unexpected pagination response: %s", recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	WriteError(recorder, NewNotFoundError("missing"))
	if recorder.Code != http.StatusNotFound || !strings.Contains(recorder.Body.String(), "missing") {
		t.Fatalf("unexpected error response: code=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	WriteError(recorder, errors.New("internal detail"))
	if recorder.Code != http.StatusInternalServerError || strings.Contains(recorder.Body.String(), "internal detail") {
		t.Fatalf("unexpected generic error response: code=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func TestWriteSuccessWithPagination(t *testing.T) {
	recorder := httptest.NewRecorder()
	WriteSuccessWithPagination(recorder, []string{"item"}, "next", true)
	if recorder.Code != http.StatusOK || !strings.Contains(recorder.Body.String(), "next") || !strings.Contains(recorder.Body.String(), "has_next") {
		t.Fatalf("unexpected pagination response: code=%d body=%s", recorder.Code, recorder.Body.String())
	}

	recorder = httptest.NewRecorder()
	WriteSuccess(recorder, nil)
	if !strings.Contains(recorder.Body.String(), `"data":""`) {
		t.Fatalf("expected nil success data to serialize as empty string: %s", recorder.Body.String())
	}
}
