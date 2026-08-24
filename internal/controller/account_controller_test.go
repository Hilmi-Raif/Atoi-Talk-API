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

	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func accountControllerRequest(method, body string, withUser bool) *http.Request {
	r := httptest.NewRequest(method, "/", strings.NewReader(body))
	if withUser {
		r = r.WithContext(context.WithValue(r.Context(), middleware.UserContextKey, &model.UserDTO{ID: uuid.New()}))
	}
	return r
}

func TestAccountControllerRejectsUnauthorizedRequests(t *testing.T) {
	controller := NewAccountController(nil)
	handlers := map[string]http.HandlerFunc{
		"password": controller.ChangePassword,
		"email":    controller.ChangeEmail,
		"delete":   controller.DeleteAccount,
	}

	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler(rr, accountControllerRequest(http.MethodPut, "{", false))
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestAccountControllerRejectsMalformedBodies(t *testing.T) {
	controller := NewAccountController(nil)
	for _, handler := range []http.HandlerFunc{controller.ChangePassword, controller.ChangeEmail, controller.DeleteAccount} {
		rr := httptest.NewRecorder()
		handler(rr, accountControllerRequest(http.MethodPut, "{", true))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
	}
}

func TestAccountControllerSuccessAndErrorResponses(t *testing.T) {
	mockAccount := servicemocks.NewMockAccountServicePort(t)
	controller := NewAccountController(mockAccount)

	userID := uuid.New()
	user := &model.UserDTO{ID: userID}

	mockAccount.EXPECT().ChangePassword(mock.Anything, userID, mock.Anything).
		Return(nil).Once()
	req := httptest.NewRequest(http.MethodPut, "/api/account/password", strings.NewReader(`{"new_password":"password123","confirm_password":"password123"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr := httptest.NewRecorder()
	controller.ChangePassword(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAccount.EXPECT().ChangePassword(mock.Anything, userID, mock.Anything).
		Return(helper.NewBadRequestError("wrong password")).Once()
	req = httptest.NewRequest(http.MethodPut, "/api/account/password", strings.NewReader(`{"new_password":"password123","confirm_password":"password123"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.ChangePassword(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)

	mockAccount.EXPECT().ChangeEmail(mock.Anything, userID, mock.Anything).
		Return(nil).Once()
	req = httptest.NewRequest(http.MethodPut, "/api/account/email", strings.NewReader(`{"email":"new@example.com","code":"123456"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.ChangeEmail(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAccount.EXPECT().ChangeEmail(mock.Anything, userID, mock.Anything).
		Return(helper.NewConflictError("email exists")).Once()
	req = httptest.NewRequest(http.MethodPut, "/api/account/email", strings.NewReader(`{"email":"new@example.com","code":"123456"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.ChangeEmail(rr, req)
	require.Equal(t, http.StatusConflict, rr.Code)

	mockAccount.EXPECT().DeleteAccount(mock.Anything, userID, mock.Anything).
		Return(nil).Once()
	req = httptest.NewRequest(http.MethodDelete, "/api/account", strings.NewReader(`{"password":"password123"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.DeleteAccount(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAccount.EXPECT().DeleteAccount(mock.Anything, userID, mock.Anything).
		Return(helper.NewForbiddenError("owner must transfer")).Once()
	req = httptest.NewRequest(http.MethodDelete, "/api/account", strings.NewReader(`{"password":"password123"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.DeleteAccount(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
}
