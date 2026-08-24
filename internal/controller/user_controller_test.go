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

func userControllerRequest(method, target, param, value string, withUser bool) *http.Request {
	r := httptest.NewRequest(method, target, nil)
	if withUser {
		r = r.WithContext(context.WithValue(r.Context(), middleware.UserContextKey, &model.UserDTO{ID: uuid.New()}))
	}
	if param != "" {
		rctx := chi.NewRouteContext()
		rctx.URLParams.Add(param, value)
		r = r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
	}
	return r
}

func TestUserControllerRejectsUnauthorizedRequests(t *testing.T) {
	controller := NewUserController(nil)
	handlers := map[string]http.HandlerFunc{
		"current": controller.GetCurrentUser,
		"profile": controller.GetUserProfile,
		"update":  controller.UpdateProfile,
		"search":  controller.SearchUsers,
		"blocked": controller.GetBlockedUsers,
		"block":   controller.BlockUser,
		"unblock": controller.UnblockUser,
	}

	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler(rr, httptest.NewRequest(http.MethodGet, "/", nil))
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestUserControllerRejectsInvalidParameters(t *testing.T) {
	controller := NewUserController(nil)
	userRequest := func(target string) *http.Request {
		return userControllerRequest(http.MethodGet, target, "", "", true)
	}

	tests := []struct {
		name    string
		handler http.HandlerFunc
		request *http.Request
	}{
		{"profile id", controller.GetUserProfile, userControllerRequest(http.MethodGet, "/", "id", "bad", true)},
		{"search limit", controller.SearchUsers, userRequest("/?limit=bad")},
		{"search include chat", controller.SearchUsers, userRequest("/?include_chat_id=bad")},
		{"search excluded chat", controller.SearchUsers, userRequest("/?exclude_chat_id=bad")},
		{"blocked limit", controller.GetBlockedUsers, userRequest("/?limit=bad")},
		{"block id", controller.BlockUser, userControllerRequest(http.MethodPost, "/", "id", "bad", true)},
		{"unblock id", controller.UnblockUser, userControllerRequest(http.MethodPost, "/", "id", "bad", true)},
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

func TestUserControllerSuccessAndErrorResponses(t *testing.T) {
	mockUser := servicemocks.NewMockUserServicePort(t)
	controller := NewUserController(mockUser)

	userID := uuid.New()
	targetID := uuid.New()
	user := &model.UserDTO{ID: userID}

	mockUser.EXPECT().GetCurrentUser(mock.Anything, userID).
		Return(&model.UserDTO{ID: userID}, nil).Once()
	req := httptest.NewRequest(http.MethodGet, "/api/users/me", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr := httptest.NewRecorder()
	controller.GetCurrentUser(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockUser.EXPECT().GetCurrentUser(mock.Anything, userID).
		Return(nil, helper.NewNotFoundError("user not found")).Once()
	rr = httptest.NewRecorder()
	controller.GetCurrentUser(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)

	mockUser.EXPECT().GetUserProfile(mock.Anything, userID, targetID).
		Return(&model.UserDTO{ID: targetID}, nil).Once()
	req = userControllerRequest(http.MethodGet, "/api/users/"+targetID.String(), "id", targetID.String(), true)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetUserProfile(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockUser.EXPECT().GetUserProfile(mock.Anything, userID, targetID).
		Return(nil, helper.NewNotFoundError("profile not found")).Once()
	rr = httptest.NewRecorder()
	controller.GetUserProfile(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)

	mockUser.EXPECT().UpdateProfile(mock.Anything, userID, mock.Anything).
		Return(&model.UserDTO{ID: userID}, nil).Once()
	req = httptest.NewRequest(http.MethodPut, "/api/users/me", strings.NewReader(`{"username":"newname"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.UpdateProfile(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockUser.EXPECT().UpdateProfile(mock.Anything, userID, mock.Anything).
		Return(nil, helper.NewConflictError("username exists")).Once()
	req = httptest.NewRequest(http.MethodPut, "/api/users/me", strings.NewReader(`{"username":"newname"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.UpdateProfile(rr, req)
	require.Equal(t, http.StatusConflict, rr.Code)

	req = httptest.NewRequest(http.MethodPut, "/api/users/me", strings.NewReader(`{`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.UpdateProfile(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)

	mockUser.EXPECT().SearchUsers(mock.Anything, userID, mock.Anything).
		Return([]model.UserDTO{{ID: targetID}}, "", false, nil).Once()
	req = httptest.NewRequest(http.MethodGet, "/api/users/search?q=test", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.SearchUsers(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockUser.EXPECT().SearchUsers(mock.Anything, userID, mock.Anything).
		Return(nil, "", false, helper.NewInternalServerError("search error")).Once()
	rr = httptest.NewRecorder()
	controller.SearchUsers(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code)

	mockUser.EXPECT().GetBlockedUsers(mock.Anything, userID, mock.Anything).
		Return([]model.UserDTO{{ID: targetID}}, "", false, nil).Once()
	req = httptest.NewRequest(http.MethodGet, "/api/users/blocked", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetBlockedUsers(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockUser.EXPECT().GetBlockedUsers(mock.Anything, userID, mock.Anything).
		Return(nil, "", false, helper.NewInternalServerError("blocked error")).Once()
	rr = httptest.NewRecorder()
	controller.GetBlockedUsers(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code)

	mockUser.EXPECT().BlockUser(mock.Anything, userID, targetID).
		Return(nil).Once()
	req = userControllerRequest(http.MethodPost, "/api/users/"+targetID.String()+"/block", "id", targetID.String(), true)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.BlockUser(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockUser.EXPECT().BlockUser(mock.Anything, userID, targetID).
		Return(helper.NewBadRequestError("cannot block self")).Once()
	rr = httptest.NewRecorder()
	controller.BlockUser(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)

	mockUser.EXPECT().UnblockUser(mock.Anything, userID, targetID).
		Return(nil).Once()
	req = userControllerRequest(http.MethodDelete, "/api/users/"+targetID.String()+"/block", "id", targetID.String(), true)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.UnblockUser(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockUser.EXPECT().UnblockUser(mock.Anything, userID, targetID).
		Return(helper.NewNotFoundError("not blocked")).Once()
	rr = httptest.NewRecorder()
	controller.UnblockUser(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
}
