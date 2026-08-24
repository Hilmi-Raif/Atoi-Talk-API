package controller

import (
	"AtoiTalkAPI/internal/helper"
	servicemocks "AtoiTalkAPI/internal/service/mocks"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func TestOTPControllerRejectsMalformedBody(t *testing.T) {
	rr := httptest.NewRecorder()
	NewOTPController(nil).SendOTP(rr, httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{")))
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestOTPControllerSendOTPSuccessAndError(t *testing.T) {
	mockOTP := servicemocks.NewMockOTPServicePort(t)
	controller := NewOTPController(mockOTP)

	mockOTP.EXPECT().SendOTP(mock.Anything, mock.Anything).
		Return(nil).Once()
	req := httptest.NewRequest(http.MethodPost, "/api/otp/send", strings.NewReader(`{"email":"test@example.com","mode":"register","captcha_token":"token"}`))
	rr := httptest.NewRecorder()
	controller.SendOTP(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockOTP.EXPECT().SendOTP(mock.Anything, mock.Anything).
		Return(helper.NewTooManyRequestsError("rate limit")).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/otp/send", strings.NewReader(`{"email":"test@example.com","mode":"register","captcha_token":"token"}`))
	rr = httptest.NewRecorder()
	controller.SendOTP(rr, req)
	require.Equal(t, http.StatusTooManyRequests, rr.Code)
}
