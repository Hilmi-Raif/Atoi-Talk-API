package controller

import (
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/middleware"
	"AtoiTalkAPI/internal/model"
	servicemocks "AtoiTalkAPI/internal/service/mocks"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func mediaControllerRequest(method, target, mediaID string, withUser bool, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
	if withUser {
		r = r.WithContext(context.WithValue(r.Context(), middleware.UserContextKey, &model.UserDTO{ID: uuid.New()}))
	}
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("mediaID", mediaID)
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestMediaControllerRejectsUnauthorizedRequests(t *testing.T) {
	controller := NewMediaController(nil)
	handlers := map[string]http.HandlerFunc{
		"upload":   controller.UploadMedia,
		"complete": controller.CompleteUpload,
		"url":      controller.GetMediaURL,
	}

	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler(rr, mediaControllerRequest(http.MethodPost, "/", "bad", false, "{"))
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestMediaControllerRejectsInvalidInput(t *testing.T) {
	controller := NewMediaController(nil)
	tests := []struct {
		name    string
		handler http.HandlerFunc
		request *http.Request
	}{
		{"upload body", controller.UploadMedia, mediaControllerRequest(http.MethodPost, "/", "", true, "{")},
		{"complete id", controller.CompleteUpload, mediaControllerRequest(http.MethodPost, "/", "bad", true, "")},
		{"url id", controller.GetMediaURL, mediaControllerRequest(http.MethodGet, "/", "bad", true, "")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tt.handler(rr, tt.request)
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestMediaControllerSuccessAndErrorResponses(t *testing.T) {
	mockMedia := servicemocks.NewMockMediaServicePort(t)
	controller := NewMediaController(mockMedia)

	userID := uuid.New()
	mediaID := uuid.New()
	user := &model.UserDTO{ID: userID}

	mockMedia.EXPECT().UploadMedia(mock.Anything, userID, mock.Anything).
		Return(&model.UploadMediaResponse{Media: model.MediaDTO{ID: mediaID}}, nil).Once()
	req := httptest.NewRequest(http.MethodPost, "/api/media/upload", strings.NewReader(`{"file_name":"a.jpg","file_size":100,"content_type":"image/jpeg","category":"avatar"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr := httptest.NewRecorder()
	controller.UploadMedia(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockMedia.EXPECT().UploadMedia(mock.Anything, userID, mock.Anything).
		Return(nil, helper.NewBadRequestError("bad upload")).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/media/upload", strings.NewReader(`{"file_name":"a.jpg","file_size":100,"content_type":"image/jpeg","category":"avatar"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.UploadMedia(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)

	mockMedia.EXPECT().CompleteUpload(mock.Anything, userID, mediaID).
		Return(&model.MediaDTO{ID: mediaID}, nil).Once()
	req = mediaControllerRequest(http.MethodPost, "/api/media/"+mediaID.String()+"/complete", mediaID.String(), true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.CompleteUpload(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockMedia.EXPECT().CompleteUpload(mock.Anything, userID, mediaID).
		Return(nil, helper.NewNotFoundError("media not found")).Once()
	req = mediaControllerRequest(http.MethodPost, "/api/media/"+mediaID.String()+"/complete", mediaID.String(), true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.CompleteUpload(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)

	mockMedia.EXPECT().GetMediaURL(mock.Anything, userID, mediaID).
		Return(&model.MediaURLResponse{URL: "https://cdn.example/file.jpg"}, nil).Once()
	req = mediaControllerRequest(http.MethodGet, "/api/media/"+mediaID.String()+"/url", mediaID.String(), true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetMediaURL(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockMedia.EXPECT().GetMediaURL(mock.Anything, userID, mediaID).
		Return(nil, helper.NewForbiddenError("no access")).Once()
	req = mediaControllerRequest(http.MethodGet, "/api/media/"+mediaID.String()+"/url", mediaID.String(), true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetMediaURL(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
}
