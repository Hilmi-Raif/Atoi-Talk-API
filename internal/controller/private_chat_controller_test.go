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

func TestPrivateChatControllerRejectsUnauthorized(t *testing.T) {
	rr := httptest.NewRecorder()
	NewPrivateChatController(nil).CreatePrivateChat(rr, httptest.NewRequest(http.MethodPost, "/", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
	}
}

func TestPrivateChatControllerRejectsMalformedBody(t *testing.T) {
	user := &model.UserDTO{ID: uuid.New()}
	r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader("{"))
	r = r.WithContext(context.WithValue(r.Context(), middleware.UserContextKey, user))
	rr := httptest.NewRecorder()
	NewPrivateChatController(nil).CreatePrivateChat(rr, r)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
	}
}

func TestPrivateChatControllerCreateSuccessAndError(t *testing.T) {
	mockChat := servicemocks.NewMockPrivateChatServicePort(t)
	controller := NewPrivateChatController(mockChat)

	userID := uuid.New()
	targetID := uuid.New()
	user := &model.UserDTO{ID: userID}

	mockChat.EXPECT().CreatePrivateChat(mock.Anything, userID, mock.Anything).
		Return(&model.ChatResponse{ID: uuid.New()}, nil).Once()

	req := httptest.NewRequest(http.MethodPost, "/api/chats/private", strings.NewReader(`{"target_user_id":"`+targetID.String()+`"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr := httptest.NewRecorder()
	controller.CreatePrivateChat(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockChat.EXPECT().CreatePrivateChat(mock.Anything, userID, mock.Anything).
		Return(nil, helper.NewForbiddenError("blocked")).Once()

	req = httptest.NewRequest(http.MethodPost, "/api/chats/private", strings.NewReader(`{"target_user_id":"`+targetID.String()+`"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.CreatePrivateChat(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)
}
