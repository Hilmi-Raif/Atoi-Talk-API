package controller

import (
	"AtoiTalkAPI/internal/middleware"
	"AtoiTalkAPI/internal/model"
	servicemocks "AtoiTalkAPI/internal/service/mocks"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/mock"
)

func chatRequest(method, target string, user *model.UserDTO) *http.Request {
	req := httptest.NewRequest(method, target, nil)
	if user != nil {
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserContextKey, user))
	}
	return req
}

func chatRequestWithParam(method, target, name, value string, user *model.UserDTO) *http.Request {
	req := chatRequest(method, target, user)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(name, value)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func TestChatControllerGetChats(t *testing.T) {
	user := &model.UserDTO{ID: uuid.New()}

	t.Run("requires user context", func(t *testing.T) {
		controller := NewChatController(servicemocks.NewMockChatServicePort(t))
		rr := httptest.NewRecorder()
		controller.GetChats(rr, chatRequest(http.MethodGet, "/api/chats", nil))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})

	t.Run("rejects invalid limit", func(t *testing.T) {
		controller := NewChatController(servicemocks.NewMockChatServicePort(t))
		rr := httptest.NewRecorder()
		controller.GetChats(rr, chatRequest(http.MethodGet, "/api/chats?limit=nope", user))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
	})

	t.Run("writes paginated response", func(t *testing.T) {
		fake := servicemocks.NewMockChatServicePort(t)
		fake.EXPECT().GetChats(mock.Anything, user.ID, model.GetChatsRequest{Limit: 2}).Return([]model.ChatListResponse{{ID: uuid.New()}}, "next", true, nil)
		controller := NewChatController(fake)
		rr := httptest.NewRecorder()
		controller.GetChats(rr, chatRequest(http.MethodGet, "/api/chats?limit=2", user))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
	})

	t.Run("writes service error", func(t *testing.T) {
		fake := servicemocks.NewMockChatServicePort(t)
		fake.EXPECT().GetChats(mock.Anything, user.ID, model.GetChatsRequest{Limit: 20}).Return(nil, "", false, errors.New("service failure"))
		controller := NewChatController(fake)
		rr := httptest.NewRecorder()
		controller.GetChats(rr, chatRequest(http.MethodGet, "/api/chats", user))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusInternalServerError)
		}
	})
}

func TestChatControllerGetChat(t *testing.T) {
	user := &model.UserDTO{ID: uuid.New()}
	controller := NewChatController(servicemocks.NewMockChatServicePort(t))

	t.Run("requires user context", func(t *testing.T) {
		rr := httptest.NewRecorder()
		controller.GetChat(rr, chatRequestWithParam(http.MethodGet, "/api/chats/1", "id", uuid.NewString(), nil))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})

	t.Run("rejects invalid id", func(t *testing.T) {
		rr := httptest.NewRecorder()
		controller.GetChat(rr, chatRequestWithParam(http.MethodGet, "/api/chats/bad", "id", "bad", user))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
	})

	t.Run("writes success", func(t *testing.T) {
		fake := servicemocks.NewMockChatServicePort(t)
		fake.EXPECT().GetChatByID(mock.Anything, user.ID, mock.Anything).Return(&model.ChatListResponse{ID: uuid.New()}, nil)
		controller := NewChatController(fake)
		rr := httptest.NewRecorder()
		controller.GetChat(rr, chatRequestWithParam(http.MethodGet, "/api/chats/1", "id", uuid.NewString(), user))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
	})

	t.Run("writes error", func(t *testing.T) {
		fake := servicemocks.NewMockChatServicePort(t)
		fake.EXPECT().GetChatByID(mock.Anything, user.ID, mock.Anything).Return(nil, errors.New("not found"))
		controller := NewChatController(fake)
		rr := httptest.NewRecorder()
		controller.GetChat(rr, chatRequestWithParam(http.MethodGet, "/api/chats/1", "id", uuid.NewString(), user))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d", rr.Code)
		}
	})
}

func TestChatControllerMarkAsRead(t *testing.T) {
	user := &model.UserDTO{ID: uuid.New()}
	controller := NewChatController(servicemocks.NewMockChatServicePort(t))

	t.Run("requires user context", func(t *testing.T) {
		rr := httptest.NewRecorder()
		controller.MarkAsRead(rr, chatRequestWithParam(http.MethodPost, "/api/chats/1/read", "id", uuid.NewString(), nil))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})

	t.Run("requires valid id", func(t *testing.T) {
		rr := httptest.NewRecorder()
		controller.MarkAsRead(rr, chatRequestWithParam(http.MethodPost, "/api/chats/bad/read", "id", "bad", user))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
	})

	t.Run("writes success", func(t *testing.T) {
		fake := servicemocks.NewMockChatServicePort(t)
		fake.EXPECT().MarkAsRead(mock.Anything, user.ID, mock.Anything).Return(nil)
		controller := NewChatController(fake)
		rr := httptest.NewRecorder()
		controller.MarkAsRead(rr, chatRequestWithParam(http.MethodPost, "/api/chats/1/read", "id", uuid.NewString(), user))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
	})

	t.Run("writes service error", func(t *testing.T) {
		fake := servicemocks.NewMockChatServicePort(t)
		fake.EXPECT().MarkAsRead(mock.Anything, user.ID, mock.Anything).Return(errors.New("service failure"))
		controller := NewChatController(fake)
		rr := httptest.NewRecorder()
		controller.MarkAsRead(rr, chatRequestWithParam(http.MethodPost, "/api/chats/1/read", "id", uuid.NewString(), user))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d", rr.Code)
		}
	})
}

func TestChatControllerHideChat(t *testing.T) {
	user := &model.UserDTO{ID: uuid.New()}
	controller := NewChatController(servicemocks.NewMockChatServicePort(t))

	t.Run("requires user context", func(t *testing.T) {
		rr := httptest.NewRecorder()
		controller.HideChat(rr, chatRequestWithParam(http.MethodPost, "/api/chats/1/hide", "id", uuid.NewString(), nil))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusUnauthorized)
		}
	})

	t.Run("requires valid id", func(t *testing.T) {
		rr := httptest.NewRecorder()
		controller.HideChat(rr, chatRequestWithParam(http.MethodPost, "/api/chats/bad/hide", "id", "bad", user))
		if rr.Code != http.StatusBadRequest {
			t.Fatalf("status = %d, want %d", rr.Code, http.StatusBadRequest)
		}
	})

	t.Run("writes success", func(t *testing.T) {
		fake := servicemocks.NewMockChatServicePort(t)
		fake.EXPECT().HideChat(mock.Anything, user.ID, mock.Anything).Return(nil)
		controller := NewChatController(fake)
		rr := httptest.NewRecorder()
		controller.HideChat(rr, chatRequestWithParam(http.MethodPost, "/api/chats/1/hide", "id", uuid.NewString(), user))
		if rr.Code != http.StatusOK {
			t.Fatalf("status = %d", rr.Code)
		}
	})

	t.Run("writes error", func(t *testing.T) {
		fake := servicemocks.NewMockChatServicePort(t)
		fake.EXPECT().HideChat(mock.Anything, user.ID, mock.Anything).Return(errors.New("hide failure"))
		controller := NewChatController(fake)
		rr := httptest.NewRecorder()
		controller.HideChat(rr, chatRequestWithParam(http.MethodPost, "/api/chats/1/hide", "id", uuid.NewString(), user))
		if rr.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d", rr.Code)
		}
	})
}
