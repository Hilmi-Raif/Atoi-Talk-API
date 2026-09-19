package helper

import (
	"net/http"
	"testing"
)

func TestAppErrorDefaultsAndCustomMessages(t *testing.T) {
	tests := []struct {
		name           string
		fn             func(string) *AppError
		code           int
		defaultMessage string
	}{
		{"bad request", NewBadRequestError, http.StatusBadRequest, MsgBadRequest},
		{"internal", NewInternalServerError, http.StatusInternalServerError, MsgInternalServerError},
		{"unavailable", NewServiceUnavailableError, http.StatusServiceUnavailable, MsgServiceUnavailable},
		{"not found", NewNotFoundError, http.StatusNotFound, MsgNotFound},
		{"unauthorized", NewUnauthorizedError, http.StatusUnauthorized, MsgUnauthorized},
		{"forbidden", NewForbiddenError, http.StatusForbidden, MsgForbidden},
		{"method", NewMethodNotAllowedError, http.StatusMethodNotAllowed, MsgMethodNotAllowed},
		{"too many", NewTooManyRequestsError, http.StatusTooManyRequests, MsgTooManyRequests},
		{"conflict", NewConflictError, http.StatusConflict, MsgConflict},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.fn("")
			if got.Code != tt.code || got.Message != tt.defaultMessage || got.Error() != tt.defaultMessage {
				t.Fatalf("unexpected default error: %#v", got)
			}
			custom := tt.fn("custom")
			if custom.Code != tt.code || custom.Message != "custom" {
				t.Fatalf("unexpected custom error: %#v", custom)
			}
		})
	}
}
