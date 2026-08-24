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

func messageControllerRequest(method, target string, param, value string, withUser bool, body string) *http.Request {
	r := httptest.NewRequest(method, target, strings.NewReader(body))
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

func TestMessageControllerRejectsUnauthorizedRequests(t *testing.T) {
	controller := NewMessageController(nil)
	handlers := map[string]http.HandlerFunc{
		"send":   controller.SendMessage,
		"edit":   controller.EditMessage,
		"get":    controller.GetMessages,
		"delete": controller.DeleteMessage,
	}

	for name, handler := range handlers {
		t.Run(name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			handler(rr, messageControllerRequest(http.MethodPost, "/", "", "", false, "{"))
			if rr.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
			}
		})
	}
}

func TestMessageControllerRejectsInvalidInput(t *testing.T) {
	controller := NewMessageController(nil)
	user := uuid.New()
	tests := []struct {
		name    string
		handler http.HandlerFunc
		request *http.Request
	}{
		{"send body", controller.SendMessage, messageControllerRequest(http.MethodPost, "/", "", "", true, "{")},
		{"edit id", controller.EditMessage, messageControllerRequest(http.MethodPut, "/", "messageID", "bad", true, "{}")},
		{"edit body", controller.EditMessage, messageControllerRequest(http.MethodPut, "/", "messageID", uuid.NewString(), true, "{")},
		{"get chat id", controller.GetMessages, messageControllerRequest(http.MethodGet, "/", "chatID", "bad", true, "")},
		{"get limit", controller.GetMessages, messageControllerRequest(http.MethodGet, "/?limit=bad", "chatID", uuid.NewString(), true, "")},
		{"get around id", controller.GetMessages, messageControllerRequest(http.MethodGet, "/?around_message_id=bad", "chatID", uuid.NewString(), true, "")},
		{"delete id", controller.DeleteMessage, messageControllerRequest(http.MethodDelete, "/", "messageID", "bad", true, "")},
	}
	_ = user

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

func TestMessageControllerSuccessAndErrorResponses(t *testing.T) {
	mockMessage := servicemocks.NewMockMessageServicePort(t)
	controller := NewMessageController(mockMessage)

	userID := uuid.New()
	chatID := uuid.New()
	messageID := uuid.New()
	user := &model.UserDTO{ID: userID}

	mockMessage.EXPECT().SendMessage(mock.Anything, userID, mock.Anything).
		Return(&model.MessageResponse{ID: messageID}, nil).Once()
	req := httptest.NewRequest(http.MethodPost, "/api/messages", strings.NewReader(`{"chat_id":"`+chatID.String()+`","content":"hello"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr := httptest.NewRecorder()
	controller.SendMessage(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockMessage.EXPECT().SendMessage(mock.Anything, userID, mock.Anything).
		Return(nil, helper.NewForbiddenError("blocked")).Once()
	req = httptest.NewRequest(http.MethodPost, "/api/messages", strings.NewReader(`{"chat_id":"`+chatID.String()+`","content":"hello"}`))
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.SendMessage(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)

	mockMessage.EXPECT().EditMessage(mock.Anything, userID, messageID, mock.Anything).
		Return(&model.MessageResponse{ID: messageID}, nil).Once()
	req = messageControllerRequest(http.MethodPut, "/api/messages/"+messageID.String(), "messageID", messageID.String(), true, `{"content":"edited"}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.EditMessage(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockMessage.EXPECT().EditMessage(mock.Anything, userID, messageID, mock.Anything).
		Return(nil, helper.NewForbiddenError("not owner")).Once()
	req = messageControllerRequest(http.MethodPut, "/api/messages/"+messageID.String(), "messageID", messageID.String(), true, `{"content":"edited"}`)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.EditMessage(rr, req)
	require.Equal(t, http.StatusForbidden, rr.Code)

	mockMessage.EXPECT().GetMessages(mock.Anything, userID, mock.Anything).
		Return([]model.MessageResponse{{ID: messageID}}, "next", true, "prev", false, nil).Once()
	req = messageControllerRequest(http.MethodGet, "/api/chats/"+chatID.String()+"/messages?limit=20", "chatID", chatID.String(), true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetMessages(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockMessage.EXPECT().GetMessages(mock.Anything, userID, mock.Anything).
		Return(nil, "", false, "", false, helper.NewNotFoundError("chat not found")).Once()
	req = messageControllerRequest(http.MethodGet, "/api/chats/"+chatID.String()+"/messages", "chatID", chatID.String(), true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.GetMessages(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)

	mockMessage.EXPECT().DeleteMessage(mock.Anything, userID, messageID).
		Return(nil).Once()
	req = messageControllerRequest(http.MethodDelete, "/api/messages/"+messageID.String(), "messageID", messageID.String(), true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.DeleteMessage(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	mockMessage.EXPECT().DeleteMessage(mock.Anything, userID, messageID).
		Return(helper.NewNotFoundError("message not found")).Once()
	req = messageControllerRequest(http.MethodDelete, "/api/messages/"+messageID.String(), "messageID", messageID.String(), true, "")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	rr = httptest.NewRecorder()
	controller.DeleteMessage(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)
}
