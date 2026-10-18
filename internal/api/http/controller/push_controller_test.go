package controller

import (
	"AtoiTalkAPI/internal/api/http/middleware"
	"AtoiTalkAPI/internal/domain/model"
	"context"
	"errors"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"strings"
	"testing"
)

type deviceService struct {
	calls int
	err   error
}

func (s *deviceService) Upsert(context.Context, uuid.UUID, string, uuid.UUID, model.PushDeviceRequest) error {
	s.calls++
	return s.err
}
func (s *deviceService) Unregister(context.Context, uuid.UUID, string, uuid.UUID) error {
	s.calls++
	return s.err
}
func TestPushControllerBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name, method, id, body string
		user, token, fail      bool
		status, calls          int
	}{
		{"no user", "PUT", uuid.NewString(), `{}`, false, false, false, 401, 0},
		{"no session", "PUT", uuid.NewString(), `{}`, true, false, false, 401, 0},
		{"bad installation", "PUT", "invalid", `{}`, true, true, false, 400, 0},
		{"bad json", "PUT", uuid.NewString(), `{`, true, true, false, 400, 0},
		{"unknown field", "PUT", uuid.NewString(), `{"user_id":"forged"}`, true, true, false, 400, 0},
		{"register", "PUT", uuid.NewString(), `{"token":"token","platform":"web","device_secret":"secret"}`, true, true, false, 200, 1},
		{"unregister", "DELETE", uuid.NewString(), ``, true, true, false, 200, 1},
		{"service failure", "PUT", uuid.NewString(), `{}`, true, true, true, 500, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &deviceService{}
			if tc.fail {
				s.err = errors.New("storage unavailable")
			}
			r := httptest.NewRequest(tc.method, "/", strings.NewReader(tc.body))
			ctx := r.Context()
			if tc.user {
				ctx = context.WithValue(ctx, middleware.UserContextKey, &model.UserDTO{ID: uuid.New()})
			}
			if tc.token {
				ctx = context.WithValue(ctx, middleware.TokenContextKey, "session")
			}
			route := chi.NewRouteContext()
			route.URLParams.Add("installationID", tc.id)
			ctx = context.WithValue(ctx, chi.RouteCtxKey, route)
			w := httptest.NewRecorder()
			NewPushController(s).Device(w, r.WithContext(ctx))
			require.Equal(t, tc.status, w.Code)
			require.Equal(t, tc.calls, s.calls)
		})
	}
}
