package controller

import (
	"AtoiTalkAPI/internal/helper"
	"AtoiTalkAPI/internal/model"
	servicemocks "AtoiTalkAPI/internal/service/mocks"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestAuthControllerRejectsMalformedBodies(t *testing.T) {
	controller := NewAuthController(nil)
	tests := []struct {
		name string
		hand func(http.ResponseWriter, *http.Request)
	}{
		{name: "login", hand: controller.Login},
		{name: "google exchange", hand: controller.GoogleExchange},
		{name: "register", hand: controller.Register},
		{name: "reset password", hand: controller.ResetPassword},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			tt.hand(rr, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{")))
			if rr.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
			}
		})
	}
}

func TestAuthControllerLogoutRejectsMissingOrMalformedAuthorization(t *testing.T) {
	controller := NewAuthController(nil)
	for _, header := range []string{"", "Basic token", "Bearer", "Bearer one two"} {
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		if header != "" {
			r.Header.Set("Authorization", header)
		}
		rr := httptest.NewRecorder()
		controller.Logout(rr, r)
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("header %q: status = %d, want %d", header, rr.Code, http.StatusUnauthorized)
		}
	}
}

func TestAuthControllerGoogleAuthInitHandlesSuccessAndError(t *testing.T) {
	mockAuth := servicemocks.NewMockAuthServicePort(t)
	controller := NewAuthController(mockAuth)

	mockAuth.EXPECT().BeginGoogleAuth(mock.Anything).
		Return(&model.GoogleAuthInitResponse{AuthURL: "https://auth.example"}, nil).Once()

	req := httptest.NewRequest(http.MethodGet, "/api/auth/google/init", nil)
	rr := httptest.NewRecorder()
	controller.GoogleAuthInit(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAuth.EXPECT().BeginGoogleAuth(mock.Anything).
		Return(nil, helper.NewInternalServerError("auth init failed")).Once()

	rr = httptest.NewRecorder()
	controller.GoogleAuthInit(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code)
}

func TestAuthControllerSuccessAndErrorResponses(t *testing.T) {
	mockAuth := servicemocks.NewMockAuthServicePort(t)
	controller := NewAuthController(mockAuth)

	mockAuth.EXPECT().Login(mock.Anything, mock.Anything).
		Return(&model.AuthResponse{Token: "token"}, nil).Once()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"test@example.com","password":"password123","captcha_token":"token"}`))
	rr := httptest.NewRecorder()
	controller.Login(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAuth.EXPECT().Login(mock.Anything, mock.Anything).
		Return(nil, helper.NewUnauthorizedError("invalid credentials")).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"email":"test@example.com","password":"password123","captcha_token":"token"}`))
	rr = httptest.NewRecorder()
	controller.Login(rr, req)
	require.Equal(t, http.StatusUnauthorized, rr.Code)

	mockAuth.EXPECT().Logout(mock.Anything, "valid-token").
		Return(nil).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rr = httptest.NewRecorder()
	controller.Logout(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAuth.EXPECT().Logout(mock.Anything, "valid-token").
		Return(helper.NewInternalServerError("logout failed")).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.Header.Set("Authorization", "Bearer valid-token")
	rr = httptest.NewRecorder()
	controller.Logout(rr, req)
	require.Equal(t, http.StatusInternalServerError, rr.Code)

	mockAuth.EXPECT().GoogleExchange(mock.Anything, mock.Anything).
		Return(&model.AuthResponse{Token: "token"}, nil).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/google", strings.NewReader(`{"code":"code","state":"state"}`))
	rr = httptest.NewRecorder()
	controller.GoogleExchange(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAuth.EXPECT().GoogleExchange(mock.Anything, mock.Anything).
		Return(nil, helper.NewBadRequestError("invalid state")).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/google", strings.NewReader(`{"code":"code","state":"state"}`))
	rr = httptest.NewRecorder()
	controller.GoogleExchange(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)

	mockAuth.EXPECT().Register(mock.Anything, mock.Anything).
		Return(&model.AuthResponse{Token: "token"}, nil).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"email":"test@example.com","username":"test","full_name":"Test User","password":"password123","code":"123456","captcha_token":"token"}`))
	rr = httptest.NewRecorder()
	controller.Register(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAuth.EXPECT().Register(mock.Anything, mock.Anything).
		Return(nil, helper.NewConflictError("email exists")).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/register", strings.NewReader(`{"email":"test@example.com","username":"test","full_name":"Test User","password":"password123","code":"123456","captcha_token":"token"}`))
	rr = httptest.NewRecorder()
	controller.Register(rr, req)
	require.Equal(t, http.StatusConflict, rr.Code)

	mockAuth.EXPECT().ResetPassword(mock.Anything, mock.Anything).
		Return(nil).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/reset-password", strings.NewReader(`{"email":"test@example.com","code":"123456","password":"password123","confirm_password":"password123","captcha_token":"token"}`))
	rr = httptest.NewRecorder()
	controller.ResetPassword(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockAuth.EXPECT().ResetPassword(mock.Anything, mock.Anything).
		Return(helper.NewBadRequestError("invalid code")).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/auth/reset-password", strings.NewReader(`{"email":"test@example.com","code":"123456","password":"password123","confirm_password":"password123","captcha_token":"token"}`))
	rr = httptest.NewRecorder()
	controller.ResetPassword(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}
