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

func TestReportControllerRejectsUnauthorized(t *testing.T) {
	rr := httptest.NewRecorder()
	NewReportController(nil).CreateReport(rr, httptest.NewRequest(http.MethodPost, "/", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestReportControllerRejectsMalformedBody(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{"))
	r = r.WithContext(context.WithValue(r.Context(), middleware.UserContextKey, &model.UserDTO{ID: uuid.New()}))
	rr := httptest.NewRecorder()
	NewReportController(nil).CreateReport(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestReportControllerCreateSuccessAndError(t *testing.T) {
	mockReport := servicemocks.NewMockReportServicePort(t)
	controller := NewReportController(mockReport)

	userID := uuid.New()
	targetID := uuid.New()
	user := &model.UserDTO{ID: userID}

	mockReport.EXPECT().CreateReport(mock.Anything, userID, mock.Anything).
		Return(nil).Once()

	req := httptest.NewRequest(http.MethodPost, "/api/reports", strings.NewReader(`{"target_type":"user","target_id":"`+targetID.String()+`","reason":"spam"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr := httptest.NewRecorder()
	controller.CreateReport(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockReport.EXPECT().CreateReport(mock.Anything, userID, mock.Anything).
		Return(helper.NewBadRequestError("cannot report yourself")).Once()

	req = httptest.NewRequest(http.MethodPost, "/api/reports", strings.NewReader(`{"target_type":"user","target_id":"`+targetID.String()+`","reason":"spam"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.CreateReport(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)
}
